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

Status: phase 0 (no console yet). Everything below runs on a Windows PC; the
console visit (phase 1) is the checklist at the end.

## The Xbox layer (what differs from the webOS app)

| File | What |
|---|---|
| `src/lib/shell.ts` | The page ↔ shell bridge. The shell passes `?shell=xbox&device=…&uhd=…&hdr=…`; the page posts `exit`, `changeServer` and `nativeKeys` through `window.chrome.webview`, and receives `back`. The API origin is the page's own (`servedOrigin`). |
| `src/lib/focus/keys.ts` | The controller: Windows' gamepad key codes 195–218 (A = OK, B = Back, X / Y = Subtitles / Audio pickers, bumpers = next / previous, triggers = skip, left stick = D-pad). |
| `src/lib/gamepad.ts` | The Gamepad API as the same key events, for a PC with a controller; stands down for good once the platform sends gamepad key events itself. |
| `src/lib/appExit.ts` | B on a first screen: the Exit / Cancel popup; Exit asks the shell to close the app. |
| `src/lib/device.ts`, `src/lib/platform.ts` | The device name ("Xbox Series X #1a2b") and the display's 4K / HDR, from the shell. |
| `src/app.html`, `+layout.svelte` | The 1920x1080 layout is scaled to the view with a transform (an Xbox app's web view is 960x540 CSS px). |
| `src/routes/settings/diagnostics` | Settings > Diagnostics: what the console's web view decodes (MSE, MediaCapabilities, HDR), the capability header, and a live log of every key event. |

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
```

The first build makes a local test signing certificate (`make-cert.ps1`,
`CN=OnScreen Dev`; the `.pfx` / `.cer` are gitignored).

On a PC, a UWP app can't reach the PC's own loopback address. To point the
shell at a server running on the same PC, exempt it once (admin):
`CheckNetIsolation LoopbackExempt -a -n=OnScreen.Xbox_fw6qcgmhrp7e8`.

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
   `shell\AppPackages\OnScreen.Xbox_0.1.0.0_x64_Test\` (the `.msix`, the
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
