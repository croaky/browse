package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

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

	// select divides as type does.
	got, err = parseActions([]string{"select=#rating:8"})
	is.NoErr(err)
	is.Eq(got[0], action{Name: "select", Arg: "#rating:8"})
	_, err = parseActions([]string{"select=#rating"})
	is.HasErr(err)
	is.True(strings.Contains(err.Error(), "select=selector:value"))
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

// TestHeaderGone runs everywhere: run refuses BROWSE_HEADER before it
// starts the browser. Chrome sent that header to every host the page
// contacted, so a user who still sets it learns why the login stops.
func TestHeaderGone(t *testing.T) {
	is := is.New(t)

	t.Setenv("BROWSE_HEADER", "Authorization: Bearer abc123")
	err := run([]string{"-video", "run.mp4", "http://localhost/"})
	is.HasErr(err)
	is.Eq(err.Error(), "BROWSE_HEADER is gone: Chrome sent it to every host the page contacted; use BROWSE_COOKIE, which goes to the URL's host only")
}

// TestCheckURL runs everywhere. Only http and https pass. A file URL
// with a host would load a local file into the PNG, and a chrome URL
// can crash or hang the browser.
func TestCheckURL(t *testing.T) {
	is := is.New(t)

	for _, ok := range []string{"http://localhost/", "HTTPS://example.com/", "http://127.0.0.1:3000/a?b=c"} {
		_, err := checkURL(ok)
		is.NoErr(err)
	}
	for _, bad := range []string{
		"file://localhost/etc/passwd",
		"chrome://version",
		"javascript://x/alert(1)",
		"view-source:http://localhost/",
		"/relative",
		"http:///nohost",
	} {
		_, err := checkURL(bad)
		is.HasErr(err)
		is.Eq(err.Error(), "url "+strconv.Quote(bad)+": want an absolute http or https URL")
	}
}

// TestPageText runs everywhere. A string from the page goes into an
// error with its control characters escaped and its length bounded,
// so it cannot move the cursor or start a new line on stderr.
func TestPageText(t *testing.T) {
	is := is.New(t)

	is.Eq(pageText("input"), "input")
	is.Eq(pageText("Café menu"), "Café menu")
	is.Eq(pageText("\x1b[31mred\nbrowse: forged\u009b"), `\x1b[31mred\nbrowse: forged\u009b`)

	long := pageText(strings.Repeat("é", 300))
	is.True(strings.HasSuffix(long, "..."))
	is.True(len(long) <= 203)
	is.True(utf8.ValidString(long))
}

