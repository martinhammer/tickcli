# Tickcli
Tickcli (pronounced "tickly") is the command line client for [Tickbuddy](https://github.com/martinhammer/tickbuddy).

It can serve many use cases, broadly falling into the following three categories:
- Command line as an additional interface alongside [web](https://github.com/martinhammer/tickbuddy) and [mobile](https://github.com/martinhammer/tickdroid)
- Scripting automation, e.g. monitor your jobs via Tickbuddy
- Building block of [TickLLMe](https://github.com/martinhammer/tickllme), the MCP server and Claude plugin for Tickbuddy 

## Examples

```
$ tickcli tick exercise
✓ Exercise · Sun 18 Oct

$ tickcli add coffee 2 --note "one decaf"
Cups of coffee: 5 · Sun 18 Oct  📝 one decaf

$ tickcli week
                    Su Sa Fr Th We Tu Mo
Exercise            ✓  ✓  ·  ·  ✓  ✓  ✓   2-day streak
Cups of coffee      5  2  4  ·  4  2  3   3-day streak
Affirmations        ·  ·  ·  ·  ·  ·  ·   16-day break

$ tickcli stats exercise --year 2025
Exercise, 1 Jan – 31 Dec 2025
  Total             212      Weekly mean   4.1
  Last 7 days       5        Previous 7    4
  At the end        3-day streak
  Longest streak    21 days  (2 Mar – 22 Mar 2025)
  Longest break     9 days   (4 Aug – 12 Aug 2025)
```

Nightly backup automation via cron

```crontab
30 2 * * * restic backup /srv && tickcli tick "Nightly backup" --note "$(date +%H:%M)"
```


## Installing tickcli

Option 1: Download the binary for your system from the [releases page](https://github.com/martinhammer/tickcli/releases), make it executable and put it on your `PATH`. 

Option 2: Alternatively, with Go installed:

```
go install github.com/martinhammer/tickcli/cmd/tickcli@latest
```


## Connecting to Tickbuddy

### Signing in

The default method is to use [Login flow v2](https://docs.nextcloud.com/server/stable/developer_manual/client_apis/LoginFlow/index.html#login-flow-v2):

```
tickcli login cloud.example.com
```

This will print a link to grant access via a browser. Add `--qr` for a QR code to scan with a phone. Use the link to sign in and grant access; Nextcloud automatically creates an app password named "Tickcli on \<hostname\>".

Alternatively, create an app password yourself and paste it. Note: **app password**, never your user's actual password.

```
tickcli login cloud.example.com --paste
```

Without a terminal, `--paste` reads the user and the app password as two lines from standard input. 

A URL without a scheme gets `https://`. Plain `http://` is refused except for `localhost` and private addresses; use `--insecure-http` to override.

`tickcli status` shows who is signed in and where the credentials are.

```
$ tickcli status
Signed in as user6 at http://nextcloud.local · Tickbuddy 2.3.0 · credentials in keychain
```

### Signing out

```
tickcli logout
```

`tickcli logout` forgets login credentials, and revokes the app password on the Nextcloud server if it was created through the authentication link; use `--local` to skip the revoke.

A self-created (pasted) app password is forgotten but not revoked on the Nextcloud server. 

`tickcli logout` doesn't touch the `TICKBUDDY_*` environment variables: when they are set, it still forgets what is stored and reminds you that Tickcli keeps using them.

**An app password can be revoked at any time on Nextcloud.**

### Where credentials are kept

Tickcli looks in this order:

1. **Environment variables** `TICKBUDDY_URL`, `TICKBUDDY_USER` and `TICKBUDDY_APP_PASSWORD`. All three must be set. Nothing is permanently stored. Use this approach for containers and CI, or to reach another account for one run.
2. **The system keychain**: Keychain on macOS, Credential Manager on Windows, the Secret Service (GNOME Keyring, KWallet) on Linux desktops.
3. **An encrypted file**, `~/.config/tickcli/credentials` (or under `$XDG_CONFIG_HOME`), on Linux and the BSDs when there is no keychain (such as a headless server). It is encrypted with per-user `systemd-creds` where that works (systemd 256 or newer: Debian 13, Ubuntu 24.10 and later), bound to the TPM if there is one and otherwise to a host key only root can read. Elsewhere it is encrypted with a key derived from `/etc/machine-id` and your user id.

What the file protects against: a copy of it that leaks can't be read on another machine. It does **not** protect against someone who can log in as you on that machine: they can run Tickcli as you, or derive the same key. 

The `login` command uses the keychain when it can and reports where it stored the credentials. Only one account is signed in at a time.


## Using tickcli

Tracks are named by their exact name (ignoring case) or by id retrieved through `tickcli tracks`.

Today is the default date for most commands; use `-d` to specify another day. Dates are `YYYY-MM-DD`, `today`, `yesterday` or `-N` for N days ago. "Today" means the machine's local date, not the server's.

### Command reference

| Command | What it does |
|---|---|
| `tickcli login <url> [--paste] [--qr] [--insecure-http]` | Sign in; see above. |
| `tickcli logout [--local]` | Forget the credentials and revoke the app password; see above. |
| `tickcli status` | Who is signed in and where the credentials are. |
| `tickcli [-d date] [--private]` | Today's tracks with values and notes; `-d` for another day, `--private` to include private tracks. |
| `tickcli tracks [--archived] [--private]` | Tracks with their ids; `--archived` and `--private` add those. |
| `tickcli week [--private]` | The last 7 days of each track, today first, with the current streak or break. |
| `tickcli notes [--from date] [--to date] [--private] [--archived]` | Notes of the last 7 days, or the range. |
| `tickcli stats [track] [--from date] [--to date] [--year YYYY] [--private] [--archived]` | Totals, weekly mean, last two weeks, current run, longest streak and break, for all time or a period: `--from`/`--to` (either alone) or `--year`, not both. |
| `tickcli tick <track> [-d date] [--note text [--append [--force-duplicate]] \| --append-note text [--force-duplicate]]` | Tick a yes/no track. |
| `tickcli untick <track> [-d date] [--note text [--append [--force-duplicate]] \| --append-note text [--force-duplicate]]` | Untick it. |
| `tickcli set <track> <n> [-d date] [--note text [--append [--force-duplicate]] \| --append-note text [--force-duplicate]]` | Set a counter to n; 0 removes the count. |
| `tickcli add <track> [n] [-d date] [--note text [--append [--force-duplicate]] \| --append-note text [--force-duplicate]]` | Add n (default 1) to a counter; `-2` takes 2 away. |
| `tickcli note <track> ("text" [--append [--force-duplicate]] \| --clear) [-d date]` | Set or replace a day's note (any track type). `--append` adds the text on a new line instead, skipping lines the note already holds unless `--force-duplicate`; `--clear` deletes the note. |
| `tickcli export [--private]` | The server's JSON export, unchanged: `tickcli export > backup.json`. |

- To use track id instead of its name, use the form #n where n is the id
- When used at a terminal a unique prefix (cups instead of "Cups of coffee") works too; scripts need the full name
- Notes hold up to 500 characters
- `--note "…"` is used to set the day's note in the same request (i.e. `tick`, `untick`, `set`, `add`)
- `--note ""` deletes any existing note unless `--append` is specified
- `--append-note "…"`, or `--note "…" --append` adds the text to the day's note on a new line instead of replacing it
- When appending to notes, duplicate lines are ignored so that running the same command again changes
nothing; `--force-duplicate` appends it anyway
- `stats --year 2025`, then `stats --year 2026`, compares a track year on year; `--from` and `--to` give any other period (`--from -30` is the last 30 days and today). Given any period, "Last 7 days" and "Previous 7" refer to its last two weeks, and a period that ended before today shows its run as of the last day, labelled "At the end".

### Retry
- Requests that are safe to repeat are retried up to 3 times (after 1, 2 and 4 seconds) when the server can't be reached. This can be controlled via `--retries n`. 
- `add` is never retried: if the server counted it and the answer got lost, a retry would count twice. When `add` fails with exit 5, check with `tickcli` before adding again.
- A note append with `--force-duplicate` is never retried.

### Output

- **At a terminal**, output is pretty-printed with colour. `NO_COLOR` (any value) or `TERM=dumb` turns colour off.
- **Piped**, output is tab-separated lines with dates as `YYYY-MM-DD`, and writes print nothing on success, so a script's log stays quiet.
- **`--json`** gives JSON on every command. A write answers `{"track", "trackId", "date", "value"}` (`value` is true/false on yes/no tracks and a number on counters), plus `"note"`, the whole note afterwards, when one was set or appended.
- Errors always go to stderr as `tickcli: <message>`, or with `--json` as `{"error": "<message>", "code": <exit code>}`.

### Exit codes

| Code | Meaning |
|---|---|
| 0 | success |
| 1 | error, including an archived track and server version too old for the command |
| 2 | bad usage: unknown command or flag, wrong arguments, a bad date, the wrong command for the track type |
| 3 | not signed in, or the server refused the credentials |
| 4 | track not found, or the name matches several tracks |
| 5 | server or network unreachable |


## Supported platforms

Binaries are published for:

| OS | Architecture | Typical machines |
|---|---|---|
| Linux | amd64 | x86-64 PCs and servers |
| Linux | arm64 | Raspberry Pi 3/4/5 with a 64-bit OS, ARM servers |
| Linux | armv7 | Raspberry Pi 2/3/4 with a 32-bit OS |
| macOS | amd64 | Intel Macs |
| macOS | arm64 | Apple Silicon Macs (M1 and later) |
| Windows | amd64 | x86-64 PCs |

Other platforms can install with `go install` (see [Installing tickcli](#installing-tickcli)).
