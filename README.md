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

### Groups and tags

Groups are Kuma's own: a group created here shows in Kuma's web UI, and the other way round.
`g` makes one, `v` moves the monitor under the cursor into it, and `space` folds it. A group
shows its worst monitor's state. Pausing or silencing a group covers everything in it: Kuma's own
group pause keeps checking the monitors inside it, so lazykuma pauses the group and every monitor
and group it contains, after asking; resuming a group resumes everything in it, including anything
paused on its own. Deleting a group asks whether to keep its monitors.

Tags are Kuma's too. `t` lists them, and a monitor's form ticks which it carries. `/` searches
tag names and values as well as names and targets.

### Monitor detail

`enter` on a monitor opens it at full size:
- its uptime over the last day, 30 days and year, its certificate, and its average ping
- a chart of its pings over the last hour, 6 hours, day, 7 days or 30 days (`←`/`→` switch)
- its latest beats
- the history of its state changes, fetched from Kuma as you scroll

The monitor keys work there too. `x` removes its past state changes from the history, keeping its
beats and uptime; `X` deletes all of its beats, state changes and uptime statistics, so the chart
starts again from now. Each asks first.

Instances are removed or renamed by editing `~/.config/lazykuma/config.toml` directly; there is no
menu entry for it yet. Renaming an instance, or changing its URL, means logging in again: the stored
token is tied to the URL it was issued for, not the name.

### Status pages

`S` lists the instance's public status pages with their addresses. From there:
- `n` creates one; the slug follows the title until you type your own
- `e` edits its title, description, footer, theme, refresh interval, domains and what it shows; everything else the page has (logo, custom CSS, analytics) stays as it is
- `s` arranges its sections and the monitors in each
- `i` posts or edits its incident, and `u` takes it down
- `o` opens it in your browser
- `d` deletes it, after you type its slug

Logos and custom CSS are set in Kuma's web UI.

Kuma gives a page's sections and incident only on the page itself, so `e`, `s` and `i` read them over plain HTTP from `<instance URL>/api/status-page/<slug>`. If a proxy asks for a login there, they say so and save nothing.

### Server

`A` opens the instance's server screen; `tab` and `shift+tab` move between its tabs, and `1`–`4` jump to one. The footer shows the keys of the open tab; `?` lists them all.
- **API keys** for Kuma's metrics endpoint (Prometheus, Grafana): `n` makes one and shows it once — copy it then, lazykuma does not keep it; `space` enables or disables one; `d` deletes one.
- **Proxies** monitors can check through: add, edit, delete, and set one as the default or on every monitor at once. Passwords are never shown.
- **Docker hosts** for docker monitors: add, edit, `t` to test that Kuma reaches the daemon, delete. A docker monitor's `docker_host` field, in the field editor, takes a host's id.
- **Database**: its size, `s` to shrink it (SQLite; on MariaDB there is nothing to shrink), and `X` to clear every monitor's statistics after typing the instance's name. What lazykuma already shows stays on screen; the charts fill again as monitors check.

### Keys on an instance

| Key | |
|---|---|
| `↑/k` `↓/j` | move |
| `enter` | open an instance, or log in to it |
| `enter` on a monitor | open its detail |
| `space`/`enter` on a group | fold or unfold it |
| `n` | create a monitor, in the group under the cursor |
| `g` | create a group |
| `e` | edit the selected monitor, or rename the selected group |
| `d` | delete the selected monitor, or the selected group (keeping or with its monitors) |
| `v` | move the selected monitor into a group |
| `C` | clone the selected monitor |
| `r` | edit the selected monitor's fields directly |
| `p` | pause or resume the selected monitor, or a whole group and everything in it |
| `m` `M` | silence the selected monitor or group; list what is silenced |
| `t` | the instance's tags |
| `c` | the instance's notification channels |
| `i` | the incident timeline |
| `S` | the instance's status pages |
| `A` | the instance's server: API keys, proxies, Docker hosts, database |
| `/` | search names, targets and tags |
| `f` | show only down, up, paused or maintenance monitors |
| `s` | sort by status, name, ping or uptime |
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
