import { describe, expect, it } from 'vitest';
import { notificationHref, notificationKind, NOTIFICATION_ICONS } from './notificationDisplay';

describe('notificationKind', () => {
  it('gives every request type its own presentation', () => {
    expect(notificationKind('request_created')).toBe('request_waiting');
    expect(notificationKind('request_pending')).toBe('request_pending');
    expect(notificationKind('request_approved')).toBe('request_approved');
    expect(notificationKind('request_available')).toBe('request_available');
    expect(notificationKind('request_declined')).toBe('request_problem');
    expect(notificationKind('request_failed')).toBe('request_problem');
  });

  it('keeps the existing kinds and falls back sensibly', () => {
    expect(notificationKind('new_content')).toBe('new_content');
    expect(notificationKind('scan_complete')).toBe('scan_complete');
    expect(notificationKind('system')).toBe('other');
    expect(notificationKind('request_something_new')).toBe('request_waiting');
    expect(notificationKind(undefined)).toBe('other');
  });

  it('has an icon for every kind', () => {
    for (const t of ['request_created', 'request_pending', 'request_approved', 'request_available',
      'request_declined', 'request_failed', 'new_content', 'scan_complete', 'system']) {
      expect(NOTIFICATION_ICONS[notificationKind(t)]).toMatch(/^M/);
    }
  });
});

describe('notificationHref', () => {
  it('opens the fulfilled title for request_available with an item', () => {
    expect(notificationHref({ type: 'request_available', item_id: 'item-1' })).toBe('/watch/item-1');
  });

  it('opens the Requests page for other request notices (and available without an item)', () => {
    for (const type of ['request_created', 'request_pending', 'request_approved', 'request_declined', 'request_failed']) {
      expect(notificationHref({ type })).toBe('/requests');
    }
    expect(notificationHref({ type: 'request_available' })).toBe('/requests');
  });

  it('keeps item links for other notices and nothing otherwise', () => {
    expect(notificationHref({ type: 'new_content', item_id: 'm1' })).toBe('/watch/m1');
    expect(notificationHref({ type: 'scan_complete' })).toBeNull();
  });
});
