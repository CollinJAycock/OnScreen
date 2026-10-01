# OnScreen Desktop Client

The desktop client wrapper for OnScreen. Reuses the existing
SvelteKit frontend (`web/`) inside a Tauri 2 webview, with native
capabilities (audio engine, system tray, notifications, secure
credential storage) implemented as Rust commands the webview
invokes through the Tauri IPC bridge.

**Status:** functional — wraps the SvelteKit frontend with native audio
(cpal + WASAPI shared/exclusive, bit-perfect/gapless), system tray,
notifications, media-key + Windows SMTC integration, and OS-keychain
credential storage, all driven via Tauri IPC commands.

**Release integrity (read before distributing):** installers are **not yet
code-signed / notarized**, and there is **no auto-updater**. Until signing is
wired up, Windows SmartScreen will warn on first run, and there is no
authenticated channel to push updates — distribute over a trusted link and
publish SHA-256 checksums. See "Installers" below for how they're built and
"Unsigned installers: what users see".

## Why Tauri (and not Electron / per-platform)

The v2.1 roadmap left the tooling decision open. Picking Tauri:

| Concern | Tauri 2 | Electron | Per-platform native |
|---|---|---|---|
| Frontend reuse with `web/` | ✅ direct (~80%) | ✅ direct | ❌ rewrite |
| Single codebase Windows/macOS/Linux | ✅ Rust + system webview | ✅ Chromium bundled | ❌ N codebases |
| Install size | ~10 MB | ~150 MB | ~5 MB per platform |
| Audiophile path (WASAPI exclusive / CoreAudio HOG / ALSA hw:) | ✅ via Rust `cpal` outside the webview | ✅ via node-native-bindings (Plexamp's path) | ✅ trivially |
| Cross-platform debugging surface | System webview variance (WebView2 / WebKit / WebKitGTK) | uniform Chromium | n/a |

The audiophile pillar — bit-perfect playback — is the single
biggest reason for the native client work, and Tauri achieves it by
**not** routing audio through the webview. The webview hosts the
existing SvelteKit UI; a Rust audio engine fetches the FLAC byte
stream from the server and feeds it into `cpal` with the
platform-exclusive backend. The browser audio API never touches
the bytes, so the OS mixer never resamples. Plexamp pioneered this
pattern on Electron with native node bindings; Tauri's Rust bridge
is the modern equivalent and lets us do it without bundling
Chromium.

The trade-off is system-webview variance. WebView2 (Windows),
WebKit (macOS), and WebKitGTK (Linux) each have quirks. The
mitigation is the existing `web/` codebase already runs in all
three engines via the browser, so we know the surface works.

## Layout

```
clients/desktop/
├── README.md                    ← you are here
├── package.json                 ← npm orchestration: build web → build tauri
├── build.ps1                    ← Windows installers (MSI + NSIS)
├── linux-build/                 ← Linux installers in Docker (deb, rpm, AppImage)
│   ├── Dockerfile               ← ubuntu:24.04 + the CI apt list + pinned Rust and tauri-cli
│   ├── build-in-container.sh    ← image entry point: build, copy out, sanity-check
│   ├── stage-gst-plugins.sh     ← the GStreamer plugins the AppImage carries (CI uses it too)
│   └── build.ps1                ← host wrapper for Windows / Docker Desktop
├── src-tauri/
│   ├── Cargo.toml               ← Rust app crate
│   ├── Cargo.lock               ← committed; release builds use --locked
│   ├── tauri.conf.json          ← Tauri build/runtime config
│   ├── build.rs                 ← tauri-build helper
│   ├── src/
│   │   ├── main.rs              ← entry point
│   │   └── lib.rs               ← Tauri commands + plugins
│   ├── capabilities/
│   │   └── default.json         ← Tauri 2 permission model
│   └── icons/                   ← generated from docs/store-assets/master/icon-1024.png (see its README)
└── .gitignore
```

The webview loads `web/dist/` (production) or
`http://localhost:5173/` (dev — Vite dev server). The frontend
needs no Tauri-aware changes for the basic case; it talks to the
configured OnScreen server via its existing fetch wrapper. Tauri-
specific UI (server URL picker, native audio settings) will sit
behind a runtime check `if (window.__TAURI__)` in the SvelteKit
code so the same bundle still serves the browser.

## Prereqs (one-time)

| Tool | Notes |
|---|---|
| Rust 1.90+ | `rustup install stable`. The committed `Cargo.lock` needs 1.90 (tauri 2.12; `rust-version` in `Cargo.toml`). CI and the Docker recipe build installers with 1.93.0. |
| Tauri CLI | `cargo install tauri-cli --locked --version 2.12.1` (or `make client-deps`): the version CI and the Docker recipe pin. The CLI holds the bundler, so this keeps local installers the same as CI's. |
| Platform deps | Windows: WebView2 (preinstalled on Windows 11). macOS: Xcode CLT. Linux (Debian/Ubuntu names): `build-essential pkg-config file libwebkit2gtk-4.1-dev libgtk-3-dev libsoup-3.0-dev librsvg2-dev libayatana-appindicator3-dev libasound2-dev libdbus-1-dev libssl-dev libxdo-dev` — the same list as `linux-build/Dockerfile` and CI. |

## Dev workflow

The repo has wrappers for both `make` (Linux/macOS) and PowerShell
(Windows) so a single command runs Vite + Tauri together and tears
both down on exit. First-time setup needs the Tauri CLI:

```bash
cargo install tauri-cli --locked --version 2.12.1     # one-time per box
# or `make client-deps` from the repo root
```

Then:

```bash
# From repo root — runs Vite dev server + tauri dev together
make client-dev

# Windows equivalent
cd clients\desktop
.\dev.ps1
```

Either path leaves Vite on http://localhost:5173 and opens the
Tauri webview pointing at it. Save a Svelte file → webview
hot-reloads. Ctrl+C in the terminal stops Tauri; the wrapper
sweeps the Vite child process so the dev port doesn't stay bound.

The lower-level commands work too if you want to drive the two
processes manually:

```bash
npm --prefix web run dev                # one terminal
cd clients/desktop/src-tauri && cargo tauri dev   # another
```

## Smoke check (no full build)

```bash
make client-check        # cargo check --locked, ~30s after first cache fill
```

Catches Rust-level regressions in audio.rs / lib.rs before paying
for the full Tauri bundle. Use this when you don't trust a Rust
change.

## Installers

| Platform | File | Notes |
|---|---|---|
| Windows x64 | `OnScreen_<v>_x64-setup.exe` (NSIS) | Per-user install into `%LOCALAPPDATA%\OnScreen`, no admin prompt. Fetches the WebView2 runtime if it's missing (Windows 10; Windows 11 ships it). |
| Windows x64 | `OnScreen_<v>_x64_en-US.msi` (WiX) | Per-machine install into `C:\Program Files\OnScreen` (admin prompt); the one for Intune / GPO deployment. |
| Linux x86_64 | `OnScreen_<v>_amd64.deb` | Debian/Ubuntu: `sudo apt install ./OnScreen_<v>_amd64.deb` (apt pulls WebKitGTK etc.). The package is called `on-screen`; the command is `onscreen-desktop`. |
| Linux x86_64 | `OnScreen-<v>-1.x86_64.rpm` | Fedora: `sudo dnf install ./OnScreen-<v>-1.x86_64.rpm`. Its requirements are library sonames, so other RPM distros may work too (untested). |
| Linux x86_64 | `OnScreen_<v>_amd64.AppImage` | Distros with glibc 2.39+ (tested on Ubuntu 24.04 and Fedora 44): `chmod +x` and run. About 150 MB, because it carries WebKitGTK, GTK, D-Bus and GStreamer with the plugins video needs. It uses the host's graphics stack (GL, EGL, GLESv2, GBM, DRM, and libwayland-client, which must match the host's Mesa), X11/XCB, font libraries (fontconfig, FreeType, HarfBuzz, FriBidi), expat and ALSA, which every desktop install has. |

