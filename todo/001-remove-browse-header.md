# Remove BROWSE_HEADER

Fixes: `find_01AheLnRjfPcXZ5rHe6yjYVw` (medium).

## The risk

`setHeaders` in `page.go` gives the `BROWSE_HEADER` map to
`Network.setExtraHTTPHeaders`. That method has no origin scope. Chrome
adds the headers to every request of the tab: third-party scripts,
fonts, images, and each host after a redirect.

- Attacker: the operator of any host that the captured page contacts.
- Consequence: the attacker reads the full header value, usually a
  bearer token or an API key, in its request log.
- Boundary: the origin of the URL on the command line.

The README example puts a bearer token in `BROWSE_HEADER`. The target
is an internal app on the public internet, so third-party hosts and
redirects are likely.

## Is the feature needed

No known caller uses it. The app wrappers (`cmd/browse` in each app)
and the sockeye `AGENTS.md` set `BROWSE_COOKIE` only. `BROWSE_COOKIE`
is safe already: `setCookies` scopes each cookie to the URL, so Chrome
sends it to that host only.

## Options

1. Remove `BROWSE_HEADER` (recommended). Delete `parseHeaders`,
   `setHeaders`, the usage line, the doc comment line, the README text,
   and the header assertion in `TestBrowse`. Cost: none in speed. The
   code gets smaller. An app that accepts only a bearer header loses
   support. No such app is known.
2. Scope the headers to the target origin. Replace
   `Network.setExtraHTTPHeaders` with `Fetch.enable` and a handler for
   `Fetch.requestPaused`. The handler adds the headers only when the
   request origin is the target origin, then sends
   `Fetch.continueRequest`. Cost: Chrome pauses every request for one
   pipe round trip, so each page load is slower. `page.go` gets an
   event loop in the hot path, with its own failure modes. This is a
   new moving part for a feature with no caller.
3. Accept the risk and document it. State in the README and in
   `usage` that every host receives the header. Cost: one line. The
   token still leaks on the first third-party request. Not
   recommended, because the README example teaches the unsafe use.

## Change

Title: `security: remove BROWSE_HEADER, which leaked to every host`.

- `main.go`: remove the `BROWSE_HEADER` line from the doc comment and
  from `usage`, the `parseHeaders` call, `parseHeaders`, and the
  `setHeaders` call.
- `page.go`: remove `setHeaders`.
- `main_test.go`: remove `TestParseHeaders`, and remove the
  `X-Browse` header and its assertion from `TestBrowse`.
- `AGENTS.md`: the "Tests" section says `TestBrowse` records "the
  cookie and header". Make it "the cookie". Remove "headers" from the
  `page.go` line in "Architecture".
- `README.md`: remove the `BROWSE_HEADER` example and sentence from
  "Auth". Say that `BROWSE_COOKIE` is the one way to sign in, and that
  Chrome sends it to the URL's host only. Remove "a header" from the
  `TestBrowse` list in "The browser".

A set `BROWSE_HEADER` is then ignored with no message. Consider one
error, `BROWSE_HEADER is gone; use BROWSE_COOKIE`, for one release.
That costs three lines and tells a user why the login stopped.

## Test

- The Checkfile list in `AGENTS.md`.
- A laptop run of `TestBrowse` without `-short`, because `page.go`
  changes.

## After the merge

- Publish a minor tag, because a variable goes away. Bump the pin in
  croaky/laptop.
- The blog article `articles/cmd/browse.md` in croaky/blog shows
  `BROWSE_HEADER`. Remove that line in a blog change.
- `claudesec resolve --reason fixed find_01AheLnRjfPcXZ5rHe6yjYVw`.

## Coupling

Land this before plan 002. Both edit the start of `run` in `main.go`.
