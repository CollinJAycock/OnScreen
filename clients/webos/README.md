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

## Luna permissions (ACG)

LG's ACG (Access Control Group) lets an app call a Luna service method only if that method's group is listed in `requiredACG` in `appinfo.json` ([guide](https://webostv.developer.lge.com/develop/guides/acg-guide)).

- webOS TV 25 and older, including webOS 6 on the C1, don't support ACG.
- webOS TV 26 Re:New enforces it only for apps that declare the field.
- From webOS TV 27 (March 2027) it applies to every app. `ares-package` and Seller Lounge submission are blocked without the field, even for an app with no Luna calls (`"requiredACG": []`). `ares-package` 3.2.6 and later already warns when it's missing.

The app makes one Luna call, `com.webos.service.config/getConfigs` (the HDR / UHD panel probe in `src/lib/platform.ts`), so it declares `systemconfig.query`. LG's table doesn't list `getConfigs`: the group is the one webOS OSE's configd assigns it, and LG's own webOSTV.js `deviceInfo()`, which calls the same method, lists `systemconfig.query` among its groups. Older webOSTV.js (≤ 1.2.5) `deviceInfo()` called only `getConfigs` (with `getSystemInfo` as a fallback), and LG lists just `systemconfig.query` for it. `webOSSystem.deviceInfo` and `webOS.platformBack()` are not Luna calls and need no group.

When you add a Luna call, or a webOSTV.js method that makes one:

1. Look up the method's group in the guide's API ACG list (or its webOSTV.js table).
2. Add the group to `requiredACG` (and drop a group once its last call is gone). Groups ship with the app version, so a change needs a new Seller Lounge submission. Add the method's URI and groups to `ACG_GROUPS` in `src/lunaAcg.test.ts` too: that test fails when a `luna://` or `palm://` URI in `src/` (a bare `luna://` prefix too, as in a URI built from pieces), or a webOSTV.js call that makes Luna calls (`webOS.deviceInfo()`, `webOS.service.request()`, `webOSDev`), has no group in its map or in `requiredACG`, and when `requiredACG` lists a group no call needs.
3. Test on a webOS TV 26 Re:New (or newer) set: `ares-inspect --device <that set> --app com.onscreen.tv` after clearing `localStorage['onscreen:panel-caps']` (a cached answer skips the probe). `npm run inspect-tv` targets the webOS 6 C1, which never enforces ACG. A missing group fails the call with `errorCode` -1 and `Denied method call "<method>" for category "/"`: look up that method and add its group.

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
  with version + signed-in user + server URL, the privacy policy
  (address + QR code, which LG asks every app to carry) and
  Licence & terms (`routes/settings/legal`: the end-user terms
  LG's Seller Lounge terms §7.1 ask for, the Apache-2.0 notice,
  third-party notices and licence texts; words in
  `lib/legal.ts`).
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
- **Top-nav gating** — `lib/navGates.ts`: the Discover pill shows
  only when the server has requests on (`features.requests`) and
  the account may request (`/api/v1/requests/quota`), Live TV
  and Recordings only with a configured tuner
  (`features.live_tv_configured`, or `live_tv` on an older
  server). On a server without the quota endpoint (v2.4 and
  earlier) Discover shows as in 0.2.0. Hidden while that loads
  or if it fails; a failed or 10-minute-old answer is asked
  again by the next page, the hub's Retry, and the app coming
  back to the front or back online; reset on sign-out and
  Change server.

## What's still not done

- Channel art / icon polish — see `static/`
- LG Content Store submission paperwork (separate distributor
  cert + LG developer profile + content review)

## Licence

The webOS client (everything under `clients/webos`) is licensed under the
Apache License 2.0: see [LICENSE](LICENSE). The OnScreen server and the
other clients remain under the GNU AGPL v3.0 (the repository's root
[LICENSE](../../LICENSE)).

The open-source code bundled into the app, with its licences and copyright
lines, is listed in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) and in
the app under Settings > About > Licence & terms (`src/lib/legal.ts`). Add a
runtime dependency there too.
