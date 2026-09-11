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

| Key | |
|---|---|
| `↑/k` `↓/j` | move |
| `enter` | open an instance, or log in to it |
| `p` | pause or resume the selected monitor |
| `/` | filter monitors by name or target |
| `esc` | back |
| `?` | help |
| `q` | quit, from the menu |

The menu footer says which instances answer: `home ok   vps down   lab no cred`.

## What it stores

- `~/.config/lazykuma/config.toml`: the instances, name and URL. Safe to keep in your dotfiles.
- `~/.local/state/lazykuma/tokens.json`: the login token of each instance, mode 0600.

Your password and 2FA code are sent to Kuma once, at login, and never written anywhere. When Kuma
refuses a token (it expired, or you changed your password) the instance says `bad cred` and asks you
to log in again.

## Requirements

Uptime Kuma 2.x. Kuma 1.x is detected and refused: its API differs.

## Develop

```sh
make test          # unit tests, with the race detector
make integration   # starts a throwaway Kuma 2 in podman or docker and tests against it
```

lazykuma talks to Kuma over its Socket.IO API, which it speaks itself over a websocket: see
`internal/kuma`. `testdata/kuma-v2` holds payloads captured from a real Kuma 2.5.3.
