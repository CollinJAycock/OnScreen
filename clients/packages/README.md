# Store packages

`clients/build-packages.ps1` builds the upload for each store into this folder.
Everything here except this README is gitignored: the packages are signed
binaries.

```powershell
cd clients
.\build-packages.ps1 -List                                 # the targets
.\build-packages.ps1 -Target googleplay-tv,googleplay-phone
.\build-packages.ps1 -Target all
.\build-packages.ps1 -Target roku -RokuSign -RokuHost 10.0.0.86
```

## Names

Every package is named for the store it goes to:

```
OnScreen-<Store>-<Platform>-<version>[-<build>].<ext>
```

| Target | File | Upload to |
|---|---|---|
| `googleplay-tv` | `OnScreen-GooglePlay-AndroidTV-1.6.0-29.aab` | Play Console, `tv.onscreen.android` (TV bundle) |
| `googleplay-phone` | `OnScreen-GooglePlay-Phone-0.5.0-1013.aab` | Play Console, `tv.onscreen.android` (phone bundle) |
| `amazon-firetv` | `OnScreen-AmazonAppstore-FireTV-1.6.0-29.apk` | Amazon Developer Console, ASIN B0GX2XH9P2 |
| `meta-quest` | `OnScreen-MetaHorizonStore-Quest-0.5.0-1013.apk` | Meta Horizon developer dashboard, `tv.onscreen.quest` |
| `roku` | `OnScreen-RokuChannelStore-Roku-1.2.0-1.pkg` | Roku developer dashboard (`.zip` without `-RokuSign`) |
| `lg-webos` | `OnScreen-LGContentStore-webOS-1.0.0.ipk` | LG Seller Lounge, `tv.onscreen.webos` |
| `samsung-tizen` | `OnScreen-SamsungTVApps-Tizen-1.1.3.wgt` | Samsung Apps TV Seller Office |
| `microsoft-xbox` | `OnScreen-MicrosoftStore-Xbox-0.1.12.0.msixupload` | Partner Center, Store ID 9N58M48F5XMW |
| `desktop-windows` | `OnScreen-GitHubReleases-Windows-2.7.0-x64.msi`, `…-x64-setup.exe` | GitHub release assets |
| `desktop-linux` | `OnScreen-GitHubReleases-Linux-2.7.0-amd64.deb`, `…-x86_64.rpm`, `…-x86_64.AppImage` | GitHub release assets (the AppImage is over GitHub's 100 MB repo limit, fine as a release asset) |
| `kodi` | `OnScreen-KodiRepo-Kodi-0.2.0.zip` | The OnScreen Kodi repository at on-screen.tv/kodi ("Install from zip file") |

For a channel that isn't a store (the desktop installers and the Kodi add-on),
the name carries the channel instead. A target with several files adds a suffix
for each, such as the architecture.

The version and build come from each client's own version source:
`versionName` / `versionCode` in the Gradle file, the Roku `manifest`
(major.minor, then `build_version`), `appinfo.json`, `config.xml`,
`Package.appxmanifest`, `tauri.conf.json` and the Kodi `addon.xml`. Bump those as before; the script only reads them.

Beside each package:

- `<name>.sha256`: the hash, in `sha256sum` format.
- `<name>.json`: target, store, version, build, sha256, size, the commit and
  branch it was built from, whether that client's folder was clean, the signer,
  and the build time.

## What the script checks

- **Committed code.** It stops if the client's folder has uncommitted
  changes. `-AllowDirty` overrides that, and the `.json` records `"clean": false`.
- **Fresh output.** The package must have been written by this run.
- **Android signing.** Each `.aab` / `.apk` must carry the upload key
  (SHA-256 `579BD037…FA3CAC75`). For the APKs, `aapt2` must report the expected
  package and versionCode.

## Prerequisites

Signing material stays where each client already keeps it, all gitignored:

| Target | Needs |
|---|---|
| Android (all four) | A JDK (`JAVA_HOME`, or `C:\Program Files\Java\jdk-*`), the Android SDK. TV: `clients/android/local.properties` with the `release.*` keystore entries. Phone / Quest: `clients/android_native/keystore.properties` + `keystore/upload.jks`. |
| `roku` | Node. With `-RokuSign`: a developer-mode Roku that already holds the channel's signing key (genkey was run once, ever; never run it again). The developer and signing passwords come from `ROKU_DEV_PASSWORD` / `ROKU_SIGN_PASSWORD` or a prompt, and are never written anywhere. The Roku's sideloaded dev channel is replaced by this build. |
| `lg-webos` | Node and the webOS CLI (`ares-package`). |
| `samsung-tizen` | Tizen Studio's CLI (`tizen.bat`, found on PATH or in `C:\tizen-studio\tools\ide\bin`) and the `OnScreenProd` certificate profile (author + distributor). `-TizenProfile` picks another profile. The dev `.wgt` in `clients/tizen/dist` is left alone. |
| `microsoft-xbox` | Visual Studio 2022 with the UWP workload. The script runs `clients/xbox/shell/build.ps1 -Store`. |
| `desktop-windows` | Rust and the tauri-cli version CI pins (`cargo install tauri-cli --locked --version 2.12.1`). `clients/desktop/build.ps1` refuses any other CLI, because the CLI holds the bundler. The script runs that `build.ps1`. |
| `desktop-linux` | Docker Desktop (Linux containers). The script runs `clients/desktop/linux-build/build.ps1`, which has the pinned CLI in its image. |
| `kodi` | Python on PATH. Publishing the repository is still `clients/kodi/scripts/build_repo.py --out site/kodi` plus a site deploy; this target builds the add-on zip. |

The Android builds run Gradle with `--no-daemon`, so nothing is left holding
files afterwards.

## Store notes

- **LG** previously got the ares-package name (`tv.onscreen.webos_<ver>_all.ipk`).
  If Seller Lounge ever refuses the store-named file, upload it under that name
  instead; the contents are identical.
- **Roku**: without `-RokuSign` the target stops at the channel `.zip`. Sideload
  that zip, sign it with the device's Packager (Utilities → Packager, user
  `rokudev`), and upload the `.pkg`.
