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
      stores/             # auth, profile, player state
  static/                 # icon.png, largeIcon.png
```
