#!/usr/bin/env bash
# Apply pending DB migrations. Idempotent — goose tracks applied versions
# in goose_db_version and only runs what's missing. Called automatically
# by start.sh and by the systemd unit's ExecStartPre, so the operator
# never has to remember to run it manually.
#
# Reads DATABASE_URL from .env if it isn't already in the environment
# (systemd's EnvironmentFile populates it for us; start.sh sources .env
# before calling this script).

set -euo pipefail
cd "$(dirname "$0")"

if [ -z "${DATABASE_URL:-}" ]; then
    if [ ! -f .env ]; then
        echo "ERROR: DATABASE_URL not set and no .env to load from." >&2
        exit 1
    fi
    set -a
    # shellcheck source=/dev/null
    . ./.env
    set +a
fi

if [ -z "${DATABASE_URL:-}" ]; then
    echo "ERROR: DATABASE_URL is empty after loading .env." >&2
    exit 1
fi

if [ ! -x ./goose ]; then
    echo "ERROR: ./goose missing or not executable. Re-run chmod +x goose." >&2
    exit 1
fi

if [ ! -d ./migrations ]; then
    echo "ERROR: ./migrations directory missing — was the tarball extracted cleanly?" >&2
    exit 1
fi

./goose -dir migrations postgres "$DATABASE_URL" up
