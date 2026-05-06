# OnScreen webOS Client

LG TV (webOS 6+) client for OnScreen. Built as a SvelteKit SPA packaged into an `.ipk` via `ares-package`.

## Prerequisites

- Node.js 24+
- LG webOS TV Development Tools (ares CLI): `npm install -g @webos-tools/cli`
- LG C1 or newer (webOS 6+) with Developer Mode enabled
  - Install the "Developer Mode" app from the LG Content Store
  - Sign in with an LG Developer account, enable dev mode, note the passphrase + IP
  - Register the TV: `ares-setup-device` → add device named `tv` with the TV's IP and `ares-novacom --device tv --getkey` (or paste passphrase)

## Dev loop

```bash
# local dev in a regular browser (scaled to 1920x1080 viewport)
npm install
npm run dev     # http://localhost:5174

# build + side-load to the TV
npm run package
npm run install-tv
npm run launch-tv
npm run inspect-tv   # opens Chromium DevTools targeting the TV app
```

## API endpoint

The client reads its API origin from `localStorage['onscreen.api_origin']`, set on the login screen. No build-time baking.

## Focus model

All focusable elements use `use:focusable` from `src/lib/focus/focusable.ts`. The focus manager listens for arrow keys / enter / back and routes them through spatial navigation — see `src/lib/focus/spatial.ts`.

## Project structure

```
webos/
  appinfo.json            # webOS manifest
  src/
    routes/               # SvelteKit pages (SPA mode)
    lib/
      api/                # REST client (ported from web/)
      focus/              # spatial navigation + remote key handling
      components/         # TV-sized UI primitives
      player/             # hls-loader, progress reporter, trickplay parser
  static/                 # icon.png, largeIcon.png
```

## What's done

- **Auth flow** — server URL setup → username/password login or
  device pairing (PIN) → bearer token persisted, refresh-token
  rotation in `lib/api/client.ts`. Pair screen surfaces an SSO
  hint when the server has OIDC / SAML configured.
- **Hub / Library / Item / Search / Favorites / History /
  Collection / Photo** — all routes wired against the standard
  read endpoints.
- **Player** (`routes/watch/[id]`) — hls.js + HTML5 `<video>`
  HLS pipeline for transcode sessions. Audio + subtitle pickers
  (Yellow / Blue), skip-intro/credits markers with dismissal,
  Up Next overlay 25 s before EOS, EOS chain to next sibling,
  chapter nav (Red/Green), trickplay scrub previews via CSS
  `background-position`, online subtitle search via the
  server's `/items/{id}/subtitles/{search,download}` endpoints.
- **Cross-device SSE progress sync** — watch screen mounts an
  `EventSource` on `/api/v1/notifications/stream` and snaps to
  remote progress when the local player is paused.
- **Settings + sign-out** — `routes/settings/+page.svelte`.
  Sign-out keeps the server URL; forget-server clears
  everything and routes back through `/setup`. About section
  with version + signed-in user + server URL.
- **TMDB Discover + in-app requests** —
  `routes/discover/+page.svelte` searches via
  `/api/v1/discover/search`, surfaces in-library /
  active-request state, submits requests via
  `/api/v1/requests`.
- **Live TV** — `routes/livetv/+page.svelte` lists enabled
  channels with now/next EPG, plays via hls.js against
  `/api/v1/tv/channels/{id}/stream.m3u8`. Single-page model
  (grid ↔ player) so channel surfing doesn't re-fetch.
- **DVR Recordings** — `routes/recordings/+page.svelte` groups
  scheduled / recording / completed / failed / cancelled.
  Completed rows with `item_id` route through the standard
  `/item/[id]` flow.

## What's still not done

- Channel art / icon polish — see `static/`
- LG Content Store submission paperwork (separate distributor
  cert + LG developer profile + content review)
