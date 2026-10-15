# Client Capability Profiles — Declarative Playback Negotiation (Design)

**Status:** Proposed · **Builds on:** ADR-016 (decision order), ADR-030 (HDR
forces transcode) · **Supersedes:** the ad-hoc `supports_hevc`/`supports_av1`
booleans on the transcode-start request.

## 1. Why

The 7.1-AAC bug was the symptom: the server transcoded 7.1 sources to
8-channel AAC, which browsers can't decode, so **120 of 175 4K titles were
unplayable in-browser**. We fixed it with a server-side 5.1 cap (commit
`21a4a92`) — a correct *default*, but a band-aid over the real shape problem:

**Playback decisions are driven by 2–3 ad-hoc booleans + hardcoded assumptions,
not by what the client can actually play.** The transcode-start request carries
`supports_hevc`, `supports_av1`, and an unused-in-the-decision
`max_audio_channels`; everything else (audio codecs, containers, HDR, bit-depth,
channel limits) is guessed. Each client re-implements its own decision logic and
sends a different, partial subset of flags (web: 2 bools; Android: 2 hardcoded
bools; Tizen: 1 bool). New gaps will keep surfacing the same way.

Meanwhile **a real capability model already exists in the tree but is wired to
nothing:**

- `internal/transcode/decision.go` — `Decide(file, caps, serverCaps) Decision`
  returns DirectPlay / DirectStream / Transcode and already checks video codec,
  audio codec, container, HDR (+ Dolby Vision distinctly), video bit-depth, and
  resolution against a `ClientCapabilities`. Has tests (`decision_test.go`).
- `internal/transcode/capability.go` — `ClientCapabilities` struct +
  `ParseCapabilities()` for an `X-Client-Capabilities` header
  (`videoDecoder=h264:h265,audioDecoder=ac3:aac,maxWidth=...,maxAudioChannels=...`).
- **But:** the transcode-start handler (`internal/api/v1/transcode.go`) never
  calls `Decide` or `ParseCapabilities`. It reads the booleans and builds targets
  itself. `Decide` and `ParseCapabilities` are effectively dead code.
- And `Decide` itself has the **same audio blind spot**: it never checks
  `MaxAudioChannels` (or audio bitrate), which is exactly why 7.1 slipped through.

So this isn't a green-field build. It's: **adopt the model that's already here,
fill its audio gap, feed it a real per-client profile, and delete the booleans.**

## 2. Current state (grounded)

| Concern | Today | Where |
|---|---|---|
| Play decision | Each **client** decides direct-play vs transcode; server just executes | web `playback-decision.ts`; Android `PlaybackHelper.kt`; etc. |
| Server decision fn | `Decide()` exists, handles video/container/HDR/DV/bitdepth/res | `decision.go:40` |
| Audio in decision | **Not checked** — codec & channels ignored by `Decide` | `decision.go:56` (codec only via caps list), no channel check |
| Capability transport | `X-Client-Capabilities` header parser exists, **unused** | `capability.go:30` |
| Transcode-start req | `supports_hevc`, `supports_av1`, `max_audio_channels` (downmix only), `height`, `video_copy`, `audio_stream_index` | `transcode.go:222` |
| Target selection | Handler picks output codec/res/channels itself from booleans | `transcode.go:571–634` |
| Audio channel cap | server hard cap `maxAACChannels=6` (the band-aid) | `audio.go:7` |
| What clients send | web: hevc/av1 bools; Android: 2 hardcoded bools; Tizen: hevc bool only | `api.ts:2161`, `TranscodeRequest.kt:28`, `endpoints.ts:141` |

**Gaps:** audio codec/channel support never negotiated; codec profile/level never
considered; container preference only assumed; HDR support inferred client-side
not declared; no shared profile; clients diverge.

## 3. Goals / non-goals

**Goals**
1. One declarative **capability profile** per client — "here's what I can play."
2. The **server** owns the decision (direct-play / remux / transcode) *and* the
   transcode **targets** (output codec, resolution, audio codec + channels),
   computed from the profile via the existing `Decide()` + a target step.
3. Fix the *class* of bug (7.1 AAC and future codec gaps) declaratively: the
   server never emits a stream the client didn't say it can play.
4. Retire `supports_hevc`/`supports_av1` (kept only as a back-compat shim).
5. Conservative **safe defaults** when no profile arrives (h264/aac, 1080p, 5.1,
   8-bit, mp4/ts) — so a profile-less client still plays.

