package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
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
	stderr  *stderrTail
	exited  chan struct{}

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

// subscription is one caller waiting for an event on one session.
// A one-shot subscription has ch: the reader sends the first match on
// it, closes it, and drops the subscription. A stream has ready: the
// reader adds each match to queue and signals ready.
type subscription struct {
	method    string
	sessionID string
	ch        chan json.RawMessage
	ready     chan struct{}
	queue     []json.RawMessage
}

// stderrTail keeps the last 4 KB that Chrome writes to stderr. When
// Chrome exits at start, the reason is there, and the connection
// error shows only EOF.
type stderrTail struct {
	mu sync.Mutex
	b  []byte
}

func (t *stderrTail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > 4096 {
		t.b = t.b[len(t.b)-4096:]
	}
	return len(p), nil
}

// reason is Chrome's last FATAL line, or else its last line. It is ""
// when Chrome wrote nothing.
func (t *stderrTail) reason() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	lines := strings.Split(strings.TrimSpace(string(t.b)), "\n")
	for _, line := range slices.Backward(lines) {
		if strings.Contains(line, "FATAL") {
			return strings.TrimSpace(line)
		}
	}
	return strings.TrimSpace(lines[len(lines)-1])
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
	stderr := &stderrTail{}
	cmd.Stderr = stderr
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
		stderr:  stderr,
		exited:  make(chan struct{}),
		pending: map[int]chan message{},
		closed:  make(chan struct{}),
	}
	// Wait returns after Chrome exits and its stderr is copied, so
	// exited also means stderr is complete.
	go func() {
		_ = cmd.Wait()
		close(c.exited)
	}()
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
			// Give Chrome a moment to exit, so its last stderr line is
			// in the error. A Chrome that fails at start says why there.
			select {
			case <-c.exited:
			case <-time.After(2 * time.Second):
			}
			readErr := fmt.Errorf("chrome closed the connection: %w", err)
			if reason := c.stderr.reason(); reason != "" {
				readErr = fmt.Errorf("%w; chrome said: %s", readErr, reason)
			}
			c.mu.Lock()
			c.readErr = readErr
			for id, ch := range c.pending {
				close(ch)
				delete(c.pending, id)
			}
			for _, s := range c.subs {
				if s.ch != nil {
					close(s.ch)
				}
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
			if s.method != m.Method || s.sessionID != m.SessionID {
				kept = append(kept, s)
				continue
			}
			params, _ := json.Marshal(m.Params)
			if s.ready != nil {
				// A stream never blocks the reader and never drops an
				// event. Its caller keeps the queue short.
				s.queue = append(s.queue, params)
				select {
				case s.ready <- struct{}{}:
				default:
				}
				kept = append(kept, s)
				continue
			}
			s.ch <- params
			close(s.ch)
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
		// A Chrome that exited closes its end, so the write fails with
		// a broken pipe. The reader's error says why Chrome exited, so
		// wait for the reader and return that error.
		select {
		case <-c.closed:
			c.mu.Lock()
			readErr := c.readErr
			c.mu.Unlock()
			return fmt.Errorf("%s: %w", method, readErr)
		case <-ctx.Done():
			return fmt.Errorf("%s: write: %w", method, err)
		}
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

// stream receives every event named method on one session, in order.
type stream struct {
	c *chrome
	s *subscription
}

// listen starts a stream. Like subscribe, start it before the call
// that causes the events. The reader queues each event until next
// reads it, so the caller must read about as fast as Chrome sends.
func (c *chrome) listen(method, sessionID string) *stream {
	s := &subscription{method: method, sessionID: sessionID, ready: make(chan struct{}, 1)}
	c.mu.Lock()
	c.subs = append(c.subs, s)
	c.mu.Unlock()
	return &stream{c: c, s: s}
}

// next returns the oldest event that is not read yet. It waits for
// one, and returns false when stop closes or the connection ends.
func (st *stream) next(stop <-chan struct{}) (json.RawMessage, bool) {
	for {
		st.c.mu.Lock()
		if len(st.s.queue) > 0 {
			raw := st.s.queue[0]
			st.s.queue = st.s.queue[1:]
			st.c.mu.Unlock()
			return raw, true
		}
		st.c.mu.Unlock()
		select {
		case <-st.s.ready:
		case <-stop:
			return nil, false
		case <-st.c.closed:
			return nil, false
		}
	}
}

// close removes the stream. The reader then drops its events.
func (st *stream) close() {
	st.c.mu.Lock()
	defer st.c.mu.Unlock()
	for i, s := range st.c.subs {
		if s == st.s {
			st.c.subs = append(st.c.subs[:i], st.c.subs[i+1:]...)
			return
		}
	}
}

// close asks Chrome to exit, waits a moment for it, kills it if it
// stayed, waits for the reader to see the pipe close, and removes the
// profile directory.
func (c *chrome) close() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = c.call(ctx, "", "Browser.close", nil, nil)
	c.w.Close()
	select {
	case <-c.exited:
	case <-ctx.Done():
		_ = c.cmd.Process.Kill()
		<-c.exited
	}
	<-c.closed
	os.RemoveAll(c.dataDir)
}
