# RaspberryRelay

A small network relay controller appliance for a Raspberry Pi. One Go daemon
drives N relay outputs over GPIO, exposes an HTTP API for triggering them,
and serves an embedded web UI for configuring relays and network settings.

The driving use case is gate/door control: an access-control panel or
home-automation system POSTs to a trigger endpoint and a relay closes for a
configured pulse.

## Software stack

- Go + `net/http` (stdlib, no framework)
- `config.json` with atomic writes — no database
- [`go-gpiocdev`](https://github.com/warthog618/go-gpiocdev) for GPIO (pure
  Go, no cgo — the kernel character-device ABI libgpiod wraps, without
  needing libgpiod itself)
- systemd to run it as a service
- NetworkManager, driven over D-Bus, for network configuration
- Vanilla HTML/CSS/JS frontend, embedded in the binary

## Building

```
go build -o relayd ./cmd/relayd        # for this machine
make arm64                             # Raspberry Pi 3/4/5, 64-bit OS
make arm7                              # Pi Zero 2 / 32-bit Raspberry Pi OS
make cross                             # all of the above
```

No C toolchain is needed for any target — `go-gpiocdev` is pure Go, so
`CGO_ENABLED=0` cross-compiles cleanly to a single static binary. The web UI
is embedded via `embed.FS`, so that binary is the entire deployment
artifact.

## Running locally (no Pi required)

```
make run
# or:
RELAYD_GPIO=mock go run ./cmd/relayd -config /tmp/relayd-dev/config.json -listen :8080
```

`RELAYD_GPIO=mock` (or `-gpio=mock`) swaps in an in-memory GPIO backend that
logs transitions instead of touching hardware — the default (`-gpio=auto`)
already falls back to it automatically whenever `/dev/gpiochip0` isn't
present, so the daemon runs unmodified on a development machine. Network
configuration degrades the same way: if NetworkManager/D-Bus isn't
reachable, `/api/network` returns `503` and everything else keeps working.

Open `http://localhost:8080/` — default login is **admin / admin**, and
you'll be forced to set a new password before anything else works.

## Deploying to a Pi

```
make arm64
scp build/relayd-arm64 pi@<host>:/tmp/relayd
ssh pi@<host>
sudo /path/to/deploy/install.sh /tmp/relayd
```

`deploy/install.sh` creates a dedicated `relayd` system account (in the
`gpio` group, so it can open `/dev/gpiochip0` without running as root),
installs the binary, the systemd unit (`deploy/relayd.service`), and a
polkit rule (`deploy/90-relayd.rules`) scoped narrowly to that account for
NetworkManager and hostname changes, then enables and starts the service.
Re-running it after an upgrade replaces the binary and restarts the
service without touching the existing `config.json`.

```
systemctl status relayd
journalctl -u relayd -f
```

## HTTP API

