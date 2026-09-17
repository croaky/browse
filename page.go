package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// page is one tab, attached as a flat session so every call carries
// its sessionId.
type page struct {
	c         *chrome
	sessionID string
}

// cookie is one cookie the run sets before the first navigation. The
// URL scopes it, so Chrome sends it to that host and no other.
type cookie struct {
	Name  string
	Value string
}

// viewport is the screen the page renders into.
type viewport struct {
	Width  int
	Height int
	// Scale is device pixels per CSS pixel. 1 for a desktop; 2 for a
	// phone, so text in the PNG is as crisp as on the device.
	Scale int
	// Mobile turns on the mobile viewport, a touch screen, and a phone
	// user agent, so a page that branches on any of them takes the
	// phone branch.
	Mobile bool
}

// phoneUserAgent is what a page sees from -phone. An iPhone rather than
// an Android because that is what a mobile-first page here is checked
// against first, and one string is enough to take a UA-sniffed branch.
const phoneUserAgent = "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) " +
	"AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1"

// open creates a tab and attaches to it. It sets the viewport and
// nothing else: cookies and headers are the caller's next calls.
func open(ctx context.Context, c *chrome, v viewport) (*page, error) {
	var t struct {
		TargetID string `json:"targetId"`
	}
	if err := c.call(ctx, "", "Target.createTarget", map[string]any{"url": "about:blank"}, &t); err != nil {
		return nil, err
	}
	var a struct {
		SessionID string `json:"sessionId"`
	}
	params := map[string]any{"targetId": t.TargetID, "flatten": true}
	if err := c.call(ctx, "", "Target.attachToTarget", params, &a); err != nil {
		return nil, err
	}
	p := &page{c: c, sessionID: a.SessionID}
	if err := p.call(ctx, "Page.enable", nil, nil); err != nil {
		return nil, err
	}
	metrics := map[string]any{
		"width":             v.Width,
		"height":            v.Height,
		"deviceScaleFactor": v.Scale,
		"mobile":            v.Mobile,
	}
	if err := p.call(ctx, "Emulation.setDeviceMetricsOverride", metrics, nil); err != nil {
		return nil, err
	}
	if v.Mobile {
		touch := map[string]any{"enabled": true, "maxTouchPoints": 5}
		if err := p.call(ctx, "Emulation.setTouchEmulationEnabled", touch, nil); err != nil {
			return nil, err
		}
		ua := map[string]any{"userAgent": phoneUserAgent, "platform": "iPhone"}
		if err := p.call(ctx, "Emulation.setUserAgentOverride", ua, nil); err != nil {
			return nil, err
		}
	}
	return p, nil
}

func (p *page) call(ctx context.Context, method string, params any, out any) error {
	return p.c.call(ctx, p.sessionID, method, params, out)
}

// setCookies sets each cookie for url. Chrome answers success false
// rather than an error for a cookie it will not store, such as a
// Secure cookie on an http URL, so that answer becomes the error.
func (p *page) setCookies(ctx context.Context, url string, cookies []cookie) error {
	for _, ck := range cookies {
		var res struct {
			Success bool `json:"success"`
		}
		params := map[string]any{"name": ck.Name, "value": ck.Value, "url": url}
		if err := p.call(ctx, "Network.setCookie", params, &res); err != nil {
			return err
		}
		if !res.Success {
			return fmt.Errorf("chrome refused cookie %s for %s", ck.Name, url)
		}
	}
	return nil
}

// setHeaders adds headers to every request the page makes.
func (p *page) setHeaders(ctx context.Context, headers map[string]string) error {
	if len(headers) == 0 {
		return nil
	}
	if err := p.call(ctx, "Network.enable", nil, nil); err != nil {
		return err
	}
	return p.call(ctx, "Network.setExtraHTTPHeaders", map[string]any{"headers": headers}, nil)
}

