// X-Client-Capabilities profile for Samsung Tizen TVs.
//
// Tizen TVs decode H.264 and HEVC (including Main 10 / 4K HDR) and AAC/AC-3/
// E-AC-3 natively via the AVPlay hardware path; AV1 support varies by model and
// is not claimed. The behaviorally-significant fields here (HEVC + the 5.1
// channel cap) match what the watch page already declares to the transcode
// endpoint (supports_hevc: true, no channel override → 5.1), so sending this is
// additive: it gives the server the full declarative profile for transcode
// target selection and — once the watch page adopts POST /items/{id}/playback-
// decision — the server-authoritative play decision. See
// docs/capability-profiles.md (grammar) + the web client for the reference.
export function clientCapabilitiesHeader(): string {
  return [
    'videoDecoder=h264:h265',
    'audioDecoder=aac:ac3:eac3',
    'protocols=mp4:ts',
    'maxWidth=3840',
    'maxHeight=2160',
    'maxAudioChannels=6',
    'maxbitdepth=10',
    'hdr=1',
  ].join(',');
}
