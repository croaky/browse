# browse

Open a page in headless Chrome, run a few actions, write a PNG. For an
agent or a person who wants to see a page of a local app without a
browser window.

```sh
go install github.com/croaky/browse@latest

browse http://localhost:3000/help
browse -width 390 http://localhost:3000/portfolio
browse http://localhost:3000/companies/12 click=.tab:nth-child(2) wait=.drawer.active
browse -full -out tmp/index.png http://localhost:3000/
```

On success it prints the path it wrote and nothing else. On failure it
prints why and exits 1. The default path is `<slug>.png` in the
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

An action that fails names itself and the selector:
`click=#missing: no element matches "#missing"`.

## Flags

- `-width`, `-height`: the viewport, default 1280 by 900.
- `-full`: capture the whole document, not the viewport.
- `-out <path>`: where to write the PNG.
- `-chrome <path>`: a Chrome binary, instead of the pinned headless
  shell.
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

## No browser library

The DevTools Protocol client is about 250 lines in `cdp.go`. It speaks
to Chrome over `--remote-debugging-pipe`: JSON messages on file
descriptors 3 and 4, each ended by a NUL byte. A pipe rather than a
port, so two runs at once do not race for a port and nothing listens
on the machine.

The tool calls fourteen protocol methods. A library such as `chromedp`
brings generated bindings for the whole protocol, tens of megabytes in
the module cache and a binary about 15 MB larger, and it tracks Chrome
releases. Fourteen methods by hand is smaller than the import, and
there is nothing to keep current.

## GitHub repo is a mirror

Development happens on [cibot](https://dancroak.com/cmd/cibot/), a
self-hosted review and CI server, which holds in progress branches.
GitHub receives `main` so `go install` works.

## License

MIT
