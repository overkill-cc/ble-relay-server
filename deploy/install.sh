#!/usr/bin/env bash
# One-shot installer for relayd + its auto-update timer on a systemd host.
#
# Idempotent: safe to re-run to pick up changed unit files. An existing
# /etc/relayd/relayd.env is never overwritten.
#
#   sudo ./deploy/install.sh

set -euo pipefail

BIN_DIR=/opt/relayd/bin
CONF_DIR=/etc/relayd
STATE_DIR=/var/lib/relayd
UNIT_DIR=/etc/systemd/system
SRC_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

[ "$(id -u)" -eq 0 ] || { echo "install.sh must run as root" >&2; exit 1; }
command -v systemctl >/dev/null || { echo "systemd is required" >&2; exit 1; }

for dep in curl tar sha256sum install; do
  command -v "$dep" >/dev/null || { echo "missing required tool: $dep" >&2; exit 1; }
done

echo "==> creating relayd system user"
if ! id -u relayd >/dev/null 2>&1; then
  useradd --system --no-create-home --shell /usr/sbin/nologin relayd
fi

echo "==> creating directories"
install -d -m 0755 "$BIN_DIR" "$CONF_DIR" "$STATE_DIR"
install -d -m 0750 -o root -g relayd "$CONF_DIR/tls"

echo "==> installing update script"
install -m 0755 "$SRC_DIR/relayd-update.sh" "$BIN_DIR/relayd-update.sh"

if [ ! -f "$CONF_DIR/relayd.env" ]; then
  echo "==> writing default $CONF_DIR/relayd.env"
  cat >"$CONF_DIR/relayd.env" <<'EOF'
# Arguments passed to relayd. Edit the cert and key paths to match your setup.
#
# For a public deployment TLS is required. Point these at a real certificate,
# e.g. certbot's /etc/letsencrypt/live/<domain>/{fullchain,privkey}.pem, and
# make sure the relayd user can read them.
RELAYD_ARGS=-addr :8443 -tls-cert /etc/relayd/tls/fullchain.pem -tls-key /etc/relayd/tls/privkey.pem
EOF
  chmod 0644 "$CONF_DIR/relayd.env"
else
  echo "==> keeping existing $CONF_DIR/relayd.env"
fi

echo "==> installing systemd units"
install -m 0644 "$SRC_DIR/relayd.service"        "$UNIT_DIR/relayd.service"
install -m 0644 "$SRC_DIR/relayd-update.service" "$UNIT_DIR/relayd-update.service"
install -m 0644 "$SRC_DIR/relayd-update.timer"   "$UNIT_DIR/relayd-update.timer"
systemctl daemon-reload

echo "==> fetching the current release"
# Enable relayd but do not start it yet: the update script installs the binary
# and starts the service itself.
systemctl enable relayd.service >/dev/null
"$BIN_DIR/relayd-update.sh"

echo "==> enabling the update timer"
systemctl enable --now relayd-update.timer

cat <<EOF

Done.

  Config    $CONF_DIR/relayd.env      <- set your TLS cert and key here
  Binary    $BIN_DIR/relayd
  Version   $STATE_DIR/installed-version

  systemctl status relayd
  systemctl list-timers relayd-update.timer
  journalctl -u relayd-update -f

The timer checks for a new release every 5 minutes and deploys it
automatically, rolling back to the previous binary if the service fails
to come up.
EOF
