// Stream-type maths for the analytics page: normalising the per-day rows
// (including payloads from servers older than the direct-play /
// direct-stream split), resolving range totals, and turning counts into
// share-of-reported-plays percentages. Pulled out of +page.svelte so the
// rounding and fallback rules can be unit-tested without mounting the page.
//
// Pure module — no DOM, no Svelte store dependencies.

import type { DayStreamTypes, StreamTotals } from '$lib/api';
import { t, tn, type MessageKey, type PluralKey } from '$lib/i18n/index.svelte';

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

// The text fields are catalog keys (docs/i18n.md), translated where they
// are shown: templates use $i18n(seg.label) / $i18n.n(seg.count, n), so the
// labels follow a language change.
export interface StreamSegment<K extends StreamKey = StreamKey> {
  key: K;
  label: MessageKey;  // summary text
  legend: MessageKey; // legend text
  count: PluralKey;   // "{count} direct play", for tooltips
  hint: MessageKey;   // legend tooltip
  cls: string;        // colour class
}

export const NOT_REPORTED_NOTE: MessageKey = 'analytics.stream.notReportedNote';

/** The not-reported footnote in the active language. */
export function notReportedNote(): string {
  return t(NOT_REPORTED_NOTE);
}

/** The bits of a translator streamBreakdown needs: $i18n in a template. */
export interface Tx {
  (key: MessageKey): string;
  n: (key: PluralKey, count: number) => string;
}
const scriptTx: Tx = Object.assign((key: MessageKey) => t(key), {
  n: (key: PluralKey, count: number) => tn(key, count),
});

export const REPORTED_SEGMENTS: readonly StreamSegment<ReportedKey>[] = [
  {
    key: 'direct_play', label: 'analytics.stream.directPlay', legend: 'analytics.stream.directPlay',
    count: 'analytics.stream.countDirectPlay', hint: 'analytics.stream.directPlayHint', cls: 'direct-play',
  },
  {
    key: 'direct_stream', label: 'analytics.stream.directStream', legend: 'analytics.stream.directStreamLegend',
    count: 'analytics.stream.countDirectStream', hint: 'analytics.stream.directStreamHint', cls: 'direct-stream',
  },
  {
    key: 'transcode', label: 'analytics.stream.transcode', legend: 'analytics.stream.transcode',
    count: 'analytics.stream.countTranscode', hint: 'analytics.stream.transcodeHint', cls: 'transcode',
  },
];

// Stack order, bottom-up, shared by the day bars, the per-client bars and
// the legend so all three read the same way.
export const STREAM_SEGMENTS: readonly StreamSegment[] = [
  ...REPORTED_SEGMENTS,
  {
    key: 'unknown', label: 'analytics.stream.notReported', legend: 'analytics.stream.notReported',
    count: 'analytics.stream.countNotReported', hint: NOT_REPORTED_NOTE, cls: 'unknown',
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
// A legacy template passes $i18n so the text follows a language change;
// script code can leave it out.
export function streamBreakdown(c: StreamCounts, tx: Tx = scriptTx): string {
  return STREAM_SEGMENTS.map(s => tx.n(s.count, n(c[s.key]))).join(tx('analytics.stream.listSeparator'));
}
