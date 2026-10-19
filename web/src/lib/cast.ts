// Google Cast (Chromecast) sender helpers.
//
// Pure module — no SDK calls — so the LOAD-message build, MIME mapping,
// and "is this file castable" predicate can be unit-tested without
// loading cast_sender.js or wiring a real receiver.
//
// Why a pure module: the Cast SDK is loaded async via a Google-hosted
// script and only exists on the window once the device is reachable.
// All the actual sender code (open picker, connect, send LOAD) lives
// behind that runtime check. This module captures the bits that DON'T
// depend on the SDK so tests can drive them directly.
//
// Receiver target: Google's Default Media Receiver. No custom receiver
// app — that would require a Google Developer Console registration +
// $5 and a publish cycle. Default Receiver plays MP4 H.264/HEVC + AAC
// and HLS.
//
// Any video casts: a file the receiver plays as-is (MP4/WebM the server's
// play decision calls directPlay for the Chromecast's own capability
// profile, CHROMECAST_CAPS_*) is handed over as /media/stream/{id}; anything
// else becomes an OnScreen HLS session (remux or transcode) started with
// that profile, and the receiver plays its playlist. The segment token in
// those URLs never rotates: it lives as long as the session and its expiry
// is an idle timeout refreshed by every fetch (transcode/segtoken.go), so a
// three-hour film doesn't outlive it. The SDK glue is in castSession.ts.

// ── types ────────────────────────────────────────────────────────────

/** Fields off `ItemDetail` we look at — kept minimal so tests don't
 * need to mint full item DTOs. */
export interface CastableItem {
  id: string;
  type: string; // 'movie' | 'episode' | 'track' | 'photo' | …
  title: string;
  poster_path?: string | null;
  parent_title?: string | null;
}

/** Fields off `ItemFile` we look at. video_codec / audio_codec /
 * container drive the MIME map; status gates eligibility. */
export interface CastableFile {
  id: string;
  // 'active' | 'missing' | …. Item details don't send it (they list only
  // playable files), so absent counts as active.
  status?: string | null;
  container?: string | null;
  video_codec?: string | null;
  audio_codec?: string | null;
  // The file's length (ItemFile.duration_ms); duration_seconds is the older
  // spelling, still read when duration_ms is absent.
  duration_ms?: number | null;
  duration_seconds?: number | null;
  stream_token?: string | null;
  resolution_w?: number | null;
  resolution_h?: number | null;
  // Set on one part of a multi-part movie: a direct cast would play only
  // that part, so the movie goes over as an HLS session instead (the
  // server streams every part as one).
  part_index?: number | null;
}

/** Cast media-metadata-type constants. Values are part of the Cast
 * protocol — don't change them. */
export const CAST_METADATA_TYPE = {
  GENERIC: 0,
  MOVIE: 1,
  TV_SHOW: 2,
  MUSIC_TRACK: 3,
  PHOTO: 4,
} as const;

export interface CastMediaInfo {
  contentId: string;
  contentType: string;
  streamType: 'BUFFERED' | 'LIVE' | 'NONE';
  metadata: {
    type: number;
    metadataType?: number; // SDK wants both spellings on some versions
    title: string;
    subtitle?: string;
    images?: { url: string }[];
  };
  duration?: number;
  customData?: { itemId: string; fileId: string };
  /** Same URL as contentId; newer receivers prefer it. HLS casts only. */
  contentUrl?: string;
  /** HLS casts: the segment formats the Default Media Receiver must be
   *  told (castHlsFormatFields). */
  hlsSegmentFormat?: string;
  hlsVideoSegmentFormat?: string;
  /** Side-loaded text tracks (castSubtitleTrack). */
  tracks?: CastTextTrack[];
}

/** A side-loaded WebVTT text track, in the Cast protocol's Track shape. */
export interface CastTextTrack {
  trackId: number;
  type: 'TEXT';
  subtype: 'SUBTITLES';
  trackContentId: string;
  trackContentType: 'text/vtt';
  name: string;
  language: string;
}

// ── pure helpers ─────────────────────────────────────────────────────

