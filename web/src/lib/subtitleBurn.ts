// Image-based subtitles (PGS, VobSub, DVB) on the web player.
//
// The browser can't draw an image subtitle at all — not on direct play, not
// through HLS — so picking one resolves to one of two things: the track's OCR
// text version when the server has one (a plain external subtitle, no
// restart), otherwise a server transcode that burns the track into the
// picture. A burn is a full video re-encode, so it overrides a remux's video
// copy and outlives every restart of the session (audio switch, quality
// switch, a seek outside the HLS window) until the viewer picks something
// else. The watch page owns the playback switching; this module owns the
// decisions so they can be tested without a <video>.

import type { ExternalSubtitle, ItemFile, SubtitleStream } from '$lib/api';

// Server errors that mean "this burn request can't happen" (400s): the page
// shows the message and stays on what it was playing.
export const BURN_ERROR_CODES: ReadonlySet<string> = new Set([
  'INVALID_SUBTITLE_STREAM',
  'SUBTITLE_NOT_IMAGE',
  'BURN_IN_UNSUPPORTED',
]);

// Codec fallback for servers that predate subtitle_streams[].image_based.
const imageSubCodecs = new Set([
  'hdmv_pgs_subtitle', 'pgssub', 'dvd_subtitle', 'dvb_subtitle', 'xsub',
]);

export function isImageSubtitle(s: SubtitleStream): boolean {
  return s.image_based ?? imageSubCodecs.has(s.codec.toLowerCase());
}

// The external subtitle OCR'd from this stream. text_version_id is the
// server's link; older servers only tag the OCR row (source "ocr",
// source_id "stream_<index>").
export function textVersionOf(file: ItemFile, s: SubtitleStream): ExternalSubtitle | undefined {
  const externals = file.external_subtitles ?? [];
  if (s.text_version_id) {
    const linked = externals.find((e) => e.id === s.text_version_id);
    if (linked) return linked;
  }
  return externals.find((e) => e.source === 'ocr' && e.source_id === `stream_${s.index}`);
}

export interface ImageSubtitleOption {
  stream: SubtitleStream;
  /** Present when the track has an OCR text version to show instead. */
  textVersion?: ExternalSubtitle;
  /** "<lang>" — the menu appends "(burned in)" or the text marker. */
  label: string;
}

export function imageSubtitleOptions(file: ItemFile | undefined): ImageSubtitleOption[] {
  if (!file) return [];
  return (file.subtitle_streams ?? [])
    .filter(isImageSubtitle)
    .map((stream) => ({
      stream,
      textVersion: textVersionOf(file, stream),
      label: stream.title || stream.language || `Track ${stream.index}`,
    }));
}

// What the viewer picked in the subtitle menu. T is the page's own shape for
// a text subtitle it already lists.
export type SubtitlePick<T> =
  | { kind: 'off' }
  | { kind: 'text'; sub: T }
  | { kind: 'image'; option: ImageSubtitleOption };

// What the page should do about it.
//   select — show this text subtitle (or none) in the cue overlay; when
//            `unburn` is set the stream carries a burned track that has to
//            come off, so playback restarts without the burn.
//   burn   — restart playback as a transcode burning this stream index.
//   none   — the pick is already what's on screen.
export type SubtitlePlan<T> =
  | { action: 'select'; text: { sub: T } | { external: ExternalSubtitle } | null; unburn: boolean }
  | { action: 'burn'; streamIndex: number }
  | { action: 'none' };

export function planSubtitlePick<T>(pick: SubtitlePick<T>, burnedIndex: number | null): SubtitlePlan<T> {
  const unburn = burnedIndex !== null;
  switch (pick.kind) {
    case 'off':
      return { action: 'select', text: null, unburn };
    case 'text':
      return { action: 'select', text: { sub: pick.sub }, unburn };
    case 'image': {
      const { stream, textVersion } = pick.option;
      if (textVersion) return { action: 'select', text: { external: textVersion }, unburn };
      if (burnedIndex === stream.index) return { action: 'none' };
      return { action: 'burn', streamIndex: stream.index };
    }
  }
}

export interface TranscodeShape {
  height: number;
  videoCopy: boolean;
  burnSubtitleStreamIndex?: number;
}

// applyBurn rewrites a session request for an active burn. Every path that
// (re)starts a transcode goes through it, so an audio switch, a quality
// switch or a seek-restart keeps the burned track. A burn can't ride a video
// copy: a remux request (height 0, video copy) becomes a full transcode at
// the source's tier — the server never upscales, so a DVD stays 480p.
export function applyBurn(
  req: { height: number; videoCopy: boolean },
  burnedIndex: number | null,
  file: Pick<ItemFile, 'resolution_w' | 'resolution_h'> | undefined,
): TranscodeShape {
  if (burnedIndex === null) return { height: req.height, videoCopy: req.videoCopy };
  let height = req.height;
  if (height <= 0) {
    const w = file?.resolution_w ?? 0;
    const h = file?.resolution_h ?? 0;
    height = w >= 3840 || h >= 2160 ? 2160 : 1080;
  }
  return { height, videoCopy: false, burnSubtitleStreamIndex: burnedIndex };
}
