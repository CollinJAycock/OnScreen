// Pure formatting for the Now Playing cards (who / where / why). Every field
// the server added is optional — an older server simply renders less.
import type { ActiveSession, StreamFormat } from '$lib/api';

/** Max characters of the optional message shown to a stopped viewer
 *  (mirrors the server's cap). */
export const STOP_MESSAGE_MAX = 200;

export type DecisionTone = 'direct' | 'stream' | 'transcode';

export function decisionLabel(d: string): string {
  switch (d) {
    case 'directPlay': return 'Direct Play';
    case 'directStream': return 'Direct Stream';
    case 'remux': return 'Remux';
    default: return 'Transcoding';
  }
}

export function decisionTone(d: string): DecisionTone {
  if (d === 'directPlay') return 'direct';
  if (d === 'directStream' || d === 'remux') return 'stream';
  return 'transcode';
}

/** "collin", "collin · Kids" for a managed profile, or '' when unknown. */
export function viewerLabel(s: Pick<ActiveSession, 'username' | 'profile_name'>): string {
  if (!s.username) return s.profile_name ?? '';
  return s.profile_name ? `${s.username} · ${s.profile_name}` : s.username;
}

/** Avatar initial: the profile's (who is actually watching), else the account's. */
export function viewerInitial(s: Pick<ActiveSession, 'username' | 'profile_name'>): string {
  const name = (s.profile_name || s.username || '').trim();
  return name ? name[0].toUpperCase() : '?';
}

export function locationLabel(loc?: string): string {
  if (loc === 'lan') return 'LAN';
  if (loc === 'remote') return 'Remote';
  return '';
}

export function videoCodecLabel(c?: string): string {
  switch ((c ?? '').toLowerCase()) {
    case '': return '';
    case 'h264': case 'avc': case 'avc1': return 'H.264';
    case 'hevc': case 'h265': case 'hvc1': return 'HEVC';
    case 'av1': return 'AV1';
    case 'vp9': return 'VP9';
    case 'mpeg4': return 'MPEG-4';
    case 'mpeg2video': return 'MPEG-2';
    case 'vc1': return 'VC-1';
    default: return c!.toUpperCase();
  }
}

export function audioCodecLabel(c?: string): string {
  switch ((c ?? '').toLowerCase()) {
    case '': return '';
    case 'aac': return 'AAC';
    case 'ac3': return 'AC-3';
    case 'eac3': return 'E-AC-3';
    case 'truehd': return 'TrueHD';
    case 'dts': case 'dts-hd': case 'dtshd': return 'DTS';
    case 'flac': return 'FLAC';
    case 'opus': return 'Opus';
    case 'vorbis': return 'Vorbis';
    case 'mp3': return 'MP3';
    case 'copy': return '';
    default: return c!.toUpperCase();
  }
}

export function containerLabel(c?: string): string {
  const v = (c ?? '').toLowerCase();
  if (!v) return '';
  if (v === 'hls') return 'HLS';
  if (v.includes('matroska') || v === 'mkv') return 'MKV';
  if (v.includes('mp4') || v === 'mov' || v === 'm4v') return 'MP4';
  if (v === 'mpegts' || v === 'ts') return 'MPEG-TS';
  return v.split(',')[0].toUpperCase();
}

export function hdrLabel(h?: string): string {
  switch ((h ?? '').toLowerCase()) {
    case 'hdr10': return 'HDR10';
    case 'hdr10plus': return 'HDR10+';
    case 'hlg': return 'HLG';
    case 'dolby_vision': return 'Dolby Vision';
    default: return '';
  }
}

/** 3840×2160 → "4K", 1920×1080 → "1080p", 720-high → "720p", else "480p"/"SD". */
export function resolutionLabel(w?: number, h?: number): string {
  if (!w && !h) return '';
  const height = h ?? 0;
  const width = w ?? 0;
  if (width >= 3200 || height >= 2000) return '4K';
  if (width >= 2400 || height >= 1400) return '1440p';
  if (width >= 1700 || height >= 1000) return '1080p';
  if (width >= 1200 || height >= 700) return '720p';
  if (height >= 470) return '480p';
  return 'SD';
}

export function channelsLabel(n?: number): string {
  switch (n) {
    case undefined: case 0: return '';
    case 1: return 'Mono';
    case 2: return 'Stereo';
    case 6: return '5.1';
    case 8: return '7.1';
    default: return `${n}ch`;
  }
}

export function mbps(kbps?: number): string {
  if (!kbps) return '';
  return `${(kbps / 1000).toFixed(1)} Mbps`;
}

/** One side of the source → output line, e.g. "HEVC 4K HDR10 · E-AC-3 5.1 · 25.0 Mbps". */
export function formatStream(f?: StreamFormat): string {
  if (!f) return '';
  const video = [videoCodecLabel(f.video_codec), resolutionLabel(f.width, f.height), hdrLabel(f.hdr)]
    .filter(Boolean).join(' ');
  const audio = [audioCodecLabel(f.audio_codec), channelsLabel(f.audio_channels)].filter(Boolean).join(' ');
  return [video, audio, mbps(f.bitrate_kbps)].filter(Boolean).join(' · ');
}

/** The card's "source → output" pair. A direct play's output is the file
 *  itself; a missing side is '' (older server / unknown file). */
export function sourceOutput(s: Pick<ActiveSession, 'decision' | 'source' | 'output'>): { source: string; output: string } {
  const source = formatStream(s.source);
  let output = formatStream(s.output);
  if (!output && s.decision === 'directPlay' && source) output = 'Original file';
  return { source, output };
}

/** Hide Stop only where the server says it can't act (can_stop === false);
 *  an older server that omits the field keeps the button. */
export function canStop(s: Pick<ActiveSession, 'can_stop'>): boolean {
  return s.can_stop !== false;
}

/** Validation message for the stop dialog's note, '' when fine. */
export function stopMessageError(msg: string): string {
  const n = [...msg.trim()].length;
  return n > STOP_MESSAGE_MAX ? `Keep the message under ${STOP_MESSAGE_MAX} characters (${n}).` : '';
}

/** Player-style clock (1:02:35 / 0:45). */
export function fmtClock(ms: number): string {
  const s = Math.floor(ms / 1000);
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = s % 60;
  const mm = h > 0 ? String(m).padStart(2, '0') : String(m);
  return `${h > 0 ? h + ':' : ''}${mm}:${String(sec).padStart(2, '0')}`;
}
