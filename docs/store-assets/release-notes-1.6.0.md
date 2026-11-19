# Release notes — TV client 1.6.0 (versionCode 28)

Follows 1.5.1 (27), built on 2026-11-09 for the closed track. The app is now
in Spanish and Simplified Chinese, and carries the Android TV fixes from the
2026-11-15 client review: signing out, pairing, the notifications stream, one
player session start at a time, lighter trickplay previews, and photos
opened outside their library grid. A watch limit or an admin's "stop this
stream" now shows as soon as the server says so.

Nothing that Amazon's Deceptive and Malicious Behavior finding was about comes
back. The Fire TV build still leaves out online subtitle search and Live TV /
Recordings (`ONLINE_SUBTITLE_SEARCH` / `LIVE_TV` = false, pinned by
`FlavorFeaturesTest`).

Device test (2026-11-19, Fire TV Stick 4K Max, AFTKRT, Fire OS on Android 11,
QA server, account akadmin): the firetv release APK re-signed with the debug key
so it updated the existing 1.5.1 sideload in place (sign-in kept). Passed: cold
start signed in, Home rows and artwork, a title page (Resume / Play from
Beginning / Mark unwatched / Add to Watchlist), 1917 resumed as a remux (HEVC
3840x1600, TrueHD source) and advancing on the transport bar (2:08 → 2:17),
Back to Home, nothing in the crash buffer. Not tried: the googletv build on a
Google TV (the Hisense didn't answer), background audio, the Spanish and
Chinese screens on the device, the photo viewer. Seen, older than this
release: the title page right after leaving the player showed the previous
resume point for a moment (as noted for phone 0.4.3).

Artifacts (built 2026-11-19 from branch claude/android-1.6.0, upload key
SHA-256 `579bd037…fa3cac75`, target 36, min 24):
- Google Play: `clients/android/app/build/outputs/bundle/googletvRelease/app-googletv-release.aab`,
  sha256 `8bd67d3b…462b3062`.
- Amazon: `clients/android/app/build/outputs/apk/firetv/release/app-firetv-release.apk`
  (copied to `clients/firetv/dist/onscreen-firetv-release.apk`), sha256
  `dc316015…936af66f`.

---

## What's New — Amazon Appstore (Fire TV) and Google Play (Android TV)

442 characters (the limit is 500 on both stores).

```
• OnScreen is now in Spanish and Simplified Chinese. It follows your account's language, the app's setting or your TV's.
• A watch limit or a stop from your server's admin shows right away.
• Signing out happens at once and stops anything still playing.
• "Play on this TV" keeps working through long idle periods.
• Steadier playback starts; scrubbing previews use less memory.
• Photos opened from Home or Search browse their whole library.
```

---

## Testing instructions (Amazon "Testing instructions")

Use the 1.5.0 text (release-notes-1.5.0.md) with its last paragraph,
NEW IN 1.5.0, replaced by this one. The password goes only in the
submission's sign-in fields (username `reviewer`), never in this text or any
file.

```
NEW IN 1.6.0
The app is now available in Spanish and Simplified Chinese: it follows the account's language (Settings on the web app), the app's own Settings > Language, or the Fire TV's language. Everything else is fixes: signing out, pairing, playback starts, and photos opened from Home or Search.
```

---

## Internal changelog (since 1.5.1 / 27)

- Spanish and Simplified Chinese, stages 4–5 (c0c407d4; device checks
  490e5d7d).
- A watch limit or admin stop from the play decision shown at once
  (0c0ae400).
- 2026-11-15 client review: bearer scoped to the server's scheme, its cache
  dropped on every token write (8e5078b3); notifications stream kept open
  through idle periods with backed-off re-dials (a50b6e5a); sign out locally
  first, tokens stored before re-dialing (d4e7a32c); on-screen audio stopped
  and image caches dropped on sign-out (b5037c30); artwork disk cache follows
  the server's Cache-Control (453857d7); http for every local-network host,
  Keystore work off the main thread (a7235525); the previous server URL put
  back when a server check is cancelled (39f337cf); the pairing screen stops
  polling and minting codes (97a0927d); Shuffle fetches albums together
  (ea107bb9); the background player lets go when the next track can't play
  (7f1fe674); one server session start at a time (8d0c7a6e); the player's
  per-item setup runs once (3617560b); one download per trickplay sheet, two
  kept, RGB_565 (cbacde77); the photo viewer opens on the library listing
  (917caedf) and a photo from Home or Search asks the server for its row
  (e2e0e5ef).
- Build: KSP 2.3.14, Gradle 9.9.1 (53c55cd6), Gradle dependency bumps
  (8c9ae19b, e41ab7e4).
