#!/usr/bin/env bash
# Build the GPU image and apply any pending schema migrations.
# Run from the repo root on the TrueNAS host:  ./docker/deploy-gpu.sh
#
# Restarting the server container itself is left to the TrueNAS app UI
# (or `docker restart <name>`) — this script only handles the build and
# the one step that's easy to forget.

set -euo pipefail

IMAGE="${IMAGE:-onscreen:gpu}"
FFMPEG_IMAGE="${FFMPEG_IMAGE:-onscreen-ffmpeg:latest}"
SERVER_CONTAINER="${SERVER_CONTAINER:-}"
DATABASE_URL="${DATABASE_URL:-}"
REBUILD_FFMPEG="${REBUILD_FFMPEG:-0}"

cd "$(dirname "$0")/.."

# Build the ffmpeg base image if missing or if explicitly requested. This one
# is slow (~30 min on first build), so skip it when a cached copy is already
# available.
if [[ "$REBUILD_FFMPEG" == "1" ]] || ! sudo docker image inspect "$FFMPEG_IMAGE" >/dev/null 2>&1; then
    echo "==> Building $FFMPEG_IMAGE (base)"
    sudo docker build -f docker/Dockerfile.ffmpeg -t "$FFMPEG_IMAGE" .
else
    echo "==> Using cached $FFMPEG_IMAGE (set REBUILD_FFMPEG=1 to force)"
fi

echo "==> Building $IMAGE"
sudo docker build -f docker/Dockerfile.gpu -t "$IMAGE" .

if [[ -z "$DATABASE_URL" ]]; then
    # Pull it from the running server container's env, if we can find one.
    if [[ -z "$SERVER_CONTAINER" ]]; then
        SERVER_CONTAINER=$(sudo docker ps --filter "ancestor=$IMAGE" --format '{{.Names}}' | head -n1 || true)
    fi
    if [[ -n "$SERVER_CONTAINER" ]]; then
        DATABASE_URL=$(sudo docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "$SERVER_CONTAINER" | grep '^DATABASE_URL=' | head -n1 | cut -d= -f2-)
    fi
fi

if [[ -z "$DATABASE_URL" ]]; then
    echo "!! DATABASE_URL not found. Set it explicitly and re-run:" >&2
    echo "   DATABASE_URL='postgres://...' ./docker/deploy-gpu.sh" >&2
    exit 1
fi

echo "==> Applying migrations"
sudo docker run --rm --network host --entrypoint /usr/local/bin/goose \
    "$IMAGE" -dir /migrations postgres "$DATABASE_URL" up

echo "==> Done. Restart the server container to pick up the new image."