/** castContentType maps container + codec to a Cast-friendly MIME.
 *
 * Default Media Receiver's documented support:
 *   - video/mp4 with avc1.* (H.264) or hvc1.* (HEVC, Cast Ultra) +
 *     mp4a.40.* (AAC)
 *   - video/webm with vp08 / vp09 (newer receivers)
 *   - audio/mp3, audio/mp4 (AAC), audio/webm
 *
 * We don't emit a codec param yet — the Default Receiver's autodetect
 * is robust enough for v1. Returns "" for unsupported combos so the
 * UI can grey out the Cast button rather than offer it and fail.
 */
export function castContentType(file: CastableFile): string {
  const ct = (file.container ?? '').toLowerCase();
  const vc = (file.video_codec ?? '').toLowerCase();
  const ac = (file.audio_codec ?? '').toLowerCase();
  // Audio-only files (no video codec).
  if (!vc && ac) {
    if (ct === 'mp3' || ac === 'mp3') return 'audio/mp3';
    if (ct === 'm4a' || ct === 'mp4' || ac === 'aac' || ac.startsWith('mp4a')) return 'audio/mp4';
    if (ct === 'flac') return 'audio/flac';
    if (ct === 'ogg' || ac === 'opus' || ac === 'vorbis') return 'audio/ogg';
    return '';
  }
  // Video files. Restrict to containers Default Receiver natively
  // mounts; mkv works in practice but isn't documented support, so
  // skip it for v1. Codec list mirrors the H.264 / HEVC subset that
  // ships on every Cast device since 2018.
  if (vc !== 'h264' && vc !== 'hevc' && vc !== 'h265') return '';
  if (ct === 'mp4' || ct === 'm4v' || ct === 'mov') return 'video/mp4';
  if (ct === 'webm') return 'video/webm';
  return '';
}

/** castMetadataType maps an OnScreen item.type to the Cast protocol's
 * metadata-type integer. Unknown types fall back to GENERIC so the
 * receiver still renders the title / poster. */
export function castMetadataType(item: CastableItem): number {
  switch (item.type) {
    case 'movie':
      return CAST_METADATA_TYPE.MOVIE;
    case 'episode':
    case 'show':
    case 'season':
      return CAST_METADATA_TYPE.TV_SHOW;
    case 'track':
    case 'music':
      return CAST_METADATA_TYPE.MUSIC_TRACK;
    case 'photo':
      return CAST_METADATA_TYPE.PHOTO;
    default:
      return CAST_METADATA_TYPE.GENERIC;
  }
}

function fileActive(file: CastableFile): boolean {
  return file.status == null || file.status === 'active';
}

/** isCastable returns whether the receiver can play the file itself
 * (direct cast: a receiver-compatible container + codec, and a stream
 * token for the URL). Whether it actually goes over directly is the play
 * decision's call too — see planCast. */
export function isCastable(file: CastableFile): boolean {
  if (!fileActive(file)) return false;
  if (!file.stream_token) return false;
  if (file.part_index != null) return false;
  return castContentType(file) !== '';
}

/** buildCastMediaInfo assembles the LOAD payload the sender sends to
 * the receiver. baseUrl is the absolute origin (e.g.
 * "https://onscreen.example") because Cast devices fetch on the LAN
 * directly — they need an absolute URL, not the relative one the web
 * app uses internally.
 *
 * Returns null if the file isn't castable (caller should never have
 * surfaced the button in that case, but defensive).
 */
export function buildCastMediaInfo(
  item: CastableItem,
  file: CastableFile,
  baseUrl: string,
  assetToken: string | null = null,
): CastMediaInfo | null {
  if (!isCastable(file)) return null;
  const contentType = castContentType(file);
  // baseUrl normalisation: trim trailing slash so concatenation
  // doesn't double up. Empty string → relative URL, which Cast
  // rejects, so callers must pass an absolute origin.
  const origin = trimOrigin(baseUrl);
  const url = `${origin}/media/stream/${encodeURIComponent(file.id)}?token=${encodeURIComponent(file.stream_token!)}`;
  return {
    contentId: url,
    contentType,
    streamType: 'BUFFERED',
    metadata: castMetadata(item, origin, assetToken),
    duration: fileDurationSec(file),
    customData: { itemId: item.id, fileId: file.id },
  };
}

