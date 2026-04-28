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
| Amazon Appstore (not Play Store) | Different submission process; APK is the same |
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
| Node.js 24+ | for the wrapper scripts |
| `adb` on PATH | bundled with the Android SDK at `~/AppData/Local/Android/Sdk/platform-tools/adb` |
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

Two paths, both served by the same APK from `npm run build`:

1. **Sideload** — the dev workflow above. Power-user friendly;
   most "Plex/Jellyfin on Fire TV" guides walk users through it
   when the official Amazon Appstore version is missing or
   outdated. Doesn't require Amazon's review.

2. **Amazon Appstore** — submit the APK at
   [developer.amazon.com](https://developer.amazon.com/dashboard).
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
    build.mjs                # cd ../android && gradlew assembleDebug → copy APK here
    sideload.mjs             # adb connect + adb install -r
  dist/                      # built APKs (gitignored)
```

The Android codebase under [`../android/`](../android/) is the
source of truth. Anything we'd want to do differently for Fire TV
(e.g., Alexa voice integration, Fire-specific Amazon SSO) would
live as a Gradle product flavor inside that project, not as a
duplicate codebase here.
