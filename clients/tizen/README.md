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

The wrapper at [`src/lib/player/avplay.ts`](src/lib/player/avplay.ts)
exposes a small typed surface and falls back to no-op stubs when
running outside Tizen (so `vite dev` against a desktop browser
loads cleanly — it just won't actually demux HLS there since
we don't ship `hls.js` for the production bundle).

## API endpoint

The client reads its server URL from
`localStorage['onscreen.api_origin']`, set on the setup screen.
Bearer-token auth is the only path (cookies don't survive
cross-origin from the Tizen webview to a plain-http server, same
as Tauri / webOS / Android-TV).

## Focus model

Same as the webOS scaffold — `use:focusable` from
`src/lib/focus/focusable.ts`, spatial navigation in
`src/lib/focus/spatial.ts`. The Tizen-specific bit is
[`src/lib/focus/keys.ts`](src/lib/focus/keys.ts) which maps the
Samsung remote's `VK_*` keycodes (different integers from webOS)
to the same semantic `RemoteKey` union.

`registerTizenKeys()` in the layout root tells the firmware to
forward Back, Play/Pause/Stop, FF/RW, and the colored A/B/C/D
buttons into the webview — without it only the always-on D-pad +
Enter come through.

## Project structure

```
tizen/
  config.xml                  # Tizen widget manifest (W3C Widget format + tizen:application)
  src/
    routes/                   # SvelteKit pages (SPA mode, adapter-static)
    lib/
      api/                    # REST client (Bearer auth, refresh-token)
      focus/                  # spatial nav + Tizen VK_* remote key map
      player/avplay.ts        # Tizen AVPlay wrapper (HW HLS/DASH/MP4)
      player/progress-reporter.ts  # /items/{id}/progress polling
      components/             # TV-sized UI primitives
  scripts/
    assemble-package.mjs      # copy config.xml + icon into build/
    package.mjs               # tizen package -t wgt
    sideload.mjs              # tizen install over sdb
    launch.mjs                # tizen run on the TV
  images/README.md            # what icon files Tizen needs (drop PNGs alongside)
```

## What's done

- **Project skeleton** — SvelteKit + adapter-static + svelte-check,
  config.xml that the Tizen runtime accepts, build/package/install/
  launch scripts wired against the Tizen Studio CLI.
- **Auth flow** — server URL setup → username/password login →
  bearer token persisted in localStorage. Refresh-token rotation in
  `src/lib/api/client.ts`.
- **Hub render** — `routes/hub/+page.svelte` fetches `/api/v1/hub`
  and renders the rows via `HubRow.svelte` + `PosterCard.svelte`.
- **Player** — `routes/watch/[id]/+page.svelte` calls `avplay.open`
  on the transcode session URL with the bearer appended as
  `?token=`. HTML5 `<video>` fallback for `vite dev`.
- **Tizen key registration** — `registerTizenKeys()` fires on app
  mount so Back / MediaPlay / MediaPause / colored buttons reach the
  focus handler.

## What's not done yet

- Audio / subtitle track pickers (AVPlay exposes
  `getStreamInfo()`; wire when the picker UI lands)
- Cross-device progress sync via the SSE notification stream
- Skip intro / credits, trickplay scrub previews
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
