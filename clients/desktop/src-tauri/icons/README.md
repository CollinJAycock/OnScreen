# Icons

These are placeholder copies of the existing OnScreen web favicons
so the Tauri scaffold builds end-to-end without missing-asset
errors. They render fine but aren't sized for native taskbar /
dock conventions.

**Before any release build** swap them for proper raster +
platform-native icons:

| File | Size | Notes |
|---|---|---|
| `32x32.png` | 32×32 | Linux notification area, Windows tray |
| `128x128.png` | 128×128 | Linux app drawer |
| `128x128@2x.png` | 256×256 | macOS Retina, high-DPI Linux |
| `icon.ico` | multi-resolution | Windows installer + .exe icon |
| `icon.icns` (TODO) | multi-resolution | macOS bundle icon |

`cargo tauri icon path/to/source.png` regenerates the entire set
from a single 1024×1024+ master. Add `icon.icns` to
`tauri.conf.json#bundle.icon` once the .icns is generated for the
first macOS build.
