# LG Content Store: re-registration checklist (webOS 0.3.0)

## Where this stands

- **2026-10-19:** 0.2.1 was rejected at the App QA pretest (QA2026100824196,
  "Insufficient UX scenario": no screens in the scenario). The same day
  0.2.2 was resubmitted with the illustrated scenario (commit 437f1b5f).
- **By 2026-11-05:** `com.onscreen.tv` was gone from Seller Lounge >
  Application. Its Defect Info page went with it. The only QA email on hand
  is the 10-19 one, which gives no details and lists **Available Countries
  (0)**.
- **Ruled out:** the test password in the scenario is still valid (checked
  2026-11-05).
- **Not ruled out:** the review server, onscreen-beta.wolverscreen.com, is
  behind Cloudflare (`Server: cloudflare`). LG's testers connect from Korea.
- **Asked LG:** a 1:1 Inquiry for the defect details and the reason for the
  deletion. Read the answer before submitting.

The re-registration ships **0.3.0** (main after the 2026-11-05 merges): 0.2.2
plus library search with an A–Z list, D-pad fixes on the sign-in keyboards,
a retry for stalled requests, and Back to Libraries keeping focus.
`com.onscreen.tv_0.3.0_all.ipk` builds from `clients/webos` with
`npm run package`, after 918 passing tests and a clean `npm run check`.

## Before submitting

1. **LG's answer.** Fix whatever the inquiry reply names first. If it names
   something not covered below, add it here.
2. **Cloudflare.** In the Cloudflare dashboard for wolverscreen.com, check
   Security > Events for blocked or challenged requests to
   `onscreen-beta.wolverscreen.com` from KR around the 10-19 test. Make sure
   that host serves the app's API without a challenge: no Bot Fight Mode or
   managed challenge, and no country block for Korea. A browser challenge
   page can't be passed by the TV app, so to LG it looks like "can't
   connect".
3. **On the TV.** Install 0.3.0 on the C1 (`ares-install -d tv
   com.onscreen.tv_0.3.0_all.ipk`; on Node 26 set
   `NODE_OPTIONS=--require <node-util-shim.cjs>`, see the webOS notes). Run
   scenario steps 1–14, including 11b, signed in to the review server as
   `reviewer`.
4. **Re-shoot two figures** on the TV running 0.3.0, as 1920×1080 JPEGs
   (DevTools `Page.captureScreenshot` through `ares-inspect`, as for the
   other figures):
   - `ux/07-library-grid.jpg`: Movies with the Search chip and the A–Z
     list showing;
   - `ux/28-settings-about.jpg`: it must read Version 0.3.0.
5. **Render the scenario PDF** from the repo root:
   `REVIEWER_PASSWORD='…' node docs/store-assets/lg-content-store/render-pdf.mjs ux-scenario.pdf`.
   Open it and check figures 7 and 28 and the password line.

## Registering in Seller Lounge

- **App ID:** try `com.onscreen.tv` again. If LG refuses it as taken or
  reserved, choose a new ID and change it in `clients/webos` (appinfo.json,
  package.json, svelte.config.js, scripts/install.mjs,
  scripts/rewrite-file-paths.mjs, README.md, THIRD_PARTY_NOTICES.md). Then
  rebuild and update the scenario's header line.
- **Countries:** select them. The last submission went in with **none**
  (Available Countries (0)). At least the US; add every country where
  English (or Russian, which has a description) works.
- **Models / webOS versions:** last time was UHD models only. The app
  targets webOS 6.0+ (Chromium 79 build target).
- **Package:** `clients/webos/com.onscreen.tv_0.3.0_all.ipk`. `requiredACG`
  is `["systemconfig.query"]` (needed from March 2027).
- **Listing:** title OnScreen, category Entertainment, descriptions
  `description-en.txt` / `description-ru.txt`, icon `app-icon-512.png`,
  splash `splash-1920x1080.png`, screenshots in `screenshots/` (they show
  only review-server content).
- **Privacy policy URL:** https://onscreen.wolverscreen.com/privacy. QA must
  be up while LG reviews.
- **Test Info:** the rendered UX scenario PDF and the test account
  (server, `reviewer`, password). Fill LG's Self-Checklist from its
  current template.
- **webOS Cloud Test Lab:** once the app is registered, its Edit page links
  to the Cloud Test Lab. Run the package there on a recent webOS version
  before requesting QA.
