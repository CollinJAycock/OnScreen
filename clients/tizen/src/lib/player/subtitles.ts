// Subtitles for the player, drawn by the page itself. A server HLS session
// carries video and one audio track only (no subtitle renditions), so the
// old video.textTracks toggling had nothing to show: subtitles never
// rendered on webOS. Like the web client (and Android's side-loads), the
// player fetches the chosen track as WebVTT, parses it here and draws the
// active cues in a DOM overlay timed by the player's own clock:
//
//  - embedded tracks: /media/subtitles/{fileId}/{index}, where index is the
//    ABSOLUTE ffprobe stream index (the server's ServeSubtitle maps
//    `0:<index>`; not the subtitle ordinal). Image tracks (PGS, VobSub, DVB,
//    XSUB) can't become WebVTT (the server answers 415) and aren't offered.
//  - downloaded / OCR'd tracks: files[].external_subtitles, at their `url`.
//
// Cue times are CONTENT time (the whole file's clock), so they are compared
// with the content position (stream time + the session's offset), never
// re-based per session: a re-issue changes nothing about the cues.
//
// Pure (types and labels only) so the parser, the option building and the
// timing are unit-tested.

import { stableSort } from '$lib/stableSort';
import type { ItemFile } from '../api/types';
import { subtitleLabel } from '../langName';
import { toContentMs } from './session';

/** Bitmap subtitle codecs the server can't serve as WebVTT. */
export const IMAGE_SUBTITLE_CODECS: ReadonlySet<string> = new Set([
  'hdmv_pgs_subtitle',
  'pgssub',
  'dvd_subtitle',
  'dvb_subtitle',
  'xsub',
]);

export function isImageSubtitle(codec: string | null | undefined): boolean {
  return IMAGE_SUBTITLE_CODECS.has((codec ?? '').toLowerCase());
}

/** One row of the subtitle picker. */
export interface SubtitleOption {
  /** Stable across item refreshes and session re-issues: 'emb:<stream
   *  index>' or 'ext:<external id>'. The choice is kept by key, so a
   *  re-issue (a seek, an audio switch) or a refreshed item after a
   *  download never moves it to another track. */
  key: string;
  label: string;
  language: string;
  forced: boolean;
  sdh: boolean;
  /** An external row (downloaded, or OCR'd from an image track). */
  downloaded: boolean;
  /** Server path of the WebVTT (signed with api.assetUrl at fetch time). */
  path: string;
  /** External rows: their id, and the provider's id (the OpenSubtitles
   *  file id of a download). */
  externalId?: string;
  sourceId?: string;
}

/** The picker's rows for a file: embedded text tracks in stream order, then
 *  the external ones in the server's order. */
export function buildSubtitleOptions(
  file: Pick<ItemFile, 'id' | 'subtitle_streams' | 'external_subtitles'> | null | undefined,
): SubtitleOption[] {
  if (!file) return [];
  const rows: SubtitleOption[] = [];
  for (const s of file.subtitle_streams ?? []) {
    if (isImageSubtitle(s.codec)) continue;
    rows.push({
      key: `emb:${s.index}`,
      label: subtitleLabel({
        language: s.language,
        title: s.title,
        forced: s.forced,
        sdh: !!s.sdh,
        downloaded: false,
        fallback: `Track ${s.index}`,
      }),
      language: s.language ?? '',
      forced: !!s.forced,
      sdh: !!s.sdh,
      downloaded: false,
      path: `/media/subtitles/${file.id}/${s.index}`,
    });
  }
  for (const e of file.external_subtitles ?? []) {
    if (!e.url) continue;
    rows.push({
      key: `ext:${e.id}`,
      label: subtitleLabel({
        language: e.language,
        title: e.title,
        forced: !!e.forced,
        sdh: !!e.sdh,
        downloaded: true,
        fallback: 'External',
      }),
      language: e.language ?? '',
      forced: !!e.forced,
      sdh: !!e.sdh,
      downloaded: true,
      path: e.url,
      externalId: e.id,
      sourceId: e.source_id ?? undefined,
    });
  }
  return rows;
}