func TestWriteGIF(t *testing.T) {
	is := is.New(t)

	sized := func(w, h int, c color.Color) []byte {
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.Draw(img, img.Bounds(), &image.Uniform{c}, image.Point{}, draw.Src)
		var buf bytes.Buffer
		is.NoErr(png.Encode(&buf, img))
		return buf.Bytes()
	}
	// Each frame shows until the next one, and the last until end. A
	// gap below 20ms writes the shortest delay a browser honors.
	frames := []frame{
		{PNG: sized(4, 3, color.White), At: 100},
		{PNG: sized(4, 3, color.Black), At: 100.5},
		{PNG: sized(4, 3, color.White), At: 100.505},
	}
	var buf bytes.Buffer
	is.NoErr(writeGIF(&buf, frames, 101.505))
	g, err := gif.DecodeAll(&buf)
	is.NoErr(err)
	is.Eq(g.Delay, []int{50, 2, 100})
	is.Eq(g.Image[0].Bounds().Dx(), 4)
	is.Eq(g.Image[0].Bounds().Dy(), 3)

	// A frame that is not a PNG is an error.
	is.HasErr(writeGIF(&buf, []frame{{PNG: []byte("no"), At: 1}}, 2))

	// A page color that no fixed palette holds keeps its exact value.
	navy := color.RGBA{0x0b, 0x1f, 0x6b, 0xff}
	buf.Reset()
	is.NoErr(writeGIF(&buf, []frame{{PNG: sized(4, 3, navy), At: 1}}, 2))
	g, err = gif.DecodeAll(&buf)
	is.NoErr(err)
	is.Eq(color.RGBAModel.Convert(g.Image[0].At(0, 0)), color.Color(navy))

	// A rare color keeps a slot. 300 greys of 10 pixels each fill more
	// than 256 slots, and 5 red pixels, as in a small avatar, follow.
	img := image.NewRGBA(image.Rect(0, 0, 3005, 1))
	for i := range 3000 {
		v := uint8(i / 10 / 2)
		img.Set(i, 0, color.RGBA{v, v, v + uint8(i/10%2), 0xff})
	}
	red := color.RGBA{0xd0, 0x20, 0x20, 0xff}
	for i := 3000; i < 3005; i++ {
		img.Set(i, 0, red)
	}
	var enc bytes.Buffer
	is.NoErr(png.Encode(&enc, img))
	buf.Reset()
	is.NoErr(writeGIF(&buf, []frame{{PNG: enc.Bytes(), At: 1}}, 2))
	g, err = gif.DecodeAll(&buf)
	is.NoErr(err)
	is.Eq(color.RGBAModel.Convert(g.Image[0].At(3004, 0)), color.Color(red))

	// Frames of other sizes fit the last frame's canvas. A headful
	// Chrome sends 1280 by 813 frames for a 1280 by 900 viewport, and
	// a frame at half the width is one at a lower device scale.
	buf.Reset()
	is.NoErr(writeGIF(&buf, []frame{
		{PNG: sized(8, 5, color.Black), At: 1},
		{PNG: sized(4, 3, color.Black), At: 2},
		{PNG: sized(8, 6, color.Black), At: 3},
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

// TestPixel runs everywhere. The direct read of *image.RGBA and
// *image.NRGBA gives the same color as src.At, alpha included, so a
// GIF frame does not change with the image type that image/png picks.
func TestPixel(t *testing.T) {
	is := is.New(t)

	r := image.Rect(2, 1, 6, 4)
	srcs := []image.Image{image.NewRGBA(r), image.NewNRGBA(r)}
	for _, src := range srcs {
		m := src.(draw.Image)
		for y := r.Min.Y; y < r.Max.Y; y++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				m.Set(x, y, color.NRGBA{uint8(x * 40), uint8(y * 60), 0xc8, uint8(0x30 + x*y*20)})
			}
		}
		at := pixel(src)
		for y := r.Min.Y; y < r.Max.Y; y++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				cr, cg, cb, _ := src.At(x, y).RGBA()
				is.Eq(at(x, y), color.RGBA{uint8(cr >> 8), uint8(cg >> 8), uint8(cb >> 8), 0xff})
			}
		}
	}
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

// TestStderrReason runs everywhere. The headless shell writes a page's
// console text to its stderr as an INFO:CONSOLE line, with the page's
// bytes raw. Only a FATAL log header picks a line, and the line comes
// back escaped and cut, so the page cannot shape the error.
func TestStderrReason(t *testing.T) {
	is := is.New(t)

	var tail stderrTail
	_, _ = tail.Write([]byte("[1:FATAL:zygote_host_impl_linux.cc:129] No usable sandbox!\n" +
		"[1109/204100.454268:INFO:CONSOLE:1] \"FATAL \x1b[31mred\rbrowse: forged\", source: http://x/ (1)\n"))
	is.Eq(tail.reason(), "[1:FATAL:zygote_host_impl_linux.cc:129] No usable sandbox!")

	// With no FATAL header, the last line is the reason, escaped.
	var page stderrTail
	_, _ = page.Write([]byte("[1109/204100.454268:INFO:CONSOLE:1] \"FATAL \x1b[31mred\rbrowse: forged" + strings.Repeat("x", 300) + "\"\n"))
	got := page.reason()
	is.True(!strings.ContainsAny(got, "\x1b\r\n"))
	is.True(strings.Contains(got, `\x1b[31mred\rbrowse: forged`))
	// pageText cuts the line to 200 bytes before it escapes them.
	is.True(strings.HasSuffix(got, "..."))
	is.True(len(got) < 300)
}

// TestCallAfterDeadline runs everywhere. When the run's deadline kills
// Chrome during Go work, such as a long GIF encode, the next call
// reports the timeout, not Chrome's stderr.
func TestCallAfterDeadline(t *testing.T) {
	is := is.New(t)

	c := &chrome{
		pending: map[int]chan incoming{},
		readErr: errors.New("chrome closed the connection: EOF; chrome said: page text"),
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-ctx.Done()
	err := c.call(ctx, "", "Page.captureScreenshot", nil, nil)
	is.True(errors.Is(err, context.DeadlineExceeded))
	is.True(!strings.Contains(err.Error(), "chrome said"))
}

// replyPipe is the fd 3 end of a fake Chrome. It keeps each message
// that call writes and answers with reply on the fd 4 end.
type replyPipe struct {
	w     *io.PipeWriter
	reply string
	got   chan string
}

func (p *replyPipe) Write(b []byte) (int, error) {
	p.got <- string(b)
	go p.w.Write([]byte(p.reply + "\x00"))
	return len(b), nil
}

func (p *replyPipe) Close() error { return p.w.Close() }

// TestCallInvalidUTF8 runs everywhere. A page string can hold a lone
// UTF-16 surrogate, and a selector from the command line can hold
// invalid UTF-8. call sends and receives both with U+FFFD in place,
// as encoding/json v1 did, and does not wait for a reply it dropped.
func TestCallInvalidUTF8(t *testing.T) {
	is := is.New(t)

	r, w := io.Pipe()
	pipe := &replyPipe{w: w, reply: `{"id":1,"result":{"value":"a\ud800b"}}`, got: make(chan string, 1)}
	exited := make(chan struct{})
	close(exited)
	c := &chrome{
		w:       pipe,
		r:       bufio.NewReader(r),
		stderr:  &stderrTail{},
		exited:  exited,
		pending: map[int]chan incoming{},
		closed:  make(chan struct{}),
	}
	go c.read()
	defer w.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var res struct {
		Value string `json:"value"`
	}
	is.NoErr(c.call(ctx, "", "Runtime.evaluate", map[string]any{"expression": "a\xffb"}, &res))
	is.Eq(res.Value, "a\uFFFDb")
	is.Eq(<-pipe.got, `{"id":1,"method":"Runtime.evaluate","params":{"expression":"a`+"\uFFFD"+`b"}}`+"\x00")
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
// server records the cookie it received, the page has a button that
// reveals a box, and the test asserts on the record and on the PNG
// dimensions.
func TestBrowse(t *testing.T) {
	if testing.Short() {
		t.Skip("-short: skipping the browser run")
	}
	is := is.New(t)

	var mu sync.Mutex
	var gotCookie, gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if c, err := r.Cookie("session"); err == nil {
			gotCookie = c.Value
		}
		gotUA = r.UserAgent()
		mu.Unlock()
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/hostile" {
			// A page that writes terminal control sequences and a forged
			// browse: line into the two errors it can shape.
			_, _ = w.Write([]byte(`<!doctype html>
<select id="evil"><option value="x" label="&#27;[31mred&#10;browse: forged">x</option></select>
<button id="b">b</button>
<script>Element.prototype.scrollIntoView = () => { throw new Error("\x1b[2J\nbrowse: forged") }</script>`))
			return
		}
		_, _ = w.Write([]byte(`<!doctype html>
<title>browse test</title>
<style>#box { display: none; width: 100px; height: 100px; background: #0079ff }</style>
<button id="reveal" onclick="document.getElementById('box').style.display='block'">Reveal</button>
<div id="box"></div>
<input id="q">
<select id="pick" onchange="document.getElementById('picked').style.display = this.value === 'b' ? 'block' : 'none'">
<option value="">None</option><option value="a">Alpha</option><option value="b">Beta</option>
</select>
<div id="picked" style="display:none;width:10px;height:10px"></div>
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

	// select picks an option by its value or its label, and fires
	// change: #picked shows only when the page's handler sees "b".
	err = run([]string{"-out", out, "-wait", "200ms", srv.URL + "/page", "select=#pick:b", "wait=#picked"})
	is.NoErr(err)
	err = run([]string{"-out", out, "-wait", "200ms", srv.URL + "/page", "select=#pick:Beta", "wait=#picked"})
	is.NoErr(err)

	// An option that is not there names the options that are.
	err = run([]string{"-out", out, srv.URL + "/page", "select=#pick:c"})
	is.HasErr(err)
	is.True(strings.Contains(err.Error(), `select=#pick:c: no option "c" in "#pick"; want a value or label of: "" (None), "a" (Alpha), "b" (Beta)`))

	// A match that is not a select says so.
	err = run([]string{"-out", out, srv.URL + "/page", "select=#q:a"})
	is.HasErr(err)
	is.True(strings.Contains(err.Error(), `select=#q:a: "#q" is <input>, not <select>`))

	// A hostile page's text reaches the error escaped: no raw ESC and
	// no raw newline, so it stays on the one line browse writes.
	for _, a := range []string{"select=#evil:y", "click=#b"} {
		err = run([]string{"-out", out, srv.URL + "/hostile", a})
		is.HasErr(err)
		t.Log(err)
		is.True(!strings.ContainsAny(err.Error(), "\x1b\n"))
		is.True(strings.Contains(err.Error(), `\x1b[`))
	}

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

// pagePNG encodes a w by h frame that looks like a screenshot of an
// app page: a white body, a dark header, grey rows of text with
// anti-aliased edges, a border, and a 64 by 64 photo. Each benchmark
// of the -video path gets the same frame, so a result compares to the
// last one.
func pagePNG(tb testing.TB, w, h int) []byte {
	tb.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(0, 0, w, 56), &image.Uniform{color.RGBA{0x0b, 0x1f, 0x6b, 0xff}}, image.Point{}, draw.Src)
	for y := 96; y+12 < h; y += 24 {
		for x := 32; x < w-32; x++ {
			// A glyph is 7 pixels wide with a gap, and its edge pixels
			// take one of four greys, as a text renderer leaves them.
			if x%9 == 8 {
				continue
			}
			for dy := range 12 {
				v := uint8(0x22 + (x*7+dy*13)%4*0x30)
				img.SetRGBA(x, y+dy, color.RGBA{v, v, v, 0xff})
			}
		}
		img.SetRGBA(16, y, color.RGBA{0xdd, 0xdd, 0xdd, 0xff})
	}
	for y := range 64 {
		for x := range 64 {
			img.SetRGBA(w-96+x, 72+y, color.RGBA{uint8(x * 4), uint8(y * 4), uint8(0x80 + x + y), 0xff})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		tb.Fatal(err)
	}
	return buf.Bytes()
}

// BenchmarkWriteGIF encodes five 1280 by 900 frames, about a second of
// a -video run on a desktop viewport.
func BenchmarkWriteGIF(b *testing.B) {
	data := pagePNG(b, 1280, 900)
	frames := make([]frame, 5)
	for i := range frames {
		frames[i] = frame{PNG: data, At: float64(i) / 5}
	}
	b.SetBytes(int64(len(data) * len(frames)))
	b.ReportAllocs()
	for b.Loop() {
		if err := writeGIF(io.Discard, frames, 2); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkPaletted maps one decoded 1280 by 900 frame to its palette,
// without the PNG decode and the GIF encode.
func BenchmarkPaletted(b *testing.B) {
	src, err := png.Decode(bytes.NewReader(pagePNG(b, 1280, 900)))
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(1280 * 900 * 4)
	b.ReportAllocs()
	for b.Loop() {
		paletted(src, 1280, 900)
	}
}

// BenchmarkReadScreencast feeds the reader goroutine the messages of a
// -video run: screencast frames that a stream queues, and events that
// no caller asked for.
func BenchmarkReadScreencast(b *testing.B) {
	data := base64.StdEncoding.EncodeToString(pagePNG(b, 1280, 900))
	var in bytes.Buffer
	for i := range 5 {
		fmt.Fprintf(&in, `{"method":"Page.screencastFrame","params":{"data":%q,"metadata":{"timestamp":%d.5,"deviceWidth":1280,"deviceHeight":900},"sessionId":%d},"sessionId":"S1"}`+"\x00", data, i, i+1)
		fmt.Fprintf(&in, `{"method":"Network.dataReceived","params":{"requestId":"%d","timestamp":%d.25,"dataLength":512,"encodedDataLength":0},"sessionId":"S1"}`+"\x00", i, i)
	}
	exited := make(chan struct{})
	close(exited)
	b.SetBytes(int64(in.Len()))
	b.ReportAllocs()
	for b.Loop() {
		s := &subscription{method: "Page.screencastFrame", sessionID: "S1", ready: make(chan struct{}, 1)}
		c := &chrome{
			r:      bufio.NewReader(bytes.NewReader(in.Bytes())),
			stderr: &stderrTail{},
			exited: exited,
			subs:   []*subscription{s},
			closed: make(chan struct{}),
		}
		c.read()
		if len(s.queue) != 5 {
			b.Fatalf("queued %d frames, want 5", len(s.queue))
		}
	}
}

// BenchmarkStderrReason reads a full 4 KB tail of console lines with a
// FATAL line at the start, the most work reason does.
func BenchmarkStderrReason(b *testing.B) {
	var tail stderrTail
	_, _ = tail.Write([]byte("[1:FATAL:zygote_host_impl_linux.cc:129] No usable sandbox!\n"))
	for i := 0; len(tail.b) < 4096; i++ {
		_, _ = fmt.Fprintf(&tail, "[1109/204100.%06d:INFO:CONSOLE:1] \"render row %d\", source: http://localhost:3000/app.js (1)\n", i, i)
	}
	b.ReportAllocs()
	for b.Loop() {
		tail.reason()
	}
}

func BenchmarkPageText(b *testing.B) {
	for _, bc := range []struct{ name, s string }{
		{"label", "Select a rating"},
		{"exception", "TypeError: Cannot read properties of null (reading 'click')\n    at <anonymous>:1:38\n    at async run (app.js:12:3)"},
		{"hostile", strings.Repeat("\x1b[31mred\rbrowse: forged ", 40)},
	} {
		b.Run(bc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				pageText(bc.s)
			}
		})
	}
}

// BenchmarkParseArgs parses a long command line: the URL, the actions,
// and BROWSE_COOKIE, then names the output file.
func BenchmarkParseArgs(b *testing.B) {
	args := []string{"click=.tab", "wait=#drawer", `type=li\:nth-child(2) input[name=q]:acme corp`, "select=#rating:8", "hover=nav a", "sleep=250ms"}
	cookies := "session=4f1c2a9e8b7d6c5e4f3a2b1c0d9e8f7a; theme=dark; csrf=ab12cd34ef56"
	b.ReportAllocs()
	for b.Loop() {
		u, err := checkURL("http://localhost:3000/companies/1234/Settings?tab=docs")
		if err != nil {
			b.Fatal(err)
		}
		if _, err := parseActions(args); err != nil {
			b.Fatal(err)
		}
		if _, err := parseCookies(cookies); err != nil {
			b.Fatal(err)
		}
		slug(u)
	}
}
