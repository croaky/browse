// Command browse opens a page in headless Chrome, runs a short list of
// actions, and writes a PNG. It is for an agent or a person who wants
// to see a page of a local app without a browser window.
//
//	browse [flags] <url> [action...]
//
// Actions run in order after the page loads and before the screenshot:
//
//	click=<selector>       click the first match
//	hover=<selector>       move the pointer over the first match
//	type=<selector>:<text> focus the first match and type text
//	wait=<selector>        wait until a match is visible
//	sleep=<duration>       wait a fixed time, for a transition
//
// The first colon in a type argument divides the selector from the
// text. Write a colon in the selector as \: , as in
// type=li\:nth-child(2):hello. The text needs no escape.
//
// click, hover, and type act on the element that is there now, and no
// match is an error at once. A step that needs an element the page has
// still to render takes a wait before it. One wait has -wait, five
// seconds by default, and the whole run has -timeout.
//
// The viewport is 1280 by 900 at one device pixel per CSS pixel.
// -phone is 390 by 844 at two, with a touch screen and a phone user
// agent, for a page built for a phone first.
//
// Auth comes in through the environment, not flags, so a secret is
// not in a process list or a shell history:
//
//	BROWSE_COOKIE  cookies for the URL's host: name=value; name2=value2
//	BROWSE_HEADER  headers on every request, one per line: Name: value
//
// The browser is a pinned chrome-headless-shell from Chrome for
// Testing, downloaded once into the user cache directory. -chrome or
// BROWSE_CHROME names another binary. The profile is a temporary
// directory that the run removes, so no cookie or history of the
// user's own is read or written.
//
// On success it prints the path it wrote and nothing else. On failure
// it prints why and exits 1.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "browse:", err)
		os.Exit(1)
	}
}

func usage(fs *flag.FlagSet) {
	fmt.Fprintf(fs.Output(), "usage: browse [flags] <url> [action...]\n\n")
	fmt.Fprintf(fs.Output(), "actions:\n")
	fmt.Fprintf(fs.Output(), "  click=<selector>        click the first match\n")
	fmt.Fprintf(fs.Output(), "  hover=<selector>        move the pointer over the first match\n")
	fmt.Fprintf(fs.Output(), "  type=<selector>:<text>  focus the first match and type text\n")
	fmt.Fprintf(fs.Output(), "  wait=<selector>         wait until a match is visible\n")
	fmt.Fprintf(fs.Output(), "  sleep=<duration>        wait a fixed time\n\n")
	fmt.Fprintf(fs.Output(), "The first colon in a type argument divides the selector from the\n")
	fmt.Fprintf(fs.Output(), "text. Write a colon in the selector as \\: .\n\n")
	fmt.Fprintf(fs.Output(), "environment:\n")
	fmt.Fprintf(fs.Output(), "  BROWSE_COOKIE  cookies for the URL's host: name=value; name2=value2\n")
	fmt.Fprintf(fs.Output(), "  BROWSE_HEADER  headers on every request, one per line: Name: value\n")
	fmt.Fprintf(fs.Output(), "  BROWSE_CHROME  a Chrome binary, instead of the pinned headless shell\n\n")
	fmt.Fprintf(fs.Output(), "flags:\n")
	fs.PrintDefaults()
}

func run(args []string) error {
	fs := flag.NewFlagSet("browse", flag.ContinueOnError)
	fs.Usage = func() { usage(fs) }
	width := fs.Int("width", 1280, "viewport width in CSS pixels")
	height := fs.Int("height", 900, "viewport height in CSS pixels")
	phone := fs.Bool("phone", false, "a phone: 390 by 844 at 2x, touch, and a mobile user agent")
	full := fs.Bool("full", false, "capture the whole document, not the viewport")
	out := fs.String("out", "", "PNG path; default is <slug>.png from the URL path")
	chromeFlag := fs.String("chrome", "", "a Chrome binary, instead of the pinned headless shell")
	timeout := fs.Duration("timeout", 30*time.Second, "limit for the whole run")
	wait := fs.Duration("wait", 5*time.Second, "limit for one wait action")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		usage(fs)
		return errors.New("a URL is required")
	}
	rawURL := fs.Arg(0)
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("url %q: want an absolute http or https URL", rawURL)
	}
	actions, err := parseActions(fs.Args()[1:])
	if err != nil {
		return err
	}
	cookies, err := parseCookies(os.Getenv("BROWSE_COOKIE"))
	if err != nil {
		return err
	}
	headers, err := parseHeaders(os.Getenv("BROWSE_HEADER"))
	if err != nil {
		return err
	}
	outPath := *out
	if outPath == "" {
		outPath = slug(u) + ".png"
	}
	v := viewport{Width: *width, Height: *height, Scale: 1}
	if *phone {
		v = viewport{Width: 390, Height: 844, Scale: 2, Mobile: true}
		// -width or -height beside -phone narrows or lengthens the phone.
		fs.Visit(func(f *flag.Flag) {
			switch f.Name {
			case "width":
				v.Width = *width
			case "height":
				v.Height = *height
			}
		})
	}

	// The download on a first run is outside the timeout: it is a
	// one-time cost measured in the network, not in the page.
	path, err := chromePath(context.Background(), *chromeFlag)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	c, err := launch(ctx, path, v.Width, v.Height)
	if err != nil {
		return err
	}
	defer c.close()

	p, err := open(ctx, c, v)
	if err != nil {
		return err
	}
	if err := p.setCookies(ctx, rawURL, cookies); err != nil {
		return err
	}
	if err := p.setHeaders(ctx, headers); err != nil {
		return err
	}
	if err := p.navigate(ctx, rawURL); err != nil {
		return err
	}
	if err := p.run(ctx, actions, *wait); err != nil {
		return err
	}
	png, err := p.screenshot(ctx, *full)
	if err != nil {
		return err
	}
	if dir := filepath.Dir(outPath); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if err := os.WriteFile(outPath, png, 0o644); err != nil {
		return err
	}
	fmt.Println(outPath)
	return nil
}

// parseCookies reads BROWSE_COOKIE: name=value pairs separated by
// semicolons, the same shape as a Cookie request header.
func parseCookies(s string) ([]cookie, error) {
	var out []cookie
	for part := range strings.SplitSeq(s, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, value, ok := strings.Cut(part, "=")
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			return nil, fmt.Errorf("BROWSE_COOKIE: %q: want name=value", part)
		}
		out = append(out, cookie{Name: name, Value: strings.TrimSpace(value)})
	}
	return out, nil
}

// parseHeaders reads BROWSE_HEADER: one "Name: value" per line.
func parseHeaders(s string) (map[string]string, error) {
	out := map[string]string{}
	for line := range strings.SplitSeq(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, value, ok := strings.Cut(line, ":")
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			return nil, fmt.Errorf("BROWSE_HEADER: %q: want Name: value", line)
		}
		out[name] = strings.TrimSpace(value)
	}
	return out, nil
}

// slug names the output file for a URL: the path, lowercased, with
// each run of characters outside [a-z0-9] as one hyphen. The root is
// "index". The query is left out, so two views of one page share a
// name and the later run replaces the earlier.
func slug(u *url.URL) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(u.Path) {
		alnum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if alnum {
			b.WriteRune(r)
			dash = false
			continue
		}
		if b.Len() > 0 && !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	s := strings.TrimSuffix(b.String(), "-")
	if s == "" {
		return "index"
	}
	return s
}
