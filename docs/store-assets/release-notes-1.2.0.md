# Release notes — TV client 1.2.0 (versionCode 17)

Covers everything since the live 1.1.0 (14). One file, three audiences:
the store "What's New" fields, the Amazon resubmission notes, and the
internal record. Keep future versions in this format at
`docs/store-assets/release-notes-<version>.md`.

---

## What's New (paste into BOTH store consoles)

Play limit is 500 characters; this is well under and works for Amazon too.

```
• Fixed: navigation could get stuck after opening a search result and
  pressing Back — the remote works again everywhere.
• Fixed: the device-pairing screen now shows your server's correct
  (https) address when the server uses TLS.
• Changed: Search now searches your own library only. The option to
  request titles that aren't in your library has been removed from the
  TV app; server administrators can still manage their library from
  the web app.
```

---

## Amazon resubmission notes (Appstore "Testing instructions" / appeal text)

The reviewer note that accompanies the resubmission after the policy
rejections of 1.1.0–1.1.2. Factual, no argument — describes what the
app is and what changed in the binary.

```
OnScreen is a client application for a media server that users install
and run on their own hardware (similar in model to Plex or Jellyfin).
The app browses and plays only the media library on the user's own
server. It does not host, index, or provide access to any content
itself.

Changes in this build (1.2.0) responding to the previous review:
the "content request" feature has been removed entirely. In earlier
builds, searching could show titles from a metadata catalog (TMDB)
that were not in the user's library, with an option to request that
the server's administrator add them. That feature no longer exists in
this app: Search returns results solely from the user's own server
library. There is no facility anywhere in this app to search for,
request, obtain, or download media from any third-party source.

Test account (also used for the screenshots):
  Server URL: onscreen.wolverscreen.com
  Username:   testUser
  Password:   testPassword
This account is scoped to demonstration libraries containing only
public-domain and Creative Commons titles (Blender Foundation open
films; public-domain features such as Nosferatu, Metropolis, and The
General). Attribution for the demo content is maintained in the
project repository.
```

---

## Internal changelog

Relative to 1.1.0 (14), the live build on both stores:

- **Search: focus no longer lost after Detail → Back** (82d967d).
  Leanback's `focusOnResults()` raced the row rebuild and focus fell
  through to the focusable `lb_search_frame`, where the D-pad is dead.
  Six per-flow collectors collapsed into one `combine()` (one rebuild
  per change) plus a focus backstop on the frame. Field-reported;
  verified on the Fire TV Stick.
- **Content requests removed from Search** (f3758d3). Amazon rejected
  1.1.0–1.1.2 under the Deceptive and Malicious Behavior policy over
  the TMDB request row; the 1.1.2 reword didn't change the verdict, so
  the capability is gone: request row, presenter, dialogs, strings,
  discover fetch and state. Search is library-only. The request flow
  survives on the web app, whose audience is the operator. Release APK
  verified to contain none of the request strings.
- **Pairing screen shows the healed https origin** (8619ce9). With a
  stored cleartext origin against a TLS server, the screen kept
  displaying `http://…/pair` even after the TLS self-heal upgraded
  every actual request. Now re-reads the origin post-heal. Verified on
  device from a forced stale-http state: URL renders https, PIN
  appears, claim completes.
  (The related field report — "no pair code" — was the pre-v2.4.1
  shared rate bucket, fixed server-side and live since 2026-08-06;
  1.1.0 clients recover without an update.)
- Versions 1.1.1 (15) and 1.1.2 (16) were store-rejected and never
  published; their changes are folded into this release. versionCodes
  15–16 are burned at Amazon; 17 is the next clean code.
