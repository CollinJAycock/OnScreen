# Linux installer builder for the OnScreen desktop client (.deb, .rpm,
# .AppImage). Mirrors the Linux leg of .github/workflows/desktop-client.yml
# (same base release, same apt packages, same Rust and tauri-cli versions) so
# a local build and a CI build link against the same libraries and bundle
# with the same tooling. Usage is in ../README.md ("Linux, in Docker"); the
# short version, from the repo root:
#
#   docker build -t onscreen-desktop-linux clients/desktop/linux-build
#   docker run --rm -v "$PWD:/src:ro" -v "$PWD/dist/desktop-linux:/out" \
#     -v onscreen-desktop-target:/target \
#     -v onscreen-desktop-cargo:/usr/local/cargo/registry \
#     onscreen-desktop-linux
#
# The source is mounted read-only and copied into the container; the cargo
# target dir lives in a named volume so it never collides with a Windows
# build's src-tauri/target on the same checkout.
FROM ubuntu:24.04

ARG DEBIAN_FRONTEND=noninteractive

# Keep this list in lockstep with the "Install Linux deps" step in
# .github/workflows/desktop-client.yml.
#   webkit2gtk-4.1 / gtk3 / soup3 / rsvg  Tauri 2 webview + GLib stack
#   ayatana-appindicator                  system tray (loaded at runtime)
#   asound2                               cpal's ALSA backend (alsa-sys)
#   dbus-1                                keyring's Secret Service + souvlaki's MPRIS
#   xdo                                   Tauri's menu/shortcut stack
#   file                                  linuxdeploy (AppImage bundling)
#   gstreamer1.0-* + patchelf             the AppImage's GStreamer plugins
#                                         (see stage-gst-plugins.sh)
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      build-essential \
      ca-certificates \
      curl \
      file \
      patchelf \
      pkg-config \
      gstreamer1.0-alsa \
      gstreamer1.0-gl \
      gstreamer1.0-libav \
      gstreamer1.0-plugins-bad \
      gstreamer1.0-plugins-base \
      gstreamer1.0-plugins-good \
      libasound2-dev \
      libayatana-appindicator3-dev \
      libdbus-1-dev \
      libgtk-3-dev \
      librsvg2-dev \
      libsoup-3.0-dev \
      libssl-dev \
      libwebkit2gtk-4.1-dev \
      libxdo-dev \
 && rm -rf /var/lib/apt/lists/*

ENV RUSTUP_HOME=/usr/local/rustup \
    CARGO_HOME=/usr/local/cargo \
    PATH=/usr/local/cargo/bin:$PATH

# Pinned to the values in desktop-client.yml (env: RUST_VERSION,
# TAURI_CLI_VERSION); change both places together. The dependency set is
# pinned by src-tauri/Cargo.lock (builds use --locked). Declared after the
# apt layer, so bumping them doesn't redo apt.
ARG RUST_VERSION=1.93.0
ARG TAURI_CLI_VERSION=2.12.1

RUN curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs \
      | sh -s -- -y --no-modify-path --profile minimal \
          --default-toolchain "${RUST_VERSION}" \
 && cargo install tauri-cli --locked --version "${TAURI_CLI_VERSION}" \
 && rm -rf /usr/local/cargo/registry

# CARGO_TARGET_DIR: mount a named volume here to keep incremental builds.
# APPIMAGE_EXTRACT_AND_RUN: linuxdeploy is itself an AppImage, and a
# container has no FUSE to mount it.
ENV CARGO_TARGET_DIR=/target \
    APPIMAGE_EXTRACT_AND_RUN=1

COPY build-in-container.sh /usr/local/bin/build-installers
RUN chmod 0755 /usr/local/bin/build-installers

ENTRYPOINT ["/usr/local/bin/build-installers"]
