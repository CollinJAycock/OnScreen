# v3 Plan

State 2026-11-05, `main` @ `bef35c94`. v2.7.0 was released on 2026-11-03 as
the last planned server release of the v2 line ([v2.7-roadmap.md](v2.7-roadmap.md)).
Every client is now live or with its store: Fire TV 1.5.0, LG
`tv.onscreen.webos` 1.0.0 (resubmitted), Samsung 1.1.1, Roku 1.0.0, Xbox and
Meta Quest are in review, and Google Play's TV 26 and phone 1010 are in closed
testing, waiting on 12 testers.

Built from: the "Later decisions" and store-wave tables of the v2.7 roadmap,
"Where OnScreen trails" in [comparison-matrix.md](comparison-matrix.md), the
remaining known issues in [ARCHITECTURE.md](../ARCHITECTURE.md), the open steps
in [ha-roadmap.md](ha-roadmap.md), the one open GitHub issue (#41), and what
the 2026-11-05 device runs found.

Sizes as before, for one person working with agents: S ≈ a day or two,
M ≈ a week, L ≈ several weeks, XL ≈ months.

## Decisions (owner, 2026-11-05)

1. **Direction: A, Reach.**
2. **Languages:** the two most common after English. Taken as **Spanish**
   (`es`) and **Simplified Chinese** (`zh-Hans`). Mandarin is second by total
   speakers. Hindi and Spanish are close for third, and Spanish is chosen
   because far more people use software in Spanish than in Hindi.
3. **API:** keep `/api/v1` and **remove dead fields and endpoints**. No
   `/api/v2`.
4. **Kodi and Steam Deck: yes.**
5. **Second-site DR: not now** (no budget for a second site). Items 9 and 10
   move to Parked.
6. **Dolby Vision profile 5 → SDR: yes.**

The final scope is under [v3.0 scope](#v30-scope) below.

---

## What makes it v3 rather than v2.8

v2.7 closed the feature list the v2 line set out with. v3 is the point to:

- **choose a direction** rather than clear a backlog;
- **allow breaking API changes**, with every first-party client moving in
  lockstep (the standing rule since the v2.2 server lock was lifted);
- **keep the release rule** v2.7 set: one batched server release, client store
  builds whenever they're ready, patches only for security, data loss or
  blocked playback.

## Three directions (A chosen)

| | Direction | What it means | Fits |
|---|---|---|---|
| **A** | **Reach** *(recommended)* | Get OnScreen to more people: the apps in every store, translations, the platforms not yet covered (Kodi, Flathub, Steam Deck, LG Full HD). | Store distribution is the one place the matrix still says OnScreen trails all three rivals. Most of the work is small to medium and parallel. |
| **B** | **Run it for real** | HA becomes something people can deploy: the second-site DR the app side is already ready for, automatic Postgres promotion, a documented reference deployment. | "Higher sight A" in ha-roadmap.md. Mostly ops and documentation now, not app code. |
| **C** | **Platform engine** | Multi-tenancy, billing, optional DRM: OnScreen as the backend for someone streaming content they own. | "Higher sight B". XL and a different product; **not recommended for v3**. |

**A** was chosen. The B items below (9, 10) are parked: there is no second site for now.

---

## v3.0 candidates

### Reach (direction A)

| # | Item | Size | Why / source |
|---|---|---|---|
| 1 | **Translations: i18n foundation** in the web app, the TV web apps (webOS, Tizen, Xbox) and the phone app, plus the first two languages | L | Planned for v2.6 and v2.7, never started. Neither the web app nor the TV web apps has an i18n library. The Android TV app is already all string resources (241, none hardcoded), so it needs translation only. The phone app needs extraction first: 149 hardcoded `Text("…")` calls against 36 resources (checked 2026-11-05). |
| 2 | **Kodi add-on** | M | v2.7 store wave, not started. The server is ready for it (`?token=` URLs, the Decide endpoint). A new small Python codebase. |
| 3 | **Linux desktop on Flathub** | S–M | v2.7 store wave, not started. MPRIS is already in the desktop app. Also covers Steam Deck desktop mode. |
| 4 | **Steam Deck 10-foot mode** | M | Needs a Deck. Rides on Flathub (#3). |
| 5 | **webOS Full HD build** (1280×720 canvas, a second IPK) | M | v2.7 store wave, not started. The LG listing leaves out FHD models. Do it after `tv.onscreen.webos` 1.0.0 clears LG QA. |
| 6 | **Google Play production** | none | 12 closed testers for 14 days. The recruiting kit is in [play-store/beta-testers.md](store-assets/play-store/beta-testers.md). |

### Playback

| # | Item | Size | Why / source |
|---|---|---|---|
| 7 | **Live TV timeshift** (pause and rewind live) | L | Planned for v2.6 and v2.7, never started. Builds on the DVR's segment store. |
| 8 | **Dolby Vision profile 5 → SDR** | M–L | The one playback row where OnScreen still trails (vs Jellyfin, since 10.8). Today profile 5 is refused when it would need a re-encode. libplacebo is already in the tone-map path. |

### Run it for real (the cheap half of direction B)

| # | Item | Size | Why / source |
|---|---|---|---|
| 9 | **Second-site DR, active/passive**, as a documented, tested deployment: ZFS replication + a cross-WAN Postgres standby + promotion | M (ops + docs) | ha-roadmap step 6. The app side is done (per-site path remap, failover DSN, `/health/cluster`); what's missing is the runbook and a drill. |
| 10 | **Automatic Postgres promotion** (Patroni or an operator recipe) | M | ha-roadmap step 2, the "remaining" part. Pairs with #9. |

### API (breaking changes, lockstep)

| # | Item | Size | Why / source |
|---|---|---|---|
| 11 | **API audit** — fields and endpoints no first-party client reads any more; decide whether v3 keeps `/api/v1` with removals or opens `/api/v2` | S to decide, M to do | v3 is the only point where removals are cheap. Every client (web, Android TV, phone, Quest, webOS, Tizen, Xbox, Roku, desktop) has to move in the same release. |

### Small, owner's call

| # | Item | Size | Notes |
|---|---|---|---|
| 12 | **Cloudflare hides 502/504 errors** | S | Proposed 2026-10-13, deferred twice. A 4xx for upstream failures keeps the error codes clients show. |
| 13 | **Refresh or close #41 (CI/CD buildout)** | S | v2.7 added Playwright nightly and the TV Vitest suites; check what's left. |

---

## Already queued for the next client builds (no server release needed)

Merged on 2026-11-05, waiting for a build number:

- **Android TV / Fire TV 27:** e-books open their title page instead of
  "Playback error HTTP 422" (#197); the empty Watchlist names the
  "Add to Watchlist" button (#195).
- **Phone 1011:** an artist with no albums of its own says "No albums by this
  artist in your library." (#195).
- **Tizen, next build:** library search and the A–Z rail (verified on every
  other platform 2026-11-05).

## Hardware checks still owed (QA, not features)

- Android TV background audio, three checks never run on hardware: re-entry
  to the same chapter, pause + Home for 10 minutes, the queue-end stop
  (closed out by the owner 2026-11-05 without the run).
- AV1 on AMF (needs an RDNA3+ GPU on Windows), still listed under "trails".
- Burned-in picture subtitles on VAAPI and QSV (known issue 12).
- Casting from the web player on a Chromecast (known issue 8).

## Parked

- **Second-site DR and automatic Postgres promotion** (items 9, 10): no
  second site for now (owner, 2026-11-05). The app side stays ready.

- **Manga:** parked until the volume/chapter model is planned. The code and
  schema are intact; reviving it is the owner's call.
- **Multi-region / active-active writes:** only for a real deployment that
  needs it (ha-roadmap).
- **Platform engine (direction C):** a separate product decision.

## Stays out (standing decisions)

DLNA, sync watch / watch parties, trailers and extras, picture-in-picture in
the Tauri desktop app, automatic face recognition in photos, DASH,
ML-driven personalised recommendations, Tidal / Qobuz streaming, media-request
features in the Android apps, Apple apps (iOS / iPadOS / tvOS / macOS) and
AirPlay.

---

## v3.0 scope

| Track | Items | Notes |
|---|---|---|
| **v3.0 server release** | 1 (i18n foundation + `es` + `zh-Hans`), 7 (Live TV timeshift), 8 (Dolby Vision profile 5 → SDR), 11 (API audit and removals in `/api/v1`) | Every first-party client moves with the API removals in the same release. |
| **Alongside, no server tag** | 2 (Kodi), 3 (Flathub), 4 (Steam Deck), 5 (webOS Full HD), 6 (Play production) | Ship through their stores on their own schedule. |
| **Small, if time** | 12 (Cloudflare 502/504), 13 (#41) | 12 is still the owner's call. |
| **Parked** | 9, 10 (second-site DR, automatic promotion) | No second site for now. |

**Estimate:** larger than v2.7, about **8–10 weeks**. Items 1, 7 and 8 are each
L-sized, and translation needs a review pass by a fluent reader for each
language. Store and platform work runs in parallel.

**Suggested order:** the API audit first (it decides what every client must
change), then the i18n foundation (string extraction touches the same
screens), then timeshift and Dolby Vision profile 5 in parallel branches.
Kodi, Flathub and Steam Deck can start any time.

**Language notes:** the TV web apps and Roku must render Chinese. Check each
platform's system fonts before relying on them, and right-to-left support
isn't needed for these two languages. Machine-drafted translations need a
native reader before release.