/**
 * The row to select after an online download: the attached row the server
 * returned (by id), else the row whose source_id is the downloaded file's
 * provider id, else an external row that wasn't there before the download.
 * null when none can be told apart (the selection is left alone).
 */
export function pickDownloadedSubtitle(
  before: readonly SubtitleOption[],
  after: readonly SubtitleOption[],
  providerFileId: number,
  createdId?: string | null,
): string | null {
  const external = after.filter((o) => o.downloaded);
  if (createdId) {
    const byId = external.find((o) => o.externalId === createdId);
    if (byId) return byId.key;
  }
  const isNew = (o: SubtitleOption) => !before.some((b) => b.key === o.key);
  const id = String(providerFileId);
  const bySource = external.filter((o) => o.sourceId === id);
  const pick = bySource.find(isNew) ?? bySource[0] ?? external.find(isNew);
  return pick ? pick.key : null;
}

// ── WebVTT ────────────────────────────────────────────────────────────

/** A cue in content time; `text` has its markup stripped, lines split by \n. */
export interface Cue {
  startMs: number;
  endMs: number;
  text: string;
}

/** "hh:mm:ss.ttt" or "mm:ss.ttt" → ms; null if it isn't one. Lenient about
 *  the hour width, a comma decimal (SRT habits) and the fraction's width. */
export function parseVttTimestamp(raw: string): number | null {
  const m = /^(?:(\d+):)?(\d{1,2}):(\d{1,2})(?:[.,](\d{1,3}))?$/.exec(raw.trim());
  if (!m) return null;
  const h = m[1] ? parseInt(m[1], 10) : 0;
  const min = parseInt(m[2], 10);
  const sec = parseInt(m[3], 10);
  const frac = m[4] ? Math.round(parseFloat(`0.${m[4]}`) * 1000) : 0;
  if (min > 59 || sec > 59) return null;
  return ((h * 60 + min) * 60 + sec) * 1000 + frac;
}

const ENTITIES: Record<string, string> = {
  amp: '&',
  lt: '<',
  gt: '>',
  quot: '"',
  apos: "'",
  nbsp: ' ',
  lrm: '‎',
  rlm: '‏',
};

function decodeEntity(whole: string, body: string): string {
  if (body[0] === '#') {
    const hex = body[1] === 'x' || body[1] === 'X';
    const n = parseInt(body.slice(hex ? 2 : 1), hex ? 16 : 10);
    if (!Number.isFinite(n) || n <= 0 || n > 0x10ffff) return whole;
    try {
      return String.fromCodePoint(n);
    } catch {
      return whole;
    }
  }
  return ENTITIES[body.toLowerCase()] ?? whole;
}

/**
 * A cue's payload as plain text: WebVTT tags stripped (<i>, <b>, <c.x>,
 * <v Speaker>, karaoke <00:01:02.000> timestamps) but a <br> kept as a line
 * break, ASS override blocks ({\an8}) a conversion left behind dropped,
 * entities decoded, each line trimmed and blank lines dropped. The overlay
 * renders the result as text, so nothing from the file becomes markup.
 */
