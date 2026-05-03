#!/usr/bin/env bash
# Remove the OnScreen systemd unit. Stops the running service, disables
# auto-start, and deletes /etc/systemd/system/onscreen.service. Leaves
# the install dir + .env + Postgres / Valkey state intact — only the
# unit registration is removed.

set -euo pipefail

if [ "$(id -u)" -ne 0 ]; then
    echo "ERROR: uninstall-service.sh must run as root." >&2
    echo "Try: sudo ./uninstall-service.sh" >&2
    exit 1
fi

UNIT_DST="/etc/systemd/system/onscreen.service"

if systemctl list-unit-files onscreen.service >/dev/null 2>&1; then
    echo "==> Stopping onscreen.service"
    systemctl stop onscreen.service || true
    echo "==> Disabling onscreen.service"
    systemctl disable onscreen.service || true
fi

if [ -f "$UNIT_DST" ]; then
    echo "==> Removing $UNIT_DST"
    rm -f "$UNIT_DST"
fi

systemctl daemon-reload
echo "==> Done. Install dir + .env + media + DB state are untouched."