**Versions.** `<v>` is the client's version. A CI build of a `v*` tag stamps
the tag's `X.Y.Z` (`v2.5.0` builds `OnScreen_2.5.0_*`); every other build
uses `version` in `tauri.conf.json`, 2.5.0 for the v2.5.0 cycle.
- **Pre-release tags.** WiX accepts only numeric versions, so a tag's `-rc1`
  or `+build` part is dropped. `v2.5.0-rc1` builds 2.5.0 installers, with
  the same version and file names as the final `v2.5.0`. Over an rc
  install, the final `.deb`/`.rpm` is the same version, so it needs a
  reinstall (below). The MSI and NSIS setups install over it anyway.
- **Each release cycle.** Bump `version` in `tauri.conf.json`, `Cargo.toml`
  and `package.json`, then run `cargo update -w` to update `Cargo.lock`. The
  tag's version is stamped on the installers and the .exe's version
  resource, but the Rust crate's own version
  (`env!("CARGO_PKG_VERSION")`, reported by `get_app_version`) comes from
  `Cargo.toml`. A tag build whose version differs from `Cargo.toml` passes
  with a warning.

The Linux builds need **glibc 2.39 or newer** (Ubuntu 24.04+, Debian 13+,
Fedora 40+, Mint 22+), because they're built on Ubuntu 24.04. Older distros
(Ubuntu 22.04, Debian 12) would need a build on an older base.

