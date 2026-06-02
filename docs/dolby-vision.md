# Dolby Vision — handling strategy + decision

**Status:** Decision proposed. Triggered by 2 DV titles in the QA 4K library
(Hellraiser, The Sound of Music) failing to play in-browser. Research: deep
multi-source pass, 2026-06-02 (19 verified claims; sources cited inline).

## TL;DR / the decision

**Should OnScreen support Dolby Vision? — Support DV *content* (tonemap it so it
plays correctly everywhere); do NOT build DV *passthrough*.**

DV splits into two very different commitments:
1. **Make DV titles play correctly** (tonemap DV → SDR/HDR10). Bounded effort,
   high value — the titles currently don't play at all. **Recommended.**
2. **Deliver the DV *experience*** (passthrough the DV bitstream + per-scene RPU
   to DV-capable displays). Large, ongoing complexity (per-client DV matrix,
   Profile-7 dual-layer demux via `dovi_tool`, RPU preservation) for a tiny
   footprint, and **OnScreen's clients mostly can't direct-play DV anyway**
   (browsers/MSE never can). **Not worth it now — explicitly out of scope.**

So: treat DV as "a harder HDR variant we tonemap," using **libplacebo** for
correct colors. This is the same posture as our HDR10 tonemapping, just with the
DV-aware filter. **Caveat that gates everything:** this requires our ffmpeg build
to expose `libplacebo` with `apply_dolbyvision` + Profile-5 reshaping — verify
before committing (see Open items).

## Why DV is different — the one axis that matters

A DV file is easy or hard based entirely on its **BL signal cross-compatibility
ID** (`dv_bl_signal_compatibility_id`) — whether it carries a standards-compliant
base layer a non-DV decoder can use:

| Profile | compat ID | Base layer | Layers | Real-world source |
|---|---|---|---|---|
| **5** (dvhe.05) | **0** | **none — proprietary IPTPQc2/IPT** | single | Netflix/Apple TV+/streaming, re-encodes |
| 8.1 | 1 | HDR10 (PQ/BT.2020) | single | encodes, hybrid remuxes/WEB-DL |
| 8.2 | 2 | SDR (BT.709) | single | — |
| 8.4 | 3/4 | HLG | single | — |
| 7 (dvhe.07) | 6 | HDR10 (PQ/BT.2020) | **dual** (BL+EL+RPU, 12-bit) | UHD Blu-ray remuxes |

*Sources: Dolby "Dolby Vision Profiles and levels" V1.2.92 spec (cross-compat ID
table p.7–9); en.wikipedia.org/wiki/Dolby_Vision; trash-guides.info.*

- **Profile 8.1 / 8.4 / 7** carry a standards base → a non-DV decoder plays the
  base directly, and the base can be tonemapped with **standard HDR10 pipelines**.
- **Profile 5** carries **no standard base** — the color is in proprietary
  IPTPQc2. **Naive HDR10/SDR tonemapping → the notorious green/purple/washed
  cast.** It requires **RPU-aware** conversion. *Our two titles are Profile 5.*
