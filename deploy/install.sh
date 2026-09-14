#!/usr/bin/env bash
# Installs relayd as a systemd service on a Raspberry Pi.
#
# Usage: sudo ./install.sh /path/to/relayd-arm64
#
# Idempotent: re-running it after a binary upgrade just replaces the binary
# and restarts the service, leaving the existing /etc/relayd/config.json
# (and therefore the operator's password and relay settings) untouched.
set -euo pipefail

if [[ $EUID -ne 0 ]]; then
  echo "error: must run as root (sudo ./install.sh <binary>)" >&2
  exit 1
fi

BINARY="${1:-}"
if [[ -z "$BINARY" || ! -f "$BINARY" ]]; then
  echo "usage: sudo ./install.sh /path/to/relayd-<arch>" >&2
  exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "==> creating relayd service account"
if ! id relayd >/dev/null 2>&1; then
  useradd --system --no-create-home --shell /usr/sbin/nologin relayd
fi
# Grants access to /dev/gpiochip0 via the group Raspberry Pi OS's udev rule
# already assigns it to.
usermod -aG gpio relayd || echo "warning: no 'gpio' group found; add relayd to whatever group owns /dev/gpiochip0"

echo "==> installing binary"
install -o root -g root -m 0755 "$BINARY" /usr/local/bin/relayd

echo "==> seeding configuration"
mkdir -p /etc/relayd
chown relayd:relayd /etc/relayd
chmod 0750 /etc/relayd
# relayd itself creates config.json with a default admin/admin account
# (must-change forced) on first start if the file doesn't exist yet — no
# seeding needed here beyond the directory.

echo "==> installing systemd unit"
install -o root -g root -m 0644 "$SCRIPT_DIR/relayd.service" /etc/systemd/system/relayd.service

echo "==> installing polkit rule (NetworkManager + hostname1 access for relayd)"
mkdir -p /etc/polkit-1/rules.d
install -o root -g root -m 0644 "$SCRIPT_DIR/90-relayd.rules" /etc/polkit-1/rules.d/90-relayd.rules

echo "==> enabling and starting relayd"
systemctl daemon-reload
systemctl enable relayd
systemctl restart relayd

echo "==> done. Default login is admin/admin; you will be forced to change it on first sign-in."
echo "    journalctl -u relayd -f    # follow logs"
echo "    systemctl status relayd    # check status"
