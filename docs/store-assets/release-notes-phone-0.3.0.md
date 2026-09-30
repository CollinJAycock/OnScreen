# Release notes — phone client 0.3.0 (versionCode 1007)

Covers everything since 0.1.2 (1003), the last phone build known to have
been uploaded to Google Play. The phone app has never had a production
release; it ships on the shared `tv.onscreen.android` listing's testing
tracks (phone builds use versionCodes 1000+, the TV app below 1000).
Format as set by release-notes-1.2.0.md.

---

## What's New (Play console, phone release)

Under Play's 500-character limit.

```
• Music: a now-playing screen with cover art, gapless albums, and
  ReplayGain (Settings).
• Albums show Disc 1 / Disc 2 and play in disc order.
• Audiobooks: per-book speed, bookmarks, and a sleep timer that stops at
  the chapter's end.
• Next Up, Plan to Watch, mark watched, and Report a problem.
• Headset keys control the video you're watching.
• Faster player, and smoother returns to book and album pages.
```

---

## Reviewer notes (Play "App access")

```
OnScreen is a client application for a media server that users install
and run on their own hardware (similar in model to Plex or Jellyfin).
The app browses and plays only the media library on the user's own
server. It does not host, index, or provide access to any content
itself, and it has no feature to search for, request, obtain, or
download media from any third-party source.

Test account: see the reviewer credentials provided with this
submission. The account is scoped to demonstration libraries containing
only public-domain and Creative Commons titles.
```

---

## Internal changelog

Relative to 0.1.2 (1003):

**This release (1007)**
- **Media3 1.3.1 → 1.11.1.** Kept the old behaviour where 1.11 changed
  defaults: no notification for a stopped player, swipe-away pauses and
  stops the service, the media-key backstop judges key-downs only (1.9
  also delivers key-ups), and every controller keeps full command access
  (1.11 would limit ones it can't verify, which risked locking out
  Bluetooth, watch and car controls). Media3 1.9's stuck-player detectors
  are off: a phone call longer than 10 minutes would otherwise end the
  paused book or album, and a VBR MP3 whose header under-reports its
  length would stop a minute past it.
- **OpenStreetMap tile policy:** the photo map identifies itself as
  "OnScreen/0.3.0" with the project URL and shows the "© OpenStreetMap
  contributors" attribution.

**Since 0.1.2**
- Audio player: cover art (the album's or book's), title and artist
  above the controls, side by side in landscape; controls always shown,
  including when opened over audio already playing (ce856944).
- Book and album pages refresh in place instead of flashing "No playable
  files"; albums get disc headings (1cc3002f, 870f8576, cb19d7e4).
- The full player opens at once over background audio (6faa44d0).
- Headset and Bluetooth keys during a video control the video
  (6faa44d0, 870f8576, cb19d7e4).
- Audiobooks: speed, bookmarks, sleep at chapter end (808c97a1).
- Multi-disc albums play in disc order (a9fd1710).
- v2.5 catch-up (b77cd47e): Next Up and Plan to Watch rows, remove from
  Continue Watching, show/season Play from /up-next, mark watched /
  unwatched, library watch filter and Surprise me, Report a problem,
  admin Stop; ReplayGain (Off/Track/Album + preamp) and gapless album
  queues; album and artist pages play; SSE backoff. All request
  functionality removed (Search › Discover).
- targetSdk 36 / Android 16 safety (5b920606, 1cf5e49b); client security
  audit fixes (241dae7e, 44e57e79); mini-player ✕ clears the queue
  (1bcd55ea); on-device E2E fixes (fc796ac3).

versionCode 1007 is the next clean code: 1004–1006 were built (1006 as
more than one build) and may or may not have been uploaded.
