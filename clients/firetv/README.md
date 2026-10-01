# OnScreen Fire TV Client

Amazon Fire TV (Stick, Cube, Smart TV) build/distribute layer for
the OnScreen Android client. **No separate codebase** — Fire OS is
an Android fork and accepts the exact APK that
[`clients/android/`](../android/) produces. This folder exists
only to centralise the Fire-specific dev workflow + Amazon Appstore
submission notes.

## Why no separate code

Fire OS = Android with Amazon's launcher + Alexa + their UI. The
Android APIs (Leanback, Media3, Hilt, etc.) all work. The only
real differences relevant to OnScreen:

| Difference | Impact on us |
|---|---|
| No Google Play Services | None — we don't use GMS |
| Amazon Appstore (not Play Store) | Different submission process; same code, built as the `firetv` flavor, which drops the EPG permissions and (since 1.4.1) Live TV and online subtitle search — see [Project layout](#project-layout) |
| Alexa instead of Google Assistant | Voice search isn't wired in our app yet |
| Fire TV remote (no colored buttons on most models) | Our skip-intro / skip-credits use OK; no colored-button dependency |
| Different banner / icon size requirements for the Amazon storefront | Just additional submission assets |

The shared APK approach is what Plex, Jellyfin, and Emby do too —
one Android codebase, two distribution channels.

The `amazon.hardware.fire_tv` feature flag (declared in
[`clients/android/app/src/main/AndroidManifest.xml`](../android/app/src/main/AndroidManifest.xml)
with `required="false"`) is what Amazon's launcher reads to
classify the app under the TV category. Stock Android TV / Google TV
devices ignore it.

## Prereqs

| Tool | Notes |
|---|---|
| Everything for the Android client | Tizen-Studio-equivalent: Android Studio + Android SDK; see [`clients/android/README.md`](../android/README.md) |
| Node.js 20+ | for the wrapper scripts (matches `package.json` engines) |
| `adb` | bundled with the Android SDK at `…/Android/Sdk/platform-tools/adb`. Put it on PATH, or set `ANDROID_SDK_ROOT` / `ANDROID_HOME` (or `ADB=<path>`); `sideload.mjs` resolves it from any of those and errors clearly if it can't. |
| Fire TV with developer options enabled | one-time setup below |

### Enable Developer Options on a Fire TV

1. From the Fire TV launcher: **Settings → My Fire TV → About** →
   click the device name **7 times** until "Developer Options" unlocks.
2. **Settings → My Fire TV → Developer Options** → toggle on:
   - **ADB Debugging**
   - **Apps from Unknown Sources** (the same Fire TV-wide setting,
     not the per-app variant Android phones have)
3. Note the IP under **Settings → My Fire TV → About → Network**.
4. From your dev machine: `adb connect <fire-tv-ip>:5555`. The Fire
   TV will show an "Allow USB debugging from this computer?" prompt
   the first time — accept and check "always allow."

If `adb devices` lists your Fire TV, sideloading works.

## Dev loop

```bash
cd clients/firetv
npm install                          # one-time

# Build the APK from clients/android/ and stage it here:
npm run build                        # → clients/firetv/dist/onscreen-firetv-debug.apk

# Install + launch on the connected Fire TV:
FIRETV_HOST=<fire-tv-ip> npm run sideload
```

The Fire TV launcher refreshes after install — the OnScreen tile
appears in **Your Apps & Channels** within ~10 seconds.

`adb logcat | grep tv.onscreen.android` over the connection streams
runtime logs the same way the Android-TV dev loop does. The package
name is the same since we're shipping the same APK.

## Distribution

Two paths. **Sideload uses the debug build; the Appstore upload must be the
release build** (`--release` runs R8/proguard + the tuned release config in
`clients/android/app/build.gradle.kts` — submitting the debug APK was the old
mistake).

1. **Sideload** — the dev workflow above (`npm run build` → debug APK).
   Power-user friendly; most "Plex/Jellyfin on Fire TV" guides walk users
   through it when the official Amazon Appstore version is missing or
   outdated. Doesn't require Amazon's review.

2. **Amazon Appstore** — build the **release** variant first:
   ```bash
   npm run build -- --release        # → clients/firetv/dist/onscreen-firetv-release.apk
   ```
   then submit that APK at
   [developer.amazon.com](https://developer.amazon.com/dashboard).
   (Configure a signing config in `app/build.gradle.kts` so the release APK
   isn't `*-unsigned`; the build script warns if it is.)
   Amazon re-signs the APK with their certificate during the
   submission process; you upload your developer-signed build,
   they handle the rest. Review cycle is typically 3-7 days.
   Per-region content rating + age gating must be filled in
   alongside the binary. Amazon-specific submission assets
   (icon, screenshots, video preview) live in the Developer
   Console, not in the APK.

## Project layout

```
firetv/
  README.md                  # you are here
  package.json               # npm scripts wrapping Gradle + adb
  scripts/
    build.mjs                # cd ../android && gradlew assembleFiretv{Debug,Release} → copy APK here
    sideload.mjs             # resolve adb + adb connect + adb install -r
  dist/                      # built APKs (gitignored)
```

The Android codebase under [`../android/`](../android/) is the
source of truth. Fire TV-specific differences live as the `firetv`
Gradle product flavor inside that project, not as a duplicate
codebase here. Today that flavor:

- strips the TV-provider EPG permissions (`WRITE_EPG_DATA` /
  `READ_EPG_DATA`) — requesting them makes the Amazon Appstore require
  an EPG-capable device and filter the app off most Fire TV hardware —
  via
  [`../android/app/src/firetv/AndroidManifest.xml`](../android/app/src/firetv/AndroidManifest.xml);
- since 1.4.1 (versionCode 24), leaves out two features the Google TV
  build keeps, through BuildConfig flags set in `productFlavors`
  ([`../android/app/build.gradle.kts`](../android/app/build.gradle.kts)):

  | Flag | googletv | firetv | What Fire TV loses |
  |---|---|---|---|
  | `ONLINE_SUBTITLE_SEARCH` | `true` | `false` | The player's "Find more online…" subtitle entry (an OpenSubtitles search and download through the server). The picker lists only the item's own tracks, with no "· downloaded" tag; subtitles added from the web app still show up. The Subtitles button appears only when the item has a subtitle track. |
  | `LIVE_TV` | `true` | `false` | The Live TV and Recordings cards on Home and the screens behind them (channel guide, live channel player, recordings list). Recordings saved to a library still play as library items. |

  The Amazon Appstore rejected 1.4.0 (and earlier 1.1.0–1.1.2) under its
  Deceptive and Malicious Behavior policy, which names apps that "save,
  convert, stream or download media from third-party sources"; these are
  the two features that match that wording. Fire TV users still have both
  in the server's web app. The flags are compile-time constants, and
  `proguard-rules.pro` leaves the Live TV screens out of its blanket Hilt
  entry-point and Fragment keeps and lets R8 drop Retrofit API methods
  nothing calls (`proguard-googletv.pro` puts the original rules back for
  googletv), so R8 and the resource shrinker take the screens, their
  strings and icons, and the online subtitle search and download
  endpoints out of the Fire TV APK. `FlavorFeaturesTest` in
  `src/testFiretv` and `src/testGoogletv` pins the values.

Anything else (e.g., Alexa voice, Fire-specific Amazon SSO) belongs
in that same flavor.
