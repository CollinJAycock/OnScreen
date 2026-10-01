#!/usr/bin/env bash
# Entry point of the onscreen-desktop-linux image (see Dockerfile and
# ../README.md). Builds the .deb, .rpm and .AppImage from a read-only
# checkout mounted at /src, copies them to /out with a SHA256SUMS file,
# then sanity-checks what it built.
#
# Expects web/dist to exist in the checkout: build the frontend on the host
# first (cd web && npm ci && npm run build), the same bundle every platform
# ships.
#
# DESKTOP_VERSION=X.Y.Z (optional, -e on docker run) stamps that version
# instead of tauri.conf.json's, the way CI stamps a tag's version.
set -euo pipefail

SRC=${SRC:-/src}
OUT=${OUT:-/out}
WORK=${WORK:-/build}
TARGET=${CARGO_TARGET_DIR:-/target}

if [[ ! -f "$SRC/clients/desktop/src-tauri/tauri.conf.json" ]]; then
  echo "error: mount the repository root at $SRC (-v \"\$PWD:/src:ro\")" >&2
  exit 1
fi
if [[ ! -f "$SRC/web/dist/index.html" ]]; then
  echo "error: $SRC/web/dist is missing; build the frontend first:" >&2
  echo "       cd web && npm ci && npm run build" >&2
  exit 1
fi

echo "==> Copying sources into $WORK"
rm -rf "$WORK"
mkdir -p "$WORK"
# Only what the bundle needs. The host's src-tauri/target (a Windows build
# on a Windows checkout, several GB) stays behind; tauri.conf.json finds the
# frontend at ../../../web/dist, so the relative layout is kept. Cargo.lock
# comes along: it's committed, and the build below is --locked.
tar -C "$SRC" \
  --exclude='clients/desktop/src-tauri/target' \
  --exclude='clients/desktop/node_modules' \
  -cf - clients/desktop web/dist | tar -C "$WORK" -xf -

cd "$WORK/clients/desktop/src-tauri"
config_args=()
if [[ -n "${DESKTOP_VERSION:-}" ]]; then
  if [[ ! "$DESKTOP_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo "error: DESKTOP_VERSION must be X.Y.Z (numeric), got '$DESKTOP_VERSION'" >&2
    exit 1
  fi
  version=$DESKTOP_VERSION
  config_args=(--config "{\"version\":\"$version\"}")
else
  # The first "version" key in tauri.conf.json is the top-level one.
  version=$(grep -m1 -oP '"version"\s*:\s*"\K[^"]+' tauri.conf.json)
fi

# The AppImage's GStreamer plugins (bundleMediaFramework); see the script.
gst_dir="$WORK/gst-plugins"
bash "$WORK/clients/desktop/linux-build/stage-gst-plugins.sh" "$gst_dir"
export GSTREAMER_PLUGINS_DIR="$gst_dir"

# tauri-bundler only clears each format's staging dir, so installers of an
# earlier version would otherwise pile up in the target volume.
bundle="$TARGET/release/bundle"
rm -rf "$bundle"

echo "==> cargo tauri build (deb, rpm, appimage), version $version"
echo "    $(rustc --version); $(cargo tauri --version)"
cargo tauri build --bundles deb,rpm,appimage "${config_args[@]}" -- --locked

# Exactly one file per format, of the version just built.
pick() {
  local matches=("$@")
  if [[ ${#matches[@]} -ne 1 || ! -f "${matches[0]}" ]]; then
    echo "error: expected one installer, found: ${matches[*]}" >&2
    exit 1
  fi
  printf '%s\n' "${matches[0]}"
}
shopt -s nullglob
deb_src=$(pick "$bundle"/deb/*_"$version"_*.deb)
rpm_src=$(pick "$bundle"/rpm/*-"$version"-*.rpm)
appimage_src=$(pick "$bundle"/appimage/*_"$version"_*.AppImage)
shopt -u nullglob

mkdir -p "$OUT"
rm -f "$OUT"/*.deb "$OUT"/*.rpm "$OUT"/*.AppImage "$OUT"/SHA256SUMS
cp "$deb_src" "$rpm_src" "$appimage_src" "$OUT"/
deb="$OUT/$(basename "$deb_src")"
appimage="$OUT/$(basename "$appimage_src")"
(cd "$OUT" && sha256sum -- *.deb *.rpm *.AppImage > SHA256SUMS)

echo
echo "==> Sanity checks"
check=$(mktemp -d)
status=0

echo "--- dpkg-deb -I $(basename "$deb")"
dpkg-deb -I "$deb"
echo "--- dpkg-deb -c $(basename "$deb")"
dpkg-deb -c "$deb"

# Every shared library the installed binary links must resolve. (This image
# has the -dev packages, so it proves the link set, not the deb's Depends;
# install the deb in a clean container for that, see the README.)
dpkg-deb -x "$deb" "$check/deb"
for bin in "$check"/deb/usr/bin/*; do
  echo "--- ldd ${bin#"$check/deb"}"
  ldd "$bin"
  if ldd "$bin" | grep -q 'not found'; then
    echo "error: unresolved libraries in ${bin#"$check/deb"}" >&2
    status=1
  fi
done
echo "--- .desktop"
cat "$check"/deb/usr/share/applications/*.desktop

# The AppImage runtime extracts itself without FUSE.
echo "--- $(basename "$appimage") --appimage-extract"
(cd "$check" && "$appimage" --appimage-extract >/dev/null)
root="$check/squashfs-root"
ls -l "$root" "$root/usr/bin"
echo "bundled libraries: $(find "$root/usr/lib" -name '*.so*' | wc -l)"
staged=$(find "$gst_dir" -mindepth 1 | wc -l)
bundled=$(find "$root/usr/lib/gstreamer-1.0" -name 'libgst*.so' 2>/dev/null | wc -l)
echo "bundled GStreamer plugins: $bundled (staged: $staged)"
if [[ "$bundled" -ne "$staged" || ! -f "$root/apprun-hooks/linuxdeploy-plugin-gstreamer.sh" ]]; then
  echo "error: the AppImage is missing its GStreamer plugins or their AppRun hook" >&2
  status=1
fi
# The host's Mesa must use the host's libwayland-client: a bundled (older)
# copy lacks symbols newer Mesa needs, EGL fails and the window stays blank
# (seen on Fedora 44). The linuxdeploy that tauri-cli < 2.12 fetches bundled it.
if compgen -G "$root/usr/lib/libwayland-client.so*" >/dev/null; then
  echo "error: the AppImage bundles libwayland-client; use tauri-cli 2.12.1 or later" >&2
  status=1
fi
rm -rf "$check"

# Hand the files to whoever owns the output mount (a Linux host would
# otherwise get root-owned files).
chown "$(stat -c %u:%g "$OUT")" "$OUT"/* 2>/dev/null || true

echo
echo "==> Installers in $OUT"
ls -l "$OUT"
cat "$OUT/SHA256SUMS"
exit "$status"
