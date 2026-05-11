# Tizen emulator dev loop

Samsung ships an official Tizen TV emulator with **Tizen Studio**.
The emulator runs a real Tizen image with the same WebKit-based
webview production Samsung TVs use, so most layout / focus / API
bugs surface without a real TV.

## One-time setup

1. Install **Tizen Studio** from
   https://developer.samsung.com/smarttv/develop/tools/tizen-studio.html
   Pick **Tizen Studio with IDE** for the GUI Device Manager; the
   command-line-only variant works too if you prefer pure CLI.
2. Open Tizen Studio's **Package Manager** → install:
   - "Extras → Samsung Certificate Extension" (needed to sign WGTs)
   - "TV Extensions → Tizen 6.5 TV / 7.0 TV emulator images"
3. Open **Device Manager** → **Create Emulator** → pick a TV image
   (Tizen 7.0 is a reasonable default — Chromium-based WebKit) →
   screen profile FHD 1920×1080 → finish.
4. **Launch** the emulator from Device Manager. Wait for the home
   screen to appear.
5. Verify the emulator is reachable via sdb:
   ```
   sdb devices
   ```
   Should list something like `emulator-26101  device`. If the port
   differs (multiple emulators running, etc.), set `TIZEN_DEVICE` in
   the env to the real name before running the npm scripts.
6. Make sure `tizen` and `sdb` are on PATH. They live under
   `~/tizen-studio/tools/` on macOS / Linux and
   `C:\tizen-studio\tools\` on Windows. The Tizen Studio installer
   usually adds them automatically; if not, add manually.

## Iteration loop

```
npm run dev:emu
```

One-shot: builds + packages + installs to the running emulator +
launches the app on it. Console output from the launched app surfaces
in the terminal via Tizen's logger; for full devtools open Chrome at
`http://<emulator-ip>:7011` (Tizen emulator exposes the webview
inspector here) while the app runs.

Individual steps:
- `npm run install-emu` — package + sideload to emulator only
- `npm run launch-emu` — start the installed app on the emulator

If you need to target a specific emulator instance (multiple running,
or a non-default sdb port), set the env var:
```
TIZEN_DEVICE=emulator-26103 npm run install-tv
```
(The `install-tv` / `launch-tv` scripts honour `TIZEN_DEVICE` the same
way — emulator names are just sdb device IDs.)

## Notes

- The emulator advertises HLS support that doesn't fully work; **real
  Tizen TVs are still required for playback validation**.
- Tizen emulator boots slowly (1-2 min) compared to webOS. Leave it
  running between iterations.
- D-pad navigation: arrow keys on your keyboard. Back / Home are on
  the emulator toolbar. The Tizen virtual remote pane (View → Remote
  Control) gives you the colour buttons + Smart Hub button.
- Tizen Studio's "Author certificate" expires; if a sideload starts
  failing with "Invalid author signature" months from now, re-issue
  the cert in Certificate Manager.

## Common emulator gotchas

- **`tizen install` errors with "package not signed"** — the
  build-output WGT wasn't signed because the cert profile expired
  or wasn't selected. Open Certificate Manager and apply the
  active profile.
- **Emulator stuck on boot** — Tizen images are picky about KVM /
  Hyper-V conflicts. On Windows, disable Hyper-V for the emulator
  user, or use the slower software-rendering toggle in Device
  Manager.
- **sdb can't see the emulator** — `sdb kill-server && sdb start-server`
  resets the daemon; the next `sdb devices` should re-detect.