**Non-goals (for this pass)**
- Audio **passthrough/bitstreaming** (TrueHD/DTS-HD/Atmos → receiver). That's the
  real home of 7.1+ surround; the profile is *designed to grow into it* (§5) but
  the transcode fallback stays ≤5.1 AAC.
- Full DLNA/Plex-parity profiles. Start minimal and extensible.

## 4. End-state architecture: server decides, clients declare

Move the decision **server-side**, where `Decide()` already lives, and reduce
each client to "probe my platform → declare a profile." This kills the per-client
decision divergence (three different `playback-decision` implementations today).

```
client (probe platform) ──profile──▶ server
                                       │  Decide(file, caps) ─▶ directPlay | directStream | transcode
                                       │  if transcode: pickTargets(file, caps) ─▶ {vcodec,w,h,vbitrate, acodec,channels,abitrate}
                                       ▼
                                   stream / remux / transcode-to-targets
```

The client stops choosing codecs/resolutions; it only reports capability and
plays whatever URL the server returns.

## 5. The capability profile

Internal type stays `transcode.ClientCapabilities` (already the `Decide` input).
Extend it minimally and feed it from the client. Conceptual shape:

```jsonc
{
  "video": {
    "codecs": [ {"codec":"h264","maxLevel":"5.1"}, {"codec":"hevc","maxLevel":"5.1","bitDepth":10} ],
    "maxWidth": 3840, "maxHeight": 2160, "maxBitrateKbps": 120000,
    "hdr": {"hdr10": true, "hlg": true, "dolbyVision": false}
  },
  "audio": {
    "codecs": [ {"codec":"aac","maxChannels":6}, {"codec":"ac3","maxChannels":6}, {"codec":"opus","maxChannels":2} ],
    "maxChannels": 6
  },
  "containers": ["mp4","hls-fmp4","hls-ts"],
  // future:
  "audioPassthrough": ["eac3","truehd","dts"]
}
```

**What to add to `ClientCapabilities` now:** per-audio-codec `maxChannels` (or at
least honor the existing global `MaxAudioChannels` in `Decide` + target
selection). Video profile/level is optional and can come later — bit-depth +
resolution already cover the common decode failures.

**Transport — recommendation: the `X-Client-Capabilities` header.**
- Reuse the existing `ParseCapabilities()`; one parse path for *every* request
  (GET segment/playlist/direct-stream included, which have no body).
- Extend its grammar for audio channels-per-codec and HDR/DV flags (it already
  has `maxAudioChannels`, `maxbitdepth`, `videoDecoder`, `audioDecoder`,
  `protocols`, `maxWidth/Height`).
- A JSON body field on transcode-start is richer but only works for that POST;
  the header is uniform. (Open question §9.)

### 5.1 Header grammar (as implemented)

`X-Client-Capabilities` is a list of `key=value` pairs separated by `,` or `&`;
list values are separated by `:`. Keys are case-insensitive; unknown keys are
ignored. A boolean is "yes" for `true` or any positive integer (`1`) and "no"
for anything else (`0`, `false`).

```
videoDecoder=h264:h265:vp9,audioDecoder=aac:ac3,protocols=mp4:mkv,maxWidth=3840,maxHeight=2160,maxAudioChannels=8,maxbitdepth=10,vp9MaxBitDepth=8,hdr=1,hlg=0
```

| Key | Value | When absent | Drives |
|---|---|---|---|
| `videoDecoder` | codecs (`h264`, `h265`/`hevc`, `av1`, `vp9`, …) | `h264` | video codec check |
| `audioDecoder` | codecs (`aac`, `ac3`, `eac3`, `dts`, …) | `aac` | audio codec check |
| `protocols` | containers (`mp4`, `mkv`, `ts`, …) | `mkv`/`mp4`/`mov` assumed | container check |
| `maxWidth` / `maxHeight` | pixels | 1920 / 1080 | resolution check + transcode output size |
| `maxAudioChannels` | channel count | 6 (5.1) | channel check + downmix target |
| `maxbitdepth` (alias `videobitdepth`) | 8 / 10 / 12 | 8 | HEVC depth check + 8-bit transcode output |
| `vp9MaxBitDepth` | 8 / 10 / 12 | not declared — VP9 isn't depth-gated | VP9 depth check (below) |
| `hdr` | boolean | no | HDR10 / HDR10+, and HLG unless `hlg` is sent |
| `hlg` | boolean | not declared — `hdr` decides HLG | HLG check (below) |
| `dovi` (alias `dolbyvision`) | boolean | no | Dolby Vision (else `unsupported`) |
| `optimized` | boolean | no | the client plays an `optimized_version` from the decision (§13) |
| `audioPassthrough` | codecs bitstreamed to a receiver: `ac3`, `eac3`, `truehd`, `dts` (`dts-hd` folds to `dts`; others ignored) | none | channel cap lifted for these + remux copies them (below) |

