# Release notes — phone client 0.6.0 (versionCode 1014)

For Google Play on the shared `tv.onscreen.android` listing (phone builds use
versionCodes 1000+). Follows 0.5.0 (1013), the first production release.
This release brings tablet and Chromebook support (PR #297). Phones keep
their layout: everything new needs a window at least 600dp wide and 480dp
tall, and the keyboard shortcuts only answer a physical keyboard.

The Quest flavor (`tv.onscreen.quest`) is built from the same code at 1014.
Its default panel (1024×640dp) is wide enough for the navigation rail and
the side-by-side layouts too; that hasn't been tried on a headset yet. It
goes to the Horizon Store once Meta finishes with 1011.

---

## What's New (Play console)

Wrap it in the listing's language tag in the release-notes box
(`<en-US>` … `</en-US>`).

```
• Tablets and Chromebooks: a navigation rail, Settings and libraries side by side with what you pick, and title pages that use the whole screen.
• Keyboard shortcuts: Space plays or pauses, the arrow keys skip and turn photos and comic pages, Esc goes back.
• Right-click a poster for the same options as a long press.
• Books re-flow when you resize the window.
```

---

## Device test

2026-11-23, Galaxy S24 FE (Android 17), QA server, signed in as the owner.
The release build ran with a temporary `.devcheck` application id next to
the Play install (0.4.3), and was uninstalled afterwards.

The phone layout is unchanged:

- **Home, portrait:** identical to 0.5.0. No rail; the overflow menu is still there.
- **Home, landscape:** no rail. The window is 2340×1080 but only about 410dp tall.
- **Settings → Playback quality:** opens full screen on its own.
- **A title page:** single column with the full-width hero.

Nothing in the crash buffer.

The tablet and Chromebook layouts and the keyboard shortcuts were tested
for PR #297, on Android 16 and 14 tablet emulators signed in to QA.

**Not tried on a device:** a real tablet, Chromebook or keyboard;
right-click; the Quest build (its panel will show the rail).

## Artifacts

Built 2026-11-23 by `clients/build-packages.ps1` from commit d97a3570 (clean
tree), upload key SHA-256 `579bd037…fa3cac75`, target 36, into
`clients/packages`:

- Google Play: `OnScreen-GooglePlay-Phone-0.6.0-1014.aab`, sha256
  `8abbb957…63e0e3fb`.
- Meta Horizon Store (`tv.onscreen.quest` 1014, min 29):
  `OnScreen-MetaHorizonStore-Quest-0.6.0-1014.apk`, sha256 `76e8096f…b276fb10`,
  for after Meta finishes with 1011.

---

## Internal changelog (since 0.5.0 / 1013)

- Tablets and Chromebooks, PR #297 (cefa156d):
  - keyboard: `KeyShortcuts` through `MainActivity.dispatchKeyEvent`,
    physical alphabetic keyboards only. Player Space/K, Left/J, Right/L, M.
    Photo viewer and comic reader arrows, Page Up/Down and Space, following
    the reading direction. EPUB Left/Right. Esc as Back.
  - right-click (`onSecondaryClick`) on library, Home and Watchlist cards
    and episode rows;
  - `PagerArrows` on windows 600dp and wider;
  - EPUB `rendition.resize` on resize;
  - `configChanges` + `density|fontScale|navigation|touchscreen`;
  - ChromeOS `<layout>` default window size;
  - a navigation rail (Home, Search, Watchlist, Favorites, Downloads,
    Settings);
  - Settings and library list-detail at 840dp and wider;
  - a two-pane title page;
  - readable 840dp widths;
  - 150dp grid posters on wide content.
- Strings `nav_home`, `page_previous` and `page_next` (en, es, zh-Hans).
