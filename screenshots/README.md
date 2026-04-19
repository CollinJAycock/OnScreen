# Screenshots

These images are referenced from the top-level `README.md` and the landing page.
Names and aspect ratios are fixed — replace the placeholders at these exact paths.

| File | What to capture | Aspect | Size target |
|------|----------------|--------|-------------|
| `hero.png` | Hub page in dark theme, with artwork populated. First image readers see — should feel alive, not empty. | 16:9 | 1600×900 |
| `watch-desktop.png` | `/watch/[id]` page mid-playback, controls visible, subtitle enabled, chapter marker on the progress bar. | 16:9 | 1600×900 |
| `watch-mobile.png` | Same page on a phone (or DevTools mobile view), fullscreen landscape with bottom-sheet menu open. | 19:9 | 780×1690 |
| `library.png` | Library grid for a movie library, 4K Movies section ideally, hover state on one card if possible. | 16:9 | 1600×900 |
| `admin-transcode.png` | Admin → Transcode settings tab, showing encoder tuning + a live session or two. | 16:9 | 1600×900 |
| `theme-compare.png` | Side-by-side of the same hub page in dark and light themes. Can be a 2-up Photoshop comp. | 2:1 | 1600×800 |
| `android-tv.png` | Photo or screen grab of the Leanback browse row on an actual TV (or emulator). | 16:9 | 1600×900 |
| `demo.gif` | 10–15 sec loop: hub → click a show → click an episode → player starts. Keep under 8 MB. | 16:9 | 1200×675 |

## Capture tips

- Use a real (non-blank) library. Empty-state screenshots look dead.
- Dark theme for the hero; it's more striking.
- Hide the dev-tools / tab bar in browser screenshots (use Firefox screenshot tool or Chrome's "capture full page").
- For mobile, use DevTools device toolbar at 390×844 (iPhone 14 Pro) — render at 2× for retina.
- Run through `pngquant --quality 65-85` before committing to keep the repo lean.
- For `demo.gif`, record with ScreenToGif / Kap, then compress with gifsicle: `gifsicle -O3 --lossy=80 -o demo.gif input.gif`.