**Audio passthrough (`audioPassthrough`).** A client that hands a codec to an
AV receiver undecoded (HDMI/S/PDIF bitstreaming: Android TV's
`AudioCapabilities`) lists it here. The receiver decodes it, so:
- **Decide** treats the codec as playable whatever `maxAudioChannels` says
  (and whether or not `audioDecoder` lists it): a 7.1 TrueHD track on a
  `maxAudioChannels=6` client direct-plays from MKV/MP4 instead of
  remuxing to a 5.1 AAC downmix. The container rule stays: TrueHD in MPEG-TS
  is still a remux, because no player demuxes it there.
- **Remux** (`video_copy`) copies the track instead of converting it to AAC.
  AC-3/E-AC-3 copy into the usual segments. TrueHD and DTS can't ride MPEG-TS
  to a player, so their copy is packaged as **fMP4** (`.m4s` + `init.mp4`, even
  for H.264 video) with ffmpeg's `-strict -2` (its mp4 muxer calls TrueHD
  experimental). That needs a worker that advertises the `audio_passthrough`
  feature; on an all-old fleet, for a client that asked for TS segments, or for
  an audio-only file, TrueHD/DTS fall back to the AAC conversion.
- **Transcode** (video re-encoded) is unchanged: audio is AAC, as the ABR
  ladder's CODECS string advertises.

It's opt-in on purpose: `audioDecoder` alone never widens the copy, since a
client may list a codec there it can demux but not output (the phone lists
`dts`). Item details give each audio stream ffprobe's `profile` ("DTS-HD MA",
"Dolby TrueHD + Dolby Atmos", "Dolby Digital Plus + Dolby Atmos") for the
track label, on files scanned (or re-probed) since it was stored.

**HLG (`hlg`).** An HDR10 display isn't necessarily an HLG one: NVIDIA SHIELD
outputs HDR10 but sends HLG flagged as SDR (dark, green picture). So for an HLG
source (`hdr_type = hlg`):
- `hlg=1` — direct play, or remux with the HLG signal intact, whatever `hdr` says.
- `hlg=0` — `transcode`, tone-mapped to SDR: exactly what an HDR source gets on
  an `hdr=0` client (every HDR transcode tone-maps). Remux is not offered, since
  it would carry the HLG signal through.
- no `hlg` key — `hdr` decides, as it did for every client before the key
  existed (old TV builds, web, Tizen, webOS, Roku are unchanged).

`hlg` never affects HDR10, HDR10+ or Dolby Vision sources.

**10-bit VP9 (`vp9MaxBitDepth`).** VP9 Profile 2 (10-bit) is its own decoder
profile — SHIELD hardware-decodes 8-bit VP9 but not Profile 2 — so VP9 gets its
own depth ceiling rather than a yes/no token: it mirrors `maxbitdepth`, and it
also covers 12-bit Profile 3. For a VP9 source of known depth ≥ 10:
- depth ≤ `vp9MaxBitDepth` (e.g. `vp9MaxBitDepth=10` for Profile 2) — direct play.
- depth > `vp9MaxBitDepth` (e.g. `vp9MaxBitDepth=8`) — `transcode` to the usual
  output codec (H.264/HEVC/AV1; VP9 is never an output), whose depth follows
  `maxbitdepth`.
- no `vp9MaxBitDepth` key — not gated: 10-bit VP9 follows VP9 support, the
  pre-existing assumption.

`maxbitdepth` does not apply to VP9, and `vp9MaxBitDepth` applies to nothing
else. 8-bit VP9 and VP9 of unknown depth are never gated.