- DV vs HDR10/HDR10+/HLG: DV (and HDR10+) carry **dynamic, per-scene** metadata
  (DV's is the **RPU**); HDR10 is static; HLG is metadata-free.

## Detection (scan time)

Read the **DOVI configuration record** and persist it:
- `ffprobe -show_frames`/`-show_streams` exposes `dv_profile`, `dv_level`,
  `dv_bl_signal_compatibility_id`, and BL/EL/RPU present flags.
- MediaInfo shows the DV profile (since v17.12; explicit profile since v24.01).

The `dv_bl_signal_compatibility_id` is the **dispatch key**: `0` → Profile-5
(hard, needs RPU reshape); `1/2/3/4/6` → has a base to tonemap directly.

## Correct tonemapping — libplacebo, not tonemap_cuda

- **libplacebo `apply_dolbyvision=true`** reads + **reshapes the DV RPU**
  (Profile 5 and 8.x, single-layer / "BL only"), always outputs **BT.2020+PQ**;
  then standard tonemap → correct SDR/HDR10. Runs on GPU. *This is the modern,
  correct path.* (libplacebo README; FFmpeg `vf_libplacebo` docs.)
- **`tonemap_cuda` / ffmpeg-native alone** drops the RPU and **cannot fix
  Profile 5** → green/IPT result. (This is what OnScreen's HDR path uses today.)
- **Profile 7** (dual-layer): ffmpeg **skips the enhancement layer** ("Skipping
  NAL unit 62…") → must demux/convert with **`dovi_tool`** first. (We have no
  Profile-7 content; not a priority.)
- Tonemap-order correctness (FFmpeg `tonemap` docs): linearize PQ → tonemap in
  linear light → gamut-map BT.2020→709 **after** tonemap (or `desat=0`). *Two
  popular "fixes" were refuted by the research and must NOT be used: explaining
  the washed look via `desat=2.0`, and detecting HDR10 fallback via ST-2086/CLL
  presence.*

Approach trade-offs:
| Approach | Profiles | Quality | Complexity |
|---|---|---|---|
| **libplacebo `apply_dolbyvision`** | 5, 8.x (single-layer) | correct, GPU | low — one filter |
| dovi_tool extract→convert→inject | all incl. P7 | correct | high — extra pass, EL video lost |
| ffmpeg-native / tonemap_cuda | none (drops RPU) | **wrong for P5** | low |

## The OnScreen plan (if we adopt option 1 — tonemap-only)

1. **Scan:** persist `dv_profile` + `dv_bl_signal_compatibility_id` + RPU flag.
2. **Per client:** browsers/MSE/hls.js **never** direct-play DV → always tonemap.
   (We are *not* adding DV passthrough, so this is uniform: DV → tonemap for all.)
3. **Tonemap method by profile:**
   - compat ID `1/2/3/4/6` (8.1/8.4/7) → tonemap the standards base
     (`apply_dolbyvision=false`).
   - compat ID `0` (Profile 5) → **`libplacebo apply_dolbyvision=true`** →
     BT.709 SDR or HDR10 by the client's HDR capability.
4. Route DV through **libplacebo** instead of `tonemap_cuda`; keep `tonemap_cuda`
   for plain HDR10/HLG.

## The current bug (the 2 Profile-5 titles) — two layers

1. **Won't play at all** → the segment's PTS starts at ~10.08s but the playlist
   says segment 0 / `start_offset_sec=0`, so hls.js can't sync (`currentTime`
   stuck at 0). Fix: normalize output PTS to 0 (`setpts`/`asetpts=PTS-STARTPTS`),
   independent of DV.
2. **Even once it plays, colors are likely wrong** → Profile 5 tonemapped via
   `tonemap_cuda` (no RPU reshape) → green/IPT. Fix: route DV through libplacebo.

## Open items (gate the plan)

- **Does our ffmpeg/libplacebo build expose `apply_dolbyvision` with Profile-5
  reshaping?** Check: `ffmpeg -h filter=libplacebo` (look for `apply_dolbyvision`)
  on the QA/GPU image. If absent → rebuild needed, which raises the cost of even
  option 1 (re-weigh vs "just fix PTS, accept imperfect colors for 2 titles").
- Licensing note: tonemapping DV (stripping it) is what other OSS servers
  (Jellyfin/Emby via libplacebo/dovi_tool) do; passthrough would push DV decode
  onto the licensed client. We're choosing tonemap-only, so no server-side DV
  decode-license question beyond what ffmpeg/libplacebo already ship.
- (Deferred — only if we ever reconsider passthrough) per-client DV-over-HLS
  matrix for ExoPlayer/Tizen/webOS/Roku; libplacebo vs tonemap_cuda GPU perf.

## Sources
Dolby "Dolby Vision Profiles and levels" V1.2.92; en.wikipedia.org/wiki/Dolby_Vision;
trash-guides.info; github.com/quietvoid/dovi_tool (README); github.com/haasn/libplacebo
(README) + FFmpeg `vf_libplacebo`/`tonemap` docs; mediaarea.net/MediaInfo/ChangeLog;
jellyfin issues #10468 / android-tv #5073; OSMC Vero-V DV notes.
