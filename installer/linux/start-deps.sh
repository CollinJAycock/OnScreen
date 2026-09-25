#!/usr/bin/env bash
# Brings up Postgres + Valkey via Docker Compose for OnScreen's deps.
# Equivalent to start-deps.ps1 on Windows. Same docker-compose.deps.yml
# is used on both platforms — Postgres on 127.0.0.1:5432, Valkey on
# 127.0.0.1:6379.
#
# Postgres password: docker-compose.deps.yml has no default any more (the old
# one, "onscreen", is public and the role is a superuser reachable by every
# local process on the loopback port). On first run this script generates a
# random DB_PASS into .env and points DATABASE_URL at it.
#
# Requires Docker Engine (or Docker Desktop on macOS / Windows-with-WSL).
# `docker compose` (v2 plugin) is the modern path; falls back to the
# legacy `docker-compose` script if v2 isn't installed.

set -euo pipefail
cd "$(dirname "$0")"

if docker compose version >/dev/null 2>&1; then
    DC="docker compose"
elif command -v docker-compose >/dev/null 2>&1; then
    DC="docker-compose"
else
    echo "ERROR: docker compose plugin not found. Install Docker Engine 20.10+ with the compose v2 plugin." >&2
    exit 1
fi

if [ ! -f .env ]; then
    echo "ERROR: .env not found. Copy .env.example to .env (and set SECRET_KEY) first." >&2
    exit 1
fi
# .env holds SECRET_KEY and the DB password: owner-only.
chmod 600 .env

if ! grep -Eq '^[[:space:]]*(export[[:space:]]+)?DB_PASS="?[^"[:space:]]' .env; then
    if docker volume inspect onscreen-deps_postgres_data >/dev/null 2>&1; then
        # An existing volume keeps the password it was initialised with — almost
        # certainly the old default. Keep it working, but say how to rotate.
        echo 'DB_PASS="onscreen"' >> .env
        echo "WARNING: existing Postgres volume found; set DB_PASS=\"onscreen\" (the old public default) in .env so it keeps working." >&2
        echo "         Rotate it: $DC -f docker-compose.deps.yml exec postgres psql -U onscreen -c \"ALTER ROLE onscreen PASSWORD '<new>'\"" >&2
        echo "         then update DB_PASS and the password in DATABASE_URL in .env." >&2
    else
        NEW_PASS="$(od -An -N24 -tx1 /dev/urandom | tr -d ' \n')"
        echo "DB_PASS=\"$NEW_PASS\"" >> .env
        # Point the default DATABASE_URL (old default password or the
        # .env.example placeholder) at the generated one.
        sed -i -E "s#(postgres://onscreen:)(onscreen|CHANGE_ME_DB_PASS)@#\1${NEW_PASS}@#" .env
        echo "==> Generated a random DB_PASS in .env (DATABASE_URL updated to match)."
    fi
fi

$DC -f docker-compose.deps.yml up -d
echo "==> Postgres on 127.0.0.1:5432, Valkey on 127.0.0.1:6379. Tail: $DC -f docker-compose.deps.yml logs -f"
