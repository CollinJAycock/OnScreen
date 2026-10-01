#!/usr/bin/env bash
# Stages the GStreamer plugins the AppImage carries, for
# bundle.linux.appimage.bundleMediaFramework in tauri.conf.json.
#
#   bash stage-gst-plugins.sh <dir>
#   export GSTREAMER_PLUGINS_DIR=<dir>    # then: cargo tauri build
#
# Tauri's AppImage step runs linuxdeploy-plugin-gstreamer, which copies every
# file in $GSTREAMER_PLUGINS_DIR (default: the whole system plugin dir) into
# the AppDir, pulls in their shared libraries and adds an AppRun hook that
# points GStreamer at the bundled copies. WebKitGTK plays <video>/<audio> and
# MSE (hls.js) through GStreamer, so without them video depends on the host's
# plugins, which the bundled GStreamer core can't find outside Debian's
# multiarch layout (e.g. Fedora's /usr/lib64/gstreamer-1.0).
#
# Bundling the whole plugin dir (~250 plugins) would drag in every plugin's
# dependencies; this list is what WebKitGTK playback needs: demuxers,
# parsers, decoders (gst-libav for H.264/HEVC/AAC/AC-3/E-AC-3/DTS), audio
# sinks and the GL video path. The .deb and .rpm don't use it; they get
# GStreamer from the distro.
#
# Packages (Ubuntu 24.04; keep in lockstep with the Dockerfile and the CI
# Linux deps step): gstreamer1.0-plugins-base, -plugins-good, -plugins-bad,
# gstreamer1.0-libav, gstreamer1.0-gl, gstreamer1.0-alsa. The plugin script
# also needs patchelf.
set -euo pipefail

dest=${1:?usage: stage-gst-plugins.sh <dir>}
src=${GST_SYSTEM_PLUGINS_DIR:-/usr/lib/$(uname -m)-linux-gnu/gstreamer-1.0}

plugins=(
  # core (libgstreamer1.0-0)
  coreelements
  # gstreamer1.0-plugins-base
  playback app typefindfunctions pbtypes gio rawparse
  audioconvert audioresample audiorate audiomixer volume
  videoconvertscale videorate
  ogg opus vorbis
  audiotestsrc videotestsrc
  # gstreamer1.0-alsa, gstreamer1.0-gl
  alsa opengl
  # gstreamer1.0-plugins-good
  isomp4 matroska wavparse id3demux apetag icydemux adaptivedemux2
  audioparsers flac mpg123 vpx
  autodetect pulseaudio audiofx videofilter
  # gstreamer1.0-plugins-bad (autoconvert: autovideoflip, without which
  # WebKitGTK turns off video rotation handling)
  videoparsersbad mpegtsdemux opusparse debugutilsbad autoconvert
  # gstreamer1.0-libav
  libav
)

rm -rf "$dest"
mkdir -p "$dest"
for p in "${plugins[@]}"; do
  f="$src/libgst$p.so"
  if [[ ! -f "$f" ]]; then
    echo "error: GStreamer plugin '$p' not found ($f); install the packages listed in $0" >&2
    exit 1
  fi
  # The plugin script copies with plain cp, which follows the link.
  ln -s "$f" "$dest/"
done
echo "Staged ${#plugins[@]} GStreamer plugins in $dest"