**Video on Linux** plays through WebKitGTK, which decodes with GStreamer.
Music through the native engine doesn't depend on it.
- **AppImage**: carries its own GStreamer and the plugins listed in
  `linux-build/stage-gst-plugins.sh` (`bundle.linux.appimage.bundleMediaFramework`):
  - Containers: MP4 (including fragmented), Matroska/WebM and MPEG-TS
    demuxers, plus native HLS.
  - Codecs, from gst-libav (FFmpeg's decoders, most of the size): H.264,
    HEVC, AAC, AC-3, E-AC-3, DTS and TrueHD. Also VP8/VP9, Opus, Vorbis,
    FLAC and MP3.
  - Output: PulseAudio/PipeWire and ALSA audio, and the GL video path.
    `autoconvert` (plugins-bad) provides `autovideoflip`, which WebKitGTK
    needs to honour rotation metadata (phone clips); without it they play
    sideways.

  Its AppRun hook points GStreamer at the bundled plugins, so it needs none
  from the host. On Fedora 44 with no GStreamer installed, the bundled one
  decoded H.264/AAC (plain and fragmented MP4), HEVC/AC-3, E-AC-3 and
  VP9/Opus test files.
- **`.deb`**: uses the distro's GStreamer. WebKitGTK depends on the base and
  good plugin sets. The `.deb` also recommends `gstreamer1.0-libav` (H.264,
  HEVC, AAC, AC-3) and `gstreamer1.0-plugins-bad`, which apt installs unless
  Recommends are turned off.
- **`.rpm`**: uses the distro's GStreamer. Fedora's repositories leave out
  most patent-encumbered decoders, so for H.264/HEVC either install codec
  plugins from RPM Fusion or use the AppImage.

`tauri.conf.json` adds `libasound2t64 | libasound2` and `libdbus-1-3` to the
`.deb`'s dependencies (and the matching sonames to the `.rpm`'s): the binary
links both, and without them a minimal install failed to start. The order
matters: on Ubuntu 24.04 a bare `libasound2` can be satisfied by
`liboss4-salsa-asound2`, an OSS shim that lacks `snd_pcm_get_params`, and
the app dies with a symbol lookup error.

The `.desktop` file lists the app under Audio/Video (`"category": "Video"`),
and the icons install into the standard `hicolor` sizes (32 to 512 px); see
`src-tauri/icons/README.md`.

First build after a clean checkout pulls ~300+ crates and takes
5-10 minutes; subsequent builds with a warm cache land in 30-90s.

### Windows (MSI + NSIS), on a Windows box

```powershell
cd clients\desktop
.\build.ps1             # web build, then cargo tauri build -- --locked
```

```powershell
# or by hand, from the repo root
cd web; npm ci; npm run build
cd ..\clients\desktop\src-tauri; cargo tauri build -- --locked
```

