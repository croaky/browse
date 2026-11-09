# Stage the Linux shell inside /opt, not /tmp

Fixes: `find_01Pk4gx4E7mExxfowTVrmq53` (medium).

## The risk

The README "Linux" steps run `sudo unzip ... -d /tmp` and then
`sudo mv /tmp/chrome-headless-shell-linux64 /opt/...`. The name in
`/tmp` is fixed. A local user can make that directory first. `unzip`
extracts into it and keeps its owner, and `mv` keeps the owner too. So
the user owns the version directory in `/opt` and can replace the
binary.

- Attacker: a local unprivileged account on a shared Linux host. It
  must make the directory before the administrator installs.
- Consequence: the attacker's program runs as each user who runs
  `browse` with `BROWSE_CHROME`. It gets that user's files and
  `BROWSE_COOKIE`. The AppArmor profile also gives it user
  namespaces. It does not get root.
- Boundary: root and other local users.

## Options

1. Extract into the root-owned `/opt/chrome-headless-shell`
   (recommended). Cost: two changed lines in the README. No code, no
   tag.
2. Remove the "Linux" section. No known run of `browse` is on Linux:
   CI skips the browser. But a Linux user then meets
   `No usable sandbox!` with no guide, and may pass `--no-sandbox`.
   That is worse than the bug.

## Change

Title: `security: stage the Linux shell in /opt, not /tmp`.

In `README.md`, replace the two staging lines with:

```sh
sudo unzip -q chrome-headless-shell-linux64.zip -d /opt/chrome-headless-shell
sudo mv /opt/chrome-headless-shell/chrome-headless-shell-linux64 /opt/chrome-headless-shell/$v
```

Add one sentence after the block: root extracts inside the root-owned
directory, so no other user can make a path that the steps use.

## Test

On a Linux host or VM, as an unprivileged user, run
`mkdir /tmp/chrome-headless-shell-linux64`. Then follow the steps as
an administrator. `find /opt/chrome-headless-shell ! -user root`
prints nothing, and `find /opt/chrome-headless-shell -perm /022`
prints nothing. Then run `browse https://example.com/` with
`BROWSE_CHROME` set.

## After the merge

No tag: the change is docs only.
`claudesec resolve --reason fixed find_01Pk4gx4E7mExxfowTVrmq53`.

## Coupling

None. Land in any order.