function trimOrigin(baseUrl: string): string {
  return baseUrl.replace(/\/+$/, '');
}

function fileDurationSec(file: CastableFile): number | undefined {
  if (file.duration_ms != null && file.duration_ms > 0) return file.duration_ms / 1000;
  return file.duration_seconds ?? undefined;
}

/** castPosterUrl is the absolute poster URL the receiver loads itself:
 * /artwork/<library-relative path> (each segment encoded), with the
 * purpose=asset token, since the Chromecast has none of this browser's
 * cookies and /artwork requires auth. The API's poster_path is relative
 * ("Movies/Sintel/poster.jpg"); a leading "/" or "/artwork/" is tolerated.
 * Without a token the receiver gets a 401 and shows no poster. */
export function castPosterUrl(origin: string, posterPath: string, assetToken: string | null): string {
  const rel = posterPath.replace(/^\/+/, '').replace(/^artwork\//, '');
  const enc = rel.split('/').map(encodeURIComponent).join('/');
  const url = `${trimOrigin(origin)}/artwork/${enc}?w=600`;
  return assetToken ? `${url}&token=${encodeURIComponent(assetToken)}` : url;
}

function castMetadata(item: CastableItem, origin: string, assetToken: string | null): CastMediaInfo['metadata'] {
  const type = castMetadataType(item);
  const images: { url: string }[] = [];
  if (item.poster_path) {
    images.push({ url: castPosterUrl(origin, item.poster_path, assetToken) });
  }
  return {
    type,
    metadataType: type,
    title: item.title,
    subtitle: item.parent_title ?? undefined,
    images,
  };
}

// ── Chromecast capability profiles ───────────────────────────────────
//
// The X-Client-Capabilities profiles a cast sends on the playback-decision
// and transcode-start calls it makes ON BEHALF of the Chromecast, instead
// of this browser's own header. THE one copy in the web code; the phone app
// keeps the same strings, and docs/capability-profiles.md ("Chromecast
// (Default Media Receiver)") is the reference both are checked against.
//
// protocols lists mp4 so an MP4 the receiver plays as-is comes back
// directPlay (cast directly); with hls alone no file container ever
// matched and every MP4 was remuxed. hls says the device takes HLS.

export type CastProfile = 'hd' | '4k';

export const CHROMECAST_CAPS: Readonly<Record<CastProfile, string>> = {
  // Chromecast gen 1–3, Chromecast HD, and anything unrecognised.
  hd: 'videoDecoder=h264,audioDecoder=aac:mp3,protocols=mp4:hls,maxWidth=1920,maxHeight=1080,maxAudioChannels=2,maxbitdepth=8,hdr=0',
  // Chromecast Ultra, Chromecast with Google TV, Google TV Streamer,
  // SHIELD, Android/Google TV sets (Bravia).
  '4k': 'videoDecoder=h264:h265,audioDecoder=aac:mp3,protocols=mp4:hls,maxWidth=3840,maxHeight=2160,maxAudioChannels=6,maxbitdepth=10,hdr=1',
};

const FOUR_K_DEVICE = /ultra|google tv|streamer|shield|android tv|bravia/i;

/** Exact model names (lowercased mDNS `md=` values) of 4K receivers the
 *  pattern misses. Same list as the phone's ChromecastCaps. */
const FOUR_K_MODELS = new Set(['mitv-mssp2']);

/** Exact model names that never get 4K: 1080p Chromecasts, smart displays,
 *  audio-only speakers and groups (a "Bose Smart Ultra Soundbar" is not a
 *  Chromecast Ultra). Plain "Chromecast" isn't listed: gen 1–3 and the 4K
 *  Chromecast with Google TV both report it. Same list as the phone's. */
const HD_MODELS = new Set([
  'chromecast hd',
  'chromecast audio',
  'eureka dongle',
  'google home',
  'google home mini',
  'google nest mini',
  'nest audio',
  'nest wifi point',
  'google nest hub',
  'google nest hub max',
  'google cast group',
  'lenovo smart display 7',
  'lenovo smart display 10',
  'bose smart ultra soundbar',
]);

/** What the Web Sender SDK tells us about a receiver: a chrome.cast.Receiver
 *  (session.getCastDevice()) has `friendlyName` and `capabilities`
 *  (chrome.cast.Capability strings: 'video_out', 'audio_out', …) but no
 *  model name. `modelName` is read in case a future SDK adds one. */
export interface CastReceiverInfo {
  friendlyName?: string | null;
  modelName?: string | null;
  capabilities?: readonly string[] | null;
}

/** castProfileForDevice picks the profile for a receiver:
 *  1. a receiver whose capabilities lack video_out (a speaker, a group) is
 *     never 4K;
 *  2. a model name, when there is one: a known HD/audio model is HD, a known
 *     4K model or one matching the 4K pattern is 4K;
 *  3. the friendly name against the 4K pattern: a set-up name like "SHIELD"
 *     or "BRAVIA 4K" still matches;
 *  4. otherwise HD, which every Chromecast plays. A renamed 4K device
 *     ("Living Room TV") lands here on the web, because the Web Sender SDK
 *     reports no model; a 4K load the receiver can't play is retried as HD.
 *  A bare string is taken as the friendly name. */
export function castProfileForDevice(
  device: string | CastReceiverInfo | null | undefined,
): CastProfile {
  const info: CastReceiverInfo =
    typeof device === 'string' ? { friendlyName: device } : (device ?? {});
  const caps = info.capabilities;
  if (Array.isArray(caps) && caps.length > 0 && !caps.includes('video_out')) return 'hd';
  const model = (info.modelName ?? '').trim().toLowerCase();
  if (model) {
    if (HD_MODELS.has(model)) return 'hd';
    if (FOUR_K_MODELS.has(model) || FOUR_K_DEVICE.test(model)) return '4k';
  }
  const name = info.friendlyName ?? '';
  return name && FOUR_K_DEVICE.test(name) ? '4k' : 'hd';
}

/** The profile to retry with after the receiver failed to play a load:
 * 4K falls back to HD once; HD has nothing left. */
export function fallbackCastProfile(p: CastProfile): CastProfile | null {
  return p === '4k' ? 'hd' : null;
}

// ── play decision → cast plan ────────────────────────────────────────

export type CastPlan =
  | { mode: 'direct' }
  | { mode: 'hls'; videoCopy: boolean }
  | { mode: 'refuse'; message: string };

/** canCastFile: the Cast button is offered for any playable file — the
 * plan (direct or an HLS session) is made when casting starts. */
export function canCastFile(file: CastableFile): boolean {
  return fileActive(file) && !!(file.video_codec || file.audio_codec);
}

/** castRefusal is the message for a decision no cast can play, else null.
 * unsupported = Dolby Vision profile 5 (no base layer anything but a DV
 * display shows; a transcode would come out green and purple); damaged =
 * the integrity probe found the bitstream undecodable. */
export function castRefusal(decision: string): string | null {
  switch (decision) {
    case 'unsupported':
      return "This title is Dolby Vision profile 5, which a Chromecast can't play and the server can't convert.";
    case 'damaged':
      return "This file is damaged — parts of it can't be decoded, so it can't be cast.";
    default:
      return null;
  }
}

/** planCast maps the server's decision for the Chromecast's profile onto a
 * cast: the file itself when the receiver plays it as-is (isCastable AND
 * directPlay), else an HLS session — a video copy (remux) when the decision
 * lets the video through untouched, a transcode otherwise. A non-default
 * audio track can't go over directly (the file plays its default track),
 * and a burned-in subtitle needs the transcode. */
export function planCast(
  file: CastableFile,
  decision: string,
  opts: { audioTrackSelected?: boolean; burnIn?: boolean } = {},
): CastPlan {
  const refusal = castRefusal(decision);
  if (refusal) return { mode: 'refuse', message: refusal };
  if (opts.burnIn) return { mode: 'hls', videoCopy: false };
  // An audio file the receiver plays as-is (mp3 / AAC / FLAC / Ogg) goes
  // over directly whatever the verdict: the profile's protocols name no
  // audio container, so the server answers directStream for every one.
  if (!file.video_codec && isCastable(file) && !opts.audioTrackSelected) return { mode: 'direct' };
  if (decision === 'directPlay' && isCastable(file) && !opts.audioTrackSelected) return { mode: 'direct' };
  return { mode: 'hls', videoCopy: decision === 'directPlay' || decision === 'directStream' };
}

/** The profiles' resolution ceilings (maxHeight). */
const CAST_MAX_HEIGHT: Readonly<Record<CastProfile, number>> = { hd: 1080, '4k': 2160 };

/** castTranscodeHeight is the explicit height a cast's transcode-start asks
 * for: the source's tier (by width or height, so scope 3840×1600 counts as
 * 2160), capped by the profile. Never 0 (Auto): Auto on a re-encode can
 * start an ABR ladder, whose master playlist can't carry one segment
 * format and whose rung switches restart encodes under the receiver. */
export function castTranscodeHeight(
  file: { resolution_w?: number | null; resolution_h?: number | null },
  profile: CastProfile,
): number {
  const cap = CAST_MAX_HEIGHT[profile];
  const w = file.resolution_w ?? 0;
  const h = file.resolution_h ?? 0;
  const tier = w >= 3840 || h >= 2160 ? 2160
    : w >= 1920 || h >= 1080 ? 1080
      : w >= 1280 || h >= 720 ? 720
        : h > 0 ? h
          : cap;
  return Math.min(tier, cap);
}

/** The transcode-start body a cast sends. supports_hevc is explicit (false
 * for HD, true for 4K) because the server lets the body's value beat the
 * header's codec list in either direction. */
export function buildCastStartBody(o: {
  fileId: string;
  profile: CastProfile;
  file: { resolution_w?: number | null; resolution_h?: number | null };
  positionSec: number;
  videoCopy: boolean;
  audioStreamIndex?: number;
  burnStreamIndex?: number | null;
}): {
  file_id: string;
  height: number;
  position_ms: number;
  video_copy: boolean;
  audio_stream_index: number | null;
  supports_hevc: boolean;
  burn_subtitle_stream_index?: number;
} {
  const body: ReturnType<typeof buildCastStartBody> = {
    file_id: o.fileId,
    height: castTranscodeHeight(o.file, o.profile),
    position_ms: Math.max(0, Math.round(o.positionSec * 1000)),
    video_copy: o.videoCopy,
    audio_stream_index: o.audioStreamIndex ?? null,
    supports_hevc: o.profile === '4k',
  };
  if (o.burnStreamIndex != null) body.burn_subtitle_stream_index = o.burnStreamIndex;
  return body;
}

/** The decision a cast's progress reports carry (analytics + Now Playing). */
export function castProgressDecision(plan: CastPlan): 'directPlay' | 'remux' | 'transcode' {
  if (plan.mode === 'direct') return 'directPlay';
  if (plan.mode === 'hls' && plan.videoCopy) return 'remux';
  return 'transcode';
}

/** The client_name a cast reports progress under (Now Playing, "Play on…"). */
export function castClientName(friendlyName: string | null | undefined): string {
  const n = (friendlyName ?? '').trim();
  return n ? `Chromecast — ${n}` : 'Chromecast';
}

// ── HLS LOAD payload ─────────────────────────────────────────────────

export type HlsSegmentFormat = 'ts' | 'fmp4';

/** castHlsFormatFields is what the Default Media Receiver must be told
 * about an HLS stream's segments (it doesn't sniff them): the
 * chrome.cast.media.HlsSegmentFormat / HlsVideoSegmentFormat values.
 * OnScreen's segments carry audio and video muxed together: MPEG-TS for
 * H.264 ('ts' + 'mpeg2_ts'), fMP4 for HEVC/AV1 ('fmp4' + 'fmp4'). An
 * audio-only session is AAC in MPEG-TS ('ts_aac', no video format). */
export function castHlsFormatFields(
  format: HlsSegmentFormat,
  audioOnly: boolean,
): { hlsSegmentFormat: string; hlsVideoSegmentFormat?: string } {
  if (format === 'fmp4') return { hlsSegmentFormat: 'fmp4', hlsVideoSegmentFormat: 'fmp4' };
  if (audioOnly) return { hlsSegmentFormat: 'ts_aac' };
  return { hlsSegmentFormat: 'ts', hlsVideoSegmentFormat: 'mpeg2_ts' };
}

export type PlaylistSniff =
  | { kind: 'master'; variants: string[] }
  | { kind: 'media'; format: HlsSegmentFormat | null };

/** sniffHlsPlaylist reads what a playlist says about the stream: a master
 * (an ABR ladder: its variant URIs, as written) or a media playlist and its
 * segment container — fMP4 when it has an EXT-X-MAP init or .m4s segments,
 * MPEG-TS for .ts, null when it lists neither yet. */
export function sniffHlsPlaylist(text: string): PlaylistSniff {
  const lines = text.split(/\r?\n/).map((l) => l.trim());
  if (lines.some((l) => l.startsWith('#EXT-X-STREAM-INF'))) {
    const variants: string[] = [];
    for (let i = 0; i < lines.length; i++) {
      if (!lines[i].startsWith('#EXT-X-STREAM-INF')) continue;
      const uri = lines.slice(i + 1).find((l) => l !== '' && !l.startsWith('#'));
      if (uri) variants.push(uri);
    }
    return { kind: 'master', variants };
  }
  const segPath = (l: string) => l.split('?')[0];
  if (lines.some((l) => l.startsWith('#EXT-X-MAP')) ||
      lines.some((l) => l !== '' && !l.startsWith('#') && segPath(l).endsWith('.m4s'))) {
    return { kind: 'media', format: 'fmp4' };
  }
  if (lines.some((l) => l !== '' && !l.startsWith('#') && segPath(l).endsWith('.ts'))) {
    return { kind: 'media', format: 'ts' };
  }
  return { kind: 'media', format: null };
}

/** absoluteUrl resolves a server path (or URL) against the server origin:
 * the receiver fetches on its own, so every URL it gets must be absolute. */
export function absoluteUrl(origin: string, pathOrUrl: string): string {
  return new URL(pathOrUrl, trimOrigin(origin) + '/').toString();
}

/** The subtitle a cast side-loads: an embedded text stream or an external
 * subtitle of the file being cast. */
export interface CastSubtitleChoice {
  origin: 'embedded' | 'external';
  fileId: string;
  streamIndex?: number;
  subId?: string;
  label: string;
  language: string;
}

/** castSubtitleTrack builds the WebVTT text track for a cast. The receiver
 * fetches it without cookies, so the URL carries the file's stream token
 * (bound to that file id: /media/subtitles/{fileId}/{index}, or
 * /media/subtitles/{fileId}/external/{subId} for an external one), and
 * start_ms rebases the cues onto a stream that starts mid-title (the
 * session's start_offset_sec; 0 for a direct cast or an ABR ladder, whose
 * timeline is the whole title). Null without a token or a usable source. */
export function castSubtitleTrack(
  origin: string,
  sub: CastSubtitleChoice,
  streamToken: string | null | undefined,
  startMs: number,
): CastTextTrack | null {
  if (!streamToken) return null;
  const file = encodeURIComponent(sub.fileId);
  let path: string;
  if (sub.origin === 'embedded' && sub.streamIndex != null) {
    path = `/media/subtitles/${file}/${sub.streamIndex}`;
  } else if (sub.origin === 'external' && sub.subId) {
    path = `/media/subtitles/${file}/external/${encodeURIComponent(sub.subId)}`;
  } else {
    return null;
  }
  const q = new URLSearchParams({ token: streamToken });
  const start = Math.round(startMs);
  if (start > 0) q.set('start_ms', String(start));
  return {
    trackId: CAST_SUBTITLE_TRACK_ID,
    type: 'TEXT',
    subtype: 'SUBTITLES',
    trackContentId: `${trimOrigin(origin)}${path}?${q}`,
    trackContentType: 'text/vtt',
    name: sub.label,
    language: sub.language || 'und',
  };
}

/** The id the cast's one side-loaded text track goes by (activeTrackIds). */
export const CAST_SUBTITLE_TRACK_ID = 1;

/** buildCastHlsMediaInfo assembles the LOAD for an OnScreen HLS session on
 * the Default Media Receiver. playlistUrl is the session's playlist_url made
 * absolute (it carries its token). */
export function buildCastHlsMediaInfo(
  item: CastableItem,
  file: CastableFile,
  opts: {
    origin: string;
    playlistUrl: string;
    segmentFormat: HlsSegmentFormat;
    subtitle?: CastTextTrack | null;
    /** purpose=asset token for the poster URL (see castPosterUrl). */
    assetToken?: string | null;
  },
): CastMediaInfo {
  const audioOnly = !file.video_codec && !!file.audio_codec;
  const info: CastMediaInfo = {
    contentId: opts.playlistUrl,
    contentUrl: opts.playlistUrl,
    contentType: 'application/x-mpegurl',
    streamType: 'BUFFERED',
    metadata: castMetadata(item, trimOrigin(opts.origin), opts.assetToken ?? null),
    customData: { itemId: item.id, fileId: file.id },
    ...castHlsFormatFields(opts.segmentFormat, audioOnly),
  };
  if (opts.subtitle) info.tracks = [opts.subtitle];
  return info;
}

// ── timeline ─────────────────────────────────────────────────────────
//
// A single-rendition HLS session started at position_ms has a timeline of
// its own that begins at 0; the server reports where in the title that 0
// is as start_offset_sec (position_ms, or the keyframe before it on a
// video copy). An ABR ladder or a pre-encoded one covers the whole title
// from 0 (start_offset_sec 0), and the receiver seeks to the position. The
// sender keeps offset = start_offset_sec and shows/reports
// offset + the receiver's currentTime.

/** castLoadStartSec is the LOAD's currentTime (stream time): the requested
 * content position on the session's timeline, and never before the silent
 * head of a remux's segment 0 (seg0_audio_gap_sec). */
export function castLoadStartSec(positionSec: number, startOffsetSec: number, seg0AudioGapSec = 0): number {
  return Math.max(0, positionSec - startOffsetSec, seg0AudioGapSec);
}

/** castContentTime maps the receiver's currentTime to content time. */
export function castContentTime(offsetSec: number, receiverSec: number): number {
  return offsetSec + Math.max(0, Number.isFinite(receiverSec) ? receiverSec : 0);
}

export type CastSeek =
  | { kind: 'receiver'; streamTime: number }
  | { kind: 'restart'; positionMs: number };

/** planCastSeek decides how a seek to content time targetSec reaches the
 * receiver. A direct cast or a full-timeline stream (ABR / pre-encoded
 * ladder) seeks on the receiver. A single-rendition session only holds what
 * ffmpeg has produced since its start: a target inside that window
 * (seekableEndSec, the receiver's seekable end in stream time) is a
 * receiver seek, anything else — before the session's start, past what's
 * been encoded, or with the window unknown — starts a new session there. */
export function planCastSeek(
  targetSec: number,
  s: {
    fullTimeline: boolean;
    offsetSec: number;
    seekableEndSec: number | null;
    durationSec: number;
  },
): CastSeek {
  let t = Math.max(0, targetSec);
  if (s.durationSec > 0) t = Math.min(t, s.durationSec);
  if (s.fullTimeline) return { kind: 'receiver', streamTime: t };
  const streamTime = t - s.offsetSec;
  // A second of slack: landing right on the produced edge only stalls.
  if (streamTime >= 0 && s.seekableEndSec != null && streamTime <= s.seekableEndSec - 1) {
    return { kind: 'receiver', streamTime };
  }
  return { kind: 'restart', positionMs: Math.round(t * 1000) };
}
