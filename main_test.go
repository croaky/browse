package main

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/croaky/is"
)

func TestParseActions(t *testing.T) {
	is := is.New(t)

	got, err := parseActions([]string{"click=.tab", "wait=#drawer", "type=input[name=q]:acme", "sleep=250ms", "hover=a"})
	is.NoErr(err)
	is.Eq(len(got), 5)
	is.Eq(got[0], action{Name: "click", Arg: ".tab"})
	is.Eq(got[2], action{Name: "type", Arg: "input[name=q]:acme"})

	_, err = parseActions([]string{"tap=.x"})
	is.HasErr(err)
	is.True(strings.Contains(err.Error(), "unknown name \"tap\""))

	_, err = parseActions([]string{"click"})
	is.HasErr(err)
	is.True(strings.Contains(err.Error(), "want name=arg"))

	_, err = parseActions([]string{"type=input"})
	is.HasErr(err)
	is.True(strings.Contains(err.Error(), "type=selector:text"))

	// A selector with a colon in it: \: is one literal colon, and the
	// first colon the backslash does not escape divides.
	_, err = parseActions([]string{`type=li\:nth-child(2):hello`})
	is.NoErr(err)

	_, err = parseActions([]string{"sleep=soon"})
	is.HasErr(err)
}

func TestSplitType(t *testing.T) {
	is := is.New(t)

	sel, text, ok := splitType("#q:hello")
	is.True(ok)
	is.Eq(sel, "#q")
	is.Eq(text, "hello")

	// A colon in the text needs no escape: only the first one divides.
	sel, text, ok = splitType("#q:12:30")
	is.True(ok)
	is.Eq(sel, "#q")
	is.Eq(text, "12:30")

	// A colon in the selector takes a backslash.
	sel, text, ok = splitType(`li\:nth-child(2):hello`)
	is.True(ok)
	is.Eq(sel, "li:nth-child(2)")
	is.Eq(text, "hello")

	sel, text, ok = splitType(`input\:checked + label\:first-child:yes:no`)
	is.True(ok)
	is.Eq(sel, "input:checked + label:first-child")
	is.Eq(text, "yes:no")

	// No divider, and an empty selector, are both a refusal.
	_, _, ok = splitType("#q")
	is.True(!ok)
	_, _, ok = splitType(`li\:nth-child(2)`)
	is.True(!ok)
	_, _, ok = splitType(":hello")
	is.True(!ok)
}

func TestParseCookies(t *testing.T) {
	is := is.New(t)

	got, err := parseCookies("session=abc; theme = dark ;")
	is.NoErr(err)
	is.Eq(got, []cookie{{Name: "session", Value: "abc"}, {Name: "theme", Value: "dark"}})

	got, err = parseCookies("")
	is.NoErr(err)
	is.Eq(len(got), 0)

	_, err = parseCookies("nocookie")
	is.HasErr(err)
}

func TestParseHeaders(t *testing.T) {
	is := is.New(t)

	got, err := parseHeaders("Authorization: Bearer x:y\n\nX-Test: 1\n")
	is.NoErr(err)
	is.Eq(got, map[string]string{"Authorization": "Bearer x:y", "X-Test": "1"})

	_, err = parseHeaders("nocolon")
	is.HasErr(err)
}

