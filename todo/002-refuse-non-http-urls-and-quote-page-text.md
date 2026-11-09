# Refuse non-http URLs and quote page text in errors

Fixes: `find_01LZ9UsYMPdn1LmvMQXK5kTU` (low) and
`find_01XnWynAfXPx9zdZNWsnzVCB` (low).

Both findings are the same kind of bug: `browse` trusts input that
crosses a boundary. Each fix is a few lines. One change shares one
laptop run of `TestBrowse` and one patch tag.

## Finding 1: the URL scheme

The check in `run` (`main.go`) requires a scheme and a host. It does
not compare the scheme with `http` or `https`. So
`file://localhost/etc/passwd`, `chrome://<host>`, and
`javascript://<host>/...` reach `Page.navigate`.

- Attacker: text that an agent reads and that chooses the URL.
- Consequence: a file or a directory listing that the user can read
  goes into a PNG in the working directory, and then to the agent.
  A `chrome://` debug URL can crash or hang the browser.
- Boundary: the URL argument and the local disk.

Fix: refuse a scheme that is not `http` or `https`, compared with
`strings.EqualFold`, before Chrome starts. Keep the error text
`want an absolute http or https URL`. Add no opt-in for `file:`,
because no caller needs it. Cost: one condition and a few rows in a
unit test. No speed cost.

## Finding 2: page text on stderr

`eval` and `selectOption` in `page.go` put page strings into an error
with `%s`: the exception description, the tag name, and each option
label. `main.go` writes the error to stderr as it is. A page can put
ESC sequences and newlines on the terminal, or a forged `browse:`
line in front of an agent.

- Attacker: the page, or content it renders into an option label.
- Consequence: the page controls what the terminal shows and what
  the agent reads as the failure message. Some terminals also let a
  sequence set the title or the clipboard.
- Boundary: the page and the `browse` process.

Options:

1. Quote at the source (recommended). Use `%q` for the tag name and
   each option label. Use `%q` for the exception description, cut to
   about 200 bytes first. Cost: three format verbs and one cut. A
   JavaScript stack trace then prints on one line with `\n` escapes.
   `browse` keeps its own lines and their shape.
2. Escape once in `main.go`. Replace each control character in the
   final error before `fmt.Fprintln`. Cost: one small function. It also
   covers a string that a later change adds. But it also escapes the
   newlines of `browse`'s own messages, such as the Chrome stderr tail,
   so those messages change shape.

Option 1 fixes the bug where it is. Option 2 is a net under every
path. Choose option 1 unless a review finds more page strings that
reach an error.

## Test

- Move the check into a `checkURL` Go function, and add a table test
  `TestCheckURL`. `file://localhost/etc/passwd`, `chrome://version`,
  and `javascript://x/alert(1)` fail with the existing message.
  `http://localhost/` and `HTTPS://example.com/` pass. The test needs
  no browser, so it runs in CI.
- `TestBrowse`: add a page with a `<select>` whose option label holds
  `&#27;[31m` and `&#10;browse: forged`. Assert that the `select=`
  error holds no raw ESC byte and no raw newline. Laptop only.
- The Checkfile list in `AGENTS.md`.

## After the merge

- Publish a patch tag. Bump the pin in croaky/laptop.
- `claudesec resolve --reason fixed` for both IDs.

## Coupling

Land after plan 001. Both edit the start of `run` in `main.go`.
