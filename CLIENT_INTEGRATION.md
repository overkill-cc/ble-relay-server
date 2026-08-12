# Integrating with the BLE Relay relay server — client implementation guide

This document is for implementing a **client** in a different app/codebase that
connects directly to the relay server (`relayd`, the Go server in this repo)
over WebSocket, without using the [BLE Relay](https://blerelay.overkill.cc)
Flutter app at all. It documents the wire protocol precisely enough to
reimplement the client side from scratch in any language.

If you also need to implement the **host** side (the role that owns a real
BLE connection and shares it), see the "Host-only messages" section near the
end — but the primary audience here is a client-only integration.

## Architecture

```
[Host app]  <--WebSocket-->  [relayd]  <--WebSocket-->  [Client app / your app]
   |                                                          |
   owns the real BLE connection                    reads/writes/subscribes to
   to the physical peripheral                       the host's GATT tree, relayed
```

- The relay (`relayd`) only understands session/auth control messages and
  routes everything else opaquely between one host and N clients. It has no
  idea what "GATT" is — that semantics lives entirely in the apps on both
  ends.
- One host session = one username/password pair, created by the host, and
  can expose multiple BLE devices under that one session.
- A client authenticates once against a session (username/password), then
  can open any of that session's devices and perform GATT operations on it.
- Everything is JSON over a single WebSocket connection per client.

Source of truth for everything below: `internal/broker/frame.go`,
`internal/wsserver/handlers.go`, `internal/wsserver/protocol_payloads.go` in
this repo, and the Dart reference implementation in the app repo
(`app/lib/relay_client/protocol.dart` / `app/lib/client/client_session_controller.dart`).

## Endpoints

- `GET /healthz` — plain HTTP, returns `200 ok`. Check this before opening a
  WebSocket to fail fast on a wrong address, matching what `ConnectScreen`
  does in the app.
- `GET /ws/client` — WebSocket upgrade, client role (this is what you want).
- `GET /ws/host` — WebSocket upgrade, host role (only needed if you're also
  implementing the host side).

Use `ws://` for a relay started with `-insecure-http` (local dev only), or
`wss://` for a real deployment. **The client's password is sent in plaintext
during auth** (see below) — always use `wss://` (TLS) outside of local
development.

## Wire format

Every message, in both directions, is a single JSON text WebSocket frame
shaped like this:

```json
{
  "v": 1,
  "ts": 1737558123456,
  "type": "some_message_type",
  "id": "optional-request-id",
  "clientId": "optional, relay-assigned",
  "deviceId": "optional, which device this concerns",
  "payload": { "...": "message-specific fields" }
}
```

- `v`: protocol version, currently always `1`. Not currently checked by the
  relay, but include it.
- `ts`: unix epoch milliseconds. Not required on outgoing messages — the
  relay stamps `ts` itself if you omit it — but harmless to include.
- `type`: one of the fixed strings in the message catalog below.
- `id`: only present on request/response pairs. Generate a fresh unique
  string (e.g. a UUID) per request; the matching response echoes the same
  `id` back, so you can correlate concurrent in-flight requests.
- `clientId`: assigned to you by the relay in the `auth_ok` response. Not
  something you need to set yourself on outgoing messages in most cases —
  the relay already knows which connection sent a frame.
- `deviceId`: which bridged device a message concerns. Omitted for
  session-level messages (auth, device list, ping).
- `payload`: message-specific fields, see each message type below. Omitted
  entirely (not `null`, not `{}`) for message types that carry no payload.

## Quick start: minimal client flow

1. `GET /healthz`, confirm `200`.
2. Open a WebSocket to `/ws/client`.
3. Send `auth` with the username/password a host shared with you (as
   plaintext — that's expected, see below).
4. Wait for `auth_ok` (or handle `auth_error`).
5. Wait for an unsolicited `device_list` push (the relay tells the host you
   joined, and the host proactively sends you its current device list — you
   don't need to request it).
6. Pick a `deviceId`, send `device_open_request` for it.
7. Wait for the matching `gatt_tree` push — this is the device's services/
   characteristics/handles.
8. Use `read_request` / `write_request` / `subscribe_request` /
   `unsubscribe_request` against handles from that tree.
9. Listen for `value_changed` pushes for anything you've subscribed to.
10. Send a `ping` at least every ~30s (see Keepalive below) or the relay will
    drop you as idle.

## Connecting & authentication

Send, as your very first frame after the WebSocket opens:

```json
{"type": "auth", "id": "req-1", "payload": {"username": "MyDevice3", "password": "Xb25sdw9"}}
```

- `username`/`password`: exactly what the host displayed (its share screen
  shows these as plain text plus a QR code encoding `username:password`).
- The password is sent **as plaintext** here — the relay hashes+compares it
  server-side against what the host registered. There is no client-side
  hashing step; that's a host-only concern. This is exactly why `wss://`
  matters for anything beyond local dev.

Success:

```json
{"type": "auth_ok", "id": "req-1", "clientId": "a1b2c3d4e5f6a7b8",
 "payload": {"sessionId": "MyDevice3", "hostOnline": true}}
```

- `clientId`: save this — some flows key off it — though you generally don't
  need to attach it to your own outgoing frames.
- `hostOnline`: `false` means the host is mid-reconnect (still within its
  grace period) — you're authenticated, but device operations may fail or
  hang until `host_reconnected` arrives.

Failure:

```json
{"type": "auth_error", "id": "req-1", "payload": {"reason": "invalid_credentials"}}
```

Possible `reason` values: `invalid_credentials`, or `rate_limited` (5 failed
attempts per source IP per minute triggers a 5-minute lockout — the payload
then also includes `retryAfterSeconds`). On `auth_error`, the relay closes
the connection; reconnect and retry (with backoff if rate-limited).

There is no separate "resume" flow for clients (unlike the host side) — if
your connection drops, just reconnect and send `auth` again with the same
credentials.

## Device list

Pushed to you automatically right after a successful auth (and again any
time the host's device set changes — device added/removed, or a device's
connection state changes):

```json
{"type": "device_list", "payload": {"devices": [
  {"deviceId": "d16d637d-61c4-42ef-9643-c1cc53890952", "name": "DOTTS", "connectionState": "connected"}
]}}
```

`connectionState` is one of: `connected`, `connecting`, `reconnecting`,
`disconnected` — the host's real BLE connection state to that physical
device, not your relay connection state.

## Opening a device

```json
{"type": "device_open_request", "deviceId": "d16d637d-61c4-42ef-9643-c1cc53890952"}
```

No `payload` needed — just set `deviceId` on the envelope. In response
(pushed, not directly correlated by `id`) you'll get:

```json
{"type": "gatt_tree", "deviceId": "d16d...", "payload": {"services": [
  {
    "uuid": "180a",
    "primary": true,
    "characteristics": [
      {
        "uuid": "2a29",
        "handle": 3,
        "properties": ["read"],
        "descriptors": []
      }
    ]
  },
  {
    "uuid": "6e400001-b5a3-f393-e0a9-e50e24dcca9e",
    "primary": true,
    "characteristics": [
      {
        "uuid": "6e400002-b5a3-f393-e0a9-e50e24dcca9e",
        "handle": 5,
        "properties": ["write", "writeWithoutResponse"],
        "descriptors": []
      },
      {
        "uuid": "6e400003-b5a3-f393-e0a9-e50e24dcca9e",
        "handle": 6,
        "properties": ["read", "notify"],
        "descriptors": [{"uuid": "2902", "handle": 7}]
      }
    ]
  }
]}}
```

You'll also get a `ble_state_changed` push for the device right after,
reflecting its current connection state (same enum as in `device_list`).

**Important gotchas:**

- **UUIDs may be short-form.** Standard Bluetooth SIG 16-bit UUIDs (like
  `180a` for Device Information, or `2902` for the CCCD descriptor) can
  arrive as bare 4-character hex strings, *not* the full 128-bit canonical
  form — confirmed directly from real hardware while building the app's
  own Android bridge (it crashed on `java.util.UUID.fromString("180a")`
  until short-form UUIDs were expanded first). Custom 128-bit UUIDs (like
  the `6e400001-...` Nordic UART-style service above) arrive in full form.
  If your platform's UUID type requires the full 128-bit form, expand short
  UUIDs yourself via the standard Bluetooth Base UUID:
  `0000XXXX-0000-1000-8000-00805F9B34FB` (zero-pad the short form to 8 hex
  digits, insert it in place of `XXXX`).
- **`handle` is an opaque integer, not a real ATT handle.** Real BLE ATT
  handles aren't stable across platforms (CoreBluetooth on iOS/macOS doesn't
  expose them at all), so the host assigns its own synthetic sequential
  integers at discovery time. Treat `handle` as an opaque key scoped to this
  one `deviceId` — just echo back whatever value you got from the tree when
  making requests. Don't assume any particular numbering or gaps.
- **`properties`** is a list of zero or more of: `read`, `write`,
  `writeWithoutResponse`, `notify`, `indicate`, `broadcast`,
  `authenticatedSignedWrites`, `extendedProperties`.
- Services commonly include GAP (`1800`) and GATT (`1801`) — these are not
  filtered out at the relay/host level (only the app's own Linux/Android
  local-peripheral-bridge feature filters them, for BlueZ/Android GATT
  server registration reasons specific to that feature). Your client will
  see them like any other service.

## GATT operations

All four request types below follow the same request/response shape: send
with a fresh `id`, correlate the response by that same `id`.

### Read

```json
→ {"type": "read_request", "id": "req-2", "deviceId": "d16d...", "payload": {"handle": 3}}
← {"type": "read_response", "id": "req-2", "deviceId": "d16d...", "payload": {"handle": 3, "ok": true, "value": "gA=="}}
```

`value` is base64-encoded raw bytes. On failure you get an `error` envelope
instead (see Errors below) with the same `id`.

### Write

```json
→ {"type": "write_request", "id": "req-3", "deviceId": "d16d...",
   "payload": {"handle": 5, "value": "AQ==", "withResponse": true}}
← {"type": "write_response", "id": "req-3", "deviceId": "d16d...", "payload": {"handle": 5, "ok": true}}
```

- `value`: base64-encoded bytes to write.
- `withResponse: true` → real BLE "write with response" (ATT confirms it
  landed); you get a `write_response` back.
- `withResponse: false` → real BLE "write without response" (fire-and-
  forget, matches BLE semantics exactly). **No response is sent at all** —
  don't wait for one, and don't reuse `id` correlation for these.

### Subscribe / unsubscribe

```json
→ {"type": "subscribe_request", "id": "req-4", "deviceId": "d16d...",
   "payload": {"handle": 6, "mode": "notify"}}
← {"type": "subscribe_response", "id": "req-4", "deviceId": "d16d...", "payload": {"handle": 6, "ok": true}}
```

`mode` is `"notify"` or `"indicate"` (matches the characteristic's actual
supported properties — check `properties` on the tree first).

```json
→ {"type": "unsubscribe_request", "id": "req-5", "deviceId": "d16d...", "payload": {"handle": 6}}
← {"type": "unsubscribe_response", "id": "req-5", "deviceId": "d16d...", "payload": {"handle": 6, "ok": true}}
```

Once subscribed, value changes arrive unsolicited (no `id`, not a response
to anything):

```json
{"type": "value_changed", "deviceId": "d16d...", "payload": {"handle": 6, "value": "ig=="}}
```

Multiple clients can subscribe to the same characteristic independently —
the host fans out to all of them.

## Connection state changes

Pushed whenever the host's real BLE connection to a device transitions:

```json
{"type": "ble_state_changed", "deviceId": "d16d...", "payload": {"state": "disconnected"}}
```

Also watch for these session-level pushes (no `deviceId`):

```json
{"type": "host_disconnected", "payload": {"graceSeconds": 58}}
{"type": "host_reconnected"}
```

`host_disconnected` means the host's own relay connection dropped (not the
same as a specific device's BLE state) — the relay holds the session open
for `graceSeconds` in case the host reconnects. If it doesn't reconnect in
time, your own connection will eventually just go idle/get closed by the
relay; treat a `host_disconnected` you never see a matching
`host_reconnected` for (within the grace window, generously ~90s) as "host
gone, stop retrying operations until you see it come back or the user
re-authenticates."

## Keepalive

Send `{"type": "ping"}` (no payload) periodically — **every 20–30 seconds**
is a safe interval. The relay replies `{"type": "pong"}` (ignorable, or use
it to detect the round trip). If the relay receives *no* frames at all from
you (ping or otherwise) for **60 seconds**, it closes your connection as
idle. This exists because a WebSocket can look "open" from your side long
after the underlying network is actually dead (mobile radio suspended, NAT
mapping expired) — sending isn't optional if you want to detect that.

## Errors

Any request can fail with an `error` envelope instead of its normal
response, correlated by the same `id` you sent:

```json
{"type": "error", "id": "req-2", "deviceId": "d16d...", "payload": {"code": "unknown_handle", "message": "no characteristic at handle 99"}}
```

Common `code` values you'll see in practice: `unknown_device`,
`unknown_handle`, `gatt_error` (the underlying real BLE operation failed on
the host), `write_failed`, `timeout`. Treat `code` as informational/for
logging — don't hard-code behavior on every possible value, new ones can
appear from the host side's own GATT error mapping.

## Reconnecting

On disconnect, just open a fresh WebSocket to `/ws/client` and send `auth`
again with the same username/password. There's no session state to restore
client-side beyond whatever `deviceId`s/handles you already know about — the
host will push you a fresh `device_list`, and you should re-send
`device_open_request` for anything you were previously using (a fresh
`gatt_tree` will follow) and re-subscribe to anything you need notifications
for again — subscriptions do not survive a client reconnect.

## Message catalog (reference)

| Direction | Type | Payload |
|---|---|---|
| → | `auth` | `{username, password}` |
| ← | `auth_ok` | `{sessionId, hostOnline}` (also sets envelope `clientId`) |
| ← | `auth_error` | `{reason, retryAfterSeconds?}` |
| ← | `device_list` | `{devices: [{deviceId, name, connectionState}]}` |
| → | `device_open_request` | *(none — set `deviceId` on envelope)* |
| ← | `gatt_tree` | `{services: [{uuid, primary, characteristics: [{uuid, handle, properties, descriptors: [{uuid, handle}]}]}]}` |
| → | `read_request` | `{handle}` |
| ← | `read_response` | `{handle, ok, value}` |
| → | `write_request` | `{handle, value, withResponse}` |
| ← | `write_response` | `{handle, ok}` *(only if `withResponse: true`)* |
| → | `subscribe_request` | `{handle, mode}` |
| ← | `subscribe_response` | `{handle, ok}` |
| → | `unsubscribe_request` | `{handle}` |
| ← | `unsubscribe_response` | `{handle, ok}` |
| ← | `value_changed` | `{handle, value}` *(unsolicited)* |
| ← | `ble_state_changed` | `{state}` *(unsolicited)* |
| ← | `host_disconnected` | `{graceSeconds}` *(unsolicited)* |
| ← | `host_reconnected` | *(none, unsolicited)* |
| ↔ | `ping` / `pong` | *(none)* |
| ← | `error` | `{code, message}` |

## Host-only messages

Not needed for a client-only integration, included for completeness if you
later implement the host side too:

| Direction | Type | Payload |
|---|---|---|
| → | `register_session` | `{desiredUsername, passwordHash, passwordSalt}` |
| ← | `session_registered` | `{username}` (relay may suffix your desired username if taken) |
| → | `resume_session` | `{username, passwordHash, passwordSalt}` |
| ← | `session_resumed` | `{ok, pendingClients}` |
| ← | `client_joined` | `{clientId}` |
| ← | `client_left` | `{clientId, reason}` |

Registering a host session requires hashing the password yourself before
sending it — the relay never sees a plaintext host password. The algorithm
(must match exactly, see `internal/session/password.go` here and
`app/lib/relay_client/credentials.dart` in the app repo):

```
salt = 16 random bytes, base64-encoded (standard alphabet, no padding)
hash = base64(SHA-256("{salt}:{plaintext_password}")), no padding
```

Send both `passwordHash` and `passwordSalt` in `register_session`/
`resume_session`; store the plaintext password yourself to display to
whoever you're sharing the session with (the relay only ever stores/sees the
hash).
