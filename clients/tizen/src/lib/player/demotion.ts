// Which codec claim a refused stream proves wrong, if any.
//
// A refusal (hls.js: a fatal bufferAppendError; AVPlay and the Xbox's native
// player: "source not supported") is the panel's decoder turning the bytes
// down. It used to demote HEVC whatever the session carried, from the item's
// FIRST file: one append error on an H.264 remux, or on an AV1 version picked
// over an H.264 first file, took HEVC away for good, and every HEVC title
// after it was re-encoded. A claim is demoted now only when the session was
// delivering that codec:
//   - what the player itself says it was handed (hls.js names the codecs it
//     created its SourceBuffers for), when it says;
//   - else for a remux the playing file's own codec (the video is copied);
//   - else for a transcode what the server encodes to: H.264 unless HEVC or
//     AV1 was claimed. Its start response says how it packaged the segments,
//     and HEVC goes into MPEG-TS only for a client that asks for TS (AV1
//     never), so 'ts' there is H.264. Where that says nothing (a client that
//     always asks for TS, a pre-encoded ladder, an older server), the
//     server's own rule (api/v1/transcode.go preferHEVC / preferAV1): AV1
//     for an AV1 source when AV1 is claimed, HEVC for an HEVC or a 2160-line
//     source when HEVC is claimed.
// A refusal by the audio SourceBuffer says nothing about the video decoders.
//
// Pure, so the rule is tested without a player.

export type DemotableCodec = 'hevc' | 'av1';
export type VideoFamily = 'h264' | 'hevc' | 'av1' | 'other';

const AUDIO_CODECS = new Set([
  'mp4a', 'aac', 'ac-3', 'ac3', 'ec-3', 'eac3', 'opus', 'flac', 'mp3', 'alac',
  'vorbis', 'dtsc', 'dtse', 'dtsh', 'dtsl', 'dts', 'truehd', 'mlpa',
]);

/** The video family in a codec string: a file's video_codec ('hevc', 'h264',
 *  'av1', 'vp9'...) or an RFC 6381 list as hls.js reports it ('mp4a.40.2,
 *  hvc1.2.4.L150.B0'). Audio entries are skipped; null when no video codec
 *  is named. */
export function videoFamily(codecs: string | null | undefined): VideoFamily | null {
  if (!codecs) return null;
  for (const raw of codecs.split(',')) {
    const c = raw.trim().toLowerCase();
    if (!c) continue;
    const head = c.split('.')[0];
    if (head === 'hevc' || head === 'h265' || head === 'hvc1' || head === 'hev1' || head === 'dvh1' || head === 'dvhe') {
      return 'hevc';
    }
    if (head === 'av1' || head === 'av01' || head === 'dav1') return 'av1';
    if (head === 'h264' || head === 'avc' || head === 'avc1' || head === 'avc3') return 'h264';
    if (AUDIO_CODECS.has(head)) continue;
    return 'other';
  }
  return null;
}

/** What the page knows about the session whose stream was refused. */
export interface RefusedSession {
  /** The session stream-copies the source's video (a remux, no burn-in). */
  remux: boolean;
  /** The playing file's video codec (what a remux carries). */
  fileCodec?: string | null;
  /** The playing file's height (resolution_h). */
  fileHeight?: number | null;
  /** The start response's segment_format ('ts' / 'fmp4'), when it has one. */
  segmentFormat?: string | null;
  /** This client always asks for MPEG-TS segments (Tizen's AVPlay), so
   *  'ts' says nothing about the codec. */
  tsSegments?: boolean;
  /** The claims the session was started with (supports_hevc / supports_av1). */
  claimedHEVC: boolean;
  claimedAV1: boolean;
  /** The codec string(s) the player was handed for the refused track, when
   *  it reports them (hls.js BUFFER_CODECS). */
  playerCodecs?: string | null;
  /** The SourceBuffer that refused the bytes (hls.js 'audio' / 'video' /
   *  'audiovideo'), when known. */
  refusedTrack?: string | null;
}

/** The video codec the session was delivering, as far as the page can tell. */
export function deliveredVideo(s: RefusedSession): VideoFamily | null {
  const told = videoFamily(s.playerCodecs);
  if (told) return told;
  if (s.remux) return videoFamily(s.fileCodec);
  // A transcode: never HEVC or AV1 unless claimed.
  if (!s.claimedHEVC && !s.claimedAV1) return 'h264';
  const source = videoFamily(s.fileCodec);
  if (!s.tsSegments && s.segmentFormat === 'ts') return 'h264';
  if (s.claimedAV1 && source === 'av1') return 'av1';
  if (!s.tsSegments && s.segmentFormat === 'fmp4') return s.claimedHEVC ? 'hevc' : 'h264';
  if (s.claimedHEVC && (source === 'hevc' || (s.fileHeight ?? 0) >= 2160)) return 'hevc';
  return 'h264';
}

/** The claim to demote for this refusal, or null when the session wasn't
 *  delivering a claimed HEVC / AV1 stream (then it is some other failure,
 *  for the rest of the recovery ladder). */
export function codecToDemote(s: RefusedSession): DemotableCodec | null {
  if (s.refusedTrack === 'audio') return null;
  const v = deliveredVideo(s);
  if (v === 'hevc' && s.claimedHEVC) return 'hevc';
  if (v === 'av1' && s.claimedAV1) return 'av1';
  return null;
}
