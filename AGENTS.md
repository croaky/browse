# Agents guide

browse is a command that opens a page in headless Chrome, runs a few
actions, and writes a PNG. See `README.md` for the contract and the
reasons.

## Writing

Write every word in ASD-STE100 Simplified Technical English (STE):
Markdown docs, code comments, commit messages, and replies in an agent
conversation. See
<https://en.wikipedia.org/wiki/Simplified_Technical_English>.

- One idea per sentence. Keep an instruction to 20 words and a
  description to 25.
- Active voice, present tense, and the actor named.
- One word, one meaning. Keep a term the same everywhere.
- Use the simple verb, not a noun made from it.
- Cut what carries nothing: "simply", "just", "note that".
- Put a warning or a limit before the step it applies to.

Apply it to prose, not to code: an identifier, a command, and a quoted
error message stay as they are.

## Architecture

One `main` package at the repo root. Four files, one concern each:

- `shell.go` pins a `chrome-headless-shell` version and downloads it
  into the user cache on the first run. It knows nothing about the
  protocol.
- `cdp.go` starts the browser and speaks the DevTools Protocol over
  `--remote-debugging-pipe`. It knows message ids, sessions, and
  events. It knows no method by name.
- `page.go` is one tab: viewport, cookies, headers, navigation, the
  action vocabulary, and the screenshot. Every protocol method the
  tool uses is named here.
- `main.go` is the command line: flags, the environment, the output
  path, and the order of the run.

There is no browser library, and the README says why. Before adding a
dependency, count what it replaces. A new protocol method is a
`p.call` with a `map[string]any` and a small result struct; that is
the whole pattern.

Do not switch the default browser back to the installed Google Chrome.
The README's "The browser" section records what it does on macOS and
what was tried. A version bump of the shell is its own commit, with a
laptop run of `TestBrowse` in the description.

An action is a whole literal on the command line, parsed by
`parseActions` into a name and an argument. A new action is one case
in `parseActions`, one case in `run`, one line in `usage`, one line in
the README, and one assertion in `TestParseActions`.

## Checks

The root `Checkfile` is the list, and CI runs it on every push. Run
the same things before committing:

```sh
goimports -local "$(go list -m)" -w .
go vet ./...
go test -trimpath -buildvcs=false -race -cover ./...
git ls-files -z '*.go' | xargs -0 gopls check -severity=hint
deadcode -test ./...
dprint fmt
```

`goimports` and `dprint` write here, where CI runs `goimports -l` and
`dprint check`. Fix the formatting in the change.

`TestBrowse` drives the real browser and skips under `-short`, which
is how the `Checkfile` runs it. So CI proves the parsers and the
compile, and a laptop proves the browser. A change to `cdp.go`,
`page.go`, or `shell.go` needs a laptop run of the suite without
`-short`, and the change description says so.

## Tests

`main_test.go` holds two kinds of test:

- Unit tests of the parsers and the slug, which run everywhere.
- `TestBrowse`, which serves a page from `httptest`, records the cookie
  and header the browser sent, clicks, waits, types, and decodes the
  PNG. It also asserts the two failure messages a user reads most: an
  action with no match, and a server that is not there.

Assert on the message text where the message is the product. A user
of this tool is often an agent reading stderr, and the sentence is
what it acts on.

## Changes

Work happens on a cibot change. `cibot checkout` allocates one and
prints a worktree; `cibot edit` sets its title and description before
the code. After a push, read the checks with
`git push && cibot show --wait`.

## Commits

- Prefix with what the change acts on: `cdp:`, `page:`, `cli:`, `doc:`,
  `ci:`.
- Imperative mood, lowercase except proper nouns. Hard-wrap at 72.
- Say why, not only what.
- Sign your work with a `Co-Authored-By` trailer.
