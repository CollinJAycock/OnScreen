# Release notes — TV client 1.5.1 (versionCode 27)

A small update to 1.5.0 (26), which Amazon published and Google Play put on
the closed testing track on 2026-11-05. No new screens: it fixes playback of
videos above what the device's decoder takes, a wrong turn for e-books and
the empty Watchlist's wording, and moves the build to AGP 9.5 and
OkHttp 5.5 / Retrofit 3.

Nothing that Amazon's Deceptive and Malicious Behavior finding was about comes
back. The Fire TV build still leaves out online subtitle search and Live TV /
Recordings (`ONLINE_SUBTITLE_SEARCH` / `LIVE_TV` = false, pinned by
`FlavorFeaturesTest`).

Device test (2026-11-09, Hisense Google TV, Android 11, QA server, the
signed-in owner account): the googletv release APK re-signed with the debug
key so it updated the existing sideloaded install in place. Passed: cold
start signed in, home rows, a title page (Resume / Play from Beginning /
Add to Watchlist), a 4K Dolby Vision title transcoded to 1080p for the HD
screen, resume, fast-forward and rewind mid-transcode, and an e-book
opening its page ("Book reading isn't available on TV…") instead of the
player. No crashes. Not tried: background audio, the Fire TV build on a
stick (the Firestick didn't answer on 10.0.0.192). Seen, older than this
release: the e-book page shows "0m" as its length.

Artifacts (built 2026-11-09 from branch claude/android-closed-track, upload key
SHA-256 `579bd037…fa3cac75`, target 36, min 24, native libraries 16 KB-aligned):
- Google Play: `clients/android/app/build/outputs/bundle/googletvRelease/app-googletv-release.aab`,
  sha256 `f6a63d28…a13177f6`. Leanback required, touchscreen and fire_tv
  optional, EPG permissions kept.
- Amazon: `clients/android/app/build/outputs/apk/firetv/release/app-firetv-release.apk`,
  sha256 `670fc06f…59711445`. Leanback not required, no EPG permissions.

---

## What's New — Amazon Appstore (Fire TV) and Google Play (Android TV)

231 characters (the limit is 500 on both stores).

```
• Videos above what your TV's decoder can play are converted to fit, instead of failing to start.
• An e-book opens its page instead of the player.
• The empty Watchlist tells you how to add a title.
• Updated networking libraries.
```

---

## Testing instructions (Amazon "Testing instructions")

Use the 1.5.0 text (release-notes-1.5.0.md) with its last paragraph,
NEW IN 1.5.0, replaced by this one. The password goes only in the
submission's sign-in fields (username `reviewer`), never in this text or any
file.

```
NEW IN 1.5.1
Fixes only. The app tells the user's server which video profiles and levels the Fire TV's decoders play, so the server converts a file above them instead of sending one that fails to start. An e-book opens its page instead of the player. The empty Watchlist says how to add a title.
```

---

## Internal changelog (since 1.5.0 / 26)

- Capability header: h264Profile / h264Level, hevcProfile / hevcLevel and
  av1Profile / av1Level from the hardware decoders' MediaCodecInfo
  profileLevels (8f6838e2); a codec with no hardware decoder sends neither.
- Dolby Vision profile 5 follows the server's verdict (tone-mapped to SDR
  on NVIDIA workers) and is refused locally only when the decision call
  fails (6e08ca05).
- An e-book opens its detail page, not the player (5639443e).
- Empty Watchlist: "Choose Add to Watchlist on a title's page" (9ee638ae).
- Build: AGP 9.5 / Kotlin 2.4 migration (5fba97db), OkHttp 5.5.0 and
  Retrofit 3.0.0 (a6a5968e), Gradle dependency bumps (900004c2, dfab9d42).
