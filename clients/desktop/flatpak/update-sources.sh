#!/usr/bin/env bash
# Regenerates cargo-sources.json and node-sources.json, the offline sources
# the Flatpak build uses, from the lockfiles at a git ref of this repository:
#
#   clients/desktop/src-tauri/Cargo.lock  -> cargo-sources.json
#   web/package-lock.json                 -> node-sources.json
#
#   ./update-sources.sh            # the tag the manifest builds
#   ./update-sources.sh v2.8.0     # another ref (then update tag/commit in
#                                  # tv.on_screen.OnScreen.yml to match)
#
# Needs python3 (with venv) and network access. Uses flatpak-builder-tools
# from $FBT if set, otherwise clones it into a temporary directory.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
manifest="$here/tv.on_screen.OnScreen.yml"
repo=$(git -C "$here" rev-parse --show-toplevel)

ref=${1:-$(sed -n 's/^ *tag: *\(v[0-9][^ ]*\) *$/\1/p' "$manifest" | head -n1)}
if [[ -z "$ref" ]]; then
  echo "error: no ref given and no tag found in $manifest" >&2
  exit 1
fi
echo "==> Lockfiles from $ref ($(git -C "$repo" rev-parse "$ref^{commit}"))"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/web"
git -C "$repo" show "$ref:clients/desktop/src-tauri/Cargo.lock" >"$work/Cargo.lock"
git -C "$repo" show "$ref:web/package-lock.json" >"$work/web/package-lock.json"

fbt=${FBT:-$work/flatpak-builder-tools}
if [[ ! -d "$fbt" ]]; then
  git clone --quiet --depth 1 https://github.com/flatpak/flatpak-builder-tools.git "$fbt"
fi
echo "    flatpak-builder-tools $(git -C "$fbt" rev-parse --short HEAD)"

# A PYTHONPATH from the environment (the flathub-infra CI image sets one
# with an older flatpak-node-generator in it) would shadow the venv's copy.
unset PYTHONPATH
python3 -m venv "$work/venv"
# shellcheck disable=SC1091
. "$work/venv/bin/activate"
pip install --quiet --disable-pip-version-check aiohttp tomlkit "$fbt/node"

echo "==> cargo-sources.json"
python3 "$fbt/cargo/flatpak-cargo-generator.py" "$work/Cargo.lock" \
  -o "$here/cargo-sources.json"

# --no-playwright-browsers: @playwright/test is a devDependency of web/ for
# its end-to-end tests; the build never runs it, so don't download browsers.
echo "==> node-sources.json"
flatpak-node-generator npm "$work/web/package-lock.json" \
  --no-playwright-browsers -o "$here/node-sources.json"

echo "==> Done. If the ref changed, set tag and commit in $(basename "$manifest") to:"
echo "    tag: $ref"
echo "    commit: $(git -C "$repo" rev-parse "$ref^{commit}")"
