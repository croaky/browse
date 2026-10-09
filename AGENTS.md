# Agents guide

browse is a command that opens a page in headless Chrome, runs a few
actions, and writes a PNG. See `README.md` for the contract and the
reasons.

## Writing

Write every word in ASD-STE100 Simplified Technical English (STE):
Markdown docs, code comments, commit messages, change descriptions,
and replies in an agent conversation.
See <https://en.wikipedia.org/wiki/Simplified_Technical_English>.

STE is a controlled English for technical writing: one meaning per
word, one idea per sentence, and the actor named. It is not a house
style. It exists so every reader reads a sentence the same way. That
includes a tired reader, a reader in a second language, and an agent
that matches on words.

- One idea per sentence. Keep an instruction to 20 words and a
  description to 25.
- Active voice, present tense, and the actor named: say what acts,
  rather than writing "the shell is started".
- One word, one meaning. Keep a term the same everywhere rather than
  varying it for tone.
- Use the simple verb, not a noun made from it: "run the formatter",
  not "perform execution of the formatter".
- Cut what carries nothing: "simply", "just", "note that", "in order
  to".
- Put a warning or a limit before the step it applies to.
- STE limits a sentence, not a text. Keep each sentence short, but
  keep the sentence that defines a term or connects a cause to its
  effect.
- STE permits technical names. Name the page, the flag, or the
  function, not a vague noun such as "the snapshot".

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
go run golang.org/x/tools/cmd/goimports@v0.45.0 -local "$(go list -m)" -w .
go vet ./...
go test -trimpath -buildvcs=false -race -cover ./...
git ls-files -z '*.go' | xargs -0 gopls check -severity=hint
go run golang.org/x/tools/cmd/deadcode@v0.45.0 -test ./...
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

Work happens on a sockeye change. `soc checkout` allocates one and
prints a worktree; `soc edit` sets its title and description before
the code. Push with `soc push --wait`, which waits for the checks. If `main` moved
ahead, run `soc sync` to rebase the change, then `soc push --force`. A
plain push after a sync stops and says so.

## Commits

- Prefix with what the change acts on: `cdp:`, `page:`, `cli:`, `doc:`,
  `ci:`.
- Imperative mood, lowercase except proper nouns. Hard-wrap at 72.
- Say why, not only what.
- Write the subject for a teammate who has not read the code. Name the
  command, the flag, or the output, not a term the change makes up.
- Write the body as a change description (below). The first push of a
  branch with one commit copies the commit message to the change.
- Sign your work with a `Co-Authored-By` trailer.

## Change descriptions

sockeye squashes a change into one commit whose message is the
change's title and description (`soc edit`). Write the description as
that commit message.

Write for an engineer reading it a year from now. They know Go and
Git. They did not see your conversation, and they do not have the diff
open.

Put the most important fact first. A reader who stops after any
paragraph has the most important part so far. Use this order, and
leave out a part that does not apply:

1. The need. Name who uses what, what went wrong or was missing, and
   what the change does about it.
2. What a user sees now. Name the command, the flag, or the output.
   Say what stops happening and what a user does differently. If no
   user sees the change, say what it protects: a test, a release, or a
   cost.
3. How it works. Name each part at its first mention, with its
   kind: "the `parseCookies` Go function", "the `lint` check".
   Give the cause of a bug or the rule a feature applies, and the
   numbers that size the effect.
4. Next steps: a command to run, a follow-up plan by its path, or a
   known gap.

Most changes fit in three to five short paragraphs. To keep a
description short and clear:

- Define a project term at its first use, or use a plainer word.
- Leave out file lists, line counts, and code sizes. The diff shows
  them.
- Leave out what a reader of `main` cannot use: options you did not
  take, each edge case the tests cover, and "tests pass". Put a design
  argument in a code comment or a plan, and give its path.
- Use plain paragraphs. Git strips a `#` line as a comment, so a
  Markdown header disappears.
- Hard-wrap at 72 columns. Don't backslash-escape backticks. Use a
  quoted heredoc such as `<<'EOF'` with `soc edit`.
- Keep `Co-Authored-By` on the commits, not in the description. The
  merge collects the trailers from the commits it squashes.
