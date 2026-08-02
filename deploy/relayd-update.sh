#!/usr/bin/env bash
# Install the latest relayd release, if it is newer than what is running.
#
# Tracks whatever GitHub reports as the latest release, which is the most
# recently published non-prerelease. Since the workflow publishes a normal
# release for every push to main, that means this deploys each new build.
#
# Dependencies are limited to curl, tar, sha256sum and systemctl. The release
# tag is resolved from an HTTP redirect rather than the JSON API, so a minimal
# server needs no jq/python3, and the deploy does not consume the
# unauthenticated api.github.com rate limit.

set -euo pipefail

REPO="${RELAYD_REPO:-overkill-cc/ble-relay-server}"
SERVICE="${RELAYD_SERVICE:-relayd.service}"
BIN_DIR="${RELAYD_BIN_DIR:-/opt/relayd/bin}"
STATE_FILE="${RELAYD_STATE_FILE:-/var/lib/relayd/installed-version}"

log()  { printf '%s relayd-update: %s\n' "$(date -uIs)" "$*"; }
fail() { log "ERROR: $*"; exit 1; }

case "$(uname -m)" in
  x86_64|amd64)   arch=amd64 ;;
  aarch64|arm64)  arch=arm64 ;;
  armv7l|armv6l)  arch=arm ;;
  *) fail "unsupported architecture: $(uname -m)" ;;
esac

# /releases/latest redirects to /releases/tag/<tag> for the newest non-prerelease.
latest_url=$(curl -fsSLI -o /dev/null -w '%{url_effective}' \
  --retry 3 --retry-delay 2 "https://github.com/$REPO/releases/latest") \
  || fail "cannot reach GitHub"

tag="${latest_url##*/tag/}"
if [ "$tag" = "$latest_url" ] || [ -z "$tag" ]; then
  log "no release published yet; nothing to do"
  exit 0
fi

current=""
[ -r "$STATE_FILE" ] && current=$(cat "$STATE_FILE")

if [ "$tag" = "$current" ]; then
  log "already on $tag"
  exit 0
fi

log "updating ${current:-<none>} -> $tag"

archive="relayd_${tag}_linux_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$tag"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# Download and verify before touching the running service, so a failed or
# corrupt download never turns into downtime.
curl -fsSL --retry 3 --retry-delay 2 -o "$tmp/$archive" "$base/$archive" \
  || fail "download failed: $base/$archive"
curl -fsSL --retry 3 --retry-delay 2 -o "$tmp/SHA256SUMS" "$base/SHA256SUMS" \
  || fail "download failed: $base/SHA256SUMS"

( cd "$tmp" && grep " $archive\$" SHA256SUMS | sha256sum -c - >/dev/null ) \
  || fail "checksum mismatch for $archive"
log "checksum verified"

tar -xzf "$tmp/$archive" -C "$tmp" relayd || fail "extract failed"
[ -x "$tmp/relayd" ] || fail "extracted binary is not executable"

install -d "$BIN_DIR" "$(dirname "$STATE_FILE")"

# Keep the outgoing binary so a failed start can be rolled back.
[ -f "$BIN_DIR/relayd" ] && cp -a "$BIN_DIR/relayd" "$BIN_DIR/relayd.previous"

log "stopping $SERVICE"
systemctl stop "$SERVICE" || true

install -m 0755 "$tmp/relayd" "$BIN_DIR/relayd"

log "starting $SERVICE"
if systemctl start "$SERVICE" && sleep 3 && systemctl is-active --quiet "$SERVICE"; then
  printf '%s\n' "$tag" >"$STATE_FILE"
  log "now running $tag"
  exit 0
fi

log "ERROR: $SERVICE did not come up on $tag"
if [ -f "$BIN_DIR/relayd.previous" ]; then
  log "rolling back to ${current:-previous binary}"
  install -m 0755 "$BIN_DIR/relayd.previous" "$BIN_DIR/relayd"
  if systemctl start "$SERVICE"; then
    log "rolled back to ${current:-previous binary}"
  else
    log "ERROR: rollback start also failed - manual intervention required"
  fi
else
  log "ERROR: no previous binary to roll back to"
fi
exit 1