export function cleanCueText(raw: string): string {
  const text = raw
    .replace(/<br\s*\/?>/gi, '\n')
    .replace(/<[^>\n]*>/g, '')
    .replace(/\{\\[^}\n]*\}/g, '')
    .replace(/&(#x[0-9a-f]+|#\d+|[a-z]+);/gi, decodeEntity);
  return text
    .split('\n')
    .map((l) => l.trim())
    .filter((l) => l !== '')
    .join('\n');
}

/**
 * Parse a WebVTT document into cues sorted by start. Handles a BOM, CRLF or
 * CR line ends, hours-less timestamps, cue identifiers, cue settings after
 * the timing (`align:start line:10%`, ignored), NOTE / STYLE / REGION blocks
 * and the header block, multi-line cues; malformed blocks are skipped, never
 * thrown on. Ported from (and stricter than) the web client's parseWebVTT,
 * which read STYLE / NOTE text and header lines as cue text.
 */
export function parseWebVtt(text: string): Cue[] {
  const lines = text.replace(/^﻿/, '').replace(/\r\n?/g, '\n').split('\n');
  const cues: Cue[] = [];
  let i = 0;
  while (i < lines.length) {
    if (lines[i].trim() === '') {
      i++;
      continue;
    }
    const block: string[] = [];
    while (i < lines.length && lines[i].trim() !== '') block.push(lines[i++]);
    const cue = parseBlock(block);
    if (cue) cues.push(cue);
  }
  // Stably (lib/stableSort: Tizen 5.5 runs Chromium 69): cues that start
  // together keep the file's order.
  return stableSort(cues, (a, b) => a.startMs - b.startMs);
}

function parseBlock(block: string[]): Cue | null {
  const head = block[0].trim();
  // Comments, style sheets and region definitions, never cues. The header
  // block ("WEBVTT", "X-TIMESTAMP-MAP=…") has no timing line.
  if (/^(NOTE|STYLE|REGION)(\s|$)/.test(head)) return null;
  const at = block.findIndex((l) => l.indexOf('-->') >= 0);
  // The timing line is the first line, or the second after an identifier.
  if (at < 0 || at > 1 || (at === 1 && /^WEBVTT/.test(head))) return null;
  const m = /^\s*(\S+)\s*-->\s*(\S+)/.exec(block[at]);
  if (!m) return null;
  const startMs = parseVttTimestamp(m[1]);
  const endMs = parseVttTimestamp(m[2]);
  if (startMs === null || endMs === null || endMs <= startMs) return null;
  const body = cleanCueText(block.slice(at + 1).join('\n'));
  return body ? { startMs, endMs, text: body } : null;
}

/** The cues showing at content time `tMs` (start inclusive, end exclusive, so
 *  back-to-back cues never show together). */
export function activeCues(cues: readonly Cue[], tMs: number): Cue[] {
  const out: Cue[] = [];
  for (const c of cues) {
    if (c.startMs > tMs) break; // sorted by start
    if (tMs < c.endMs) out.push(c);
  }
  return out;
}

/** Same cues (by identity) in the same order: the overlay needn't redraw. */
export function sameCues(a: readonly Cue[], b: readonly Cue[]): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) if (a[i] !== b[i]) return false;
  return true;
}

// ── Timing ────────────────────────────────────────────────────────────

/** The content time the cues are matched against: the element's stream time
 *  plus the session's offset, less a container timestamp shift (see
 *  ptsShiftOnFirstPlayMs). A cue at 45:10 shows 10 s into a session opened
 *  at 45:00. */
export function subtitleClockMs(streamSec: number, offsetMs: number, ptsShiftMs: number): number {
  return toContentMs(streamSec, offsetMs) - ptsShiftMs;
}

/**
 * Some sources shift their MPEG-TS timestamps (edit lists, a chapter track),
 * so a session from the top of the file plays at currentTime = shift instead
 * of 0, while the subtitles extracted from the same file start at 0. The web
 * client measures that shift once, on a session's FIRST 'playing', and
 * subtracts it from the cue clock (its hlsPtsCapturePending latch; re-measuring
 * on a later play took a pause position for the shift and desynced every
 * cue). Ported with one more guard: only a stream that started at its head
 * says anything about the container. A resume inside a full-timeline stream
 * (offset 0, started 40 min in) lands wherever hls.js's start seek puts it,
 * and that is not a timestamp shift. 0 when there is nothing to correct.
 *
 * `startSec` is where the stream was asked to start (hls.js startPosition;
 * -1 = its head), `currentSec` the element's time at the first 'playing'.
 */
export function ptsShiftOnFirstPlayMs(o: { offsetMs: number; startSec: number; currentSec: number }): number {
  if (o.offsetMs !== 0) return 0;
  const start = o.startSec > 0 ? o.startSec : 0;
  // Asked to start into the stream: a resume, not the head.
  if (start > 1) return 0;
  const shift = o.currentSec - start;
  return Number.isFinite(shift) && shift > 0.5 ? Math.round(shift * 1000) : 0;
}

// ── Loading ───────────────────────────────────────────────────────────

