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

The repo has wrappers for both `make` (Linux/macOS) and PowerShell
(Windows) so a single command runs Vite + Tauri together and tears
both down on exit. First-time setup needs the Tauri CLI:

```bash
cargo install tauri-cli --locked --version "^2.0"     # one-time per box
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
make client-check        # cargo check, ~30s after first cache fill
```

Catches Rust-level regressions in audio.rs / lib.rs before paying
for the full Tauri bundle. Use this when you don't trust a Rust
change.

## Build a release

```bash
make client-build        # builds web → cargo tauri build
# or on Windows:
cd clients\desktop && .\build.ps1
```

Output (per platform):
- Windows: `src-tauri/target/release/bundle/{msi,nsis}/OnScreen_<v>_x64.{msi,exe}`
- macOS: `src-tauri/target/release/bundle/{dmg,macos}/OnScreen.{dmg,app}`
- Linux: `src-tauri/target/release/bundle/{appimage,deb}/onscreen-<v>.{AppImage,deb}`

First build after a clean checkout pulls ~300+ crates and takes
5-10 minutes; subsequent builds with a warm cache land in 30-90s.

## CI builds

`.github/workflows/desktop-client.yml` builds installers on
Windows / macOS / Linux for every tag push (`v*`) and on manual
`workflow_dispatch`. PRs deliberately don't trigger this workflow
because Tauri builds are slow — the regular `ci.yml` covers
code-level regressions on every PR; this one exists to produce
real installers without burning CI minutes on every commit.

Trigger a build manually from the Actions tab → "Desktop client"
→ "Run workflow", or push a `v*` tag. Installers land as workflow
artefacts (`onscreen-desktop-Windows`, `onscreen-desktop-macOS`,
`onscreen-desktop-Linux`) for 14 days.

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

## What's not done yet

- **Seek under native engine**: scrubbing the seek bar does
  nothing while native playback is active. The engine has no
  `seek_to(position_ms)` yet — would need claxon + HTTP-range
  seeks (FLAC's frame-header structure means we either consult
  the seek table from the metadata block or scan forward; both
  are nontrivial against a streaming HTTP source).
- **Gapless preload**: there's a brief silence between tracks
  while the new `audio_play_url` HTTP fetch + claxon header
  parse runs (~200-500 ms). Closing this needs a second
  decoder slot in the engine (`PreloadSlot` parallel to
  `ActivePlayback`) + a new `audio_preload_url` IPC + an
  `audio_play_url` fast-path that promotes a matching preload
  rather than cold-starting. Frontend would optimistically
  preload `nextTrack` whenever it changes.
- **Exclusive-mode toggle**: WASAPI exclusive on Windows
  (`cpal::SupportedStreamConfig::buffer_size`-driven), CoreAudio
  per-stream nominal-rate switching on macOS, ALSA `hw:` device
  enumeration on Linux. cpal exposes the hooks; the UI needs to
  surface the choice.
- **Secure credential storage** — tauri-plugin-keychain swap so
  refresh tokens leave the plaintext appdata file. (See lib.rs
  comment on `KEY_ACCESS_TOKEN` for the threat model.)
- **System tray + media keys + notifications**
- **Cross-device watch-history sync** — server side is mostly
  there; client side needs the sync protocol.
