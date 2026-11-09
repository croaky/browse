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

## Threat model

`browse` is a command that a developer or a coding agent runs on a
laptop. It opens one URL in a headless Chrome, runs actions, and writes
a PNG. The usual target is an internal app on the public internet or
a development copy of it on `localhost`.

The assets:

- The secrets in `BROWSE_COOKIE` and `BROWSE_HEADER`. A wrapper in
  each app mints a session for a development user and puts it in
  `BROWSE_COOKIE`.
- The files and the environment of the user who runs `browse`.
- The text on stderr. A person or an agent reads it and acts on it.

The attackers:

1. The page and every host it contacts. This includes third-party
   scripts, fonts, analytics, images in user content, and redirect
   targets. `browse` reads their data through the DevTools Protocol.
2. Text that an agent reads and that chooses the URL or the actions,
   for example a prompt injection in an issue or a page.
3. A local unprivileged account on a shared Linux host.

The boundaries:

- The target origin and other origins. A secret for the target app
  must not go to another host.
- The page and the `browse` process. A string from the page is data.
  It must not become a terminal control sequence or a forged
  `browse:` line.
- The URL argument and the local disk. A URL must not read a local
  file.
- Root and other local users. Only root can write the Chrome binary
  that the AppArmor profile names.

Out of scope:

- An attacker who can already run commands as the user. That attacker
  can read the environment directly.
- A Chrome renderer exploit. The Chrome sandbox owns that boundary.
- The download of the shell. It uses HTTPS from Google, and the README
  says why it has no checksum.

A finding needs a named attacker from this list, a consequence, and a
boundary. A severity label alone is not enough.

## Open findings

Claude Security scanned `7f7d7ee` on 2026-11-09 (project
`spj_011Cgt9hCeaGGsQRhLvnTnhX`). It reported 4 open findings: 2 medium
and 2 low. `claudesec show <id>` prints the full text of a finding.

- `find_01AheLnRjfPcXZ5rHe6yjYVw` (medium): `BROWSE_HEADER` goes to
  every origin the tab contacts. Attacker 1, the target-origin
  boundary. Outcome: remove `BROWSE_HEADER`. Plan:
  `todo/001-remove-browse-header.md`.
- `find_01LZ9UsYMPdn1LmvMQXK5kTU` (low): the URL check accepts
  `file://localhost/...` and other schemes with a host. Attacker 2,
  the local-disk boundary. Outcome: fix. Plan:
  `todo/002-refuse-non-http-urls-and-quote-page-text.md`.
- `find_01XnWynAfXPx9zdZNWsnzVCB` (low): page strings reach stderr
  with raw control characters. Attacker 1, the page boundary.
  Outcome: fix. Plan:
  `todo/002-refuse-non-http-urls-and-quote-page-text.md`.
- `find_01Pk4gx4E7mExxfowTVrmq53` (medium): the README Linux steps
  stage the root-owned shell in a fixed path in `/tmp`. Attacker 3,
  the root boundary. Outcome: fix the docs. Plan:
  `todo/003-stage-linux-shell-in-opt.md`.

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
