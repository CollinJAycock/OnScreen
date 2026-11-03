# OnScreen for Xbox

Two parts:

- **The TV app** (this folder): the webOS app's Svelte code with an Xbox
  platform layer. It is not installed on the console: the OnScreen server
  embeds the build and serves it at **`<server>/tvapp/`** (`internal/tvui`),
  so the page and the API share one origin (no CORS, no mixed content for a
  plain-http LAN server) and the app updates with the server.
- **The shell** (`shell/`): a small UWP app (C#, WinUI 2's WebView2), the
  form Xbox runs. It asks for the server, checks that `<server>/tvapp/`
  answers, and shows it full screen. This is the Jellyfin Xbox app's design.

Status: phase 1. Runs on an Xbox Series X in Dev Mode (November 2026); what
the console showed is under "Phase 1 results" at the end.

## The Xbox layer (what differs from the webOS app)

| File | What |
|---|---|
| `src/lib/shell.ts` | The page ↔ shell bridge. The shell passes `?shell=xbox&device=…&uhd=…&hdr=…`; the page posts `exit`, `changeServer`, `nativeKeys` and `webKeys` through `window.chrome.webview`, and receives `back` and the controller's keys (`key`, played as key events with their gamepad codes). The API origin is the page's own (`servedOrigin`). |
| `src/lib/focus/keys.ts` | The controller: Windows' gamepad key codes 195–218 (A = OK, B = Back, X / Y = Subtitles / Audio pickers, bumpers = next / previous, triggers = skip, left stick = D-pad). |
| `src/lib/gamepad.ts` | The Gamepad API as the same key events, for a PC with a controller; stands down for good once the platform sends gamepad key events itself. |
| `src/lib/appExit.ts` | B on a first screen: the Exit / Cancel popup; Exit asks the shell to close the app. |
| `src/lib/device.ts`, `src/lib/platform.ts` | The device name ("Xbox Series X #1a2b") and the display's 4K / HDR, from the shell. |
| `src/app.html`, `+layout.svelte` | The 1920x1080 layout is scaled to the view with a transform (an Xbox app's web view is 960x540 CSS px). |
| `src/routes/settings/diagnostics` | Settings > Diagnostics: what the console's web view decodes (MSE, MediaCapabilities, HDR), the capability header, and a live log of every key event. |
| `shell/NativePlayer.cs`, `src/lib/player/nativeVideo.ts` | The native player (below). |

## The native player (HEVC, HDR10, Dolby audio)

The console's web view decodes only H.264 and VP9 and can't switch the TV
into HDR. So video plays in the console's own player: a `MediaPlayerElement`
behind the web view, which the shell makes transparent. The watch page drives
it through `NativeVideo`, which has the shape of the `<video>` element the
page already drives (`src`, `play()`, `currentTime`, the media events), so the
player screen, pickers, subtitles, Up Next and progress reporting are
unchanged and draw over the picture. Commands go to the shell as `native`
messages (`load`, `play`, `pause`, `seek`, `rate`, `stop`), and events come
back as `nativeEvent` messages, each tagged with its load. Music and
audiobooks stay on the web view.

- **What the server is told** (`src/lib/api/capabilities.ts`, with the shell's
  `?native=1`): H.264 and HEVC to 10-bit, AAC / MP3 / AC-3 / E-AC-3, MP4 / MKV
  / MOV, and HDR when the TV takes HDR10. So an HDR film comes as a remux of
  its own video (or the file itself), not a tone-mapped transcode.
- **HDR:** an HDR10 (or HDR10+) source that isn't re-encoded asks the shell
  for HDR10. It picks a 4K SMPTE 2084 mode nearest 60 Hz (120 Hz modes aren't
  for media; the console does 3:2 pulldown), and on stop goes back to the
  default mode. The switches are logged to the app's
  `LocalState\display-switches.txt` (Device Portal > File explorer).
- **Dolby Vision:** the header claims `dovi=8.1` with HDR, so the server keeps
  a profile 8.1 file's Dolby Vision on a direct play or remux and converts a
  profile 7 (Blu-ray) one to 8.1. The console decodes it as `dvhe.08`, and the
  shell asks for a Dolby Vision (low latency) mode, falling back to HDR10
  without one. The console offers those modes only with *Video modes > Allow
  Dolby Vision* on. Profile 5 isn't claimed.
- **The `hevcPlayback` capability** (restricted, in `Package.appxmanifest`):
  without it the console lists no HDR modes. It also gives the app 3.25 GB
  and closes it when a game starts.
- **Direct play** (the file itself, no session) only from a server on the
  home network (`onHomeNetwork`). The console downloads a file as fast as the
  link allows, and over the internet that filled the server's uplink and
  stalled the app's own requests for a minute; a session's HLS is fetched a
  segment at a time.

## Developing

```powershell
npm ci
npm test            # vitest
npm run check       # svelte-check
npm run build       # build/: what the server embeds
```

`npm run dev` serves the app on :5175; in dev only, Setup asks for a server
(the page isn't on the server's origin, so that server must allow it, as for
the other TV apps' dev servers).

The server picks the build up through `make tvui` (part of `make build` /
`make deploy`), `deploy.ps1`, the installers and the Docker images: each runs
`npm run build` here and copies `build/` to `internal/tvui/dist/`. A server
built without it serves a placeholder page at `/tvapp/`, which the shell
recognises ("This OnScreen server doesn't include the TV app yet").

## The shell

Needs Visual Studio 2022 with the **Universal Windows Platform development**
workload.

```powershell
cd shell
.\build.ps1             # Release (.NET Native) x64: AppPackages\OnScreen.Xbox_<ver>_x64_Test\
.\build.ps1 -Register   # also install it on this PC (Windows Developer Mode)
.\build.ps1 -DevTools -Register   # a PC test build with DevTools on 127.0.0.1:9229
.\build.ps1 -Store      # the Partner Center upload: AppPackages\*.msixupload
```

The package identity is the Partner Center reservation: `OnScreen.OnScreen`,
publisher `CN=4F8B1511-8473-4887-9ED7-421BC42D615E`, package family
`OnScreen.OnScreen_d8tnyqrgg61zm`, Store ID `9N58M48F5XMW`. Builds sign with
a local test certificate for that publisher (`make-cert.ps1`, run by
`build.ps1`; the `.pfx` / `.cer` are gitignored); the Store re-signs what it
ships. Tiles, logos and the splash come in every scale, made from
`docs/store-assets/master/icon-1024.png`.

On a PC, a UWP app can't reach the PC's own loopback address. To point the
shell at a server running on the same PC, exempt it once (admin):
`CheckNetIsolation LoopbackExempt -a -n=OnScreen.OnScreen_d8tnyqrgg61zm`.

Known: the Debug configuration crashes at start on this PC (an unhandled .NET
exception before the app's own code runs, likely its debug runtime set-up);
Release runs, and Release is what a console gets.

## Phase 1: the console visit

Before going:

1. **Partner Center account** (individual developer) — needed to activate Dev
   Mode.
2. **A server with the TV app**: deploy the current main to QA (or the review
   server), and check `https://<server>/tvapp/` shows the Sign in screen in a
   browser.
3. Build the package: `shell\.\build.ps1`. Take the folder
   `shell\AppPackages\OnScreen.Xbox_<ver>_x64_Test\` (the `.msix`, the
   `.cer`, and `Dependencies\x64\`).

On the console:

1. Install **Dev Mode Activation** from the Microsoft Store, sign in with the
   Partner Center account, and switch to Developer Mode (the console
   restarts).
2. In Dev Home, enable **Remote Access** (Device Portal) and set its user
   name and password; note the address it shows (`https://<console-ip>:11443`).
3. From a PC on the same network, open the Device Portal > **Add**, choose the
   `.msix`, and add the dependency packages from `Dependencies\x64\`
   (`Microsoft.NET.Native.Framework.2.2`, `Microsoft.NET.Native.Runtime.2.2`,
   `Microsoft.UI.Xaml.2.8`, `Microsoft.VCLibs.x64.14.00`). Install.
4. Start **OnScreen** from Dev Home's app list. Enter the server and sign in.

What to check (photograph the Diagnostics screen; it settles the open
questions in one go):

- [ ] **Settings > Diagnostics**: the MSE and MediaCapabilities rows (HEVC,
      HEVC Main 10, HDR10, AV1, AC-3 / E-AC-3, DTS), the display rows, and
      the X-Client-Capabilities header.
- [ ] **Controller**, on Diagnostics' key log: A, B, X, Y, LB, RB, LT, RT,
      D-pad, left stick, Menu, View. Each line shows the key code the console
      sends and what the app makes of it. Note whether B shows up as a key
      event at all (if not, the shell forwards the system Back instead).
- [ ] Moving around Home, Libraries, Search, a show's seasons; A opens, B goes
      back; holding A on a Continue Watching card opens its options.
- [ ] B on Home: the Exit / Cancel popup; Exit closes the app.
- [ ] Playback: an H.264 film; a 4K HDR HEVC film (does the TV switch to
      HDR?); switching audio and subtitles; seeking with the D-pad and the
      triggers; Up Next at the end of an episode.
- [ ] Music and an audiobook (speed), and Photos.
- [ ] Settings > Forget server: back to the shell's server prompt.
- [ ] No mouse cursor appears; the picture fills the screen with no black
      border.

Afterwards, switch the console back to Retail Mode in Dev Home (its games and
saves are untouched).

## Phase 1 results (Series X, OS 26100, WebView2 150, November 2026)

- **Controller:** the web view gets no key events from the controller (the
  key log showed only `lib/gamepad`'s polled presses, code 0). The shell now
  passes the window's gamepad keys on to the page. B reaches the shell only
  as the system's Back request, which it still forwards. Device Portal's
  remote input (`wss://<console>:11443/ext/remoteinput`) goes the same way,
  so the app can be driven from a PC for testing.
- **Codecs in the web view:** MSE answers yes for HEVC, AV1, AC-3 and E-AC-3,
  but the web view decodes none of them, even with `hevcPlayback`: HEVC fails
  decoder set-up (`kUnsupportedConfig`) at every profile, tier and size tried;
  AV1 and AC-3 / E-AC-3 append and never produce a frame. H.264 (to 4K) and VP9
  Profile 0 and 2 play. Hence the native player.
- **The native player:** plays 4K HEVC Main 10 HDR10 in MP4, MKV and the
  server's fMP4 HLS, with E-AC-3 5.1, and switches an HDR TV to
  3840x2160 @ 59.94 BT.2020 10-bit HDR10 (checked: Deadpool & Wolverine, a
  remux of its own HDR10 video; the switch log read HighDynamicRange, and back
  to SDR on leaving). The display APIs list HDR modes only with `hevcPlayback`.
  Dolby Vision: the same film (a profile 7 Blu-ray remux the server converts
  to 8.1) showed the LG C1's Dolby Vision banner with a correct picture; the
  console read the track as `dvhe.08`.
- **Checked on the console:** D-pad, A, B (pages, player, pickers), the exit
  popup, the sign-in keyboard's way down to Change server, pair sign-in,
  playback start and resume, the Audio (Y) and Subtitles (X) pickers,
  seeking, Libraries, music with next track (RB).
- **Not yet:** suspend and resume during native playback (the Guide button),
  DTS / TrueHD / Dolby Vision files (sent as transcodes), VP9 / AV1 on the
  native player.
