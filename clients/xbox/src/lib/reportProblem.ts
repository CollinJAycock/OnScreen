// "Report a problem" — pure helpers behind the item page's report dialog.
// Server contract: POST / GET /api/v1/items/{id}/issues
// (internal/api/v1/issues.go). Wording mirrors the web client
// (web/src/lib/reportProblem.ts) and the Android TV client so a report reads
// the same everywhere. Kept identical between the Tizen and webOS apps. The
// wording is in the app's language (lib/i18n), read each time it's shown.

import { t, type MessageKey } from './i18n/index.svelte';
import type { IssueKind, MediaIssue } from './api/types';

export interface IssueKindOption {
  value: IssueKind;
  /** In the app's language: read when shown, never kept (lib/i18n). */
  label: string;
}

function kindOption(value: IssueKind, key: MessageKey): IssueKindOption {
  return Object.defineProperty({ value }, 'label', { get: () => t(key), enumerable: true }) as IssueKindOption;
}

/** The kinds in the order the dialog lists them, with user-facing wording. */
export const ISSUE_KIND_OPTIONS: readonly IssueKindOption[] = [
  kindOption('video', 'report.kindVideo'),
  kindOption('audio', 'report.kindAudio'),
  kindOption('subtitles', 'report.kindSubtitles'),
  kindOption('wrong_match', 'report.kindWrongMatch'),
  kindOption('other', 'report.kindOther'),
];

/** Must match issueNoteMaxRunes on the server (media_issues note CHECK). */
export const ISSUE_NOTE_MAX = 1000;

// Item pages that offer the report (the web item page's list).
const REPORTABLE_TYPES = new Set(['movie', 'episode', 'show', 'season']);

export function canReportProblem(itemType: string | null | undefined): boolean {
  return !!itemType && REPORTABLE_TYPES.has(itemType);
}

export function issueKindLabel(kind: string): string {
  return ISSUE_KIND_OPTIONS.find((o) => o.value === kind)?.label ?? t('report.kindOther');
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

/** The dialog's sent and already-reported lines, as message keys (lib/i18n):
 *  t(REPORT_SENT_TEXT) when shown. */
export const REPORT_SENT_TEXT: MessageKey = 'report.sent';
export const ALREADY_REPORTED_TEXT: MessageKey = 'report.alreadyReported';

/** Maps a failed create to a sentence for the dialog. */
export function reportErrorMessage(err: unknown): string {
  const e = err as { status?: number; code?: string; message?: string } | null;
  switch (e?.code) {
    case 'ALREADY_REPORTED':
      return t(ALREADY_REPORTED_TEXT);
    case 'TOO_MANY_OPEN_ISSUES':
      return t('report.tooManyOpen');
    case 'RATE_LIMITED':
      return t('report.rateLimited');
  }
  if (e?.status === 409) return t(ALREADY_REPORTED_TEXT);
  if (e?.status === 429) return t('report.rateLimited');
  if (e?.status === 404) return t('report.gone');
  // The server's own message stays as it came (docs/i18n.md: stage 5).
  return e?.message ? t('report.sendFailedDetail', { error: e.message }) : t('report.sendFailed');
}