`--locked` builds exactly what the committed `Cargo.lock` pins, as CI does.
Use tauri-cli 2.12.1 (see Prereqs) so the bundler matches CI's: `build.ps1`
stops if `cargo tauri --version` reports another version
(`.\build.ps1 -AnyTauriCli` builds with it anyway, with a warning). Tauri's
bundler downloads WiX and NSIS itself on first use. Output:
`src-tauri\target\release\bundle\msi\*.msi` and
`src-tauri\target\release\bundle\nsis\*-setup.exe`. To check an MSI without
installing it, extract it: `msiexec /a <file>.msi /qn TARGETDIR=<empty dir>`
puts the app under `<dir>\PFiles\OnScreen\`.

### Linux (deb, rpm, AppImage), in Docker

Needs only Docker (Docker Desktop with Linux containers works on Windows);
no Rust, GTK headers or WSL setup on the host. `linux-build/Dockerfile` is
ubuntu:24.04 with the same apt packages as CI and the same pinned Rust
(1.93.0) and tauri-cli (2.12.1), set as build args. The packages match the
Docker host's architecture: an x86_64 host builds the x86_64 installers that
CI ships (an ARM host would build arm64 ones; untested).

```powershell
# Windows host, from clients\desktop\linux-build
.\build.ps1                   # builds web\dist first; -SkipWeb reuses it
.\build.ps1 -Version 2.5.1    # stamp a version, as a v2.5.1 tag build does
```

```bash
# Linux/macOS host, from the repo root
make client-build-linux                        # DESKTOP_VERSION=2.5.1 to stamp one
# or by hand (web/dist must exist):
docker build -t onscreen-desktop-linux clients/desktop/linux-build
docker run --rm -v "$PWD:/src:ro" -v "$PWD/dist/desktop-linux:/out" \
  -v onscreen-desktop-target:/target \
  -v onscreen-desktop-cargo:/usr/local/cargo/registry \
  onscreen-desktop-linux
```

What the container does:
1. Copies the read-only checkout in, without `src-tauri/target`. Cargo's
   target dir lives in the `onscreen-desktop-target` volume, so it never
   mixes with a Windows build's `target/`.
2. Stages the AppImage's GStreamer plugins and clears the previous
   installers from the volume.
3. Builds with `--locked`. `APPIMAGE_EXTRACT_AND_RUN=1` lets linuxdeploy run
   without FUSE.
4. Copies the installers of the version it built, and a `SHA256SUMS`, to
   `dist/desktop-linux/`.
5. Checks them, failing the build on a problem:
   - prints `dpkg-deb -I` / `-c` and the `.desktop` file;
   - runs `ldd` on the packaged binary (fails on an unresolved library);
   - extracts the AppImage and fails unless every staged GStreamer plugin and
     its AppRun hook are inside;
   - fails if the AppImage bundles `libwayland-client`. The host's Mesa needs
     the host's copy: with the older bundled one, EGL fails and the window
     stays blank (seen on Fedora 44). tauri-cli before 2.12 bundled it, which
     is why the pin can't go below 2.12.

Remove the two volumes to start from scratch.

To test the `.deb`'s dependencies, install it in a clean container, where
apt has to resolve them:

```bash
docker run --rm -e DEBIAN_FRONTEND=noninteractive \
  -v "$PWD/dist/desktop-linux:/in:ro" ubuntu:24.04 bash -c \
  'apt-get update -qq && apt-get install -y -qq ./in/*.deb >/dev/null &&
   if ldd /usr/bin/onscreen-desktop | grep "not found"; then exit 1; fi &&
   echo "installed; all libraries resolve"'