// navigate loads url and returns after the load event. A navigation
// Chrome could not start, such as a refused connection, comes back as
// errorText on the reply rather than as an error, so it is checked.
func (p *page) navigate(ctx context.Context, url string) error {
	loaded := p.c.subscribe("Page.loadEventFired", p.sessionID)
	var res struct {
		ErrorText string `json:"errorText"`
	}
	if err := p.call(ctx, "Page.navigate", map[string]any{"url": url}, &res); err != nil {
		return err
	}
	if res.ErrorText != "" {
		return fmt.Errorf("navigate %s: %s", url, res.ErrorText)
	}
	select {
	case <-loaded:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("navigate %s: no load event: %w", url, ctx.Err())
	}
}

// eval runs a JavaScript expression in the page and decodes its value
// into out. A thrown exception is the error.
func (p *page) eval(ctx context.Context, expr string, out any) error {
	var res struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text      string `json:"text"`
			Exception *struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}
	params := map[string]any{"expression": expr, "returnByValue": true, "awaitPromise": true}
	if err := p.call(ctx, "Runtime.evaluate", params, &res); err != nil {
		return err
	}
	if ex := res.ExceptionDetails; ex != nil {
		msg := ex.Text
		if ex.Exception != nil && ex.Exception.Description != "" {
			msg = ex.Exception.Description
		}
		return fmt.Errorf("javascript: %s", msg)
	}
	if out != nil && len(res.Result.Value) > 0 {
		return json.Unmarshal(res.Result.Value, out)
	}
	return nil
}

// point is the center of an element in viewport coordinates.
type point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// center scrolls the first match of selector into view and returns its
// center. No match is an error that names the selector.
func (p *page) center(ctx context.Context, selector string) (point, error) {
	sel, _ := json.Marshal(selector)
	expr := fmt.Sprintf(`(() => {
		const el = document.querySelector(%s);
		if (!el) return null;
		el.scrollIntoView({block: "center", inline: "nearest"});
		const r = el.getBoundingClientRect();
		return {x: r.left + r.width / 2, y: r.top + r.height / 2};
	})()`, sel)
	var pt *point
	if err := p.eval(ctx, expr, &pt); err != nil {
		return point{}, err
	}
	if pt == nil {
		return point{}, fmt.Errorf("no element matches %q", selector)
	}
	return *pt, nil
}

func (p *page) mouse(ctx context.Context, kind string, pt point, button string) error {
	params := map[string]any{"type": kind, "x": pt.X, "y": pt.Y}
	if button != "" {
		params["button"] = button
		params["clickCount"] = 1
	}
	return p.call(ctx, "Input.dispatchMouseEvent", params, nil)
}

// click moves the pointer to the element and presses the left button.
// A real pointer event rather than element.click(), so a handler that
// reads the event's coordinates or listens for mousedown sees what a
// user's click would give it.
func (p *page) click(ctx context.Context, selector string) error {
	pt, err := p.center(ctx, selector)
	if err != nil {
		return err
	}
	if err := p.mouse(ctx, "mouseMoved", pt, ""); err != nil {
		return err
	}
	if err := p.mouse(ctx, "mousePressed", pt, "left"); err != nil {
		return err
	}
	return p.mouse(ctx, "mouseReleased", pt, "left")
}

// hover moves the pointer over the element and leaves it there, so a
// :hover rule or a mouseenter handler is in effect for the screenshot.
func (p *page) hover(ctx context.Context, selector string) error {
	pt, err := p.center(ctx, selector)
	if err != nil {
		return err
	}
	return p.mouse(ctx, "mouseMoved", pt, "")
}

// typeText focuses the element and inserts text as typed input, so
// input and change handlers fire as they do for a keyboard.
func (p *page) typeText(ctx context.Context, selector, text string) error {
	sel, _ := json.Marshal(selector)
	expr := fmt.Sprintf(`(() => {
		const el = document.querySelector(%s);
		if (!el) return false;
		el.focus();
		return true;
	})()`, sel)
	var found bool
	if err := p.eval(ctx, expr, &found); err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("no element matches %q", selector)
	}
	return p.call(ctx, "Input.insertText", map[string]any{"text": text}, nil)
}