Both rules also show on the admin Now Playing cards ("HLG not supported by client
(tonemapped to SDR)", "10-bit VP9 → client max 8-bit").

## 6. Server-side refactor

1. **Middleware/helper**: parse `X-Client-Capabilities` once per request into
   `ClientCapabilities` (fall back to safe defaults — already the
   `ParseCapabilities` behavior: 1080p/8-bit, but **bump the channel default to
   6** to match the current cap, and default audio/video codec lists to
   `{h264,aac}` so a blank profile transcodes safely).
2. **Decide() gains an audio-channel + audio-codec-target awareness.** The
   *decision* (direct-play vs transcode) already covers audio *codec*; add: if the
   source audio exceeds `caps.MaxAudioChannels` (or the matched codec's
   maxChannels) and can't passthrough → it's a transcode (which it already is for
   AAC), and the **target** channel count is `min(source, caps cap)`.
3. **New `pickTargets(file, caps, serverCaps)`** centralizes what
   `transcode.go:571–634` does today: choose output video codec (first of
   `{hevc,av1,h264}` the client supports, honoring 4K-HEVC preference), resolution
   (`min(requested, source, caps.MaxWidth/Height, serverCaps)`), audio codec
   (prefer a client-supported passthrough/efficient codec, else AAC) and **channel
   count (`TargetAudioChannels(source, caps.MaxAudioChannels)`)**. This is where
   the 7.1→5.1 decision becomes *data-driven* rather than a hard constant.
4. **`maxAACChannels=6` stays** as the absolute ceiling for the AAC *fallback*
   (defense-in-depth), but the normal path is now `caps.MaxAudioChannels`.
5. **Retire the booleans**: `supports_hevc`/`supports_av1` become a thin shim that
   synthesizes a minimal profile when no header is present (back-compat for
   un-migrated clients), logged as deprecated.

## 7. Client integration (declare, don't decide)

Each client builds its profile from a real platform probe and sends the header.
Delete its bespoke decision logic once the server decides.

- **Web** (`web/src/lib/playback-decision.ts` already has `detectClientCaps()`
  via `MediaSource.isTypeSupported()`): extend it to enumerate audio codecs +
  channel limits (browsers: ≤5.1 AAC, stereo Opus) and emit the header. This is
  the highest-value first client — it's where the bug lived.
- **Android** (`PlaybackHelper.kt`): replace the hardcoded `true/true` with a real
  `MediaCodecList` probe (decodable codecs, max instances, channel counts);
  ExoPlayer exposes this.
- **Tizen** (`endpoints.ts`): probe via `tizen.systeminfo` / AVPlay; today it sends
  only `supports_hevc`.
- **Roku**: declare from the model's known decode matrix.

## 8. Phased rollout

- **Phase 0 — done.** Server 5.1 AAC cap (`21a4a92`) stops the bleeding for every
  client immediately.
- **Phase 1 — target selection from caps (server).** Parse the header into
  `ClientCapabilities`; route `pickTargets()` through it; honor `MaxAudioChannels`
  + codec lists for transcode *targets*. Clients keep deciding direct-play vs
  transcode but now send the header. Fixes the bug *class* declaratively. Low risk,
  back-compatible (defaults when header absent).
- **Phase 2 — centralize the decision (server).** Wire `Decide()` into a
  `/playback/decision` (or fold into transcode-start), so the server returns
  direct-play vs transcode and the clients drop their decision code. One tested
  decision path instead of N.
- **Phase 3 — passthrough + richer profile.** Add audio passthrough capability
  (true 7.1/Atmos via direct stream), video profile/level, per-codec bitrate.

## 9. Open questions (need a call)

1. **Transport:** extend the `X-Client-Capabilities` header (uniform, reuses the
   parser) vs a JSON profile body on transcode-start (richer, POST-only) vs a
   persisted per-device profile keyed by device id. *Recommendation: header now,
   JSON profile only if the grammar gets unwieldy.*
2. **Decision ownership:** commit to moving decisions fully server-side (Phase 2)
   or keep clients deciding and only centralize *targets* (Phase 1)? Phase 1 alone
   fixes the bug class; Phase 2 is the bigger architectural win the user is after.
3. **Profile freshness:** per-request header (simple, stateless) vs stored device
   profile (enables analytics + "why did this transcode" debugging).
4. **Default channel cap when no profile:** 5.1 (preserve surround where it works)
   vs stereo (safest). Current code: 5.1.

## 10. Testing

- `decision_test.go` already covers the decision matrix — add audio-channel +
  audio-codec-cap cases.
- A `pickTargets` unit table (source × profile → expected targets), incl. the 7.1
  cases that broke (7.1 source + 5.1-cap profile → 6ch).
- The `web/tests/e2e/4k-sweep.spec.ts` browser sweep is the integration backstop:
  with real profiles flowing, the whole 4K library should DirectPlay/transcode to
  playable output with no 7.1-AAC class failures.

## 11. Implementation status (2026-06-02)

Everything below is **back-compat and behavior-equivalent** — no client sees a
change until the final wiring step lands, so it's all safe to deploy as-is.

**Done — Phase 1 (server reads the profile for targets):**
- `5024767` — transcode-start handler resolves `transcode.ClientCapabilities`
  from the `X-Client-Capabilities` header via `clientCaps()` (legacy
  supports_*/max_audio_channels fold in), and drives the audio-channel count +
  HEVC/AV1 output preference (incl. the ABR ladder) from it. Logs the resolved
  caps + echoes an `X-OnScreen-Client-Caps` debug response header. No-profile
  audio default = 5.1.
- `9c6143c` — web client builds the profile from `detectClientCaps()`
  (`clientCapabilitiesHeader()`) and sends it on every request; CORS allows the
  request header + exposes the debug header.

**Done — Phase 2 model (Decide is now complete + ready):**
- `d92cce7` — `Decide()` gained the audio-channel check (7.1 source on a
  5.1/stereo client → Transcode, not DirectPlay/DirectStream) and the
  `X-Client-Capabilities` grammar gained `hdr`/`dovi` flags. `Decide` is fully
  tested but **not yet on the live path** — the handler still decides.

**Done — Phase 2 endpoint + parity prep:**
- `e082b1a` — `POST /items/{id}/playback-decision` runs `Decide` and returns
  `{decision, file_id}`. Additive, read-only; nothing uses it for playback yet.
- `23a24e4` — `Decide` bit-depth check made codec-aware (AV1/VP9 10-bit no longer
  force-transcode), closing a parity gap vs the web's `videoBitDepthOK`.
- `9c121d5` — web **shadow check**: the watch page calls the endpoint for the
  source file and logs `[capability-shadow]` server-vs-local divergences; the
  local decision still drives playback (zero behavior change). The 4K sweep
  harvests these into `4k-shadow-mismatches.json`.

**Done — Phase 2 model reconciliation + flip (validated against local):**
The local endpoint test (logged in, drove `/playback-decision` over the dev
library) surfaced two real gaps the shadow was meant to catch:
- **Decision-model mismatch.** Server `DirectStream` meant "copy both streams"
  and required browser-decodable audio, so the dominant `mkv/h264/{ac3,dts,…}`
  case fell through to a *full transcode* — where the clients stream-copy the
  video and transcode only the audio. Fixed by widening `DirectStream` to the
  video-copy + audio-transcode path (`9be5eb5`); flipping as-was would have
  turned nearly every fast remux into a full re-encode.
- **faststart.** Non-faststart `mov` files returned `directPlay` (server can't
  see faststart — it isn't on `media.File`), which would stall progressive
  playback. Kept as a client-side refinement: a `directPlay` verdict on a
  non-faststart file becomes a remux.
- **Flip (`b5d496a`):** the web watch page now drives `canDirectPlay`/
  `canRemuxVideo` from the server verdict (+ faststart refinement), with local
  fallback when the verdict isn't available. `e082b1a` endpoint, `23a24e4`
  codec-aware bit-depth, no-header h264/aac defaults all in.

**Done — response-envelope fix (`ea91ad4`), the linchpin.** The endpoint used
`respond.JSON` (raw body) instead of `respond.Success` ({"data": …} envelope), so
every client's `ApiResponse<T>` parser failed and *silently fell back to local* —
the web flip included (its validation passed only because local still plays,
masking the bug). Caught on-device: the Android phone logged `server=null` despite
a 200 + 75-byte body with no `data` wrapper. Without this, the entire flip is
inert across all clients.

**Done — all clients migrated.** Every client now sends `X-Client-Capabilities`
(from its real capability source) and — except Roku — consumes
`/playback-decision` with a local fallback:
- Web `b5d496a`; **Android mobile `267b09d` — verified on a Galaxy S24** (logged
  `server=directPlay`, ExoPlayer decoded h264+eac3 natively, no crash); Android
  TV `44fa653` (compile-checked, mirrors mobile); Tizen + webOS `163014f`
  (`svelte-check`; verdict→`video_copy`). webOS's header was corrected to an
  MSE-probe (it's hls.js/MSE, not AVPlay — can't claim HEVC/AC-3 blindly).
- **Roku flip deferred** (`9538076` = header only): `Playback_Decide` runs in the
  render thread where a blocking `Client_PostSync` hangs the UI (needs an async
  decision Task), and Roku's local decision already probes `roDeviceInfo` — the
  most accurate local decision of any client. Lowest value, highest risk; revisit
  with a real Roku.

**Done — `hlg` + `vp9MaxBitDepth` (2026-09-30, server only).** Two explicit
capabilities for NVIDIA SHIELD, which shows HDR10 but not HLG and decodes 8-bit
VP9 but not Profile 2 (rules in §5.1). Both are opt-in: a profile without the
keys decides exactly as before, so nothing changes until a client sends them.
The Android TV client's header should add `hlg=0|1` (from the display's
supported HDR types) and `vp9MaxBitDepth=8|10` (from the VP9 decoder's Profile 2
support).

**Done — the decision is final (2026-10-15).** Every client plays the
server's verdict and decides locally only when it gets none (an older server,
a failed call); the streaming routes hold a client that decided on its own to
the same outcome:
- **Transcode start** runs `Decide` for a `video_copy` request and re-encodes
  when the verdict is transcode (`videoCopyRefused`,
  internal/api/v1/decision_enforce.go). Without a capability profile only the
  viewer's resolution ceiling is held; the bitrate caps already escalated a
  copy.
