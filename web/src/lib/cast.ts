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
// $5 and a publish cycle. Default Receiver supports MP4 H.264/HEVC +
// AAC and HLS, which covers our direct-play set for v1.
//
// v1 scope: direct-play files only. Transcoded HLS to Cast is fixable
// later but the segment-token rotation interaction with Default
// Receiver's caching needs more thought.

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
  status: string; // 'active' | 'missing' | …
  container?: string | null;
  video_codec?: string | null;
  audio_codec?: string | null;
  duration_seconds?: number | null;
  stream_token?: string | null;
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

/** isCastable returns whether the file is eligible for v1 Cast (direct
 * play with a receiver-compatible codec). UI calls this to enable /
 * disable the Cast button per file. */
export function isCastable(file: CastableFile): boolean {
  if (file.status !== 'active') return false;
  if (!file.stream_token) return false;
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
): CastMediaInfo | null {
  if (!isCastable(file)) return null;
  const contentType = castContentType(file);
  // baseUrl normalisation: trim trailing slash so concatenation
  // doesn't double up. Empty string → relative URL, which Cast
  // rejects, so callers must pass an absolute origin.
  const origin = baseUrl.replace(/\/+$/, '');
  const url = `${origin}/media/stream/${encodeURIComponent(file.id)}?token=${encodeURIComponent(file.stream_token!)}`;
  const type = castMetadataType(item);
  const images: { url: string }[] = [];
  if (item.poster_path) {
    // Posters live under /artwork/<path>; absolute URL again because
    // the receiver fetches directly.
    images.push({ url: `${origin}${item.poster_path}` });
  }
  return {
    contentId: url,
    contentType,
    streamType: 'BUFFERED',
    metadata: {
      type,
      metadataType: type,
      title: item.title,
      subtitle: item.parent_title ?? undefined,
      images,
    },
    duration: file.duration_seconds ?? undefined,
    customData: { itemId: item.id, fileId: file.id },
  };
}
