import { notificationHref, notificationKind } from './notificationDisplay';

describe('request_season_available', () => {
  it('looks like request_available and opens the show', () => {
    expect(notificationKind('request_season_available')).toBe('request_available');
    expect(notificationHref({ type: 'request_season_available', item_id: 'show-1' })).toBe('/watch/show-1');
    expect(notificationHref({ type: 'request_season_available', item_id: null })).toBe('/requests');
  });
});
