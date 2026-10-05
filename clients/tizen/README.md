# OnScreen Tizen TV Client

Samsung TV (Tizen 5.5+) client for OnScreen. Built as a SvelteKit
SPA packaged into a `.wgt` widget via the Tizen Studio CLI.
Same SvelteKit shape as `clients/webos/` — different platform
glue (config.xml manifest, AVPlay video API instead of `<video>` +
hls.js, `tizen`/`sdb` CLI instead of `ares-*`).

## Prereqs

| Tool | Notes |
|---|---|
| Node.js 24+ | for the SvelteKit build + npm scripts |
| Tizen Studio + CLI | `tizen` + `sdb` go on PATH (~/tizen-studio/tools/ide/bin + ~/tizen-studio/tools/sdb) |
| Author + Distributor certificates | one-time via Tizen Studio's Certificate Manager — needed to sign the .wgt |
| Samsung TV (2019+) | with Developer Mode enabled |

### Enable Developer Mode on the TV

1. From the Smart Hub, open **Apps**.
2. Type `12345` on the remote → a Developer Mode dialog appears.
3. Toggle **On** → enter your dev machine's IP → save.
4. Power-cycle the TV. From your dev machine: `sdb connect <tv-ip>:26101`.

If `sdb devices` lists the TV, sideloading works.

### Generate certificates (one-time)

Tizen Studio → **Tools → Certificate Manager** → **+** → choose
**Tizen** profile name (default `OnScreenDev`) → next through the
author + distributor wizard. The author cert lets you sign builds;
the distributor cert pairs with the TV's partner cert. Use the
**Public** distributor profile for sideload-only dev.

## Dev loop

```bash
cd clients/tizen
npm install                          # one-time

# Local dev in a regular browser (1920x1080 layout):
npm run dev                          # http://localhost:5175

# Build + package + install + launch on the TV:
npm run check && npm test            # svelte-check + the Vitest suite
npm run build                        # SvelteKit → build/, copies config.xml + icon
npm run package                      # tizen package -t wgt → dist/onscreen-tizen-<v>.wgt
npm run install-tv                   # tizen install (over sdb)
npm run launch-tv                    # tizen run -p OnScreenTV.OnScreen
```

`TIZEN_DEVICE=<sdb-name>` selects which connected TV to install
on when more than one is paired. `TIZEN_CERT_PROFILE=<name>`
overrides the cert profile (default `OnScreenDev`).

## Why AVPlay instead of HTML5 `<video>` + hls.js

Tizen's `webapis.avplay.*` is the firmware's hardware-accelerated
playback API. It demuxes HLS / DASH / MP4 in firmware (no
`MediaSource` JS layer), decodes HEVC + AV1 on the silicon
(no CPU-fallback heat), and supports the TV's native 4K + HDR
pipeline. HTML5 `<video>` on Tizen tops out at 1080p SDR for
compatibility-mode pages and falls back to software decoders on
modern codecs — fine for short clips, painful for movies.

The player reaches AVPlay through
[`src/lib/player/avplayMedia.ts`](src/lib/player/avplayMedia.ts), which
gives it a media element's surface (see "Shared with the webOS app"
below). Outside Tizen it reports no AVPlay and the page falls back to its
`<video>`, so `vite dev` against a desktop browser loads cleanly — it just
won't play server sessions there, since `hls.js` isn't bundled.

## API endpoint

The client reads its server URL from
`localStorage['onscreen.api_origin']`, set on the setup screen.
Bearer-token auth is the only path (cookies don't survive
cross-origin from the Tizen webview to a plain-http server, same
as Tauri / webOS / Android-TV).

## Shared with the webOS app

Most of `src/` is the webOS app's (`clients/webos`), which is at parity
with the Android TV app: the hub and its saved layout, libraries (paging,
sort, genre), detail pages (Resume / Play from Beginning, watch marks,
Favorite, Report a problem), artist pages, search, Favorites / History,
Discover, Live TV, Recordings, Settings, scrobbling, and the player (server
sessions on the content timeline, re-issues past what the session has
produced, audio-track switching, subtitles drawn by the app, chapters,
Skip Intro / Credits, Up Next and auto-advance, audiobook speed, bounded
error recovery, "play on this TV" from the app-wide event stream). Port a
fix to both apps; comments in the shared code that name webOS or LG
describe where it came from.

