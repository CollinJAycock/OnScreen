# Release notes — phone client 0.5.0 (versionCode 1013)

Version code 1012 was built 2026-11-19 and uploaded to Google Play for the
first production release (2026-11-23), where Play recommended three changes,
all made in 1013 (the name stays 0.5.0; no 1012 reached users):
- "Your app uses deprecated APIs or parameters for edge-to-edge" and
  "Edge-to-edge may not display for all users": the flagged calls
  (`Window.setStatusBarColor` / `setNavigationBarColor`,
  `LAYOUT_IN_DISPLAY_CUTOUT_MODE_SHORT_EDGES`) were androidx.activity's own
  `enableEdgeToEdge()` internals (EdgeToEdgeApi23/26/29/35 per the R8
  mapping; still there in activity 1.13.0). MainActivity now sets up edge to
  edge itself with `WindowCompat.setDecorFitsSystemWindows`, the insets
  controller and the contrast flags; the transparent bar colours and the
  cutout mode (shortEdges on API 28–29, always from 30) come from the theme.
  None of the three names is left in the dex (all three were in 1012's).
- "Resource shrinking isn't enabled": `isShrinkResources = true`. 412 unused
  library resource files go (Cast expanded/mini controller UI, legacy Media3
  notification layouts, AppCompat leftovers); none of the app's own. The
  AAB shrinks from 12.78 MB to 12.13 MB.

Checked 2026-11-23 on emulators, Android 14 (API 34, emulated notch) and
Android 16 (API 36.1): 1012 installed, then updated in place to 1013. The
window attributes (`layoutInDisplayCutoutMode=always`, flags,
`FIT_INSETS_CONTROLLED`, and on 16 `EDGE_TO_EDGE_ENFORCED`) and the
screenshots of the sign-in screen were identical, 0 differing pixels, with no
crashes.

Device test of 1013 (2026-11-23, Galaxy S24 FE, Android 17, QA server as the
owner's account): the release build with a temporary `.devcheck` application
id (added by a Gradle init script, not in the source) beside the Play install
(1011), uninstalled afterwards. Home's app bar and 3-button nav strip were
pixel-identical to the Play 1011 install's. Passed: Home, the Music library
with its A–Z rail, an artist and an album, a title page, Arrival resumed and
playing full-bleed, in landscape too, clear of the punch-hole camera and the
nav bar (`layoutInDisplayCutoutMode=always`), an album track in the
background with Media3's foreground notification and One UI's media card,
the media Next and Pause keys, the mini player over Photos, Settings and
Downloads. Nothing in the crash buffer. Not checked: casting to a receiver,
a real download, Android Auto, a phone on Android 9 or 10 (the shortEdges
theme), the Quest build.

For Google Play's closed testing track, on the shared `tv.onscreen.android`
listing (phone builds use versionCodes 1000+; the TV app goes out as 28 /
1.6.0 alongside). Follows 0.4.3 (1011), built on 2026-11-09. The app is now
in Spanish and Simplified Chinese, and carries the phone fixes from the
2026-11-15 client review. The Quest flavor (`tv.onscreen.quest`) is built
from the same code at 1012 too, for the Horizon Store once Meta finishes
with 1011.

Device test (2026-11-19, Galaxy S24 FE, Android 17, QA server): the release
build with a temporary `.devcheck` application id beside the Play install
(0.4.3), uninstalled afterwards. It found the pairing bug below: while the PIN
was unclaimed the screen said "Couldn't reach server (Required value 'data_'
...)". With the fix in this build: updated in place (sign-in kept), Home rows
and artwork, a title page, 1917 resumed and playing (media session PLAYING,
02:57 of 1:58:59, Cast button shown), Back, Settings, Sign out, then Pair this
device waited 12 s of polls showing only the PIN and instructions. Not tried:
a download, Android Auto, casting to a receiver, the Spanish and Chinese
screens on the device.

Artifacts (1013, built 2026-11-23 from branch claude/android-phone-play-recs,
upload key SHA-256 `579bd037…fa3cac75`, target 36): the mobile AAB
`clients/android_native/app/build/outputs/bundle/mobileRelease/app-mobile-release.aab`,
sha256 `3d96bce4…01f1f47d` (min 24); the Quest APK
`clients/android_native/app/build/outputs/apk/quest/release/app-quest-release.apk`
(`tv.onscreen.quest` 1013, min 29), sha256 `f6644085…192242e7`, for the Horizon
Store after Meta finishes with 1011.

The 1012 builds (2026-11-19, branch claude/android-1.6.0) were the mobile AAB
`dddcfd7c…819cf7d5` (uploaded to Play, never released) and the Quest APK
`f016be6f…918baf22` (never uploaded). The S24 FE device test above ran on
1012.

---

## What's New (Play console, phone release)

431 characters (Play's limit is 500).

```
• OnScreen is now in Spanish and Simplified Chinese.
• Fixed: downloads that took over two minutes stopped and failed.
• If your session ends, your downloads are kept and unlock when you sign back in.
• A watch limit or a stop from your server's admin shows right away.
• Signing out ends a cast.
• Android Auto: search and Play on a show are quicker.
• Photos show at once, and from Home or Search they browse their whole library.
```

---

## Reviewer notes (Play "App access")

Unchanged from 0.4.2 (release-notes-phone-0.4.2.md).

---

## Internal changelog (since 0.4.3 / 1011)

- Spanish and Simplified Chinese, stages 4–5 (c0c407d4).
- A watch limit or admin stop from the play decision shown at once
  (0c0ae400).
- 2026-11-15 client review: offline downloads and the EPUB prefetch on a
  transfer client with no whole-call timeout (bf60796a); the bearer only to
  the server's own scheme (a7546d44); bare local hosts default to http by the
  shared rule (fbed20e4); pairing poll and server check as on the TV client
  (6ba8f2fc); an involuntary sign-out locks downloads (owner-stamped
  manifest, work cancelled) instead of wiping them, caches cleared
  (10e27c0d); sign-out ends a cast, background audio heartbeats again after
  (b252e7b6); foreign media controllers get transport only, car hosts by
  signing key (c4ae331f); the photo viewer shows the photo first, neighbours
  after (81b4e6d9), and a photo from a hub or search asks the server for its
  row (d91718f3); lighter seek-bar scrubbing (cfe9974d); car search and Play
  on a show fetch side by side (031f2194); reopening from Recents doesn't
  replay a voice request (610ed68a).
- Found on the device before release: the pairing poll parsed the server's
  202 "pending" body as an ApiResponse, so every poll while the PIN was
  unclaimed threw. 0.4.3 swallowed it; with 6ba8f2fc's poll loop the screen
  said "Couldn't reach server" for the whole wait and pairing gave up after
  eight polls. Fixed with the TV client's PairPollResponse (b2efb43f), and
  AuthRepositoryPairPollTest parses the real replies (it fails on the old
  type).
- Build: KSP 2.3.14, Gradle 9.9.1 (53c55cd6), Gradle dependency bumps
  (8c9ae19b, e41ab7e4).
