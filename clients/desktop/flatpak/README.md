# Flatpak / Flathub packaging

The OnScreen desktop client (`clients/desktop`, Tauri 2 + WebKitGTK) as a
Flatpak, app ID **`tv.on_screen.OnScreen`**, for Flathub. Flathub is also how
the app gets onto a Steam Deck: install it from Discover in desktop mode, then
"Add to Steam".

| File | What it is |
|---|---|
| `tv.on_screen.OnScreen.yml` | The manifest. Builds the release tag from source, offline. |
| `cargo-sources.json` | Generated: every crate in that tag's `clients/desktop/src-tauri/Cargo.lock`. |
| `node-sources.json` | Generated: every npm package in that tag's `web/package-lock.json`. |
| `update-sources.sh` | Regenerates the two files above. |
| `tv.on_screen.OnScreen.desktop` | Desktop entry (installed as-is). |
| `tv.on_screen.OnScreen.metainfo.xml` | AppStream metadata: the Flathub/Discover/GNOME Software listing. |
| `shared-modules/` | Copies of two modules from [flathub/shared-modules](https://github.com/flathub/shared-modules) (commit `d1ab132e`, 2026-10-21): libayatana-appindicator and the intltool it builds with. |
| `screenshots/` | The listing's screenshots, referenced from the metainfo by their `main` URL. |

The app ID is the reverse DNS of `on-screen.tv` (Flathub turns the hyphen into
an underscore). The ID is fixed once the app is on Flathub.

## What the manifest does

- **Runtime:** `org.gnome.Platform//51` (freedesktop-sdk 26.08). It has the
  libraries Tauri 2 links: WebKitGTK 4.1 (2.54), GTK 3, libsoup 3, ALSA, D-Bus,
  libsecret. The GNOME runtime is the only Flathub runtime with WebKitGTK.
- **SDK extensions:** `rust-stable` and `node24` (CI builds `web/` with Node 24).
- **Modules:**
  1. libayatana-appindicator (with libdbusmenu-gtk3, ayatana-ido,
     libayatana-indicator): the `tray-icon` crate dlopens
     `libayatana-appindicator3.so.1` and panics without it, and no runtime
     ships it.
  2. `onscreen`: checks out the release tag, runs `npm ci --offline` and
     `npm run build` in `web/` (the app embeds `web/dist`), then
     `cargo build --release --locked --features custom-protocol`. That is what
     `cargo tauri build --no-bundle` runs, without building the Tauri CLI in
     the sandbox; there is no AppImage/deb bundling. It installs the binary
     (`/app/bin/onscreen-desktop`), the desktop file, the metainfo and the
     icons (32, 64, 128, 256 and 512 px, from `src-tauri/icons`).
- **No network during the build** (Flathub's rule): Cargo and npm read only
  the generated sources.

### Video and audio inside the sandbox

Video plays in the webview, through WebKitGTK's GStreamer. The GNOME 51
runtime has everything the AppImage stages by hand
(`linux-build/stage-gst-plugins.sh`): the MP4/Matroska/MPEG-TS demuxers,
`adaptivedemux2`/`hls`, `videoparsersbad`, `autoconvert`, `pulseaudio`,
`pipewire`, `opengl`, `va`. H.264, HEVC, AAC, AC-3 and E-AC-3 come from
`org.freedesktop.Platform.codecs-extra`, a runtime extension Flatpak
installs with the runtime. Checked in WebKitGTK in this runtime:
`MediaSource.isTypeSupported` is true for H.264, HEVC, AV1, VP9, AAC, AC-3,
E-AC-3, FLAC and Opus, so hls.js's MSE path covers what the server sends.

Music through the native engine (cpal) uses ALSA. The runtime's
`alsa-flatpak.conf` routes ALSA's default device to PulseAudio/PipeWire, which
`--socket=pulseaudio` grants.

## Permissions (finish-args) and why

| Permission | Why |
|---|---|
| `--socket=wayland`, `--socket=fallback-x11`, `--share=ipc` | The window. X11 only where there is no Wayland. |
| `--device=dri` | GPU compositing in WebKitGTK; GL/VA-API video. |
| `--device=input` | Controllers, for the Gamepad API (WebKitGTK's libmanette backend reads `/dev/input`). TV mode in Steam's Gaming Mode is driven by the Deck's controls through it. Honoured by Flatpak 1.15.6 and later. |
| `--device=if:all:!has-input-device` | The fallback for a Flatpak too old for `input`: `--device=all` there, nowhere else. Flathub's linter requires this or `--require-version=1.16.0`, which would refuse to install on those systems at all. |
| `--socket=pulseaudio` | All audio (webview and native engine), to PulseAudio or pipewire-pulse (SteamOS, Fedora, Ubuntu). |
| `--share=network` | The OnScreen server, anywhere on the LAN or internet. |
| `--talk-name=org.freedesktop.secrets` | Sign-in tokens in the Secret Service (GNOME Keyring, KWallet). The `keyring` crate talks to it directly, not through the portal. |
| `--talk-name=org.freedesktop.Notifications` | Now-playing notifications (`tauri-plugin-notification`). |
| `--talk-name=org.kde.StatusNotifierWatcher` | The tray icon (StatusNotifierItem via libayatana-appindicator). |
| `--filesystem=xdg-run/tray-icon:create` | `tray-icon` writes the tray PNG to `$XDG_RUNTIME_DIR/tray-icon` and passes the panel that path. Sharing that one directory lets the host's panel read it; without it the tray icon is blank. |
| `--own-name=org.mpris.MediaPlayer2.onscreen` | MPRIS now-playing (souvlaki, `dbus_name: "onscreen"` in `src-tauri/src/now_playing.rs`). |

Not requested:

- **`--device=all`** (except as the fallback above). Its only use would be ALSA `hw:` devices (`/dev/snd`)
  for exclusive, bit-perfect output. The Linux engine doesn't open `hw:`
  devices yet (exclusive mode is Windows-only), and `--device=all` is the
  permission Flathub reviewers push back on hardest. Inside the Flatpak, music
  therefore always goes through the sound server, which may resample, as on
  any Linux install without exclusive mode today. If an exclusive ALSA path
  lands, revisit; Flatpak has no narrower sound-device permission, so the
  choice would be `--device=all` with a justification, or keeping bit-perfect
  mode to the `.deb`/`.rpm`/AppImage.
- **Files.** Downloads use the save dialog, a `GtkFileChooserNative` (rfd's
  GTK backend), which goes through the file-chooser portal: the user picks the
  destination and only that file is shared.

Behaviour that differs from the `.deb`:

- **Media keys:** the global shortcuts (`tauri-plugin-global-shortcut`) grab
  keys through X11, so they work in X11 sessions only. On Wayland the
  registration fails (non-fatal, as it is for the `.deb` on Wayland); MPRIS
  still gives the desktop's media keys and widgets control.
- **Audio devices:** without `/dev/snd`, cpal lists ALSA's virtual devices
  (`default`, `pulse`, `pipewire`), not the sound cards' `hw:`/`plughw:`
  entries the `.deb` shows.
- **Notification icon (not checked on a desktop yet):** notify-rust's
  `auto_icon` names the icon after the executable (`onscreen-desktop`), an
  icon name the host's theme doesn't have, since a Flatpak exports its icon
  as `tv.on_screen.OnScreen`. Servers that resolve the icon by that name may
  show a generic one; passing the app ID as the icon is a small `src-tauri`
  change.
- **App data** lives in `~/.var/app/tv.on_screen.OnScreen/`, not
  `~/.local/share/com.onscreen.desktop/`, so a user moving from the `.deb`
  signs in again.

## Updating for a new release

1. Bump the app's version as usual (`tauri.conf.json`, `Cargo.toml`,
   `package.json`) and tag the release.
2. In `tv.on_screen.OnScreen.yml`, set the `onscreen` module's `tag` and
   `commit` to the new tag.
3. Regenerate the offline sources from that tag's lockfiles:
   ```bash
   clients/desktop/flatpak/update-sources.sh v2.8.0
   ```
   (Python 3 and network; it fetches flatpak-builder-tools itself, or uses
   `FBT=/path/to/flatpak-builder-tools`.) Any change to
   `clients/desktop/src-tauri/Cargo.lock` or `web/package-lock.json` between
   tags needs this; a stale file fails the offline build.
4. Add a `<release>` at the top of `<releases>` in the metainfo.
5. Build and lint (below), then copy the changed files to the Flathub repo.

## Building and testing locally

On Linux with Flatpak:

```bash
flatpak remote-add --user --if-not-exists flathub https://dl.flathub.org/repo/flathub.flatpakrepo
flatpak install --user flathub org.flatpak.Builder
cd clients/desktop/flatpak
flatpak run org.flatpak.Builder --user --install-deps-from=flathub --force-clean \
  --repo=repo --install build-dir tv.on_screen.OnScreen.yml
flatpak run tv.on_screen.OnScreen
```

On Windows or macOS, Docker with the Flathub CI image (it needs
`--privileged` for bubblewrap):

```bash
docker run --rm -it --privileged -v "$PWD:/src" -w /src/clients/desktop/flatpak \
  ghcr.io/flathub-infra/flatpak-github-actions:gnome-51 bash
# inside:
flatpak-builder --force-clean --disable-rofiles-fuse --install-deps-from=flathub \
  --repo=/tmp/repo /tmp/build-dir tv.on_screen.OnScreen.yml
flatpak build-bundle /tmp/repo onscreen.flatpak tv.on_screen.OnScreen
```

A first build took under 4 minutes in Docker Desktop on a Windows
workstation (not counting the GNOME SDK download; about 80 s of it is Rust). The bundle is
about 6 MB and installs as 19 MB; the runtime comes from Flathub.
`flatpak install --user onscreen.flatpak` installs a bundle on any machine
with Flathub configured.

To see it start without a desktop (in the same container):

```bash
flatpak install -y --user onscreen.flatpak
Xvfb :99 -screen 0 1280x800x24 & export DISPLAY=:99
dbus-run-session -- flatpak run --user tv.on_screen.OnScreen
```

It opens on the "Connect to your OnScreen server" screen, owns
`org.mpris.MediaPlayer2.onscreen` on the session bus and writes its tray icon
to `$XDG_RUNTIME_DIR/tray-icon` on the host side.

The manifest builds the tag, not your working tree. To try uncommitted
changes, temporarily replace the `git` source with
`{type: dir, path: ../../..}` (the repository root) and regenerate the
sources from your lockfiles; don't commit that.

### Linters

Flathub runs these on every submission; run them before opening the PR:

```bash
flatpak run --command=flatpak-builder-lint org.flatpak.Builder manifest tv.on_screen.OnScreen.yml
flatpak run --command=flatpak-builder-lint org.flatpak.Builder repo repo
flatpak run --command=appstreamcli org.flatpak.Builder validate tv.on_screen.OnScreen.metainfo.xml
desktop-file-validate tv.on_screen.OnScreen.desktop
```

Expected results:

- `appstreamcli --pedantic` notes that the ID has uppercase letters. Flathub
  accepts that (the last component is the app name).
- Until this directory is on `main`, the screenshot URL 404s: the
  validators warn `screenshot-image-not-found`, and the `builddir`/`repo`
  lints fail with `appstream-missing-screenshots`.
- With the screenshot reachable, a local build with Flathub's
  `--mirror-screenshots-url=https://dl.flathub.org/media` still fails the
  `builddir`/`repo` lints with `appstream-external-screenshot-url` and
  `appstream-remote-icon-not-mirrored`: they check that the mirrored copies
  are live on dl.flathub.org, which only Flathub's own build publishes.
  Nothing else is reported.

## Submitting to Flathub (owner)

Flathub's docs: <https://docs.flathub.org/docs/for-app-authors/submission>.

1. **Fork** <https://github.com/flathub/flathub> and clone the fork's
   `new-pr` branch (`git clone --branch=new-pr`).
2. **New branch** from `new-pr`, e.g. `tv.on_screen.OnScreen`.
3. **Add the files** at the root of that branch:
   `tv.on_screen.OnScreen.yml`, `cargo-sources.json`, `node-sources.json`,
   `tv.on_screen.OnScreen.desktop`, `tv.on_screen.OnScreen.metainfo.xml`.
   For the tray module, add flathub/shared-modules as a submodule instead of
   the copy here (`git submodule add https://github.com/flathub/shared-modules.git`);
   the manifest's `shared-modules/...` path then resolves unchanged.
   Optionally add a `flathub.json` with `{"only-arches": ["x86_64"]}` until an
   aarch64 build has been tried (the Rust/npm sources are arch-independent,
   but nothing has been tested on ARM).
4. **Open the PR against `new-pr`** (not `master`), titled
   "Add tv.on_screen.OnScreen". Comment `bot, build` on it to get a test
   build; fix what it reports.
5. **Domain verification.** An app ID built from a domain is verified
   against that domain, `on-screen.tv`. Once the app is on Flathub, Flathub's developer portal
   (<https://flathub.org>, log in, the app's "Developer settings" / verification
   page) shows a token; publish it as a plain-text file at
   `https://on-screen.tv/.well-known/org.flathub.VerifiedApps.txt`, one token
   per line (the site is `site/` in this repo, deployed to S3 + CloudFront: add
   `site/.well-known/org.flathub.VerifiedApps.txt` and make sure CloudFront
   serves it as `text/plain`). Then press "Verify" in the portal. Verified
   apps get the checkmark on Flathub.
6. **Review.** Expect questions about each finish-arg; the table above has the
   answers. Once merged, Flathub creates `github.com/flathub/tv.on_screen.OnScreen`;
   later updates are PRs there (steps in "Updating for a new release").

Before submitting:

- The screenshot URLs point at `main`, so this directory must be merged
  first; Flathub's build downloads them.
- Replace the screenshot. `screenshots/connect.png` is the Flatpak itself,
  captured headless at 1280×800 on its first-run screen; Flathub's quality
  guidelines want the app in use, without empty states. Add captures with a
  library loaded (Home with artwork, a title page, the player, music) to
  `screenshots/` and list them in the metainfo, the best one as
  `type="default"`.

## Steam Deck: TV mode

Launched from Steam's Gaming Mode, the app shows the **TV app** instead of the
desktop UI: the controller-driven 10-foot UI the server serves at
`<server>/tvapp/` (the Xbox app's page, `clients/xbox`), full screen, with no
tray. `src-tauri/src/tv_mode.rs` has the details:

- **When:** `SteamGamepadUI=1` or `XDG_CURRENT_DESKTOP=gamescope`. Desktop Mode
  on the Deck sets neither and keeps the desktop UI. `ONSCREEN_TV_MODE=1` / `0`
  or the `--tv-mode` / `--desktop-mode` arguments force it either way (a Steam
  launch option, or to try it on a PC).
- **Flow:** the desktop UI starts as usual. If no server is saved, its setup
  screen asks for one (Steam+X opens the on-screen keyboard). Once a server is
  saved and its `/tvapp/` answers, the start page hands over to
  `<server>/tvapp/?shell=desktop&device=Steam%20Deck`. The TV app has its own
  sign-in, including a code to approve from another device. A server without
  `/tvapp/` (older than v2.7) or one that's down keeps the desktop UI.
- **The TV app asks the app for two things** by navigating to a URL the app
  intercepts, since a remote page gets no Tauri IPC: `onscreen-shell://exit`
  (B on a first screen, then Exit) quits, and `onscreen-shell://changeServer`
  (Settings, Forget server) opens the desktop UI's server screen.
- **Controls:** the Gamepad API, as on a PC with a controller (A = OK, B =
  Back, X / Y = subtitles / audio, bumpers and triggers = previous / next and
  skip). WebKit only reports a pad after a button press on it. If a pad isn't
  seen, the fallback is a Steam Input layout that maps the D-pad to the arrow
  keys, A to Enter and B to Escape, which the TV app also takes.
- **Screen:** the TV app lays out at 1920x1080 and scales to fit, so the
  Deck's 1280x800 shows it at 1280x720 with bars above and below.
- **Not checked on a Deck yet** (no device): tested on Windows (WebView2) with
  `--tv-mode` at 1280x800 and a simulated controller. To confirm on a Deck:
  the virtual controller under `/dev/input` is seen, and video plays through
  the runtime's GStreamer.
- **Release:** Flathub builds the tag in the manifest, so TV mode ships with
  the first tag after it merges.

### Background

- **Detecting Gaming Mode inside the Flatpak.** Flatpak passes the caller's
  environment through, minus a short blocklist (`LD_PRELOAD` among it), so a launch
  from Steam's Gaming Mode sees what Steam sets: `SteamDeck=1` (Deck hardware),
  `SteamOS=1`, `SteamGamepadUI=1` (Gaming Mode / Big Picture),
  `XDG_CURRENT_DESKTOP=gamescope`, `GAMESCOPE_WAYLAND_DISPLAY`, and
  `SteamGameId`/`SteamAppId`/`SteamClientLaunch` for a non-Steam shortcut.
  Checked with `flatpak run --command=env` on the GNOME 51 runtime: all of
  them reach the sandbox; only `LD_PRELOAD` (the Steam overlay) is stripped.
  The host's `/etc/os-release` is visible at `/run/host/os-release`
  (`ID=steamos`), which identifies SteamOS in Desktop Mode too.
  `SteamGamepadUI` or `XDG_CURRENT_DESKTOP=gamescope` is the signal for
  10-foot mode.
- **Gamepad API.** WebKitGTK in the GNOME 51 runtime is built with
  libmanette, and `navigator.getGamepads` exists. To see controllers, the
  sandbox needs the evdev nodes: `--device=input` (accepted on Flathub;
  honoured by Flatpak 1.15.6 and later, so check the Deck's `flatpak
  --version`) or `--device=all`. In Gaming Mode, Steam Input presents the
  Deck's controls to a non-Steam app as a virtual controller under
  `/dev/input`, which `--device=input` covers (to confirm on a Deck). WebKit
  only reports a pad after a button press on it (fingerprinting rule).
- **No-permission alternative:** a Steam Input layout that maps the
  controls to keys (Tab/Shift+Tab, Enter, Escape) drives the UI through
  keyboard focus, with no extra sandbox permission. The TV app takes the
  arrow keys, Enter and Escape, so this is the fallback if a Deck's virtual
  controller doesn't reach the sandbox.
