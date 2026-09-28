// "Report a problem" — pure helpers shared by the ReportProblemButton dialog
// and the admin Library health page. Server contract: POST/GET
// /items/{id}/issues (internal/api/v1/issues.go).

import type { IssueKind, MediaIssue } from './api';

export interface IssueKindOption {
  value: IssueKind;
  label: string;
}

/** The kinds in the order the dialog lists them, with user-facing wording. */
export const ISSUE_KIND_OPTIONS: readonly IssueKindOption[] = [
  { value: 'video', label: "Video won't play or looks wrong" },
  { value: 'audio', label: 'Audio problem' },
  { value: 'subtitles', label: 'Subtitles problem' },
  { value: 'wrong_match', label: 'Wrong movie/show' },
  { value: 'other', label: 'Something else' },
];

/** Must match issueNoteMaxRunes on the server (media_issues note CHECK). */
export const ISSUE_NOTE_MAX = 1000;

export function issueKindLabel(kind: string): string {
  return ISSUE_KIND_OPTIONS.find((o) => o.value === kind)?.label ?? 'Something else';
}

/** Characters left in a note. Counts code points like the server's
 *  char_length, so an emoji costs one, not two. */
export function noteRemaining(note: string): number {
  return ISSUE_NOTE_MAX - [...note.trim()].length;
}

/** "just now", "5 minutes ago", "yesterday", "2 days ago", "3 weeks ago". */
export function relativeAge(iso: string | undefined, now: number = Date.now()): string {
  if (!iso) return '';
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return '';
  const mins = Math.max(0, Math.floor((now - t) / 60_000));
  if (mins < 1) return 'just now';
  if (mins < 60) return `${mins} minute${mins === 1 ? '' : 's'} ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs} hour${hrs === 1 ? '' : 's'} ago`;
  const days = Math.floor(hrs / 24);
  if (days === 1) return 'yesterday';
  if (days < 14) return `${days} days ago`;
  const weeks = Math.floor(days / 7);
  if (days < 60) return `${weeks} weeks ago`;
  const months = Math.floor(days / 30);
  if (months < 12) return `${months} months ago`;
  const years = Math.floor(days / 365);
  return `${years} year${years === 1 ? '' : 's'} ago`;
}

/** Kinds the caller already has an open report for (the server refuses a
 *  second one with 409 ALREADY_REPORTED). */
export function openKinds(issues: readonly MediaIssue[]): Set<IssueKind> {
  return new Set(issues.filter((i) => i.status === 'open').map((i) => i.kind));
}

/** One line describing an earlier report, for the dialog's history block. */
export function describeIssue(issue: MediaIssue, now: number = Date.now()): string {
  const what = issueKindLabel(issue.kind);
  if (issue.status === 'open') {
    return `You reported “${what}” ${relativeAge(issue.created_at, now)}. An admin will take a look.`;
  }
  const verb = issue.status === 'resolved' ? 'Resolved' : 'Closed';
  const when = relativeAge(issue.resolved_at ?? issue.created_at, now);
  const note = issue.resolution_note ? ` — “${issue.resolution_note}”` : '';
  return `${verb} ${when}: “${what}”${note}`;
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
export function reportErrorMessage(err: unknown): string {
  const e = err as { status?: number; code?: string; message?: string } | null;
  switch (e?.code) {
    case 'ALREADY_REPORTED':
      return 'You already reported this problem — an admin will take a look.';
    case 'TOO_MANY_OPEN_ISSUES':
      return 'You have several reports waiting for an admin. Try again once they have been looked at.';
    case 'RATE_LIMITED':
      return 'Too many reports in a short time. Try again in a minute.';
  }
  if (e?.status === 404) return 'This title is no longer available.';
  return e?.message ? `Couldn't send the report: ${e.message}` : "Couldn't send the report.";
}
