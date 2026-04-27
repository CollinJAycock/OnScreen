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

## CORS / auth caveats for cross-origin native runs

The Tauri webview runs at `tauri://localhost` (Linux/Win) or
`https://tauri.localhost` (Win in some configs); the OnScreen
server you're connecting to runs at whatever URL the user picked
in the setup screen. That's a **cross-origin** fetch, with the
following implications operators need to know:

1. **CORS allow-list** — set `CORS_ALLOWED_ORIGINS` on the
   OnScreen server to include the Tauri origin(s). The middleware
   already handles `Access-Control-Allow-Credentials: true` when
   the origin matches.
2. **Cookie-based auth needs `SameSite=None; Secure`** — which
   means HTTPS. A plain `http://localhost:7070` server won't ship
   cookies cross-origin to the Tauri webview because the cookie
   spec forbids `Secure` on http. For local dev, run OnScreen
   under https (use `mkcert` for a trusted localhost cert) or
   wire bearer-token-only auth (planned follow-up).
3. **The api.ts wrapper already flips** `credentials: 'same-origin'`
   to `'include'` automatically when `apiBase` doesn't start with
   `/` — same-origin browser builds keep the existing behaviour.

## What's done in this scaffold

- Tauri 2 project skeleton (Rust + plugins + capabilities + icons)
- IPC commands: `get_app_version`, `get_server_url`, `set_server_url`
  (the latter validates URL is http/https before persisting via
  tauri-plugin-store)
- Frontend: `web/src/lib/native.ts` Tauri detection + IPC shims;
  api.ts honours the configured URL; layout renders a server-URL
  setup screen on first launch in the native shell

## What's not done yet

- Secure credential storage (next: tauri-plugin-keychain integration
  so PASETO refresh tokens survive process restart without
  re-typing the password)
- Bearer-token auth path (so plain-http localhost servers work
  without the cookie/HTTPS dance)
- `cpal`-based audio engine for bit-perfect playback
- System tray + media keys + notifications
- Cross-device watch-history sync
