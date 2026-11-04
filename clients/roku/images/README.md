# Channel Art

Roku rejects a channel bundle if any image referenced in `manifest`
is missing or the wrong dimensions. The scaffold ships without
binary art so the repo stays text-only — drop real PNG/JPG files
at the paths below before sideloading.

| File | Dimensions | Format | Manifest key |
|---|---|---|---|
| `icon_focus_fhd.png` | 540 × 405 | PNG | `mm_icon_focus_fhd` (also the store's app poster) |
| `icon_focus_hd.png` | 290 × 218 | PNG | `mm_icon_focus_hd` |
| `icon_focus_sd.png` | 246 × 140 | PNG | `mm_icon_focus_sd` |
| `splash_sd.jpg` | 720 × 480 | JPEG | `splash_screen_sd` (the FHD splash, letterboxed) |
| `splash_hd.jpg` | 1280 × 720 | JPEG | `splash_screen_hd` |
| `splash_fhd.jpg` | 1920 × 1080 | JPEG | `splash_screen_fhd` |

Optional but recommended for the Roku Channel Store submission:

| File | Dimensions | Format | Notes |
|---|---|---|---|
| `icon_side_hd.png` | 108 × 69 | PNG | Side-bar icon shown alongside other channel rows |
| `splash_uhd.jpg` | 3840 × 2160 | JPEG | 4K splash for UHD Roku models |

The art here is generated from `docs/store-assets/master/icon-1024.png`
on black (the manifest's `splash_color` is `#000000`, so the splash has no
seam): the tiles show the art with "OnScreen" under it, the splashes are
the brand banner (art, title and "Engage. All of your media.").
