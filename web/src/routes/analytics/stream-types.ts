// Stream-type maths for the analytics page: normalising the per-day rows
// (including payloads from servers older than the direct-play /
// direct-stream split), resolving range totals, and turning counts into
// share-of-reported-plays percentages. Pulled out of +page.svelte so the
// rounding and fallback rules can be unit-tested without mounting the page.
//
// Pure module — no DOM, no Svelte store dependencies.

import type { DayStreamTypes, StreamTotals } from '$lib/api';

export interface StreamCounts {
  direct_play: number;
  direct_stream: number; // directStream + remux
  transcode: number;
  unknown: number;       // no decision attributed to the play
}

export interface StreamDay extends StreamCounts {
  date: string;
}

export type ReportedKey = 'direct_play' | 'direct_stream' | 'transcode';
export type StreamKey = ReportedKey | 'unknown';

export interface StreamSegment<K extends StreamKey = StreamKey> {
  key: K;
  label: string;  // summary text
  legend: string; // legend text
  phrase: string; // tooltip text ("3 direct play, …")
  hint: string;   // legend tooltip
  cls: string;    // colour class
}

export const NOT_REPORTED_NOTE =
  'Not reported: plays recorded before the server attributed stream types (older history), ' +
  'or from a client that didn’t report one.';

export const REPORTED_SEGMENTS: readonly StreamSegment<ReportedKey>[] = [
  {
    key: 'direct_play', label: 'Direct play', legend: 'Direct play', phrase: 'direct play',
    hint: 'Direct play: the original file, served as-is.', cls: 'direct-play',
  },
  {
    key: 'direct_stream', label: 'Direct stream', legend: 'Direct stream (remux)', phrase: 'direct stream',
    hint: 'Direct stream / remux: the video is copied untouched into a new container; only audio may be converted.',
    cls: 'direct-stream',
  },
  {
    key: 'transcode', label: 'Transcode', legend: 'Transcode', phrase: 'transcode',
    hint: 'Transcode: the server re-encoded the video.', cls: 'transcode',
  },
];

// Stack order, bottom-up, shared by the day bars, the per-client bars and
// the legend so all three read the same way.
export const STREAM_SEGMENTS: readonly StreamSegment[] = [
  ...REPORTED_SEGMENTS,
  {
    key: 'unknown', label: 'Not reported', legend: 'Not reported', phrase: 'not reported',
    hint: NOT_REPORTED_NOTE, cls: 'unknown',
  },
];

export function emptyCounts(): StreamCounts {
  return { direct_play: 0, direct_stream: 0, transcode: 0, unknown: 0 };
}

// Coerce a possibly-missing / non-numeric count to a finite, non-negative
// number so a malformed row can't poison a sum with NaN.
function n(v: unknown): number {
  const x = Number(v);
  return Number.isFinite(x) && x > 0 ? x : 0;
}

// normalizeStreamDay maps a server row onto the four-way split. A server
// from before the split only sends the combined `direct`; that is shown as
// direct play rather than dropped, so the bar heights stay truthful.
export function normalizeStreamDay(raw: DayStreamTypes): StreamDay {
  const split = raw.direct_play != null || raw.direct_stream != null;
  return {
    date: raw.date,
    direct_play: split ? n(raw.direct_play) : n(raw.direct),
    direct_stream: n(raw.direct_stream),
    transcode: n(raw.transcode),
    unknown: n(raw.unknown),
  };
}

export function countTotal(c: StreamCounts): number {
  return n(c.direct_play) + n(c.direct_stream) + n(c.transcode) + n(c.unknown);
}

export function sumCounts(rows: StreamCounts[]): StreamCounts {
  const out = emptyCounts();
  for (const r of rows) {
    out.direct_play += n(r.direct_play);
    out.direct_stream += n(r.direct_stream);
    out.transcode += n(r.transcode);
    out.unknown += n(r.unknown);
  }
  return out;
}

// resolveTotals prefers the server's stream_totals; an older server lacks
// it, so fall back to summing the (already normalised) day rows, which
// cover the same window.
export function resolveTotals(totals: StreamTotals | null | undefined, days: StreamDay[]): StreamCounts {
  if (!totals) return sumCounts(days);
  return {
    direct_play: n(totals.direct_play),
    direct_stream: n(totals.direct_stream),
    transcode: n(totals.transcode),
    unknown: n(totals.unknown),
  };
}

// sharePercents turns counts into whole percentages that always sum to
// exactly 100 (largest-remainder rounding — independent Math.round can
// land on 99 or 101). Null when there is nothing to share out.
export function sharePercents(parts: number[]): number[] | null {
  const vals = parts.map(n);
  const total = vals.reduce((s, v) => s + v, 0);
  if (total <= 0) return null;
  const exact = vals.map(v => (v / total) * 100);
  const out = exact.map(Math.floor);
  let short = 100 - out.reduce((s, v) => s + v, 0);
  const byRemainder = exact
    .map((e, i) => ({ i, r: e - out[i] }))
    .sort((a, b) => b.r - a.r || a.i - b.i);
  for (const { i } of byRemainder) {
    if (short <= 0) break;
    out[i]++;
    short--;
  }
  return out;
}

// Whole percentages of `reported` per decision, summing to 100.
export type ReportedShares = Record<ReportedKey, number> & {
  reported: number; // plays with a known decision
};

// reportedShares is the headline split. Not-reported plays are excluded
// from the denominator — they say nothing about how media was delivered —
// and surfaced separately as a count. Null when no play in range reported.
export function reportedShares(c: StreamCounts): ReportedShares | null {
  const pcts = sharePercents([c.direct_play, c.direct_stream, c.transcode]);
  if (!pcts) return null;
  return {
    reported: n(c.direct_play) + n(c.direct_stream) + n(c.transcode),
    direct_play: pcts[0],
    direct_stream: pcts[1],
    transcode: pcts[2],
  };
}

// fmtShare renders a summary percentage: '—' with nothing reported, and
// '<1%' rather than a misleading '0%' when a category has plays that round
// away to nothing.
export function fmtShare(pct: number | null | undefined, count: number): string {
  if (pct == null) return '—';
  if (pct === 0 && count > 0) return '<1%';
  return `${pct}%`;
}

// segmentWidth is a segment's share of its bar in percent (unrounded, so
// the segments of a bar always fill it exactly). 0 for an empty bar.
export function segmentWidth(val: number, total: number): number {
  return total > 0 ? (n(val) / total) * 100 : 0;
}

// streamBreakdown is the shared tooltip / aria text:
// "3 direct play, 1 direct stream, 2 transcode, 4 not reported".
export function streamBreakdown(c: StreamCounts): string {
  return STREAM_SEGMENTS.map(s => `${n(c[s.key]).toLocaleString()} ${s.phrase}`).join(', ');
}