```

## CI builds

`.github/workflows/desktop-client.yml` builds the installers on Windows
(MSI + NSIS) and Ubuntu 24.04 (deb + rpm + AppImage) on every tag push
(`v*`) and on manual `workflow_dispatch`. `ci.yml` doesn't build the
desktop client at all.

- **PR check**: a PR that touches `clients/desktop/` or the workflow (this
  includes Dependabot's `Cargo.lock` bumps) runs only the `check` job:
  `cargo check --locked` on Ubuntu 24.04 with the pinned Rust. No frontend
  build, no bundling, no artifacts, read-only token. It catches a dependency
  that no longer compiles, or that needs a newer Rust than `RUST_VERSION`,
  before merge instead of on the next release tag. The full installer builds
  stay off PRs because they're slow. Locally, `make client-check` runs
  `cargo check --locked`; to match CI exactly, run it on the pinned
  toolchain (`cargo +1.93.0 check --locked` in `clients/desktop/src-tauri`).
- **Toolchain**: pinned in the workflow's `env` (`RUST_VERSION` 1.93.0,
  `TAURI_CLI_VERSION` 2.12.1), with the same values as the Dockerfile's
  build args (the Makefile's `client-deps` and `build.ps1` carry the same
  CLI version). Builds use `--locked` against the committed `Cargo.lock`,
  which Dependabot's cargo entry keeps current. When the PR check fails
  because a crate raised its minimum Rust, bump `RUST_VERSION` and the
  Dockerfile's `ARG RUST_VERSION` together. When Dependabot moves the
  `tauri` crate to a new minor version, move `TAURI_CLI_VERSION` to the
  matching CLI in all those places: the CLI carries the bundler.
- **AppImage check**: after the build, the Linux leg runs the same AppImage
  checks as the Docker recipe: all staged GStreamer plugins and their AppRun
  hook present, no bundled `libwayland-client`.
- **Manual run on a branch** (Actions → "Desktop client" → "Run workflow"):
  the installers are workflow artifacts, `onscreen-desktop-Windows` and
  `onscreen-desktop-Linux`, kept 14 days, versioned from `tauri.conf.json`.
- **Tag push**: the same artifacts, stamped with the tag's version. Then the
  `publish` job attaches every installer plus
  `onscreen-desktop-checksums.txt` (SHA-256) to the tag's GitHub release.
  - `release.yml` creates that release once its tests and Docker image pass
    (~30 min), usually after these builds finish, so `publish` polls for it
    for up to 60 min instead of creating it.
  - If the release never appears (the Release workflow failed), `publish`
    fails. Fix the release, then re-run the failed job. Re-runs replace the
    assets (`--clobber`).
  - `publish` is the only job with `contents: write`; the builds, which run
    npm and cargo build scripts, have read-only tokens.
- **Moved tags**: re-pushing a tag to another commit starts a second run
  for the same ref. The workflow allows one run per ref at a time
  (workflow-level `concurrency`, `cancel-in-progress`), so the newer run
  cancels the older one, builds and `publish` together, and only the run
  for the tag's current commit publishes. A manual run on a tag likewise
  cancels a run in progress for it. Before polling and again before
  uploading, `publish` checks that the tag still points at the commit it
  built, so a stale run that got that far fails instead of attaching stale
  installers.
- **Re-publishing**: to rebuild and re-attach a tag's installers after the
  artifacts expired, run the workflow manually and pick the tag under "Use
  workflow from". Any run on a `v*` tag publishes. A manual run uses the
  workflow file as of that tag, so tags up to v2.4.1, whose workflow was
  broken, can't be backfilled this way.
- **macOS** is out of the matrix: every tag build through v2.4.1 failed in
  cpal's `impl_platform_host` with E0277, "`(dyn FnMut() + 'static)` cannot
  be sent between threads safely". Add a `macos-latest` leg once that
  compiles (`icon.icns` is already generated).

## Unsigned installers: what users see

None of the installers is code-signed yet, and there is no auto-updater.

- **Windows**: the browser may flag the download as uncommon. On first run
  Microsoft Defender SmartScreen shows "Windows protected your PC"; the user
  clicks **More info → Run anyway**. The MSI's admin prompt names an
  "Unknown publisher". Code-signing fixes the publisher line; the
  SmartScreen prompt goes away once the signing certificate has built up
  reputation.
- **Linux**: no OS warning. dnf notes that it skipped the OpenPGP check
  for a local `.rpm`. The AppImage needs `chmod +x`; it mounts itself with
  FUSE (its runtime is static, so no `libfuse2` is needed, only
  `fusermount`/`fusermount3`), and where FUSE isn't available it runs with
  `--appimage-extract-and-run`.
- **Updates**: users download and install each new version themselves.
  - Windows: the MSI and the NSIS setup install over an existing copy. Every
    MSI carries the same upgrade code (pinned as
    `bundle.windows.wix.upgradeCode`), so it replaces any earlier or later
    OnScreen MSI. The NSIS setup asks first when it finds the same version.
  - Linux: `sudo apt install ./OnScreen_<new>_amd64.deb` or
    `sudo dnf install ./OnScreen-<new>-1.x86_64.rpm` upgrades in place. The
    same version (an rc, then its final release) needs
    `sudo apt install --reinstall ./…deb` or `sudo dnf reinstall ./…rpm`.
    For the AppImage, replace the file.
- **Integrity**: point users at the release's
  `onscreen-desktop-checksums.txt` (`sha256sum -c`, or `Get-FileHash` on
  Windows).

## Server URL — what to put in the setup screen

Two completely different cases depending on whether you're running
dev or a built installer:

### Dev mode (`dev.ps1` / `make client-dev`)

Tauri loads the **Vite dev server** at `http://localhost:5173`,
not the Go server directly. Vite has a proxy that forwards
`/api`, `/media`, `/health`, `/artwork` to `http://localhost:7070`
transparently — same-origin from the webview's POV, no CORS in
play.

**In the setup screen, enter:** `http://localhost:5173`
(NOT `http://localhost:7070` — that bypasses the proxy and forces
the server-side CORS dance below for no reason.)

### Production / installer (`build.ps1` / `make client-build`)

The bundled webview loads from the embedded frontend, presenting
its own origin to your server. No proxy — every API call is
genuinely cross-origin.

**In the setup screen, enter your real server URL:**
`https://onscreen.example.com` or `http://192.168.1.50:7070`.

**On the server, add the Tauri webview origin to CORS** via the
web UI's **Settings → General → CORS Allowed Origins**:

| Platform | Webview origin to add |
|---|---|
| Windows (WebView2) | `http://tauri.localhost` |
| macOS (WKWebView)  | `tauri://localhost` |
| Linux (WebKitGTK)  | `tauri://localhost` |

The middleware re-reads on the next request — no server restart
needed. If unsure of the exact origin, hit **F12** in the Tauri
client, retry the failing request, copy the `Origin` header
verbatim.

### Auth model

The api.ts wrapper sends bearer tokens (not cookies) when running
inside Tauri, so the server doesn't need to opt into credentialled
CORS — `credentials: 'omit'` keeps the preflight simple.
Plain-http localhost servers work without the HTTPS /
SameSite=None dance cookies would require.

## What's done

- **Project skeleton**: Tauri 2 + plugins + capabilities + icons.
- **Server URL config**: first-run picker + `tauri-plugin-store`
  persistence + `set_server_url` URL validation.
- **Bearer-token auth**: `get_tokens` / `set_tokens` /
  `clear_tokens` IPC; `api.ts` carries `Authorization: Bearer` on
  every request natively, refresh path posts the stored
  refresh-token in the body so plain-http localhost works without
  cookies.
- **cpal foundation**: `list_audio_devices` + `play_test_tone` +
  `stop_audio` IPC commands. Diagnostic page at
  `/native/audio-test` lists devices and plays a sine-wave test
  tone per device — proves the engine path works on your
  hardware before the FLAC streaming pipeline lands on top.
- **FLAC streaming engine**: `audio_play_url(url, bearer, device)`
  — `ureq` GET with `Authorization: Bearer …`, `claxon` decoder
  on a dedicated thread, lock-free `ringbuf` SPSC between decoder
  and cpal's realtime callback. Opens the cpal stream at the
  FLAC's native sample rate + bit depth (16-bit → I16 stream,
  ≥17-bit → I32 stream carrying 24-bit-in-32) — the bit-perfect
  contract. `audio_state` reports current playback shape (rate,
  depth, channels, source URL, position, ended); `stop_audio`
  drops the stream + signals the decoder thread to exit. Pause /
  resume via `audio_pause` / `audio_resume` — cpal callback
  writes silence rather than draining the ringbuf, so the
  decoder backpressures itself naturally and no extra CPU burns
  during a pause. Diagnostic page at `/native/audio-test`
  includes a "Play FLAC URL" form so the full pipeline is
  testable against any URL on your server.