What is Samsung's own:

| Module | What it does on Tizen |
|---|---|
| `src/lib/focus/keys.ts` | Samsung's `VK_*` codes (Return 10009, CH ▲▼ 427 / 428) and `registerTizenKeys()`, which asks the TV for the media, colour and channel keys (the arrows, OK and Return always come). |
| `src/lib/appExit.ts` | Return on a first screen (Home, Setup, Sign in) closes the app to Smart Hub (`tizen.application.getCurrentApplication().exit()`), as Samsung's checklist asks; elsewhere it goes back a page. Exit and Smart Hub stay the system's. |
| `src/lib/player/avplayMedia.ts` | AVPlay behind an `HTMLVideoElement`'s surface (`src`, `currentTime`, `play()` / `pause()`, `seekable`, the element's events), so the shared player runs unchanged. It reads the session's HLS playlist for `seekable` / `duration`, which AVPlay doesn't report, and maps AVPlay errors to `MediaError` codes (a "not supported" demotes the HEVC claim once and restarts as H.264). Music and audiobooks play on the page's own `<video>`. |
| `src/lib/player/loadingLayer.ts` | "Starting playback…" on `<body>` while AVPlay opens: its picture plane covers the page until the first frame. |
| `src/lib/player/hls-loader.ts` | hls.js is never loaded: the page takes its native-HLS path, which lands on AVPlay. (hls.js is a dev dependency for its types and tests only.) |
| `src/lib/device.ts` | The name the server lists this TV under, "Samsung TV — <model> #<tag>", from `webapis.productinfo` (the productinfo privilege). |
| `src/lib/api/capabilities.ts` | Static AVPlay claims (H.264 + HEVC, 4K, 10-bit, HDR; never AV1), corrected for good by runtime demotion. |
| `src/routes/+layout.svelte` | Key registration at boot, Return's root handler, AVPlay released while the app is in the background. |
| `src/app.html` | Polyfills for Tizen 5.5's Chromium 69 (`globalThis`, `queueMicrotask`, `Object.fromEntries`, and the newer ones webOS needs too). |

The build targets Chromium 69: `vite.config.ts` sets `cssTarget: 'chrome69'`
and rewrites Svelte's `:where()` selectors (Chrome 88), and
`src/chromium69Css.test.ts` fails on CSS Chromium 69 drops (flexbox `gap`,
`:focus-visible`, `:is` / `:where`, `aspect-ratio`). The bundle must parse
as ES2019.

## To check on a TV

Covered by tests and a simulation of AVPlay in Chromium, not yet run on a
Samsung panel:

- A film plays through AVPlay on the transparent page (no black plane over
  it, none left behind on the screen Back returns to), with "Starting
  playback…" clearing on the first frame.
- A resume start lands on its point; ← → / ◀◀ ▶▶ seek within the session;
  a jump past what the server has produced opens a new session there.
- Subtitles (drawn by the app) line up with the picture; Audio switches
  the track.
- Up Next, the end of a film, Back, and a trip to Smart Hub and back
  mid-film (the player releases AVPlay and re-opens it at the position).
- A track and an audiobook on the `<video>` element; audiobook speed.
- CH ▲▼ step tracks, chapters and Live TV channels; Return on Home closes
  the app.
- Tizen 5.5 (2020) if one is at hand: the polyfills and the CSS floor.

## What's still not done

- Channel art (real PNG icons) — see `images/README.md`
- Samsung Apps store submission paperwork (separate distributor
  cert + Samsung partner profile + content review)

## Distribution

Two paths same as Roku:

1. **Samsung Apps Store** — submit the `.wgt` to Samsung's Seller
   Portal for review. Public distribution to all Tizen TVs.
2. **Sideload (developer mode)** — power users enable Developer
   Mode and install via Tizen Studio or the `sdb` CLI (the path
   we use for dev).

Plex / Jellyfin / Emby all ship through the official store.
