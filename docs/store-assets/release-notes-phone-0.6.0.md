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

(Filled in once the release build has been run on the phone.)

## Artifacts

(Filled in from `clients/packages` once built.)

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
