# Release notes — TV client 1.4.0 (versionCode 22)

Covers everything since 1.1.0 (14), the build live on the Amazon Appstore.
Google Play has no production release yet (the closed test is still
running), and 1.2.x–1.3.0 (17–21) never reached a store's users, so their
changes are folded in here. Format as set by release-notes-1.2.0.md: the
store "What's New" text, the reviewer notes, and the internal record.

Both flavors ship from this code: `googletv` (Google Play: Android TV,
Google TV, NVIDIA SHIELD) and `firetv` (Amazon Appstore).

---

## What's New (paste into BOTH store consoles)

Under Play's 500-character limit; works for Amazon too.

```
• New: your TV switches to the video's frame rate (Settings > Playback).
• Surround: DTS and Dolby TrueHD/Atmos go to your receiver untouched.
• HDR, 4K and AV1 are used only when your screen and device support them.
• Music and audiobooks keep playing in the background; book speed,
  bookmarks and a sleep timer that stops at the chapter's end.
• Next Up, Plan to Watch, mark watched, and Report a problem.
• Better NVIDIA SHIELD support.
```

---

## Reviewer notes (Amazon "Testing instructions"; Play "App access")

Carry forward the statement from release-notes-1.2.0.md and add the
changes in this build. The reviewer account is the one in
`docs/store-assets/demo-review-library.md`; it sees only the
public-domain / Creative Commons demo library, so no commercial title
appears to the reviewer.

```
OnScreen is a client application for a media server that users install
and run on their own hardware (similar in model to Plex or Jellyfin).
The app browses and plays only the media library on the user's own
server. It does not host, index, or provide access to any content
itself. There is no facility anywhere in this app to search for,
request, obtain, or download media from any third-party source.

New in 1.4.0: playback improvements only — display frame-rate
matching (Settings > Playback > Match frame rate), audio passthrough
of DTS and Dolby TrueHD to a connected receiver, HDR/4K/AV1 used only
when the display and device support them, and background playback of
music and audiobooks with standard media controls.

Test account: see the reviewer credentials provided with this
submission. The account is scoped to demonstration libraries containing
only public-domain and Creative Commons titles.
```

---

## Internal changelog

Relative to 1.1.0 (14):

**This release (22)**
- **Media3 1.3.1 → 1.11.1.** Needed a notification-hold fix so a book
  paused and parked keeps its foreground service, and legacy subtitle
  decoding for the HLS side-loaded subtitles (Media3 1.4+ would have
  thrown on them). Media3 1.9's stuck-player detectors are off: they end
  playback a minute past a file's declared length (VBR MP3 without a Xing
  header) and after 10 minutes of suppressed playback. Upstream wins: DTS-HD MA / DTS:X in MKV now reach the
  receiver in full (Media3 1.9 Matroska DTS-HD detection), better
  underrun handling.
- **NVIDIA SHIELD** (Android 11, all models): TrueHD/Atmos claimed when
  the HDMI output takes it (the v2.5 server remuxes TrueHD in `.m2ts`,
  which Media3 can't read); `hlg=0` so HLG is converted instead of shown
  dark and green; `vp9MaxBitDepth=8` so 10-bit VP9 is transcoded; the
  fallback decision (server unreachable) no longer assumes AV1 or DTS;
  the frame-rate setting's text no longer tells SHIELD owners to turn on
  SHIELD's own beta toggle.
- **Screen stays on for video only**, so music and audiobooks no longer
  block the screensaver (Play TV quality TV-BA).
- **Media sessions**: full command access for the system's controllers
  (remote media keys, Assistant), as before Media3 1.11.

**Since 1.3.0 (21)**
- Frame-rate matching with a Settings > Playback toggle; hardware-only
  AV1; DTS claimed when the HDMI output takes a DTS bitstream (01120bf0).
- HDR and resolution claimed from the real screen; Android 16 null HDR
  capabilities mean forced SDR (3fbff487, 0faa04c2).
- Background audio: transcoded audio keeps playing; the Fire TV run's
  defects fixed (ec2c37d2, 2e922ef3, 1c08e147).
- Audiobooks: per-book speed, bookmarks, sleep at chapter end; multi-file
  books play chapters as audio and chain (808c97a1, 902aa03f).
- Multi-disc albums play in disc order (a9fd1710).

**1.2.0–1.3.0 (17–21), never published**
- v2.5 catch-up: Next Up and Plan to Watch rows, remove from Continue
  Watching, up-next Play, mark watched/unwatched, library watch filter
  and Surprise me, Report a problem, admin Stop (b77cd47e).
- All content-request functionality removed (f3758d3b, b77cd47e).
- targetSdk 36 / Android 16 BACK handling (1cf5e49b); minSdk 21 → 24
  (43362f1a); client security audit fixes (241dae7e, 44e57e79).
- Search focus fix (82d967d0); pairing screen shows the https origin
  (8619ce9d).

versionCode 22 is the next clean code: 19–21 were built and may or may
not have been uploaded, and a code only has to increase.
