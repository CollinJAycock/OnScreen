# Store-listing assets

Source-of-truth for every image, screenshot, and graphic uploaded to
the four app-store consoles OnScreen ships through. Generated assets
live here next to the master they were derived from, so regenerating
or re-cropping for a new store doesn't require hunting through the
repo or external folders.

## Why everything is here, not next to each client

The Tizen / Android-TV / Android-phone build tooling reads launcher
icons from each client's `app/src/main/res/` (Android) or the project
root `icon.png` (Tizen). Those are the only image paths the tooling
actually requires. Every other image — screenshots, feature graphics,
store-listing banners, annotated walkthroughs — is generated for
upload to an external console and never read by any build step.
Consolidating them here gives one canonical home and keeps the
per-client trees focused on code.

## Inventory

### `master/`

Source-of-truth artwork. Everything else is derived from `icon-512.png`.

| File | Purpose |
|---|---|
| `icon-512.png` | 512×512 master icon. Source for every other icon. |
| `icon-192.png` | 192×192 PWA / web-manifest variant. |
| `favicon.svg` | SVG wrapper around the master JPEG; web fallback. |
| `favicon.ico` | Multi-resolution `.ico` for legacy browsers. |
| `favicon-96x96.png` | 96×96 PNG for older favicon paths. |
| `apple-touch-icon.png` | iOS / Safari home-screen icon. |
| `site.webmanifest` | Web App Manifest declaring the icon set. |

### `play-store/` — Google Play (phone + TV)

| File | Field in Play Console |
|---|---|
| `feature-graphic-1024x500.png` | Feature graphic (phone listing) |
| `tv-banner-1280x720.png` | TV banner (Play TV listing) |
| `screenshots/phone/0[1-5]-*.png` | Phone screenshots (1080×2400 portrait) |
| `screenshots/tv/0[1-5]-*.png` | TV screenshots (1920×1080 landscape) |

App icon: Play Console reuses `master/icon-512.png` directly.

### `amazon-appstore/` — Fire TV

| File | Field in Amazon Developer Console |
|---|---|
| `icon-114.png` | Small icon (114×114) |
| `featured-1920x1080.png` | Featured image / single full-color asset |
| `featured-bg-1920x720.png` | Featured-content background (alt path; if you upload the logo + bg separately) |
| `featured-logo-640x260.png` | Featured-content logo (alt path; transparent wordmark) |
| `screenshots/0[1-5]-*.png` | Fire TV screenshots (1920×1080) |

App icon: 512×512 from `master/icon-512.png`.

App description doc: see [`clients/tizen/SAMSUNG_APP_DESCRIPTION.md`](../../clients/tizen/SAMSUNG_APP_DESCRIPTION.md)
which has the same text shape used for Amazon's long description.
(Different stores ask for the same content under different field
names; one source file works for all of them.)

### `samsung-appstore/` — Samsung TV (Tizen)

| File | Slide / field in Samsung Seller Office |
|---|---|
| `logo-1920x1080.png` | Logo Asset (1920×1080 RGBA PNG, ≤300 KB) |
| `bg-1920x1080.png` | Background Image (1920×1080 RGB PNG/JPG, ≤300 KB) |
| `ui-structure.png` | "Whole UI Structure" slide in the App Description PowerPoint |
| `menu-home.png` etc. | "Menu & function description" slides — one per major screen |
| `screenshots/0[1-5]-*.jpg` | Application screenshots (1920×1080 JPG, ≤500 KB each — the only store that mandates JPG, not PNG) |

App icon used in-tree: `clients/tizen/icon.png` (512×423 — Samsung's
launcher-tile spec, not the 512×512 the other stores use).

App description doc (paste into Samsung's Word template):
[`clients/tizen/SAMSUNG_APP_DESCRIPTION.md`](../../clients/tizen/SAMSUNG_APP_DESCRIPTION.md).

## Regenerating

The Samsung annotated `menu-*.png` images, the featured backgrounds,
the TV banners, and the consolidated app icons were all generated
from `master/icon-512.png` via PowerShell + System.Drawing scripts
inlined in chat-history. If you need to regenerate any of them, the
recipes are reproducible from the master plus the dimension specs in
the tables above — no proprietary editing tool required.

The TV screenshots were captured directly from a connected device
over `adb shell screencap`. To recapture:

```
adb connect <tv-ip>:5555
adb shell screencap -p /sdcard/onscreen-shot.png && \
  adb pull /sdcard/onscreen-shot.png ./<n>-<screen>.png && \
  adb shell rm /sdcard/onscreen-shot.png
```

Phone screenshots: same flow over a USB-debugging phone connection.

## What lives outside this folder

These paths are deliberately not consolidated here because the build
tooling expects them where they are:

| Path | Reason |
|---|---|
| `clients/android/app/src/main/res/mipmap-*/` | Android TV launcher icons; read by AGP at package time. |
| `clients/android_native/app/src/main/res/mipmap-*/` | Android phone launcher icons; same reason. |
| `clients/tizen/icon.png` | Tizen launcher; `assemble-package.mjs` copies it from the project root into `build/` and into the `.wgt`. |