func TestWriteGIF(t *testing.T) {
	is := is.New(t)

	jpg := func(c color.Color) []byte {
		img := image.NewRGBA(image.Rect(0, 0, 4, 3))
		draw.Draw(img, img.Bounds(), &image.Uniform{c}, image.Point{}, draw.Src)
		var buf bytes.Buffer
		is.NoErr(jpeg.Encode(&buf, img, nil))
		return buf.Bytes()
	}
	// Each frame shows until the next one, and the last until end. A
	// gap below 20ms writes the shortest delay a browser honors.
	frames := []frame{
		{JPEG: jpg(color.White), At: 100},
		{JPEG: jpg(color.Black), At: 100.5},
		{JPEG: jpg(color.White), At: 100.505},
	}
	var buf bytes.Buffer
	is.NoErr(writeGIF(&buf, frames, 101.505))
	g, err := gif.DecodeAll(&buf)
	is.NoErr(err)
	is.Eq(g.Delay, []int{50, 2, 100})
	is.Eq(g.Image[0].Bounds().Dx(), 4)
	is.Eq(g.Image[0].Bounds().Dy(), 3)

	// A frame that is not a JPEG is an error.
	is.HasErr(writeGIF(&buf, []frame{{JPEG: []byte("no"), At: 1}}, 2))

	// Frames of other sizes fit the last frame's canvas. A headful
	// Chrome sends 1280 by 813 frames for a 1280 by 900 viewport, and
	// a frame at half the width is one at a lower device scale.
	sized := func(w, h int, c color.Color) []byte {
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.Draw(img, img.Bounds(), &image.Uniform{c}, image.Point{}, draw.Src)
		var buf bytes.Buffer
		is.NoErr(jpeg.Encode(&buf, img, nil))
		return buf.Bytes()
	}
	buf.Reset()
	is.NoErr(writeGIF(&buf, []frame{
		{JPEG: sized(8, 5, color.Black), At: 1},
		{JPEG: sized(4, 3, color.Black), At: 2},
		{JPEG: sized(8, 6, color.Black), At: 3},
	}, 4))
	g, err = gif.DecodeAll(&buf)
	is.NoErr(err)
	is.Eq(g.Config.Width, 8)
	is.Eq(g.Config.Height, 6)
	for _, img := range g.Image {
		is.Eq(img.Bounds(), image.Rect(0, 0, 8, 6))
	}
	// The short frame keeps its aspect and pads the bottom row white.
	r, _, _, _ := g.Image[0].At(4, 5).RGBA()
	is.True(r > 0xf000)
	// The half-width frame scales up to fill the canvas width.
	r, _, _, _ = g.Image[1].At(7, 5).RGBA()
	is.True(r < 0x1000)
}

// TestVideoPath runs everywhere: run refuses the path before it
// starts the browser.
func TestVideoPath(t *testing.T) {
	is := is.New(t)

	err := run([]string{"-video", "run.mp4", "http://localhost/"})
	is.HasErr(err)
	is.True(strings.Contains(err.Error(), `-video "run.mp4": want a path that ends in .gif`))
}

// TestChromeExits runs everywhere: a fake Chrome writes the line the
// real shell writes on Ubuntu with no sandbox, then exits. The error
// carries that line, not only EOF.
func TestChromeExits(t *testing.T) {
	is := is.New(t)

	fake := filepath.Join(t.TempDir(), "chrome")
	script := "#!/bin/sh\necho 'starting' >&2\necho '[1:FATAL:zygote_host_impl_linux.cc:129] No usable sandbox!' >&2\necho 'stack trace' >&2\nexit 1\n"
	is.NoErr(os.WriteFile(fake, []byte(script), 0o755))

	err := run([]string{"-chrome", fake, "-out", filepath.Join(t.TempDir(), "x.png"), "http://localhost/"})
	is.HasErr(err)
	t.Log(err)
	is.True(strings.Contains(err.Error(), "chrome said: [1:FATAL:zygote_host_impl_linux.cc:129] No usable sandbox!"))
}

func TestSlug(t *testing.T) {
	is := is.New(t)

	for in, want := range map[string]string{
		"http://x/":                        "index",
		"http://x":                         "index",
		"http://x/help":                    "help",
		"http://x/companies/1234?tab=docs": "companies-1234",
		"http://x/Admin/Users/":            "admin-users",
		"http://x/a__b//c":                 "a-b-c",
	} {
		u, err := url.Parse(in)
		is.NoErr(err)
		is.Eq(slug(u), want)
	}
}