// waitFor polls until an element matches selector and has a box on
// the page, or the context ends. Presence alone is not enough: a
// hidden element matches querySelector and shows nothing.
func (p *page) waitFor(ctx context.Context, selector string) error {
	sel, _ := json.Marshal(selector)
	expr := fmt.Sprintf(`(() => {
		const el = document.querySelector(%s);
		return !!el && el.getClientRects().length > 0;
	})()`, sel)
	for {
		var visible bool
		if err := p.eval(ctx, expr, &visible); err != nil {
			return err
		}
		if visible {
			return nil
		}
		select {
		case <-time.After(100 * time.Millisecond):
		case <-ctx.Done():
			return fmt.Errorf("wait for %q: %w", selector, ctx.Err())
		}
	}
}

// screenshot returns a PNG of the viewport, or of the whole document
// when full is set.
func (p *page) screenshot(ctx context.Context, full bool) ([]byte, error) {
	params := map[string]any{"format": "png"}
	if full {
		var m struct {
			Size struct {
				Width  float64 `json:"width"`
				Height float64 `json:"height"`
			} `json:"cssContentSize"`
		}
		if err := p.call(ctx, "Page.getLayoutMetrics", nil, &m); err != nil {
			return nil, err
		}
		params["captureBeyondViewport"] = true
		params["clip"] = map[string]any{
			"x": 0, "y": 0, "width": m.Size.Width, "height": m.Size.Height, "scale": 1,
		}
	}
	var res struct {
		Data string `json:"data"`
	}
	if err := p.call(ctx, "Page.captureScreenshot", params, &res); err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(res.Data)
}

// action is one step between the load and the screenshot.
type action struct {
	Name string
	Arg  string
}

// actionNames is the vocabulary, in the order the usage prints it.
var actionNames = []string{"click", "hover", "type", "wait", "sleep"}

// parseActions reads the arguments after the URL. Each is name=arg;
// a name outside the vocabulary or an empty arg is an error that
// says what the vocabulary is.
func parseActions(args []string) ([]action, error) {
	var out []action
	for _, a := range args {
		name, arg, ok := strings.Cut(a, "=")
		if !ok || arg == "" {
			return nil, fmt.Errorf("action %q: want name=arg, one of %s", a, strings.Join(actionNames, ", "))
		}
		switch name {
		case "click", "hover", "wait":
		case "type":
			if _, _, ok := strings.Cut(arg, ":"); !ok {
				return nil, fmt.Errorf("action %q: want type=selector:text", a)
			}
		case "sleep":
			if _, err := time.ParseDuration(arg); err != nil {
				return nil, fmt.Errorf("action %q: %w", a, err)
			}
		default:
			return nil, fmt.Errorf("action %q: unknown name %q, want one of %s", a, name, strings.Join(actionNames, ", "))
		}
		out = append(out, action{Name: name, Arg: arg})
	}
	return out, nil
}

// run performs the actions in order. An error names the action that
// failed, so a list of five says which one.
func (p *page) run(ctx context.Context, actions []action) error {
	for _, a := range actions {
		var err error
		switch a.Name {
		case "click":
			err = p.click(ctx, a.Arg)
		case "hover":
			err = p.hover(ctx, a.Arg)
		case "type":
			sel, text, _ := strings.Cut(a.Arg, ":")
			err = p.typeText(ctx, sel, text)
		case "wait":
			err = p.waitFor(ctx, a.Arg)
		case "sleep":
			d, _ := time.ParseDuration(a.Arg)
			select {
			case <-time.After(d):
			case <-ctx.Done():
				err = ctx.Err()
			}
		default:
			err = errors.New("unknown action")
		}
		if err != nil {
			return fmt.Errorf("%s=%s: %w", a.Name, a.Arg, err)
		}
	}
	return nil
}