/** A subtitle fetch that answered with an HTTP error. `code` is the server's
 *  own error code when the body was its JSON, '' otherwise (a proxy's page). */
export class SubtitleFetchError extends Error {
  readonly status: number;
  readonly code: string;
  constructor(status: number, code = '') {
    super(`subtitle fetch failed: HTTP ${status}${code ? ` ${code}` : ''}`);
    this.status = status;
    this.code = code;
  }
}

/** The error for a failed response, with the code from the server's JSON
 *  error body (respond.Error: { error: { code } }) when it has one. */
export async function subtitleFetchError(res: { status: number; json(): Promise<unknown> }): Promise<SubtitleFetchError> {
  let code = '';
  try {
    const body = (await res.json()) as { error?: { code?: unknown } } | null;
    const c = body && body.error ? body.error.code : undefined;
    if (typeof c === 'string') code = c;
  } catch {
    /* not the server's JSON: a proxy's error page, or no body */
  }
  return new SubtitleFetchError(res.status, code);
}

/** The server's code on the 504 it sends once an embedded track's extraction
 *  has FAILED (ServeSubtitle answers one still running only when it is
 *  done). Despite the name, nothing is being prepared any more. */
export const SUBTITLE_EXTRACTION_FAILED_CODE = 'SUBTITLE_PREPARING';

/** A status a retry can't change: the track can't be served as WebVTT (415
 *  IMAGE_SUBTITLE), or it isn't there. */
export function isFinalSubtitleStatus(status: number): boolean {
  return status === 400 || status === 404 || status === 410 || status === 415;
}

export const SUBTITLE_TRIES = 3;
export const SUBTITLE_RETRY_DELAYS_MS: readonly number[] = [1_000, 2_000];

// An embedded track the server hasn't extracted yet is demuxed from the whole
// source on the first request: 25–60 s for a 4K remux, after a wait of up to
// 2 min for an extraction slot, capped at 5 min (server: ServeSubtitle). The
// server answers that request when the VTT is cached (or with a 504 once the
// extraction failed, see classifySubtitleFailure), and a request cut short
// (a proxy's timeout, its 60 s write deadline, or ours) leaves the extraction
// running to cache for the next one. So a slow answer is "still preparing",
// not a failure: each try may wait this long, and such answers keep the load
// polling for SUBTITLE_PREPARING_BUDGET_MS. Three 20 s tries used to give up
// at about a minute, and nothing fetched the VTT the server cached later.

/** Per-try ceiling: Cloudflare's own proxy timeout. */
export const SUBTITLE_FETCH_TIMEOUT_MS = 100_000;
/** How long "still preparing" answers keep the load going: the server's
 *  extraction queue (2 min) plus its extraction limit (5 min), with slack. */
export const SUBTITLE_PREPARING_BUDGET_MS = 8 * 60_000;
/** Backoff between polls of a track being prepared. */
export const SUBTITLE_PREPARING_DELAYS_MS: readonly number[] = [2_000, 4_000, 8_000, 15_000];
/** A transport failure after this long was the server holding the request
 *  for an extraction (and its connection then dropped), not the network or
 *  CORS refusing it, which fail at once. */
export const SUBTITLE_SLOW_FAILURE_MS = 20_000;

/** Gateway / server answers that mean "not ready yet": a proxy that timed
 *  out on a slow extraction (504, Cloudflare 524) or lost the origin
 *  mid-wait (502, Cloudflare 520), and a 503 from a server that is busy. Not
 *  the server's own 504 (see classifySubtitleFailure). */
export function isPreparingSubtitleStatus(status: number): boolean {
  return status === 502 || status === 503 || status === 504 || status === 520 || status === 524;
}

export type SubtitleFailure =
  /** A retry can't change it (415 image track, 404): give up now. */
  | 'final'
  /** The asset token in the URL expired: refresh, then a counted retry. */
  | 'unauthorized'
  /** Still being extracted: poll on within the preparing budget. */
  | 'preparing'
  /** Anything else: one of SUBTITLE_TRIES counted tries. */
  | 'retry';

