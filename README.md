# browse

Open a page in headless Chrome, run a few actions, write a PNG. For an
agent or a person who wants to see a page of a local app without a
browser window.

```sh
go install github.com/croaky/browse@latest

browse http://localhost:3000/help
browse -phone http://localhost:3000/portfolio
browse http://localhost:3000/companies/12 click=.tab:nth-child(2) wait=.drawer.active
browse -full -out tmp/index.png http://localhost:3000/
browse -steps -video tmp/search.gif http://localhost:3000/ 'type=#q:acme' wait=.results
```

On success it prints each path it wrote, one per line, with the main
PNG last, and nothing else. On failure it prints why and exits 1.
The default path is `<slug>.png` in the
current directory, from the URL path: `/help` is `help.png`,
`/companies/12` is `companies-12.png`, and `/` is `index.png`.

The first run downloads one pinned build of `chrome-headless-shell`
(about 100 MB) into the user cache directory. After that there is
nothing else: no Node, no driver, no library.

## Actions

Actions run in order after the page loads and before the screenshot:

- `click=<selector>` clicks the first match with a real pointer event.
- `hover=<selector>` moves the pointer over the first match and leaves
  it there, so a `:hover` rule is in effect.
- `type=<selector>:<text>` focuses the first match and types the text.
- `wait=<selector>` waits until a match is on the page and visible.
- `sleep=<duration>` waits a fixed time, for a transition the DOM does
  not signal.

The first colon in a `type` argument divides the selector from the
text. A selector holds a colon in a pseudo-class, so write that colon
as `\:`:

```sh
browse http://localhost:3000/ 'type=li\:nth-child(2):hello'
```

The text needs no escape, because only the first colon divides:
`type=#at:12:30` types `12:30`.

Only `wait` waits. `click`, `hover`, and `type` act on the element
that is there now, and no match is an error at once. A step that needs
an element the page has still to render takes a `wait` before it.
`browse` retries no action: a click that needs a retry needs a `wait`.

There is no scroll action. `click` and `hover` scroll their
target to the center of the viewport before they act, and scroll a
scroll box inside the page to reach it. So a capture of a page or a
box scrolled part way hovers an element that far down:

```sh
browse http://localhost:3000/report 'hover=tbody tr:nth-child(40)'
```

One `wait` has `-wait`, five seconds by default, and the whole run has
`-timeout`. The budget is per wait, so one selector that never shows
stops that wait and leaves the run its time.

An action that fails names itself and the selector:
`click=#missing: no element matches "#missing"`. A wait that fails
says how long it waited: `wait=#box: no element matches "#box" and is
visible after 5s`.

## Flags

- `-width`, `-height`: the viewport in CSS pixels, default 1280 by
  900.
- `-phone`: 390 by 844 at two device pixels per CSS pixel, a touch
  screen, and an iPhone user agent, so a page built for a phone
  renders and behaves as it does on one. `-width` or `-height` beside
  it changes one dimension.
- `-full`: capture the whole document, not the viewport.
- `-out <path>`: where to write the PNG.
- `-steps`: also write a PNG after each action, beside the main PNG.
  `help.png` with two actions gives `help-1-click.png` and
  `help-2-wait.png`. An agent that checks a flow can read each step.
- `-video <path.gif>`: also write an animated GIF of the run, from the
  first paint to the last action, at real speed. It is for a person:
  an agent reads images, not motion.
- `-chrome <path>`: a Chrome binary, instead of the pinned headless
  shell.
- `-wait <duration>`: a limit for one `wait` action, default 5s.
- `-timeout <duration>`: a limit for the whole run, default 30s.

## Auth

Most pages worth a screenshot are behind a login. `browse` takes
cookies and headers from the environment, not from flags, so a secret
is not in a process list or a shell history:

```sh
BROWSE_COOKIE='session=abc123' browse http://localhost:3000/
BROWSE_HEADER='Authorization: Bearer abc123' browse http://localhost:3000/
```

`BROWSE_COOKIE` is the shape of a `Cookie` header: `name=value` pairs
separated by semicolons, set for the URL's host. `BROWSE_HEADER` is
one `Name: value` per line, sent on every request.

An app that wants an agent to see its pages writes a small wrapper
that mints a session for a development user, puts it in
`BROWSE_COOKIE`, and calls `browse`. This tool knows no app.

## The browser

