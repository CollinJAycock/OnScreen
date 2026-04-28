# OnScreen Roku Client

Roku channel for OnScreen. Targets RokuOS 11+ (the floor for SceneGraph
features we use plus modern HTTPS + HLS support). Written in BrightScript
+ SceneGraph; sideloaded for development, packaged as a `.zip` for Roku
Channel Store submission.

## Why a separate Roku client (instead of reusing web)

Roku doesn't ship a web browser embedding API. Channels are first-class
BrightScript apps that talk to the Roku firmware via SceneGraph (a
declarative XML UI framework) and the BrightScript runtime. The
trade-off vs. our web/Tauri/Android-TV path:

- **Pros:** native to the platform — built-in remote-control focus,
  HLS playback through the firmware Video node (zero work to plumb),
  tiny memory footprint, fast launch, runs on the cheapest TVs and
  sticks. Roku is the largest streaming OS in North America.
- **Cons:** BrightScript is its own language. No code reuse with
  web/Android. We re-implement the API client and view layer in
  BrightScript, the way Plex / Jellyfin / Emby all do.

## Prereqs

| Tool | Notes |
|---|---|
| Roku device | Stick, box, or built-in TV. Developer Mode enabled (see below). |
| Node.js 24+ | For the package + sideload scripts. |
| VS Code + BrightScript extension (optional) | Syntax highlighting, breakpoints. |

### Enable Developer Mode on the Roku

On the Roku remote: **Home × 3, Up × 2, Right, Left, Right, Left, Right**.
A "Developer Settings" screen appears — set a webserver password (you'll
need it for sideloading) and note the device's IP from
**Settings → Network → About**. Reboot when prompted.

The Roku now exposes a sideload web UI at `http://<ip>` and a developer
remote API at `http://<ip>:8060`.

## Project layout

```
roku/
├── manifest                         # channel metadata (no extension)
├── source/
│   ├── main.brs                     # entry point — instantiates MainScene
│   ├── api/
│   │   ├── Client.brs               # roUrlTransfer wrapper w/ Bearer auth
│   │   └── Endpoints.brs            # path constants matching the Go server
│   └── util/
│       ├── Prefs.brs                # roRegistrySection-backed prefs (URL, tokens)
│       └── Json.brs                 # ParseJson / FormatJson helpers
├── components/
│   ├── MainScene.xml + .brs         # root scene; swaps Setup → Login → Home
│   ├── setup/                       # first-run server URL + login
│   ├── browse/                      # hub rows + RowList item presenters
│   └── playback/                    # Video-node-backed player scene
├── images/                          # channel art (icons, splash) — see images/README.md
└── scripts/
    ├── package.mjs                  # zip the project for sideload / store submit
    └── sideload.mjs                 # POST the zip to a developer-mode Roku
```

## Dev loop

```bash
cd clients/roku
npm install                          # one-time

# Static analysis (BrighterScript compiler — type-checks .brs +
# validates SceneGraph XML referenced scripts):
npm run check

# Unit tests for the pure helpers (URL encoding, JSON envelope
# unwrap, string utils, asset URL builders) via the brs Node-side
# interpreter. brs has no SceneGraph or roUrlTransfer support so
# this only covers source/util/* + source/api/Endpoints.brs —
# scene controllers and HTTP-touching code need real hardware.
npm test

# Package the zip (no upload — useful before store submission):
npm run package
# Output lands at dist/onscreen-roku-<version>.zip

# Sideload to a Roku on your LAN:
ROKU_HOST=192.168.1.42 ROKU_DEV_PASSWORD=mypass npm run sideload
```

**Local-only test coverage** (35 cases as of writing): `UrlEncodePath`,
`AssetStream`/`AssetArtwork` URL contracts, `Json_Parse`/`UnwrapData`/
`UnwrapList` envelope handling, `StringTrim` / `StringStripTrailingSlash`.
Anything that depends on `roUrlTransfer`, `roRegistrySection`, or
SceneGraph nodes can only be exercised on real Roku hardware.

When the sideload succeeds the Roku launches the dev channel
automatically. Subsequent reloads replace the running channel in
place. Logs stream over telnet:

```bash
telnet <roku-ip> 8085         # BrightScript console (warnings, runtime errors)
telnet <roku-ip> 8089         # BrightScript debugger (interactive)
telnet <roku-ip> 8087         # Compile log (on first install of a new bundle)
```

VS Code's BrightScript extension wires breakpoints + variable inspection
to those ports automatically; preferred over raw telnet for non-trivial
debugging.

## Server URL — what to enter on first launch

On the device, the setup screen takes the OnScreen server URL. Enter
the LAN URL the Roku can reach:

- **Local server on same LAN:** `http://192.168.1.50:7070` (whatever
  IP serves OnScreen — *not* `localhost` or `127.0.0.1`; the Roku
  isn't on your dev machine).
- **Public server via Cloudflare Tunnel / reverse proxy:**
  `https://onscreen.example.com`.

Bearer-token auth is the only path here; cookies don't work cleanly
across BrightScript's HTTP stack and the Go server's bearer route is
universal anyway.

## What's done

- **Project skeleton** — manifest, MainScene, Setup → Login → Home flow
  scaffolded so the channel installs and renders on a Roku.
- **API client** — `Client.brs` wraps `roUrlTransfer` with the Bearer
  header injection + JSON parse used by every endpoint.
- **Persistent prefs** — server URL + tokens land in
  `roRegistrySection("OnScreen")` so they survive channel reloads.
- **Hub render** — HomeScene fetches `/api/v1/hub` and renders the
  rows in a SceneGraph `RowList`. PosterCard is the per-item
  presenter (focusable, async-loaded poster art).
- **Playback** — PlayerScene wraps the firmware Video node;
  `ContentNode` is built from the item's stream URL with the bearer
  appended as `?token=` for the asset-route middleware.

## What's not done yet

- Direct-play vs. transcode decision logic (currently always direct
  play; needs the same negotiation the Android client does)
- Audio / subtitle track pickers
- Cross-device progress sync (server publishes `progress.updated` SSE
  events; Roku has no native SSE — would need long-poll fallback or
  chunked-read on `roUrlTransfer`)
- Skip intro / credits, trickplay scrub previews, chapter picker
- Search, library browse, favorites, history, settings, notifications
- Channel art (PNG icons + splash JPEGs) — see `images/README.md`

## Distribution

Roku channels reach users through one of two paths:

1. **Roku Channel Store (public)** — submit the `.zip` for review at
   the Roku Developer Dashboard. ~1-2 week review cycle. App appears
   in the device's channel store; users add it from their TV.
2. **Sideload (developer mode)** — power users enable developer mode
   on their Roku and upload the zip directly. Same path we use for
   dev. Plex's "Plex for Roku Beta" channel works this way.

Plex / Jellyfin / Emby all ship through the official store. Worth
following the same path once the channel is feature-complete.
