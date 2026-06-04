# Demo / Review Library Spec

The library an app-store reviewer's test account must land in. Its job:
look like a real, fully-populated OnScreen library across every screen the
reviewer browses (Home, Movies, Shows, Music, Audiobooks, Photos), while
containing **zero** recognizable commercial content. This is the fix for the
Amazon Appstore content-policy rejection (the prior review showed the QA
library full of Hollywood titles + the screenshots captured from it).

Apply the same library to **every** store submission (Amazon, Google Play,
Samsung) — they all currently point at `testUser` → the QA server, and Google
Play in particular can terminate the developer account over apparent piracy.

---

## Hard rules

1. **License whitelist: CC-BY, CC0, or unambiguous public domain only.**
   Avoid `-NC` (NonCommercial) and content whose PD status is murky — the app
   is *commercially* distributed, so NC content in its demo is a problem, and a
   reviewer recognizing a still-copyrighted title undoes everything.
2. **Artwork hygiene — the subtle trap.** OnScreen enriches via TMDB/TVDB/
   MusicBrainz on scan. For the **Blender open movies** this is *safe*: their
   TMDB poster art is itself the official CC-BY artwork. For **public-domain
   feature films it is NOT safe** — the *film* is PD but the *theatrical poster*
   is often still under separate copyright, so TMDB will hand you a copyrighted
   poster for a PD movie. Rule: only let TMDB art through for the Blender films;
   for anything else, disable poster fetch for that library or set the poster
   manually to a CC/PD image. Verify on localhost before capturing screenshots.
3. **Scope it to the reviewer.** Create dedicated demo libraries and a managed
   user whose library access is restricted to *only* those libraries. The
   reviewer's `testUser` must never see the operator's real catalog. (OnScreen
   already enforces per-user `CanAccessLibrary`.) Alternatively run a separate
   demo server — but a scoped user on the existing server is less work.
4. **Keep attribution.** CC-BY requires credit. Keep a `NOTICE`/credits list
   (server About page or a repo file) crediting Blender Foundation, the
   LibriVox readers, Kevin MacLeod, NASA, etc. Not shown to the reviewer, but
   correct and cheap.

---

## Library 1 — Movies  (core: Blender Foundation open movies, all CC-BY)

These look professional, are HD, have safe official artwork, and fill a grid.
Hub: <https://studio.blender.org/films> · mirrors on <https://archive.org>.

| Title | Year | License | Source |
|---|---|---|---|
| Big Buck Bunny | 2008 | CC-BY 3.0 | archive.org/details/BigBuckBunny · peach.blender.org |
| Elephants Dream | 2006 | CC-BY 2.5 | archive.org/details/ElephantsDream · orange.blender.org |
| Sintel | 2010 | CC-BY 3.0 | archive.org/details/Sintel · durian.blender.org |
| Tears of Steel | 2012 | CC-BY 3.0 | archive.org/details/TearsOfSteel · mango.blender.org |
| Cosmos Laundromat (First Cycle) | 2015 | CC-BY 4.0 | studio.blender.org/films/cosmos-laundromat |
| Glass Half | 2015 | CC-BY 4.0 | studio.blender.org/films/glass-half |
| Agent 327: Operation Barbershop | 2017 | CC-BY 4.0 | studio.blender.org/films/agent-327 |
| Hero (Grease Pencil) | 2018 | CC-BY 4.0 | studio.blender.org/films/hero |
| Spring | 2019 | CC-BY 4.0 | studio.blender.org/films/spring |
| Coffee Run | 2020 | CC-BY 4.0 | studio.blender.org/films/coffee-run |
| Sprite Fright | 2021 | CC-BY 4.0 | studio.blender.org/films/sprite-fright |
| Charge | 2022 | CC-BY 4.0 | studio.blender.org/films/charge |
| Wing It! | 2023 | CC-BY 4.0 | studio.blender.org/films/wing-it |
| Gold (Project Gold) | 2025 | CC-BY 4.0 | studio.blender.org/films |

That's ~14 — enough to fill the Movies grid and the Home "Recently Added"
strip. **Optional padding** with verified public-domain features (set posters
manually per rule #2 — do NOT trust TMDB art here): *Night of the Living Dead*
(1968), *Plan 9 from Outer Space* (1959), *His Girl Friday* (1940),
*The Cabinet of Dr. Caligari* (1920), *Charade* (1963), *Carnival of Souls*
(1962) — all on archive.org. Use these only if you want a fuller grid; the
Blender set alone is sufficient and lower-risk.

---

## Library 2 — TV Shows  (demonstrates show → season → episode hierarchy)

Caminandes is a 3-part CC-BY animated series — perfect for showing the
episode UI with pure CC-BY content.

| Show | Season / Episodes | License | Source |
|---|---|---|---|
| Caminandes | S1: E1 *Llama Drama* (2013), E2 *Gran Dillama* (2013), E3 *Llamigos* (2016) | CC-BY 4.0 | studio.blender.org/films/caminandes-* · caminandes.com |

Optional second show: group the standalone Blender shorts (Hero, Glass Half,
Wing It!, Coffee Run…) as episodes of an anthology titled **"Blender Open
Movies"** — gives a 2nd multi-episode series, still 100% CC-BY. (Do **not** use
*Pioneer One* or similar CC-BY-**NC** web series — NonCommercial conflicts with
a paid/commercial app listing.)

