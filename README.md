# BLE Relay — relay server (`relayd`)

The WebSocket relay server that backs the [BLE Relay](https://github.com/overkill-cc/ble-relay)
app. It pairs one **host** device (which owns a real Bluetooth Low Energy
connection to a peripheral) with N **client** devices, and routes frames
between them.

```
[Host app]  <--WebSocket-->  [relayd]  <--WebSocket-->  [Client app]
    |                                                        |
    owns the real BLE connection               reads/writes/subscribes to
    to the physical peripheral                  the host's GATT tree, relayed
```

`relayd` only understands session/auth control messages. Everything else is
routed opaquely — it has no idea what "GATT" is, that semantics lives entirely
in the apps on both ends.

## Install

Prebuilt binaries for Linux (amd64/arm64/armv7), macOS (amd64/arm64) and
Windows (amd64) are attached to every [release](../../releases). Each release
ships a `SHA256SUMS` file — verify a download with:

```sh
sha256sum -c SHA256SUMS
```

Tagged releases (`v*`) are permanent. The `nightly` prerelease is rebuilt on
every push to `main` and replaced each time — don't pin to it.

## Build

```sh
go build ./cmd/relayd
```

## Run

```sh
# Production: TLS is required.
./relayd -addr :8443 -tls-cert /path/cert.pem -tls-key /path/key.pem

# Local development only — plain HTTP, never for a public deployment.
./relayd -addr :8443 -insecure-http
```

| Flag | Default | Meaning |
| --- | --- | --- |
| `-addr` | `:8443` | Listen address |
| `-tls-cert` | — | TLS certificate; required unless `-insecure-http` |
| `-tls-key` | — | TLS private key; required unless `-insecure-http` |
| `-insecure-http` | `false` | Serve plain HTTP/WS instead of HTTPS/WSS (dev only) |

## Test

```sh
go test ./...
```

## Writing your own client

The wire protocol is documented in [CLIENT_INTEGRATION.md](CLIENT_INTEGRATION.md)
precisely enough to reimplement the client side from scratch in any language.

## Privacy policy

`cmd/relayd/privacy_policy.html` is served by this server as the app's public
privacy-policy URL (required by the Play Store listing). Its source of truth is
`PRIVACY_POLICY.md` in the app repo — keep the two in sync when either changes.

## License

Copyright (C) 2026 Finn Tews

This program is free software: you can redistribute it and/or modify it under
the terms of the GNU General Public License as published by the Free Software
Foundation, either version 3 of the License, or (at your option) any later
version.

This program is distributed in the hope that it will be useful, but WITHOUT ANY
WARRANTY; without even the implied warranty of MERCHANTABILITY or FITNESS FOR A
PARTICULAR PURPOSE. See the [GNU General Public License](LICENSE) for more
details.
