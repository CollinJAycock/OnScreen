#!/usr/bin/env bash
# Brings up Postgres + Valkey via Docker Compose for OnScreen's deps.
# Equivalent to start-deps.ps1 on Windows. Same docker-compose.deps.yml
# is used on both platforms — a single bind to 5432 / 6379 with the
# default creds OnScreen's .env.example points at.
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

$DC -f docker-compose.deps.yml up -d
echo "==> Postgres on 5432, Valkey on 6379. Tail: $DC -f docker-compose.deps.yml logs -f"
