# Changelog

## [0.2.0] - unreleased

Number and choice tracks, as Tickbuddy has them since its `numberTracks` and `choiceTracks` features.

- `set` writes a number track (`tickcli set Run 10.4`, `-1.5` where the track allows negative values) or a choice track (`tickcli set "Mum's mood" happy`, or `'#2'` for the second option); `""` clears the day. The server rounds a number to the track's decimal places.
- `tick`, `untick` and `add` on a number or choice track say to use `set` (exit 2). A value that isn't a number, or an option the track doesn't have, is exit 2 too.
- Every view shows number and choice days: a number at its track's decimal places with its unit (`10.4 km`, without the unit when piped), a choice's label. `--json` gives a number's stored value exactly, with `unit`, and a choice's place with its `label`.
- `--numeric` shows choice options by their place and yes/no days as `1` and `0`.
- `week` widens its columns when a number or choice track is shown; a number's unit follows its name.
- `tracks` shows each number's unit and decimal places and each choice's options under the track; `--json` adds them on those types.
- `stats` includes number and choice tracks, counting days with an entry.
- Piped, a day without a value on a track of a type this Tickcli doesn't know is now an empty field instead of `null`.
- `status` lists `numberTracks` and `choiceTracks` among the features an older server lacks.
- Package `tickbuddy`: `Decimal`, `SetNumber`, `SetChoice`, `ClearValue`, `ResolveOption`, `RenameOption`, and `CreateTrack` with `TrackSettings`. `Track.KnownType` is now `Track.Known`, `Stats.MaxValue` and `MinValue` are `Value`s, and `Stats` asks for every type (`includeAllTypes`).

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