- **Music player wiring** (Phase 1 + 2): the AudioPlayer the
  rest of the app uses now routes through the native engine
  when the user opts in via `/native/server` *and* the app is
  running inside Tauri. Track-change kicks off `audio_play_url`,
  pause/resume sync via the same flag the `<audio>` path
  watches, position polling runs at 250 ms (same cadence as
  `<audio>` `timeupdate`) and updates the store, EOS triggers
  `audio.next()` for auto-advance. Engine errors (most likely
  non-FLAC source) flip the preference back off so `<audio>`
  takes over on the next track change.
- **Gapless preload**: the engine holds a `preload` slot
  alongside `current`. Frontend optimistically calls
  `audio_preload_url(nextTrack)` whenever the upcoming track
  changes; the engine spawns a decoder thread + ringbuf in
  the background. When the user advances (or auto-advance
  fires), `audio_play_url` checks for a matching preload and
  promotes it — skipping the HTTP + claxon header round-trip
  entirely. Inter-track gap drops from ~200-500 ms (cold start)
  to whatever the cpal device-activation cost is (~10-20 ms on
  every host we care about). PreloadConsumer enum type-erases
  the i16/i32 dispatch so the engine state isn't generic.
- **Seek under native engine**: `audio_seek(position_ms)`
  tears down the current pipeline and rebuilds it with the
  decoder thread drinking-and-discarding samples up to the
  target before producing output. Correct and simple, but
  bandwidth-heavy for long jumps against remote servers
  (a 70-min seek re-streams ~70 min of FLAC — sub-second on
  gigabit LAN, ~30 s over a typical home internet link).
  HTTP-Range + frame-resync would amortise this; punted to a
  follow-up. Frontend in `commitScrub` suspends polling around
  the IPC so the in-between (engine.current = None) window
  doesn't trigger the polling loop's "engine stopped" exit.