/** What a failed try means. `elapsedMs` is how long the try took. */
export function classifySubtitleFailure(e: unknown, elapsedMs: number): SubtitleFailure {
  const status = typeof (e as { status?: unknown })?.status === 'number' ? (e as { status: number }).status : 0;
  if (status > 0) {
    if (isFinalSubtitleStatus(status)) return 'final';
    if (status === 401) return 'unauthorized';
    // The server's 504 SUBTITLE_PREPARING: the extraction failed, and every
    // request re-runs the whole-file demux. An ordinary counted try, not
    // eight minutes of polls.
    if ((e as { code?: unknown }).code === SUBTITLE_EXTRACTION_FAILED_CODE) return 'retry';
    return isPreparingSubtitleStatus(status) ? 'preparing' : 'retry';
  }
  // No HTTP answer. Our own per-try timeout aborted a request the server was
  // still holding; a connection that dropped only after a long wait was the
  // same thing ended by a proxy or the server's write deadline.
  const name = (e as { name?: unknown })?.name;
  if (name === 'AbortError' || name === 'TimeoutError') return 'preparing';
  return elapsedMs >= SUBTITLE_SLOW_FAILURE_MS ? 'preparing' : 'retry';
}

export type SubtitleLoad =
  | { kind: 'ok'; cues: Cue[] }
  /** A newer pick (or Off, or leaving) replaced this load: drop it. */
  | { kind: 'stale' }
  /** Gave up: the page says "Subtitles unavailable". */
  | { kind: 'failed' };

export interface SubtitleLoadOptions {
  tries?: number;
  delaysMs?: readonly number[];
  preparingBudgetMs?: number;
  preparingDelaysMs?: readonly number[];
  /** A 401 (the asset token in the URL expired mid-film): refresh it before
   *  the next try, which signs the URL afresh. */
  onUnauthorized?: () => Promise<unknown>;
  /** The clock (tests). */
  now?: () => number;
}

/**
 * Fetch and parse a track. Ordinary failures get `tries` tries with a short
 * backoff; "still preparing" answers (see classifySubtitleFailure) poll on
 * with a longer one until the preparing budget, counted from the first try,
 * runs out; a final answer gives up at once. The fetch generation is the
 * caller's `isCurrent`: a load that comes back after the user picked another
 * track (or Off) is dropped, never drawn over the newer choice. Never
 * rejects.
 */
export async function loadSubtitleCues(
  fetchText: () => Promise<string>,
  isCurrent: () => boolean,
  opts: SubtitleLoadOptions = {},
): Promise<SubtitleLoad> {
  const tries = opts.tries ?? SUBTITLE_TRIES;
  const delays = opts.delaysMs ?? SUBTITLE_RETRY_DELAYS_MS;
  const budget = opts.preparingBudgetMs ?? SUBTITLE_PREPARING_BUDGET_MS;
  const preparingDelays = opts.preparingDelaysMs ?? SUBTITLE_PREPARING_DELAYS_MS;
  const now = opts.now ?? Date.now;
  const started = now();
  let failures = 0;
  let polls = 0;
  for (;;) {
    const tryStarted = now();
    try {
      const text = await fetchText();
      if (!isCurrent()) return { kind: 'stale' };
      return { kind: 'ok', cues: parseWebVtt(text) };
    } catch (e) {
      if (!isCurrent()) return { kind: 'stale' };
      const failure = classifySubtitleFailure(e, now() - tryStarted);
      if (failure === 'final') break;
      let delay: number;
      if (failure === 'preparing') {
        delay = preparingDelays[Math.min(polls, preparingDelays.length - 1)] ?? 15_000;
        polls++;
        if (now() + delay - started > budget) break;
      } else {
        failures++;
        if (failures >= tries) break;
        if (failure === 'unauthorized' && opts.onUnauthorized) {
          try {
            await opts.onUnauthorized();
          } catch {
            /* the next try says what's left */
          }
        }
        delay = delays[Math.min(failures - 1, delays.length - 1)] ?? 1_000;
      }
      await sleep(delay);
      if (!isCurrent()) return { kind: 'stale' };
    }
  }
  return isCurrent() ? { kind: 'failed' } : { kind: 'stale' };
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}
