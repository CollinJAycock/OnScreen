# Release notes — phone client 0.5.0 (versionCode 1012)

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

Artifacts (built 2026-11-19 from branch claude/android-1.6.0, upload key
SHA-256 `579bd037…fa3cac75`, target 36): the mobile AAB
`clients/android_native/app/build/outputs/bundle/mobileRelease/app-mobile-release.aab`,
sha256 `dddcfd7c…819cf7d5` (min 24); the Quest APK
`clients/android_native/app/build/outputs/apk/quest/release/app-quest-release.apk`
(`tv.onscreen.quest` 1012, min 29), sha256 `f016be6f…918baf22`, for the Horizon
Store after Meta finishes with 1011.

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
