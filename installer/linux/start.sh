#!/usr/bin/env bash
# Foreground launcher for OnScreen on Linux.
#
# Use this for interactive testing, dev runs, or any case where you want
# Ctrl+C to stop the process directly. For production, register the
# systemd unit via install-service.sh.
#
# Loads .env (Bash export-style) into the current shell, prepends the
# bundled ffmpeg dir to PATH, and execs server in the foreground.
# Press Ctrl+C to stop — the Go signal handler does a graceful pool/db
# shutdown.

set -euo pipefail

cd "$(dirname "$0")"

if [ ! -f .env ]; then
    echo "ERROR: .env not found. Copy .env.example to .env and fill in DATABASE_URL + VALKEY_URL + SECRET_KEY first." >&2
    exit 1
fi

# Bash's `set -a` auto-exports every assignment in the sourced file —
# matches the shape of .env.example (export-style or bare KEY=VAL lines).
set -a
# shellcheck source=/dev/null
. ./.env
set +a

# Prepend the bundled ffmpeg dir so the server's encoder probe finds it
# before any system ffmpeg.
if [ -d "$(pwd)/ffmpeg" ]; then
    export PATH="$(pwd)/ffmpeg:$PATH"
fi

exec ./server