- **`GET /media/stream/{id}`** (`DirectPlayGate`) refuses a video the decision
  would convert whatever the device: over the internet limit from outside the
  home network or over the viewer's quality profile → 409
  `TRANSCODE_REQUIRED`; damaged → 422 `FILE_DAMAGED`. Capability verdicts
  aren't judged there (the request carries no profile). Audio-only files and
  books are exempt — the web music player streams originals without asking —
  and downloads go through `/media/download`. Each user's caps and the
  internet policy are memoised for 10 s, since every Range request is checked.
- **Roku** plays the verdict both ways (it used to keep its own and accept
  only a demotion), and its header declares `protocols=mp4` only, so the
  server never tells it to direct-play a container its Video node doesn't.
  `ac3`/`eac3` stay declared for passthrough on remux.
- **Web** awaits the verdict (up to 4 s) before starting; **Android TV and
  phone** refuse `damaged` instead of falling back to local, and retry a
  refused direct play once through the server.
- The **TV web apps** (Tizen / webOS / Xbox) still turn `directPlay` into a
  remux: their players are only tested on HLS.

**Done — Phase 3 audio passthrough (2026-10-15, server).** `audioPassthrough`
in the grammar (§5.1): `Decide` lifts the channel cap for a passed-through
codec, and the remux copies it, TrueHD and DTS into fMP4 segments with
`-strict -2` (`ResolveVideoOutput` now honours forced fMP4 on a copy; the
session's `fmp4_segments` tells the playlist handler to wait for `.m4s`). Jobs
that need it go only to workers advertising the `audio_passthrough` feature.
Checked with ffmpeg 8.1: a TrueHD copy writes an `mlpa` track and fails at the
header without `-strict -2`; DTS-HD MA copies as an `mp4a` (object type 0xA9,
DTS) track. Audio item details carry ffprobe's `profile`. Clients still have to
send the key (Android TV first), and Media3's reading of TrueHD (`mlpa`) and
DTS out of fMP4 HLS needs checking on a device with a receiver.

**Remaining:**
1. Device-test on Roku (the verdict is now final there, unit-tested only),
   Fire TV, Tizen and webOS.
2. Phase 3 rest: passthrough on full transcodes (audio is always AAC there,
   ABR ladder included), video profile/level.

Why staged: the flip changes how playback *starts*. Driving the real endpoint
per-client (local dev, then the phone on-device) caught both the `DirectStream`
model gap and the response-envelope bug that a blind cut-over would have shipped.

## 12. Internet streaming limit (2026-10, migration 00046)

The play decision is also where the home-network / internet split is enforced.
`POST /items/{id}/playback-decision` classifies the caller from the **trusted**
client IP (`r.RemoteAddr` after `TrustedRealIP` — the audit-log address, never a
raw forwarded header) with `RemoteStreamPolicy.IsLocal`: the setup gate's
`isLocalAddr` set (loopback, RFC 1918 / ULA, link-local, 100.64.0.0/10 —
Tailscale / CGNAT — and the host's own IPv6 prefixes) plus the admin's
`local_networks`. For a remote client with an internet limit configured (the
user's `max_remote_stream_bitrate_kbps` or the server's
`remote_max_bitrate_kbps`), a `directPlay` / `directStream` verdict whose video
source exceeds `min(user cap, user internet cap, server internet limit)` becomes
`transcode` with `reason: remote_bitrate_limit` (or `user_bitrate_limit` when the
user's own cap is the lower one) and `max_bitrate_kbps`. Transcode start runs the
same computation, so the copy-to-transcode escalation, the output clamp, the ABR
ladder ceiling and the Now Playing reason ("Over the internet streaming limit")
all follow it; the pre-encoded static ladder is skipped when the source is over
the cap, because its rungs aren't clamped. LAN verdicts are unchanged; audio is
never limited.

The viewer's own **quality profile** (`PUT /users/me/quality-profile`, set on
the web Account page) is applied on every network, since it is their choice
rather than a limit on them: its `max_video_height` joins the client's
`maxWidth` / `maxHeight` as a 16:9 box, a video above `max_video_bitrate_kbps`
or an audio-only file above `max_audio_bitrate_kbps` becomes `transcode` with
`reason: quality_profile`, every AAC encode is held to the audio bitrate, and
`preferred_video_codec` picks the transcode's output codec (`h264` turns HEVC /
AV1 output off; `hevc` asks for it wherever the client decodes it).

**Known gap — direct file serving.** `/media/stream/{id}` and `/media/files/*`
do not enforce the limit. A client that ignores the decision (or predates it)
and fetches the file directly gets the original bitrate. Enforcing there was
considered and rejected: refusing the request turns a playing (if over-limit)
stream into a hard failure on exactly the older first-party builds that don't
ask for a decision, and throttling a range-served file stalls playback instead
of lowering quality. Every current first-party client asks for the decision
first. Downloads (`/media/download`) are deliberately not limited either.

## 13. Optimized versions (2026-10, migration 00056)

An admin can have the server encode a file ahead of time into an *optimized
version* — a progressive MP4, H.264 + stereo AAC, faststart, at the Mobile
(720p 4 Mbps), TV (1080p 8 Mbps) or original-resolution profile — with the
converted-download runner (`internal/downloads`). It is an extra version of
the item, never one of its `files`: first-party clients that predate it pick
`files[0]` or the decision's `file_id`, and keep getting the source.

`POST /items/{id}/playback-decision` hands one out only to a client that
declares **`optimized=1`** in `X-Client-Capabilities` — a protocol capability,
not a decode one: "I play `optimized_version.stream_url` when you give me
one". For such a client, when the source's verdict is `transcode` (for any
reason: codec, HDR, resolution, bit depth, or the internet / user bitrate
cap of §12), the server looks at the item's playable versions — made from
that source file, whose content hash still matches the file's — highest
bitrate first, and picks the first that

- `Decide` judges `directPlay` for the client's profile (the version is
  described as an `mp4` file with `h264` 8-bit video, `aac` 2.0 audio and its
  own size and bitrate), and
- is within the request's bitrate cap (the same `min(user cap, user internet
  cap, server internet limit)` §12 computes; on the LAN only the user's cap).

The response is then `decision: directPlay`, `reason: optimized_version`,
`optimized_version: {id, profile, label, stream_url, stream_token, width,
height, bitrate_kbps, …}`, with `file_id` still the source and the source's
own verdict in `source_decision` / `source_reason` — the client's fallback,
e.g. when the viewer picks a non-default audio track the version doesn't
carry. Direct play and remux verdicts are never replaced: they cost the
server nothing worth saving. Without `optimized=1` the response is exactly
what it was before.

The web watch page sends `optimized=1`, keeps judging the source with
`source_decision`, and plays the version's URL where it would otherwise start
a full transcode ("Auto" quality included). `/media/optimized/{id}` applies
`/media/stream`'s gates; Now Playing shows the stream as a direct play with
`optimized_version` set and the version as its output.
