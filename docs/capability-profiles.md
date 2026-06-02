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