- **Media keys**: `tauri-plugin-global-shortcut` registers
  Play/Pause, Next, Previous, Stop globally so they work
  regardless of focus. Rust handler emits a `media-key` event
  the AudioPlayer listens for and dispatches into the audio
  store. Failures (another app holds a shortcut) are non-fatal.
- **Secure credential storage**: refresh + access tokens live in
  the OS keychain (Windows Credential Manager, macOS Keychain,
  Linux Secret Service via zbus) instead of the plaintext
  appdata JSON. `get_tokens` reads keychain-first with a
  one-shot migration fallback that copies pre-existing store
  entries into the keychain and wipes the plaintext on success.
  `set_tokens` strips legacy entries on every write so a rotated
  refresh token never leaves a stale copy on disk. `keyring 3.x`
  pinned because 4.0 pulls aegis (crypto) that needs clang-cl on
  Windows MSVC builds.
- **System tray + OS notifications**: tray menu (Show OnScreen,
  Play/Pause, Next, Previous, Quit) emits the same `media-key`
  event the global shortcut handler does, so the AudioPlayer
  listener handles both paths uniformly. Left-click brings the
  window forward (unminimises if needed). Now-playing
  notifications fire on track change via `tauri-plugin-
  notification` — only when the OnScreen window isn't focused
  (`document.hasFocus()` guard) so album playback doesn't spam
  the notification shell.
- **Cross-device watch-history sync**: `notification.Broker.Event`
  carries an optional `Data` field for non-user-facing payloads.
  The Progress handler publishes `progress.updated` events
  containing `{item_id, position_ms, duration_ms, state}` after
  every successful record. Frontend's `notifications.ts` routes
  sync events to a separate `progressUpdates` store so they
  don't pollute the bell-icon list. Watch page subscribes:
  when a sync event for the currently-rendered item arrives
  AND local playback is paused/idle, it updates the resume
  position offer and (if the video is loaded) seeks the
  element so a tap on Play picks up at the cross-device
  position. Self-loop guard ignores echoes within 2 s of the
  device's own most recent saveProgress.

## What's not done yet

- **Exclusive-mode toggle**: cpal 0.16 hard-codes
  `AUDCLNT_SHAREMODE_SHARED` in its WASAPI host (verified in
  the crate source) — there's no high-level API to request
  exclusive mode. Real bit-perfect output needs either a cpal
  fork or dropping to the raw `wasapi` crate (Windows only)
  with parallel implementations for CoreAudio HOG mode (macOS)
  and ALSA `hw:` device opens (Linux). All multi-day work each.
  Until then, `<audio>` and the cpal default-mode path both
  route through the OS mixer; a 96 kHz FLAC played to a 48 kHz-
  configured device gets resampled outside of our control. This
  is the headline limitation against the audiophile pillar.
- **OS now-playing widgets** (SMTC on Windows, MPRIS on Linux,
  MediaPlayer on macOS) — surfaces "now playing" to the OS
  shell so taskbar/lockscreen widgets show track + art and
  control transport. `souvlaki` crate is the cross-platform
  wrapper; integration is its own commit.
- **Cross-device sync — additional consumers**: watch page
  consumes resume-position events; AudioPlayer session sync
  (multi-device "what's playing", remote-control transport
  from one device to another) is the next natural consumer
  but waits on a UX design call about whether multiple
  devices should be able to *control* a session or only
  *mirror* it.
