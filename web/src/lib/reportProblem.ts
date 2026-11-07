// "Report a problem" — pure helpers shared by the ReportProblemButton dialog
// and the admin Library health page. Server contract: POST/GET
// /items/{id}/issues (internal/api/v1/issues.go).
//
// Wording comes from the i18n catalog. The display helpers take the
// translator as an optional last argument: Svelte callers pass $i18n so the
// text re-renders when the language changes; it defaults to the current one.

import { get } from 'svelte/store';
import type { IssueKind, MediaIssue } from './api';
import { i18n, type MessageKey, type Translator } from '$lib/i18n/index.svelte';

export interface IssueKindOption {
  value: IssueKind;
  /** Catalog key of the user-facing wording, shown with $i18n(label). */
  label: MessageKey;
}

/** The kinds in the order the dialog lists them, with user-facing wording. */
export const ISSUE_KIND_OPTIONS: readonly IssueKindOption[] = [
  { value: 'video', label: 'ui.reportProblem.kind.video' },
  { value: 'audio', label: 'ui.reportProblem.kind.audio' },
  { value: 'subtitles', label: 'ui.reportProblem.kind.subtitles' },
  { value: 'wrong_match', label: 'ui.reportProblem.kind.wrong_match' },
  { value: 'other', label: 'ui.reportProblem.kind.other' },
];

/** Must match issueNoteMaxRunes on the server (media_issues note CHECK). */
export const ISSUE_NOTE_MAX = 1000;

export function issueKindLabel(kind: string, tx: Translator = get(i18n)): string {
  return tx(ISSUE_KIND_OPTIONS.find((o) => o.value === kind)?.label ?? 'ui.reportProblem.kind.other');
}

/** Characters left in a note. Counts code points like the server's
 *  char_length, so an emoji costs one, not two. */
export function noteRemaining(note: string): number {
  return ISSUE_NOTE_MAX - [...note.trim()].length;
}

/** "just now", "5 minutes ago", "yesterday", "2 days ago", "3 weeks ago". */
export function relativeAge(iso: string | undefined, now: number = Date.now(), tx: Translator = get(i18n)): string {
  if (!iso) return '';
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return '';
  const mins = Math.max(0, Math.floor((now - t) / 60_000));
  if (mins < 1) return tx('ui.relative.justNow');
  if (mins < 60) return tx.n('ui.relative.minutes', mins);
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return tx.n('ui.relative.hours', hrs);
  const days = Math.floor(hrs / 24);
  if (days === 1) return tx('ui.relative.yesterday');
  if (days < 14) return tx.n('ui.relative.days', days);
  const weeks = Math.floor(days / 7);
  if (days < 60) return tx.n('ui.relative.weeks', weeks);
  const months = Math.floor(days / 30);
  if (months < 12) return tx.n('lib.reportProblem.monthsAgo', months);
  const years = Math.floor(days / 365);
  return tx.n('lib.reportProblem.yearsAgo', years);
}

/** Kinds the caller already has an open report for (the server refuses a
 *  second one with 409 ALREADY_REPORTED). */
export function openKinds(issues: readonly MediaIssue[]): Set<IssueKind> {
  return new Set(issues.filter((i) => i.status === 'open').map((i) => i.kind));
}

/** One line describing an earlier report, for the dialog's history block. */
export function describeIssue(issue: MediaIssue, now: number = Date.now(), tx: Translator = get(i18n)): string {
  const kind = issueKindLabel(issue.kind, tx);
  if (issue.status === 'open') {
    return tx('lib.reportProblem.reported', { kind, when: relativeAge(issue.created_at, now, tx) });
  }
  const when = relativeAge(issue.resolved_at ?? issue.created_at, now, tx);
  const resolved = issue.status === 'resolved';
  if (issue.resolution_note) {
    const params = { kind, when, note: issue.resolution_note };
    return resolved ? tx('lib.reportProblem.resolvedNote', params) : tx('lib.reportProblem.closedNote', params);
  }
  return resolved ? tx('lib.reportProblem.resolved', { kind, when }) : tx('lib.reportProblem.closed', { kind, when });
}

/** The earlier reports worth showing: every open one plus closed ones from
 *  the last 30 days, newest first (the server already sorts). */
export function visibleHistory(issues: readonly MediaIssue[], now: number = Date.now()): MediaIssue[] {
  const cutoff = now - 30 * 86_400_000;
  return issues.filter((i) => {
    if (i.status === 'open') return true;
    const t = Date.parse(i.resolved_at ?? i.created_at);
    return !Number.isNaN(t) && t >= cutoff;
  });
}

/** Maps a failed create to a sentence for the dialog. */
export function reportErrorMessage(err: unknown, tx: Translator = get(i18n)): string {
  const e = err as { status?: number; code?: string; message?: string } | null;
  switch (e?.code) {
    case 'ALREADY_REPORTED':
      return tx('lib.reportProblem.alreadyReported');
    case 'TOO_MANY_OPEN_ISSUES':
      return tx('lib.reportProblem.tooManyOpen');
    case 'RATE_LIMITED':
      return tx('lib.reportProblem.rateLimited');
  }
  if (e?.status === 404) return tx('lib.reportProblem.gone');
  return e?.message
    ? tx('lib.reportProblem.sendFailedDetail', { error: e.message })
    : tx('lib.reportProblem.sendFailed');
}
