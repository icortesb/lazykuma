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

```sh
go install github.com/icortesb/lazykuma/cmd/lazykuma@latest
```

Or clone and `make build`: one static binary, no runtime dependencies.

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

## What it stores

- `~/.config/lazykuma/config.toml`: the instances, name and URL. Safe to keep in your dotfiles.
- `~/.local/state/lazykuma/tokens.json`: the login token of each instance, mode 0600.

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
