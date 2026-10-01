# Icons

Generated with `cargo tauri icon` from the 1024×1024 OnScreen master,
`docs/store-assets/master/icon-1024.png` (the same artwork as the web
favicon and the Android / TV launcher icons; it's the JPEG that
`favicon.svg` embeds, decoded to PNG).

| File | Size | Used for |
|---|---|---|
| `128x128.png` | 128×128 | Listed first in `bundle.icon`: on Linux, Tauri embeds the first PNG as the window and tray icon. Also `hicolor/128x128/apps`. |
| `32x32.png` | 32×32 | Linux `hicolor/32x32/apps` |
| `64x64.png` | 64×64 | Linux `hicolor/64x64/apps` |
| `256x256.png` | 256×256 | Linux `hicolor/256x256/apps` (`cargo tauri icon` writes it as `128x128@2x.png`; renamed so the bundler doesn't put it in the scale-2 `256x256@2` directory) |
| `icon.png` | 512×512 | Linux `hicolor/512x512/apps`; the largest square PNG, so also the AppImage's own icon |
| `icon.ico` | 16, 24, 32, 48, 64, 256 | Windows: the .exe, MSI and NSIS installers, shortcuts, window and tray |
| `icon.icns` | 16 to 1024 | macOS bundle icon (no macOS build yet) |

The Linux bundler names each hicolor directory after the PNG's pixel size
and adds `@2` when the file name contains `@2x`, so keep these names.

To regenerate, from `clients/desktop/src-tauri`:

```bash
cargo tauri icon ../../../docs/store-assets/master/icon-1024.png -o /tmp/onscreen-icons
cd icons
cp /tmp/onscreen-icons/{32x32,64x64,128x128,icon}.png /tmp/onscreen-icons/icon.{ico,icns} .
cp /tmp/onscreen-icons/128x128@2x.png 256x256.png
```

`cargo tauri icon` also writes Windows Store, Android and iOS sets; this
client doesn't use them, so they aren't kept. Give it the PNG, not
`favicon.svg`: the SVG path rasterises the embedded JPEG without
smoothing, and the 32 and 64 px icons come out speckled.