// TestBrowse drives the real browser against a local server. It skips
// under -short, which is how CI runs it: a runner would download the
// shell on every cold cache and may lack its shared libraries. The
// server records the cookie and header it received, the page has a
// button that reveals a box, and the test asserts on the record and
// on the PNG dimensions.
func TestBrowse(t *testing.T) {
	if testing.Short() {
		t.Skip("-short: skipping the browser run")
	}
	is := is.New(t)

	var mu sync.Mutex
	var gotCookie, gotHeader, gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if c, err := r.Cookie("session"); err == nil {
			gotCookie = c.Value
		}
		gotHeader = r.Header.Get("X-Browse")
		gotUA = r.UserAgent()
		mu.Unlock()
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html>
<title>browse test</title>
<style>#box { display: none; width: 100px; height: 100px; background: #0079ff }</style>
<button id="reveal" onclick="document.getElementById('box').style.display='block'">Reveal</button>
<div id="box"></div>
<input id="q">
<div id="touch" style="display:none;width:10px;height:10px"></div>
<script>if (navigator.maxTouchPoints > 0) document.getElementById('touch').style.display='block'</script>`))
	}))
	defer srv.Close()

	// -video ends on the result of the last action, with no sleep after
	// it. The click turns the page red. A GIF that stops before Chrome
	// paints the click shows white, or has no frame at all.
	paint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html>
<body style="margin:0;background:#fff">
<button id="paint" onclick="document.body.style.background='#f00'">Paint</button>`))
	}))
	defer paint.Close()
	for i := range 4 {
		video := filepath.Join(t.TempDir(), "paint.gif")
		err := run([]string{"-width", "320", "-height", "240", "-out", filepath.Join(t.TempDir(), "x.png"), "-video", video, paint.URL + "/", "click=#paint"})
		is.NoErr(err)
		f, err := os.Open(video)
		is.NoErr(err)
		g, err := gif.DecodeAll(f)
		f.Close()
		is.NoErr(err)
		last := g.Image[len(g.Image)-1]
		r, gr, b, _ := last.At(300, 220).RGBA()
		t.Logf("run %d: %d frames, last frame corner %x %x %x", i, len(g.Image), r>>8, gr>>8, b>>8)
		is.True(r > 0xc000 && gr < 0x4000 && b < 0x4000)
	}

	dir := t.TempDir()
	out := filepath.Join(dir, "shot.png")
	video := filepath.Join(dir, "run.gif")
	t.Setenv("BROWSE_COOKIE", "session=abc123")
	t.Setenv("BROWSE_HEADER", "X-Browse: yes")

	err := run([]string{
		"-width", "640", "-height", "480", "-out", out, "-steps", "-video", video,
		srv.URL + "/page",
		"click=#reveal", "wait=#box", "type=#q:hello", "sleep=50ms",
	})
	is.NoErr(err)

	// -steps: one PNG after each action, named for its place and name.
	for _, name := range []string{"shot-1-click.png", "shot-2-wait.png", "shot-3-type.png", "shot-4-sleep.png"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		is.NoErr(err)
		_, err = png.Decode(bytes.NewReader(data))
		is.NoErr(err)
	}

	// -video: a GIF at the viewport size with at least one frame.
	f, err := os.Open(video)
	is.NoErr(err)
	g, err := gif.DecodeAll(f)
	f.Close()
	is.NoErr(err)
	is.True(len(g.Image) > 0)
	is.Eq(g.Config.Width, 640)
	is.Eq(g.Config.Height, 480)

	mu.Lock()
	is.Eq(gotCookie, "abc123")
	is.Eq(gotHeader, "yes")
	mu.Unlock()

	data, err := os.ReadFile(out)
	is.NoErr(err)
	img, err := png.Decode(bytes.NewReader(data))
	is.NoErr(err)
	is.Eq(img.Bounds().Dx(), 640)
	is.Eq(img.Bounds().Dy(), 480)
	mu.Lock()
	is.True(!strings.Contains(gotUA, "iPhone"))
	mu.Unlock()

	// -phone: two device pixels per CSS pixel, a phone user agent on the
	// request, and a touch screen the page can see. wait=#touch is the
	// assertion on touch: the box shows only when maxTouchPoints > 0.
	err = run([]string{"-phone", "-out", out, srv.URL + "/page", "wait=#touch"})
	is.NoErr(err)
	mu.Lock()
	is.True(strings.Contains(gotUA, "iPhone"))
	mu.Unlock()
	data, err = os.ReadFile(out)
	is.NoErr(err)
	img, err = png.Decode(bytes.NewReader(data))
	is.NoErr(err)
	is.Eq(img.Bounds().Dx(), 780)
	is.Eq(img.Bounds().Dy(), 1688)

	// The action that fails names itself and the selector.
	err = run([]string{"-out", out, srv.URL + "/page", "click=#missing"})
	is.HasErr(err)
	is.True(strings.Contains(err.Error(), `click=#missing: no element matches "#missing"`))

	// A wait spends its own budget, not the run's, and says how long
	// it waited. #box is in the page but hidden until the click.
	err = run([]string{"-out", out, "-wait", "200ms", srv.URL + "/page", "wait=#box"})
	is.HasErr(err)
	is.True(strings.Contains(err.Error(), `wait=#box: no element matches "#box" and is visible after 200ms`))

	// A server that is not there is an error before any action, and
	// the message carries Chrome's reason.
	srv.Close()
	err = run([]string{"-out", out, srv.URL + "/page"})
	is.HasErr(err)
	is.True(strings.Contains(err.Error(), "ERR_CONNECTION_REFUSED"))
}
