# lazykuma

A terminal UI for [Uptime Kuma](https://github.com/louislam/uptime-kuma) v2. Watch every monitor of
every instance you run, live, and pause or resume them without opening the browser.

```
 home · 12 monitors · 11 up · 1 down
╭──────────────────────────────╮╭─────────────────────────────────────────╮
│ ✖ vaultwarden              — ││ nextcloud                               │
│ ● nextcloud             42ms ││ https://cloud.home.lan                  │
│ ● pihole                 3ms ││ up · 99.8% 24h · cert 61 days           │
│ ‖ backup-s3           paused ││ ping  ▁▂▁▃▂▁▇▂▁▂▃▂▁▁▂▁ 42ms              │
│                              ││ beats ████████████████████              │
╰──────────────────────────────╯╰─────────────────────────────────────────╯
```

## Install

Download a binary for Linux, macOS or Windows (amd64 or arm64) from the
[releases](https://github.com/icortesb/lazykuma/releases), or:

```sh
go install github.com/icortesb/lazykuma/cmd/lazykuma@latest
```

One static binary, no runtime dependencies.

## Use

Run `lazykuma`, pick **Add instance**, give it a name and the address you open Kuma at, and log in.
From then on it connects by itself.

Monitors are created and edited from the instance screen. Four types have a form of their own —
HTTP, keyword, ping and TCP port — and every other type Kuma offers is reached through the field
editor, which edits the object Kuma stores (values are JSON: `"text"`, `20`, `true`). The same
editor opens on any monitor with `r`, for the fields a form does not show.

Notification channels live under `c`: Telegram, webhook and email have forms, anything else uses
the field editor, and `t` sends a test so a wrong token says so immediately. A channel marked as
default applies to monitors created afterwards.

`m` silences a monitor while you deploy — now until you end it, or between two times — and `M`
lists what is silenced. `i` shows when monitors went down and came back, and why.

Instances are removed or renamed by editing `~/.config/lazykuma/config.toml` directly; there is no
menu entry for it yet. Renaming an instance, or changing its URL, means logging in again: the stored
token is tied to the URL it was issued for, not the name.

| Key | |
|---|---|
| `↑/k` `↓/j` | move |
| `enter` | open an instance, or log in to it |
| `n` `e` `d` | create, edit or delete a monitor |
| `r` | edit the selected monitor's fields directly |
| `p` | pause or resume the selected monitor |
| `m` `M` | silence the selected monitor; list what is silenced |
| `c` | the instance's notification channels |
| `i` | the incident timeline |
| `/` | filter |
| `esc` | back |
| `?` | help |
| `q` | quit, from the menu |
| `ctrl+c` | quit, from anywhere |

The menu footer says which instances answer: `home ok   vps down   lab no cred`.

## Without the terminal UI

### `lazykuma watch`

Runs until stopped and reports every outage: one line per change on standard output, and a desktop
notification (D-Bus on Linux, Notification Center on macOS, toasts on Windows). The state each
monitor has when watch starts is its starting point, so a fresh start never raises an alert per
monitor. An instance that drops is reported only after 30 seconds unreachable, so a Kuma restart
does not raise an alert. Every connection change is printed too, including a refused login token,
so a watch that can no longer see an instance says so. Run it in a tmux pane, or as a service:

```ini
# ~/.config/systemd/user/lazykuma-watch.service
[Service]
ExecStart=%h/go/bin/lazykuma watch
Restart=on-failure
RestartSec=30

[Install]
WantedBy=default.target
```

### `lazykuma status`

Answers once and exits: `0` when everything is up, `1` when something is down, `2` when no instance
could be reached — so a script can tell "down" from "cannot tell". The first line is the summary;
when something is wrong, the details follow, indented.

```sh
$ lazykuma status
6 up
$ lazykuma status
1 down: shop.example.com
  shop.example.com: connect: connection refused
$ lazykuma status --json
{"text":"1 down: shop.example.com","tooltip":"shop.example.com: connect: connection refused","class":"down","up":5,"down":1,"pending":0,"paused":0,"maintenance":0}
```

`--json` is the shape waybar and similar bars read. With `--json` the exit code is always `0`,
because waybar hides a module whose command fails, which would make it vanish exactly when
something is down; `class` (`up`, `down` or `unreachable`) carries the state instead. A waybar
module:

```json
"custom/kuma": {
  "exec": "lazykuma status --json",
  "return-type": "json",
  "interval": 60
}
```

Each run connects and logs in to every instance afresh, so poll every minute or so, not every
second.

### Notifications

In `config.toml`, all optional:

```toml
[notify]
desktop = false  # the terminal UI notifies while it is open (off, so it does not repeat watch)
watch = true     # lazykuma watch notifies (it always prints)
on = "down"      # "down", or "changes" to hear about recoveries too
```

## What it stores

- `~/.config/lazykuma/config.toml` (`%AppData%\lazykuma` on Windows): the instances and the
  notification settings. Safe to keep in your dotfiles.
- `~/.local/state/lazykuma/tokens.json` (`%LocalAppData%\lazykuma` on Windows): the login token
  of each instance, readable only by you (on Windows, kept in your user profile folder, which
  other users cannot read).

`$XDG_CONFIG_HOME` and `$XDG_STATE_HOME` are honoured, if set, in place of `~/.config` and
`~/.local/state`.

Your password and 2FA code are sent to Kuma once, at login, and never written anywhere. When Kuma
refuses a token (it expired, or you changed your password) the instance says `bad cred` and asks you
to log in again.

With an `http://` URL, the password and the token cross the network unencrypted. Use `https://` for
any instance not on your own LAN.

## Requirements

Uptime Kuma 2.x. Kuma 1.x is detected and refused: its API differs.

## Develop

```sh
make test          # unit tests, with the race detector
make integration   # starts a throwaway Kuma 2 in podman or docker and tests against it
```

lazykuma talks to Kuma over its Socket.IO API, which it speaks itself over a websocket: see
`internal/kuma`. `testdata/kuma-v2` holds payloads captured from a real Kuma 2.5.3.