All routes except `/api/login` and the static UI require HTTP Basic auth
(see [Authentication](#authentication)).

| Method | Path | Purpose |
| --- | --- | --- |
| `POST` | `/api/relay/{id}/trigger` | Pulse a relay for its configured (or `?duration_ms=`) duration |
| `POST` | `/api/relay/{id}/on` | Latch a relay closed until an explicit `/off` |
| `POST` | `/api/relay/{id}/off` | Open a relay immediately |
| `GET` | `/api/relay/{id}` | One relay's live state |
| `GET` | `/api/relays` | All relays' config + live state |
| `PUT` | `/api/relays` | Replace the relay configuration (takes effect on next restart) |
| `GET` | `/api/device` | Hostname, IP, MAC, firmware version, uptime, CPU temp |
| `GET` / `PUT` | `/api/network` | Read / apply network settings |
| `POST` | `/api/network/confirm` | Keep a just-applied network change (see below) |
| `PUT` | `/api/auth/password` | Change the operator password |
| `POST` | `/api/login` | Credential check for the login page |
| `GET` | `/api/events` | Server-Sent Events stream of relay state changes |

### Repeated/duplicate requests

If a second `trigger` arrives while a relay's pulse is already in flight
(e.g. 100ms into a 2-second pulse), it is **coalesced, not extended and not
rejected**: the response reports `"deduplicated": true` with the time
remaining on the *existing* pulse, and the relay still opens at the
original deadline — never later. A client retry or a double-tap on `TEST`
can therefore never hold a gate open longer than configured.

```
t=0ms     POST /trigger  -> 200 {"state":"on","remaining_ms":2000}
t=100ms   POST /trigger  -> 200 {"state":"on","remaining_ms":1900,"deduplicated":true}
t=2000ms  relay opens                                    (not 2100ms)
```

`POST /on` during a pulse cancels the timer and latches the relay closed
indefinitely — a deliberate operator action overrides a timed pulse.
`POST /off` always wins immediately, regardless of any pulse or latch in
progress.

### Network changes and rollback

Applying a network change can sever the very HTTP connection that requested
it — a typo in a gateway field would otherwise strand a headless Pi. So
`PUT /api/network` is guarded by NetworkManager's own checkpoint/rollback
mechanism: before the change is applied, a checkpoint is created with a 60s
automatic-rollback timeout. If `POST /api/network/confirm` isn't called
within that window (reachable at whatever address turns out to be live),
NetworkManager itself reverts the change — no manual snapshot/restore code
needed. The web UI does this automatically after a save.

## Authentication

Every route is protected by HTTP Basic auth, **and** there's a styled login
page — reconciled by one deliberate detail: the server never sends a
`WWW-Authenticate` header on an API `401`, which is the only thing that
triggers a browser's native grey credential popup. `web/login.html`
collects the username/password once, builds the `Authorization: Basic ...`
header value itself, and stores it in `sessionStorage`; `web/app.js`
attaches it to every subsequent `fetch`. A `401` from any call clears the
stored credential and bounces back to the login page — a real logout,
which Basic auth normally can't offer. Machine clients are unaffected:
`curl -u user:pass ...` and any standard HTTP client send Basic
preemptively regardless of the challenge header.

Because Basic auth means every request could otherwise pay bcrypt's cost
(hundreds of ms on a Pi), verified `Authorization` header values are cached
for 60 seconds; the cache is flushed immediately on a password change so a
rotation takes effect at once. Repeated failed attempts from one source IP
are throttled with a progressive delay.

The default account is **admin / admin**. `must_change` is set on first
boot and blocks every route except `PUT /api/auth/password` until it's
changed — the UI forces this on first login.

## Configuration

Everything lives in one file, `/etc/relayd/config.json` in production
(`-config` to override), written atomically (temp file in the same
directory, fsync, rename, fsync the directory) so a power cut mid-write
can't corrupt it — the normal way these boxes get turned off.

```json
{
  "auth": { "username": "admin", "password_hash": "$2a$...", "must_change": false },
  "http": { "listen": ":80" },
  "gpio": { "chip": "gpiochip0" },
  "relays": [
    { "id": 1, "name": "Main Gate", "gpio": 17, "active_high": false,
      "duration_ms": 2000, "debounce_ms": 0, "enabled": true },
    { "id": 2, "name": "Parking Gate", "gpio": 27, "active_high": false,
      "duration_ms": 5000, "debounce_ms": 0, "enabled": true }
  ],
  "network": { "mode": "dhcp", "hostname": "relay-01" }
}
```

`active_high: false` (the default) matches the common opto-isolated relay
HATs, which are low-triggered. The `network` block records the last intent
the operator asked for; NetworkManager is always the live source of truth
for `GET /api/network`.

## Failsafe behavior

GPIO lines are requested with their inactive level as part of the kernel
line request itself, so there is no window where a pin glitches active
during startup. On `SIGTERM`/`SIGINT` every relay is de-energised and every
line released before the process exits. A `SIGKILL` bypasses that cleanup
and the pin floats once released — boards should have a pull resistor on
the control side to define a safe default in that case, since that's a
hardware property software can't fix.

## Testing

```
go test ./... -race
```

Coverage includes the coalescing behavior above (verified against wall-clock
transition timestamps, not just final state), `/off` always winning,
`/on` latching and cancelling a pending pulse, config atomic-write/validate
round-trips, HTTP auth (including the missing-`WWW-Authenticate` assertion
and the `must_change` gate), and netmask↔CIDR-prefix conversion across the
full 0–32 range.
