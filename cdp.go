package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

// chrome is one headless Chrome process and the DevTools Protocol
// connection to it. The connection is Chrome's --remote-debugging-pipe:
// Chrome reads JSON messages on fd 3 and writes them on fd 4, each
// message ended by a NUL byte. A pipe rather than a port, so two runs
// at once do not race for a port and nothing listens on the machine.
//
// The protocol is small enough here to speak by hand: a message out
// has an id, a method, params, and a sessionId; a message back has the
// same id with a result or an error, or no id and a method when it is
// an event. This file knows that and nothing about any method.
type chrome struct {
	cmd     *exec.Cmd
	dataDir string
	w       io.WriteCloser
	r       *bufio.Reader

	mu      sync.Mutex
	nextID  int
	pending map[int]chan message
	subs    []*subscription
	readErr error
	closed  chan struct{}
}

// message is every shape the pipe carries, in one struct. A field that
// is zero is left out on the way out, so a call and an event and a
// reply all marshal to what Chrome expects.
type message struct {
	ID        int             `json:"id,omitempty"`
	Method    string          `json:"method,omitempty"`
	Params    any             `json:"params,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *cdpError       `json:"error,omitempty"`
}

type cdpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// subscription is one caller waiting for one event on one session.
// The reader closes ch on the first match and drops the subscription.
type subscription struct {
	method    string
	sessionID string
	ch        chan json.RawMessage
}

// chromePath finds the browser. The flag wins, then BROWSE_CHROME, then
// the pinned chrome-headless-shell in the user cache, downloaded on
// the first run.
//
// Not the Google Chrome in /Applications. A fresh profile of it asks
// macOS to make Chrome the default browser at every launch, and macOS
// shows a dialog for that. --no-default-browser-check, the
// default-browser feature flags, and a seeded profile do not stop it.
// The headless shell has no browser UI and no such code.
func chromePath(ctx context.Context, flag string) (string, error) {
	if flag != "" {
		return flag, nil
	}
	if p := os.Getenv("BROWSE_CHROME"); p != "" {
		return p, nil
	}
	return ensureShell(ctx)
}

// launch starts Chrome headless with the debugging pipe and returns
// once the reader is running. The profile is a fresh temporary
// directory, so the run sees no cookie, history, or extension of the
// user's own profile and leaves nothing in it. close removes it.
func launch(ctx context.Context, path string, width, height int) (*chrome, error) {
	dataDir, err := os.MkdirTemp("", "browse-")
	if err != nil {
		return nil, err
	}
	// Chrome reads its fd 3 from toChrome and writes its fd 4 to
	// fromChrome. ExtraFiles maps index 0 to fd 3 and index 1 to fd 4.
	toChromeR, toChromeW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	fromChromeR, fromChromeW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	// The headless shell is headless already; --headless is for a full
	// Chrome given through -chrome.
	cmd := exec.CommandContext(ctx, path,
		"--headless",
		"--remote-debugging-pipe",
		"--user-data-dir="+dataDir,
		"--no-first-run",
		"--disable-background-networking",
		"--hide-scrollbars",
		fmt.Sprintf("--window-size=%d,%d", width, height),
		"about:blank",
	)
	cmd.ExtraFiles = []*os.File{toChromeR, fromChromeW}
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		os.RemoveAll(dataDir)
		return nil, fmt.Errorf("start %s: %w", path, err)
	}
	// The child holds its own copies. Closing ours is what lets a read
	// see EOF when Chrome exits.
	toChromeR.Close()
	fromChromeW.Close()

	c := &chrome{
		cmd:     cmd,
		dataDir: dataDir,
		w:       toChromeW,
		r:       bufio.NewReaderSize(fromChromeR, 1<<20),
		pending: map[int]chan message{},
		closed:  make(chan struct{}),
	}
	go c.read()
	return c, nil
}

// read is the one goroutine that reads the pipe. A reply goes to the
// call waiting on its id. An event goes to every subscription that
// named it. Anything else is dropped: Chrome sends events nobody asked
// for, and a reader that stalled on them would stall every call.
func (c *chrome) read() {
	defer close(c.closed)
	for {
		raw, err := c.r.ReadBytes(0)
		if err != nil {
			c.mu.Lock()
			c.readErr = fmt.Errorf("chrome closed the connection: %w", err)
			for id, ch := range c.pending {
				close(ch)
				delete(c.pending, id)
			}
			for _, s := range c.subs {
				close(s.ch)
			}
			c.subs = nil
			c.mu.Unlock()
			return
		}
		raw = raw[:len(raw)-1]
		var m message
		if err := json.Unmarshal(raw, &m); err != nil {
			continue
		}
		c.mu.Lock()
		if m.ID != 0 {
			if ch, ok := c.pending[m.ID]; ok {
				ch <- m
				delete(c.pending, m.ID)
			}
			c.mu.Unlock()
			continue
		}
		kept := c.subs[:0]
		for _, s := range c.subs {
			if s.method == m.Method && s.sessionID == m.SessionID {
				params, _ := json.Marshal(m.Params)
				s.ch <- params
				close(s.ch)
				continue
			}
			kept = append(kept, s)
		}
		c.subs = kept
		c.mu.Unlock()
	}
}

// call sends one method and waits for its reply. sessionID is empty
// for a browser-level method and a page session otherwise. out, when
// not nil, receives the result.
func (c *chrome) call(ctx context.Context, sessionID, method string, params any, out any) error {
	c.mu.Lock()
	if c.readErr != nil {
		c.mu.Unlock()
		return c.readErr
	}
	c.nextID++
	id := c.nextID
	ch := make(chan message, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	raw, err := json.Marshal(message{ID: id, Method: method, Params: params, SessionID: sessionID})
	if err != nil {
		return err
	}
	raw = append(raw, 0)
	if _, err := c.w.Write(raw); err != nil {
		return fmt.Errorf("%s: write: %w", method, err)
	}

	select {
	case m, ok := <-ch:
		if !ok {
			c.mu.Lock()
			err := c.readErr
			c.mu.Unlock()
			return fmt.Errorf("%s: %w", method, err)
		}
		if m.Error != nil {
			return fmt.Errorf("%s: %s", method, m.Error.Message)
		}
		if out != nil && len(m.Result) > 0 {
			if err := json.Unmarshal(m.Result, out); err != nil {
				return fmt.Errorf("%s: decode result: %w", method, err)
			}
		}
		return nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return fmt.Errorf("%s: %w", method, ctx.Err())
	}
}

// subscribe returns a channel that receives the params of the next
// event named method on sessionID, then closes. Subscribe before the
// call that causes the event, or the event can land first and be
// dropped.
func (c *chrome) subscribe(method, sessionID string) <-chan json.RawMessage {
	s := &subscription{method: method, sessionID: sessionID, ch: make(chan json.RawMessage, 1)}
	c.mu.Lock()
	c.subs = append(c.subs, s)
	c.mu.Unlock()
	return s.ch
}

// close asks Chrome to exit, waits a moment for it, kills it if it
// stayed, waits for the reader to see the pipe close, and removes the
// profile directory.
func (c *chrome) close() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = c.call(ctx, "", "Browser.close", nil, nil)
	c.w.Close()
	done := make(chan struct{})
	go func() {
		_ = c.cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		_ = c.cmd.Process.Kill()
		<-done
	}
	<-c.closed
	os.RemoveAll(c.dataDir)
}
