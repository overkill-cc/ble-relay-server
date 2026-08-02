# Deploying `relayd`

Automatic deployment for a systemd Linux server: a timer checks GitHub for a
new release every few minutes and installs it, so pushing to `main` reaches
production without anyone logging in.

## Install

On the server, as root:

```sh
git clone https://github.com/overkill-cc/ble-relay-server
cd ble-relay-server
sudo ./deploy/install.sh
```

Then point it at a real certificate and restart:

```sh
sudoedit /etc/relayd/relayd.env
sudo systemctl restart relayd
```

That is the whole setup. The installer creates a `relayd` system user, installs
the units, fetches the current release, and enables the update timer.

## How an update happens

`relayd-update.timer` fires every 5 minutes and runs `relayd-update.sh`, which:

1. Resolves the latest release tag from GitHub.
2. Exits immediately if it matches the installed version — the normal case.
3. Downloads the `linux_<arch>` archive **and** `SHA256SUMS`, and verifies the
   checksum. Everything so far happens with the service still running, so a
   failed or corrupt download causes no downtime.
4. Copies the running binary aside, stops `relayd`, installs the new one, and
   starts it again.
5. Confirms the service is actually active. If it is not, restores the previous
   binary and restarts — a bad build rolls back on its own.

Only then is the new version recorded in `/var/lib/relayd/installed-version`,
so a failed update retries on the next tick rather than being marked done.

## What gets deployed

The workflow publishes a normal release for **every push to `main`**, plus one
for any hand-pushed `v*` tag. GitHub reports the most recently published
release as "latest", and that is what the server installs. Pushing to `main` is
therefore a production deploy, within one timer interval.

To decouple the two, change `relayd-update.sh` to resolve a pinned tag instead
of `/releases/latest`, and bump it deliberately.

## Layout

| Path | What |
| --- | --- |
| `/opt/relayd/bin/relayd` | The running binary |
| `/opt/relayd/bin/relayd.previous` | Prior binary, kept for rollback |
| `/opt/relayd/bin/relayd-update.sh` | Update script |
| `/etc/relayd/relayd.env` | Flags passed to `relayd` |
| `/etc/relayd/tls/` | Cert and key, readable by the `relayd` group |
| `/var/lib/relayd/installed-version` | Currently deployed tag |

## Operating it

```sh
systemctl status relayd                 # is it up
journalctl -u relayd -f                 # server logs
journalctl -u relayd-update -f          # deploy logs
systemctl list-timers relayd-update.timer
systemctl start relayd-update.service   # check for an update right now
systemctl disable --now relayd-update.timer   # pause auto-deploys
```

To pin to a specific version, write the tag into the state file and the updater
will leave it alone until a newer release appears:

```sh
echo v2026.08.02-5267583 | sudo tee /var/lib/relayd/installed-version
```

## Notes

- **TLS.** `relayd` refuses to start without `-tls-cert`/`-tls-key` unless given
  `-insecure-http`, which is for local development only. If you use certbot,
  either point `relayd.env` at the live cert paths or add a renewal hook that
  copies them into `/etc/relayd/tls` with group `relayd`.
- **Binding :443.** The unit grants `CAP_NET_BIND_SERVICE`, so you can set
  `-addr :443` without running as root.
- **Checksums, not signatures.** `SHA256SUMS` comes from the same release as
  the archive, so it protects against a corrupt or truncated download, not
  against a compromised repository. Signing releases (cosign, minisign) and
  verifying in the updater would close that gap.
- **Restart drops connections.** Deploying stops and starts the process, so
  every live WebSocket session is disconnected and clients must reconnect.
