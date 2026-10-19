# Changelog

## 0.1.0 (unreleased)

First release.

- `tickcli` shows today's tracks; `tick`, `untick`, `set` and `add` write,
  with `--note` to stamp the day's note in the same request, or
  `--append-note` to add a line to it.
- `note` and `notes` set, append to, delete and list notes.
- `week`, `stats`, `tracks` and `export` read; `stats` also for a period
  with `--from`/`--to` or `--year`.
- `login` (Nextcloud Login Flow v2, with `--qr`, or `--paste`), `logout`
  (revokes an app password Tickcli created) and `status`.
- Credentials from the `TICKBUDDY_*` environment variables, the system
  keychain, or a file encrypted with `systemd-creds` or a machine-id key.
- Pretty, plain (piped) and `--json` output; fixed exit codes 0 to 5.
- The Go package `tickbuddy`, shared with TickLLMe.
