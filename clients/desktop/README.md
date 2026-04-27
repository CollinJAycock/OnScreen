# OnScreen Desktop Client (v2.1 Track E — scaffold)

The desktop client wrapper for OnScreen. Reuses the existing
SvelteKit frontend (`web/`) inside a Tauri 2 webview, with native
capabilities (audio engine, system tray, notifications, secure
credential storage) implemented as Rust commands the webview
invokes through the Tauri IPC bridge.

**Status:** scaffold only. No actual native features implemented yet
— this commit lays the project skeleton so subsequent work can land
features incrementally without a separate "set up the client repo"
ritual.

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
├── src-tauri/
│   ├── Cargo.toml               ← Rust app crate
│   ├── tauri.conf.json          ← Tauri build/runtime config
│   ├── build.rs                 ← tauri-build helper
│   ├── src/
│   │   ├── main.rs              ← entry point
│   │   └── lib.rs               ← Tauri commands + plugins
│   ├── capabilities/
│   │   └── default.json         ← Tauri 2 permission model
│   └── icons/                   ← (TODO: real icons)
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
| Rust 1.75+ | `rustup install stable` |
| Tauri CLI | `cargo install tauri-cli --locked --version "^2.0"` |
| Platform deps | Windows: WebView2 (preinstalled on Windows 11). macOS: Xcode CLT. Linux: `webkit2gtk-4.1`, `librsvg2-dev`, `build-essential`, `libssl-dev`. |

## Dev workflow

```bash
# Build the SvelteKit frontend once (or run web's vite dev in another terminal)
make -C ../.. web                       # builds web/dist
# OR for live-reload dev:
npm --prefix ../../web run dev          # leaves http://localhost:5173 up

# Then the Tauri shell:
cd src-tauri
cargo tauri dev                         # picks up devUrl per tauri.conf.json
```

## Build a release

```bash
npm --prefix ../../web run build        # produces web/dist
cd src-tauri && cargo tauri build       # produces native installer per platform
```

Output:
- Windows: `src-tauri/target/release/bundle/{msi,nsis}/OnScreen_<v>_x64.{msi,exe}`
- macOS: `src-tauri/target/release/bundle/{dmg,macos}/OnScreen.{dmg,app}`
- Linux: `src-tauri/target/release/bundle/{appimage,deb}/onscreen-<v>.{AppImage,deb}`

## What's not done yet

The scaffold gives you a webview pointing at the existing frontend,
plus one stub Rust command (`get_app_version`) to prove the IPC
bridge compiles. Everything else from Track E — server URL config
picker, secure credential storage, `cpal`-based audio engine,
cross-device watch-history sync — lands in subsequent commits.
