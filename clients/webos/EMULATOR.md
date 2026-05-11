# webOS emulator dev loop

LG ships an official webOS TV Emulator alongside the SDK. It boots a
real webOS image (the same Chromium-based webview that production TVs
use) in a local VM, so most layout / focus / API-surface bugs surface
without picking up a real TV.

## One-time setup

1. Install the **LG webOS TV SDK** from
   https://webostv.developer.lge.com/develop/tools/sdk-introduction.
   Pick the version matching the oldest webOS your client targets
   (currently webOS 5+ — see `appinfo.json`).
2. Install the **CLI** if it didn't ship with the SDK:
   ```
   npm install -g @webosose/ares-cli
   ```
3. Launch the **Emulator Launcher** from the SDK install. Pick a
   webOS version (6.0 is a reasonable default — matches Chromium 87)
   and a screen profile (FHD 1920×1080 is the canonical TV). Start
   the emulator and wait for the home screen.
4. **Register the running emulator with `ares-cli`**:
   ```
   ares-setup-device
   ```
   In the wizard, **add** a device named exactly `emulator`. The
   default emulator host is `127.0.0.1`, port `6622`, user `prisoner`,
   no password / key. (If your SDK install used a different SSH port
   for the emulator VM, check `Tools → SDK Manager → Emulator` for
   the real number.)
5. Verify:
   ```
   ares-device --list
   ```
   Should show both `emulator` and (if you've paired one) `tv`.

## Iteration loop

```
npm run dev:emu
```

One-shot: builds the dev bundle → packages → installs to the
`emulator` device → launches → opens Chrome devtools attached to the
emulator's webview. The devtools URL prints in the terminal; copy
to a real Chrome / Edge window to inspect / set breakpoints / view
network panel.

If you only want one step:
- `npm run install-emu` — package + install only
- `npm run launch-emu` — start the installed app on the emulator
- `npm run inspect-emu` — open devtools for the running app

## Notes

- The emulator advertises codecs it doesn't actually decode (HLS in
  particular). Fine for layout and API-surface testing; **final
  playback validation needs a real LG TV.**
- Emulator has desktop-class RAM. Real webOS 5/6 TVs have ~1.5 GB
  free for apps — memory-pressure bugs (DOM leaks, large-list
  rendering) only surface on real hardware.
- D-pad / colour buttons / Back / Home are mapped to the emulator
  toolbar's virtual remote. The keyboard arrow keys also work as
  D-pad shortcuts.
- HMR over `npm run dev` (the regular Vite server) won't reach the
  emulator's webview directly. The `:emu` flow is package-based,
  so each iteration is a full reinstall. For tighter loops, point
  a regular desktop Chrome at `http://localhost:5174` and only fall
  to the emulator for focus / platform-API testing.

## Common emulator gotchas

- **"Connection refused" from `ares-install`** — emulator VM isn't
  running. Start it from the SDK's Emulator Launcher.
- **Wrong webOS version in emulator** — re-run the Emulator Launcher
  and pick the version matching your app's minimum. Different webOS
  versions ship different Chromium engines; a bug that repros on
  webOS 4 may not repro on 6.
- **App doesn't appear after install** — check the LG home screen's
  "Recent Apps" or scroll the apps row; the launcher caches a few
  seconds. Or just run `npm run launch-emu` to bypass the row.
