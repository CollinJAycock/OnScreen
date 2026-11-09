# Release notes — phone client 0.4.3 (versionCode 1011)

For Google Play's closed testing track, on the shared `tv.onscreen.android`
listing (phone builds use versionCodes 1000+; the TV app goes out as 27 /
1.5.1 alongside). Follows 0.4.2 (1010), published to the closed track on
2026-11-05. The Quest flavor (`tv.onscreen.quest`) is built from the same
code and would also be 1011, but this release is for the phone only.

Device test (2026-11-09, Galaxy S24 FE, QA server as the owner's account):
the release build, with a temporary `.devcheck` application id so it could
sit beside the Play install (Play signs the installed copy with its own key,
so an upload-key build can't update it). Passed: sign-in, home rows and
artwork, a title page, playback with resume, seek and the Cast button
showing, Watchlist, Downloads and Settings, a library's A–Z rail and
search, the new "No albums by this artist in your library." text, and an
album playing in the background with the media Next and Pause keys. No
crashes. Not tried: casting to a receiver, a download, Android Auto. Seen
but older than this release: the title page shown right after leaving the
player can show the position from the last heartbeat for a moment; the
server has the right one (a fresh page shows it).

Artifacts (built 2026-11-09 from branch claude/android-closed-track): the
mobile AAB `clients/android_native/app/build/outputs/bundle/mobileRelease/app-mobile-release.aab`,
sha256 `f26c9b21…537e507d`, upload key SHA-256 `579bd037…fa3cac75`,
target 36, min 24, native libraries 16 KB-aligned.

---

## What's New (Play console, phone release)

206 characters (Play's limit is 500).

```
• Videos above what your phone's decoder can play are converted to fit, instead of failing to start.
• An artist who only appears on other albums says so.
• Updated interface, Cast and networking libraries.
```

---

## Reviewer notes (Play "App access")

Unchanged from 0.4.2 (release-notes-phone-0.4.2.md).

---

## Internal changelog (since 0.4.2 / 1010)

- Capability header: h264Profile / h264Level, hevcProfile / hevcLevel and
  av1Profile / av1Level from the hardware decoders, with the TV app's
  hardware-decoder check (8f6838e2).
- Dolby Vision profile 5 follows the server's verdict, refused locally only
  when the decision call fails (6e08ca05).
- Artist with no albums of its own: "No albums by this artist in your
  library." instead of "No playable files for this item." (9ee638ae).
- Compose BOM 2024.09.02 → 2026.09.00 (Compose UI 1.12.1; Dependabot #236)
  and play-services-cast-framework 21.5.0 → 23.0.0 (Dependabot #235).
- Build: AGP 9.5 / Kotlin 2.4 (5fba97db), OkHttp 5.5.0 and Retrofit 3.0.0
  (a6a5968e), Gradle dependency bumps (900004c2, dfab9d42).
