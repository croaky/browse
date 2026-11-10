# Plans

Each `todo/NNN-name.md` file is one plan, numbered in the order to
land it. Each plan says what to do, why, which files it touches, and
how to test it. Delete a plan in the change that ships it.

A plan names each security finding it fixes by its ID, so grep
`todo/` for a finding ID to find its plan.

## How to test a change

Run the list in the "Checks" section of `AGENTS.md`. CI runs
`TestBrowse` with `-short`, so it skips the browser. A change to
`cdp.go`, `page.go`, or `shell.go` needs a laptop run of
`go test -race ./...` without `-short`. The change description says
that the run passed.

## Open findings

Claude Security scanned `7f7d7ee` on 2026-11-09 (project
`spj_011Cgt9hCeaGGsQRhLvnTnhX`). `claudesec show <id>` prints the
full text of a finding.

- `find_01LZ9UsYMPdn1LmvMQXK5kTU` (low): the URL check accepts
  `file://localhost/...` and other schemes with a host. Attacker 2,
  the local-disk boundary. Outcome: fix. Plan:
  `todo/002-refuse-non-http-urls-and-quote-page-text.md`.
- `find_01XnWynAfXPx9zdZNWsnzVCB` (low): page strings reach stderr
  with raw control characters. Attacker 1, the page boundary.
  Outcome: fix. Plan:
  `todo/002-refuse-non-http-urls-and-quote-page-text.md`.

## Workflow

1. Land a `security:` change that fixes the finding. Delete its line
   here and its plan in the same change.
2. Add these trailers to the change description:

   ```text
   Reported-by: Claude Security at claude.ai/security
   Fixes: find_01...
   ```

   Join two or more IDs with `and`. Do not write `Fixes: CWE-...`.
3. After the change lands, run
   `claudesec resolve --reason fixed <finding id>`.

A finding that needs no code gets another reason: `false_positive`,
`accepted_risk`, `duplicate`, or `other`. Say why in `--note`.
