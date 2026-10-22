# Changelog

## [0.1.0] - 2026-10-22

First release.

- `help` and `help <command>`.
- `login` to sign in via Nextcloud Login Flow v2 or `--paste` to enter you app password.
- Credentials come from the `TICKBUDDY_*` environment variables, the system keychain, or a file encrypted with `systemd-creds` or a machine-id key.
- `tracks`, `ticks`, `week`, `stats`, and `export` to read.
- `tick`, `untick`, `set` and `add` to write with optional `--note`  or `--append-note`.
- `note` and `notes` set, append to, delete and list notes.
- Pretty, plain (piped) and `--json` output; fixed exit codes 0 to 6.
- The Go package `tickbuddy`, shared with TickLLMe.