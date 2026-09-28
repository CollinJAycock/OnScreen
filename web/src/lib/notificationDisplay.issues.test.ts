import { describe, expect, it } from 'vitest';
import { notificationHref, notificationKind, NOTIFICATION_ICONS } from './notificationDisplay';

// "Report a problem" notices (internal/notification/types.go: issue_reported,
// issue_resolved).
describe('issue notifications', () => {
  it('have their own kinds and icons', () => {
    expect(notificationKind('issue_reported')).toBe('issue_reported');
    expect(notificationKind('issue_resolved')).toBe('issue_resolved');
    expect(NOTIFICATION_ICONS.issue_reported).toMatch(/^M/);
    expect(NOTIFICATION_ICONS.issue_resolved).toMatch(/^M/);
  });

  it('send admins to Library health and reporters to the item', () => {
    expect(notificationHref({ type: 'issue_reported', item_id: 'm1' })).toBe('/settings/library-health');
    expect(notificationHref({ type: 'issue_resolved', item_id: 'm1' })).toBe('/watch/m1');
    expect(notificationHref({ type: 'issue_resolved' })).toBeNull();
  });
});
