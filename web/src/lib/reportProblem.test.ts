import { describe, expect, it } from 'vitest';
import type { MediaIssue } from './api';
import {
  ISSUE_KIND_OPTIONS,
  ISSUE_NOTE_MAX,
  describeIssue,
  issueKindLabel,
  noteRemaining,
  openKinds,
  relativeAge,
  reportErrorMessage,
  visibleHistory,
} from './reportProblem';

const NOW = Date.parse('2026-09-28T12:00:00Z');
const ago = (ms: number) => new Date(NOW - ms).toISOString();
const MIN = 60_000;
const DAY = 86_400_000;

function issue(p: Partial<MediaIssue>): MediaIssue {
  return { id: 'i', item_id: 'm', kind: 'video', status: 'open', created_at: ago(0), ...p };
}

describe('kind options', () => {
  it('lists the five kinds with friendly labels in dialog order', () => {
    expect(ISSUE_KIND_OPTIONS.map((o) => o.value)).toEqual(['video', 'audio', 'subtitles', 'wrong_match', 'other']);
    expect(issueKindLabel('video')).toBe("Video won't play or looks wrong");
    expect(issueKindLabel('wrong_match')).toBe('Wrong movie/show');
    expect(issueKindLabel('nonsense')).toBe('Something else');
  });
});

describe('noteRemaining', () => {
  it('counts code points and ignores surrounding whitespace', () => {
    expect(noteRemaining('')).toBe(ISSUE_NOTE_MAX);
    expect(noteRemaining('  abc  ')).toBe(ISSUE_NOTE_MAX - 3);
    expect(noteRemaining('😀😀')).toBe(ISSUE_NOTE_MAX - 2);
    expect(noteRemaining('x'.repeat(1001))).toBe(-1);
  });
});

describe('relativeAge', () => {
  it('reads naturally across scales', () => {
    expect(relativeAge(ago(10_000), NOW)).toBe('just now');
    expect(relativeAge(ago(MIN), NOW)).toBe('1 minute ago');
    expect(relativeAge(ago(5 * MIN), NOW)).toBe('5 minutes ago');
    expect(relativeAge(ago(3 * 60 * MIN), NOW)).toBe('3 hours ago');
    expect(relativeAge(ago(DAY + MIN), NOW)).toBe('yesterday');
    expect(relativeAge(ago(2 * DAY), NOW)).toBe('2 days ago');
    expect(relativeAge(ago(21 * DAY), NOW)).toBe('3 weeks ago');
    expect(relativeAge(ago(90 * DAY), NOW)).toBe('3 months ago');
    expect(relativeAge(ago(400 * DAY), NOW)).toBe('1 year ago');
    expect(relativeAge(undefined, NOW)).toBe('');
    expect(relativeAge('garbage', NOW)).toBe('');
    // Clock skew: a timestamp slightly in the future is "just now".
    expect(relativeAge(new Date(NOW + 5000).toISOString(), NOW)).toBe('just now');
  });
});

describe('history', () => {
  it('only open reports block a kind', () => {
    const kinds = openKinds([
      issue({ kind: 'video' }),
      issue({ kind: 'audio', status: 'resolved', resolved_at: ago(DAY) }),
    ]);
    expect([...kinds]).toEqual(['video']);
  });

  it('describes open and closed reports', () => {
    expect(describeIssue(issue({ created_at: ago(2 * DAY) }), NOW)).toBe(
      'You reported “Video won\'t play or looks wrong” 2 days ago. An admin will take a look.',
    );
    expect(
      describeIssue(
        issue({ kind: 'subtitles', status: 'resolved', resolved_at: ago(DAY + MIN), resolution_note: 'New subs added' }),
        NOW,
      ),
    ).toBe('Resolved yesterday: “Subtitles problem” — “New subs added”');
    expect(describeIssue(issue({ kind: 'other', status: 'dismissed', resolved_at: ago(5 * MIN) }), NOW)).toBe(
      'Closed 5 minutes ago: “Something else”',
    );
  });

  it('shows open reports and recently closed ones', () => {
    const list = [
      issue({ id: 'open-old', created_at: ago(200 * DAY) }),
      issue({ id: 'closed-recent', status: 'resolved', created_at: ago(40 * DAY), resolved_at: ago(3 * DAY) }),
      issue({ id: 'closed-old', status: 'dismissed', created_at: ago(90 * DAY), resolved_at: ago(45 * DAY) }),
    ];
    expect(visibleHistory(list, NOW).map((i) => i.id)).toEqual(['open-old', 'closed-recent']);
  });
});

describe('reportErrorMessage', () => {
  it('maps server codes to sentences', () => {
    expect(reportErrorMessage({ status: 409, code: 'ALREADY_REPORTED' })).toMatch(/already reported/);
    expect(reportErrorMessage({ status: 429, code: 'TOO_MANY_OPEN_ISSUES' })).toMatch(/several reports/);
    expect(reportErrorMessage({ status: 429, code: 'RATE_LIMITED' })).toMatch(/Try again in a minute/);
    expect(reportErrorMessage({ status: 404, code: 'NOT_FOUND' })).toBe('This title is no longer available.');
    expect(reportErrorMessage(new Error('HTTP 500'))).toBe("Couldn't send the report: HTTP 500");
    expect(reportErrorMessage(null)).toBe("Couldn't send the report.");
  });
});
