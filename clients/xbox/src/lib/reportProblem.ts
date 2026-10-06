// "Report a problem" — pure helpers behind the item page's report dialog.
// Server contract: POST / GET /api/v1/items/{id}/issues
// (internal/api/v1/issues.go). Wording mirrors the web client
// (web/src/lib/reportProblem.ts) and the Android TV client so a report reads
// the same everywhere. Kept identical between the Tizen and webOS apps.

import type { IssueKind, MediaIssue } from './api/types';

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

// Item pages that offer the report (the web item page's list).
const REPORTABLE_TYPES = new Set(['movie', 'episode', 'show', 'season']);

export function canReportProblem(itemType: string | null | undefined): boolean {
  return !!itemType && REPORTABLE_TYPES.has(itemType);
}

export function issueKindLabel(kind: string): string {
  return ISSUE_KIND_OPTIONS.find((o) => o.value === kind)?.label ?? 'Something else';
}

/** Characters left in a note. Counts code points like the server's
 *  char_length, so an emoji costs one, not two. */
export function noteRemaining(note: string): number {
  return ISSUE_NOTE_MAX - [...note.trim()].length;
}

/** The note cut to the server's limit (code points), for the on-screen
 *  keyboard which has no maxlength. */
export function clampNote(note: string): string {
  const chars = [...note];
  return chars.length > ISSUE_NOTE_MAX ? chars.slice(0, ISSUE_NOTE_MAX).join('') : note;
}

/** Kinds the caller already has an open report for — the server refuses a
 *  second one with 409 ALREADY_REPORTED, so the dialog marks them. */
export function openKinds(issues: readonly MediaIssue[]): Set<IssueKind> {
  return new Set(issues.filter((i) => i.status === 'open').map((i) => i.kind));
}

export const REPORT_SENT_TEXT = 'Thanks — an admin has been notified and will take a look.';
export const ALREADY_REPORTED_TEXT = 'You already reported this problem — an admin will take a look.';

/** Maps a failed create to a sentence for the dialog. */
export function reportErrorMessage(err: unknown): string {
  const e = err as { status?: number; code?: string; message?: string } | null;
  switch (e?.code) {
    case 'ALREADY_REPORTED':
      return ALREADY_REPORTED_TEXT;
    case 'TOO_MANY_OPEN_ISSUES':
      return 'You have several reports waiting for an admin. Try again once they have been looked at.';
    case 'RATE_LIMITED':
      return 'Too many reports in a short time. Try again in a minute.';
  }
  if (e?.status === 409) return ALREADY_REPORTED_TEXT;
  if (e?.status === 429) return 'Too many reports in a short time. Try again in a minute.';
  if (e?.status === 404) return 'This title is no longer available.';
  return e?.message ? `Couldn't send the report: ${e.message}` : "Couldn't send the report.";
}