`browse` runs `chrome-headless-shell` from
[Chrome for Testing](https://googlechromelabs.github.io/chrome-for-testing/),
at the version pinned in `shell.go`. The first run downloads it to
`~/Library/Caches/browse/` on macOS or `~/.cache/browse/` on Linux and
says so on stderr. A pinned version means two laptops that install the
same `browse` render a page the same way. `-chrome` wins, then
`BROWSE_CHROME`, for a run that needs another binary.

Why not the Google Chrome that is installed: on macOS, a fresh profile
of it asks the system to make Chrome the default browser at every
launch, and macOS shows a dialog for that. `--no-default-browser-check`,
the default-browser feature flags, and a seeded profile do not stop
it. The headless shell has no browser UI and does not ask.

Each run starts the browser with a fresh temporary profile and removes
it at exit. The run reads no cookie, history, or extension of the
user's own profile and writes nothing to it.

The download has no checksum. Chrome for Testing publishes none, so a
checksum here is one a person computes for each platform at each
version, and a stale entry blocks a bump. The transfer is HTTPS from
Google's bucket, a short read fails in the unzip, and the extracted
tree moves into place in one rename. So an interrupted download leaves
nothing for the next run to find.

The pin holds until a Chrome release breaks something this tool uses,
or until a page needs a newer renderer. `TestBrowse` is the guard: it
drives the real browser through a cookie, a header, a pointer event,
phone emulation, and a PNG decode. A bump is its own commit, with a
laptop run of that test in the description.

## Linux

The headless shell needs shared libraries that a server image often
leaves out. On Ubuntu 24.04:

```sh
sudo apt-get install -y libatk1.0-0t64 libatk-bridge2.0-0t64 libxcomposite1 \
  libxdamage1 libxfixes3 libxrandr2 libgbm1 libasound2t64 libatspi2.0-0t64
```

Chrome's sandbox needs unprivileged user namespaces. Ubuntu 23.10 and
later let AppArmor restrict them, so the shell stops at start with
`No usable sandbox!`, and `browse` prints that line. Do not pass
`--no-sandbox`, and do not turn the restriction off for the whole
machine. Install the shell where only root can write, and allow user
namespaces for that path only:

```sh
v=153.0.8010.47   # shellVersion in shell.go
sudo mkdir -p /opt/chrome-headless-shell
curl -fsSLO "https://storage.googleapis.com/chrome-for-testing-public/$v/linux64/chrome-headless-shell-linux64.zip"
sudo unzip -q chrome-headless-shell-linux64.zip -d /tmp
sudo mv /tmp/chrome-headless-shell-linux64 /opt/chrome-headless-shell/$v
sudo tee /etc/apparmor.d/chrome-headless-shell >/dev/null <<'EOF'
abi <abi/4.0>,
include <tunables/global>

profile chrome-headless-shell /opt/chrome-headless-shell/*/chrome-headless-shell flags=(unconfined) {
  userns,
}
EOF
sudo apparmor_parser -r /etc/apparmor.d/chrome-headless-shell
export BROWSE_CHROME=/opt/chrome-headless-shell/$v/chrome-headless-shell
```

The profile names the root-owned path, not the user cache. A profile
for a path the user can write lets any program the user puts there
use user namespaces. Chromium explains the trade-off in
[AppArmor user namespace restrictions](https://chromium.googlesource.com/chromium/src/+/main/docs/security/apparmor-userns-restrictions.md).

## The video

`-video` uses Chrome's screencast. Chrome sends a JPEG each time the
page paints, with a timestamp, and `browse` acks each frame. Each
frame shows until the next paint, so a page that does not change is
one frame held, and a `sleep` after a transition shows the transition
at its real speed. The first frame is the blank tab before the load.

An action returns before Chrome paints its result, so the screencast
alone can stop before the last action shows. The last frame is a
screenshot after the last action instead, which waits for a new paint
as the PNG does. It holds one second before the GIF plays again. The
GIF is the size of the PNG, and a screencast frame of another size
scales to that width.

The GIF uses the standard library's Plan 9 palette with no dithering.
A page is mostly flat color, and dithering adds noise that changes
from frame to frame. A gradient or a photo shows bands.

Headless Chrome draws no pointer, so a click or a hover shows only by
what it changes on the page.

## No browser library

The DevTools Protocol client is about 250 lines in `cdp.go`. It speaks
to Chrome over `--remote-debugging-pipe`: JSON messages on file
descriptors 3 and 4, each ended by a NUL byte. A pipe rather than a
port, so two runs at once do not race for a port and nothing listens
on the machine.

The tool calls nineteen protocol methods. A library such as `chromedp`
brings generated bindings for the whole protocol, tens of megabytes in
the module cache and a binary about 15 MB larger, and it tracks Chrome
releases. Nineteen methods by hand is smaller than the import, and
there is nothing to keep current.

## GitHub repo is a mirror

Development happens on [sockeye](https://sockeye.sh), a
self-hosted review and CI server, which holds in progress branches.
GitHub receives `main` so `go install` works.

## License

MIT
