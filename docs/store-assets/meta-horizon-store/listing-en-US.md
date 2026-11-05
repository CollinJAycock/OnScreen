# Meta Horizon Store listing (English, United States)

Meta Horizon Developer Dashboard → your organisation → Create app (Meta
Horizon Store, 2D app). Free, no ads, no in-app purchases. Limits are
Meta's (publish-app-metadata, asset-guidelines, publish-quest-req).

## Build

| | |
|---|---|
| Package | `tv.onscreen.quest` (the `quest` flavor of `clients/android_native`) |
| APK | `./gradlew :app:assembleQuestRelease` → `app/build/outputs/apk/quest/release/app-quest-release.apk` |
| Signing | The Android upload key (`keystore/upload.jks`, alias `upload`); APK Signature Scheme v2. Every update must use the same key. |
| ABIs | arm64-v8a (+ x86_64 for the emulator), 64-bit only (VRC.Quest.Packaging.6) |
| SDK | minSdk 29, targetSdk 36 (2D apps: 32–37) |
| Version | versionCode / versionName from `app/build.gradle.kts`; raise the code for every upload |

Verify before upload: `apksigner verify --verbose app-quest-release.apk` (v2: true).

## App details

| Field | Value |
|---|---|
| Name (max 40) | OnScreen |
| Category | Entertainment |
| Genres | Media, Video |
| Supported controllers | Touch controllers, Hands (look and pinch) |
| Play area / player modes | Sitting, Standing / Single user |
| Comfort level | Comfortable (a 2D panel) |
| Internet connection | Required (it plays from the user's own server) |
| Supported languages in app | English |
| Publisher name | OnScreen |
| Website | https://on-screen.tv |
| Privacy policy (required) | https://onscreen.wolverscreen.com/privacy |
| Support | https://github.com/CollinJAycock/OnScreen/issues |
| Price | Free |
| Ads | None |

**Short description (max 500)** — 322 characters:

> Watch and listen to your own media library on Meta Quest. OnScreen connects to your OnScreen Media Server, the free open-source server you run on your own computer or NAS, and plays your movies, shows, music, audiobooks and photos in a window you can place anywhere. It includes no content of its own. No ads, no tracking.

**Long description (max 1,500)** — 1,273 characters:

> OnScreen plays the media library on your own OnScreen Media Server, free open-source software you install on your own computer or NAS. The app includes no movies, shows, music or channels: connect it to your server to browse and play the videos, music, audiobooks and photos you have added there.
>
> To use this app you need your own OnScreen Media Server set up and running, with media you have added to it. OnScreen does not provide, sell, host or link to any movies, shows, music or TV channels. It works only with your OnScreen server. Please add only media you own or have the right to use.
>
> - Home with Continue Watching, Next Up and your libraries' recently added
> - Movies, TV shows, music, audiobooks and photos, with search
> - Resume where you left off, shared with your other OnScreen apps
> - Audio tracks, subtitles and chapters; your server converts anything the headset can't play
> - Volume and mute for the app alone, and a panel you can resize
> - Sign in with your username and password, or from your phone with a code
>
> Getting started: install OnScreen Media Server (github.com/CollinJAycock/OnScreen), add your media, then open this app, enter your server's address and sign in.
>
> No ads, no analytics and no tracking. The app talks only to the server you choose.

**Keywords** (most important first; no other companies' trademarks):

> media server, home media, personal media library, video player, movies, TV shows, music player, audiobooks, photos, self-hosted, NAS, streaming

## Content rating (IARC)

Answer the IARC questionnaire for a media player with no content of its
own: no violence, sex, language, drugs, gambling or user interaction in
the app itself; the content shown is the user's own. Contact email: the
owner's.

## Art (this folder)

| Asset | File |
|---|---|
| Hero Cover 3000×900 | `hero-cover-3000x900.png` |
| Cover Landscape 2560×1440 | `cover-landscape-2560x1440.png` |
| Cover Square 1440×1440 | `cover-square-1440x1440.png` |
| Cover Portrait 1008×1440 | `cover-portrait-1008x1440.png` |
| Mini Landscape 1080×360 | `mini-landscape-1080x360.png` |
| Icon 512×512 (opaque) | `icon-512x512.png` |
| Logo (transparent) | `logo-transparent.png` |
| Screenshots (5 × 2560×1440) | `screenshots/01-home.png` … `05-photo.png` |

`render-art.py` regenerates the art from `../master/icon-1024.png`;
`clean-shot.py` paints the Android status bar and gesture handle out of an
emulator screenshot. The screenshots are the `quest` build on a
2560×1440 emulator (density 400, ~1024 dp wide like the Quest panel)
signed in to the review server as `reviewer`, so only open-licence demo
media shows (Blender open movies, Kevin MacLeod music under CC BY 4.0,
NASA photos). No trailer yet (requirement not stated for 2D apps).

## Asset checks (VRC.Quest.Asset.*)

- **Asset.2:** the covers carry only the title, "OnScreen", with the artwork: no taglines or banners.
- **Asset.3:** the title stays out of the bleed areas, the top and bottom 20% and the left and right 10%. `render-art.py` checks this and fails if it doesn't.
- **Asset.4:** the hero's title and artwork sit together in the middle 1600px, so a centre crop keeps them whole.
- **Asset.5 / Asset.6:** the screenshots are the app's own screens: no added text, logos, headsets or controllers.
- **Asset.7 / Asset.9:** there is no trailer and no 360° preview.

## Notes for Meta's review

"OnScreen needs an OnScreen Media Server. A test server with open-licence
demo media is at https://onscreen-beta.wolverscreen.com. On first launch
enter that address, then sign in with the username `reviewer` and the
password below (or pair from a browser with the code the app shows). The
app shows only the media on that server and has no store, purchases or
ads."

## Security scan results

Meta's automated Security Vulnerability Review of build 1008 flagged three
items. Build 1009 removes the one that was ours; the other two are answered
here (paste into the review notes if Meta asks):

- **Unsafe SSL TrustManager:** fixed in 1009. The app had a custom
  `X509TrustManager` that only passed every check to Android's default;
  it is gone, and connections use OkHttp's default trust (system CAs only,
  no user CAs; see `network_security_config.xml`).
- **Insecure HostnameVerifier:** not app code. The two classes are inside
  OkHttp: `okhttp3.internal.tls.OkHostnameVerifier` is OkHttp's standard
  RFC 2818 hostname check, and `ConscryptPlatform$DisabledHostnameVerifier`
  is only used with Conscrypt, which the app doesn't bundle. The app never
  sets a hostname verifier.
- **Cleartext HTTP allowed:** intentional. OnScreen connects to a media
  server the user runs themselves, usually on their home network at an
  address like `http://192.168.1.50:7070`, where HTTPS is rarely set up.
  Android can't limit cleartext to private address ranges, so it is
  allowed app-wide. An address typed without `http://` or `https://`
  gets HTTPS unless it is a private one (`normalizeServerUrl`), and the
  review server uses HTTPS.

## Release notes (1.0)

> First release on Meta Quest: your OnScreen library in a window on Quest 3, 3S, 2 and Pro. Movies, shows, music, audiobooks and photos from your own OnScreen Media Server, with resume, subtitles, audio tracks and the app's own volume control.