---

## Library 3 — Music  (CC-BY composers — fills album/track UI, safe cover art)

| Artist | Use | License | Source |
|---|---|---|---|
| Kevin MacLeod | 2–3 "albums" of grouped tracks (e.g. *Calming*, *Cinematic*, *8-bit*) | CC-BY 3.0/4.0 | incompetech.com · archive.org (KevinMacLeod collections) |
| Scott Holmes | An album of corporate/cinematic tracks | CC-BY | freemusicarchive.org/music/scott-holmes |
| Blender film soundtracks (Jan Morgenstern) | "Sintel OST", "Tears of Steel OST" | CC-BY | film pages on studio.blender.org |

Cover art: these come with CC art, or let OnScreen/MusicBrainz fill it — verify
on localhost it didn't pull a copyrighted commercial cover (MusicBrainz can
mis-match). Generate a neutral cover if so.

---

## Library 4 — Audiobooks  (LibriVox — public-domain recordings of PD books)

Demonstrates the author → book → chapter hierarchy and the audio player.
All recordings PD, all source texts PD. Hub: <https://librivox.org>.

| Title | Author | Source |
|---|---|---|
| The Adventures of Sherlock Holmes | Arthur Conan Doyle | librivox.org |
| Alice's Adventures in Wonderland | Lewis Carroll | librivox.org |
| Dracula | Bram Stoker | librivox.org |
| Frankenstein | Mary Shelley | librivox.org |
| The Time Machine | H. G. Wells | librivox.org |
| Aesop's Fables | Aesop | librivox.org |

Cover art: use the LibriVox/Standard-Ebooks cover (CC0) or a generated text
cover — **not** a modern commercial edition's cover.

---

## Library 5 — Photos  (public-domain / CC imagery)

| Album | Source | License |
|---|---|---|
| NASA — Earth & Space | images.nasa.gov | Public domain (US gov) |
| Wikimedia Featured Pictures | commons.wikimedia.org | CC / PD (per-image, verify) |

A handful per album (10–15) fills the photo grid and full-screen viewer.

---

## Optional — Books

If you want to show the `book` library type: **Standard Ebooks**
(<https://standardebooks.org>, CC0) or **Project Gutenberg** (PD) — same titles
as the audiobooks make a tidy cross-type demo.

---

## Server setup

1. Create demo libraries (`Demo Movies`, `Demo Shows`, `Demo Music`,
   `Demo Audiobooks`, `Demo Photos`) pointing at the demo media folders.
2. Create/repurpose the managed `testUser`; restrict its library access to
   **only** the demo libraries (Settings ▸ Users ▸ Library Access).
3. Scan. Then **verify artwork on localhost** before anything else: open each
   library in the web UI and confirm no copyrighted poster/cover slipped in via
   TMDB/MusicBrainz (see rule #2). Fix any by setting the poster manually.
4. Confirm the Home page strips (Continue Watching / Trending / Recently Added)
   show only demo content for `testUser`. Trending is per-user-access-filtered,
   so a scoped user won't surface the real catalog — double-check anyway.

---

## Artwork + screenshots via localhost

Once the demo library scans clean on the local/dev server (10.0.0.122:7070):

- **Screenshots**: re-capture all store assets against the demo content —
  Amazon `screenshots/0[1-5]-*.png`, Play `screenshots/{phone,tv}/*`, Samsung
  `screenshots/0[1-5]-*.jpg`, plus `featured-*` graphics that embed real art.
  Capture flow is unchanged (`adb shell screencap`, see this folder's README) —
  just point the device's app at the scoped demo user first.
- **Featured/banner art**: rebuild any that showed commercial posters from the
  demo content or the master icon.

Mapping (each store ships the same 5 shots):
`01-home` → demo Home · `02-detail` → a Blender film detail (e.g. Sintel) ·
`03-library` → demo Movies grid · `04-playback` → a Blender film playing ·
`05-music` → a Kevin MacLeod album.

---

## Test-account block for the description doc

Replace the `testUser` section in
`clients/tizen/SAMSUNG_APP_DESCRIPTION.md` (reused for Amazon/Play) with:

> **Test account.** URL `https://onscreen.wolverscreen.com`, user `testUser`,
> password `testPassword`. This review account is scoped to a demo library
> containing **exclusively public-domain and Creative Commons titles**
> (Blender Foundation open movies under CC-BY, LibriVox public-domain
> audiobooks, Kevin MacLeod CC-BY music, NASA public-domain imagery). It is
> representative of the app's functionality and contains no commercial content.
