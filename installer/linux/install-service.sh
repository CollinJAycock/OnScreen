#!/usr/bin/env bash
# Register OnScreen as a systemd service.
#
# Materializes onscreen.service into /etc/systemd/system/ with
# ${INSTALL_DIR} and ${RUN_USER} substituted in, reloads the systemd
# daemon, enables the unit at boot, and starts it. Safe to re-run —
# the install-then-restart shape replaces the prior unit file in place.
#
# Run with sudo (writing to /etc/systemd/system requires root). The
# unit itself runs as the invoking user (i.e. SUDO_USER), not root,
# so the server reads media as a normal user.
#
# Tear down: ./uninstall-service.sh

set -euo pipefail
cd "$(dirname "$0")"

if [ "$(id -u)" -ne 0 ]; then
    echo "ERROR: install-service.sh must run as root (writes /etc/systemd/system/onscreen.service)." >&2
    echo "Try: sudo ./install-service.sh" >&2
    exit 1
fi

# Determine install dir + run-as user. SUDO_USER is the original user
# when invoked via sudo; falls back to USER for the rare case of root
# directly running it.
INSTALL_DIR="$(pwd)"
RUN_USER="${SUDO_USER:-${USER:-root}}"

if [ "$RUN_USER" = "root" ]; then
    echo "WARNING: running OnScreen as root is not recommended. Re-invoke this script via sudo from a regular user account." >&2
fi

if [ ! -f .env ]; then
    echo "ERROR: .env not found in $INSTALL_DIR. Copy .env.example to .env and fill in the required keys before installing the service." >&2
    exit 1
fi

# .env holds SECRET_KEY (forges admin tokens, decrypts stored API keys) and the
# database password. `cp .env.example .env` under the usual umask 022 leaves it
# 0644 — readable by every local account. Make it owner-only: the run user
# needs it for start.sh/migrate.sh; systemd reads EnvironmentFile= as root.
chown "$RUN_USER" .env
chmod 600 .env

# The unit runs with all of RUN_USER's groups. Membership of docker (or lxd)
# is root-equivalent, and sudo/wheel/admin are one password away from it, so a
# code-execution bug in the server or ffmpeg would inherit that. Warn rather
# than refuse, so existing installs keep working.
for g in docker lxd sudo wheel admin; do
    if id -nG "$RUN_USER" 2>/dev/null | tr ' ' '\n' | grep -qx "$g"; then
        echo "WARNING: $RUN_USER is in the '$g' group, which the OnScreen service inherits. Prefer a dedicated account, e.g.:" >&2
        echo "         sudo useradd --system --no-create-home --groups video,render onscreen   (then install as that user)" >&2
    fi
done

UNIT_SRC="$INSTALL_DIR/onscreen.service"
UNIT_DST="/etc/systemd/system/onscreen.service"

if [ ! -f "$UNIT_SRC" ]; then
    echo "ERROR: missing $UNIT_SRC — was the tarball extracted cleanly?" >&2
    exit 1
fi

echo "==> Materializing $UNIT_DST"
echo "    INSTALL_DIR=$INSTALL_DIR"
echo "    RUN_USER=$RUN_USER"

# Render the template by replacing ${VAR} placeholders. Using sed
# avoids the eval-in-shell shape that would mishandle paths with
# spaces or quotes.
sed \
    -e "s|\${INSTALL_DIR}|$INSTALL_DIR|g" \
    -e "s|\${RUN_USER}|$RUN_USER|g" \
    "$UNIT_SRC" > "$UNIT_DST"
chmod 644 "$UNIT_DST"

# Make sure the run user can read INSTALL_DIR. Don't chown wholesale
# — just confirm the user owns it (typical case: they extracted the
# tarball themselves before running this script with sudo).
if ! sudo -u "$RUN_USER" test -r "$INSTALL_DIR/server"; then
    echo "ERROR: $RUN_USER cannot read $INSTALL_DIR/server. Either chown the install dir to $RUN_USER, or extract the tarball as $RUN_USER first." >&2
    exit 1
fi

echo "==> Reloading systemd"
systemctl daemon-reload

echo "==> Enabling + starting onscreen.service"
systemctl enable onscreen.service
systemctl restart onscreen.service

echo "==> Done."
echo "    systemctl status onscreen   — show service state"
echo "    journalctl -u onscreen -f   — tail logs"
