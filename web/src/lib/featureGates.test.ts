import { describe, expect, it } from 'vitest';
import type { CapabilitiesResponse, RequestQuota } from '$lib/api';
import {
  liveTVNavVisible,
  onlineSubtitlesVisible,
  requestsNavVisible,
  requestsUsable,
  upcomingTabVisible,
} from './featureGates';

const caps = (features: Partial<CapabilitiesResponse['features']>) =>
  ({ features: { requests: false, live_tv: true, dvr: true, subtitles_external: false, ...features } }) as CapabilitiesResponse;
const quota = (can_request: boolean): RequestQuota => ({
  can_request,
  window_days: 7,
  movies: { limit: 0, used: 0, remaining: null },
  tv: { limit: 0, used: 0, remaining: null },
});

describe('requestsUsable', () => {
  it('needs the server flag and, for non-admins, the account allowance', () => {
    expect(requestsUsable(caps({ requests: true }), quota(true), false)).toBe(true);
    expect(requestsUsable(caps({ requests: true }), quota(false), false)).toBe(false);
    expect(requestsUsable(caps({ requests: false }), quota(true), false)).toBe(false);
  });

  it('reads anything not yet known as off for non-admins', () => {
    expect(requestsUsable(null, quota(true), false)).toBe(false);
    expect(requestsUsable(caps({ requests: true }), undefined, false)).toBe(false); // loading
    expect(requestsUsable(caps({ requests: true }), null, false)).toBe(false); // failed
  });

  it('only asks the server flag for an admin', () => {
    expect(requestsUsable(caps({ requests: true }), null, true)).toBe(true);
    expect(requestsUsable(caps({ requests: false }), quota(true), true)).toBe(false);
  });
});

describe('requestsNavVisible', () => {
  it('always shows for admins', () => {
    expect(requestsNavVisible(null, undefined, true)).toBe(true);
    expect(requestsNavVisible(caps({ requests: false }), null, true)).toBe(true);
  });

  it('follows requestsUsable for everyone else', () => {
    expect(requestsNavVisible(caps({ requests: true }), quota(true), false)).toBe(true);
    expect(requestsNavVisible(caps({ requests: true }), quota(false), false)).toBe(false);
    expect(requestsNavVisible(caps({ requests: false }), quota(true), false)).toBe(false);
  });
});

describe('upcomingTabVisible', () => {
  it('needs requests and an enabled Radarr/Sonarr for non-admins', () => {
    expect(upcomingTabVisible(caps({ requests: true, upcoming: true }), quota(true), false)).toBe(true);
    expect(upcomingTabVisible(caps({ requests: true, upcoming: false }), quota(true), false)).toBe(false);
    expect(upcomingTabVisible(caps({ requests: false, upcoming: true }), quota(true), false)).toBe(false);
    expect(upcomingTabVisible(caps({ requests: true, upcoming: true }), quota(false), false)).toBe(false);
  });

  it('keeps the tab on a server older than features.upcoming', () => {
    expect(upcomingTabVisible(caps({ requests: true }), quota(true), false)).toBe(true);
  });

  it('always shows for admins', () => {
    expect(upcomingTabVisible(caps({ requests: false, upcoming: false }), null, true)).toBe(true);
  });
});

describe('liveTVNavVisible', () => {
  it('needs a configured tuner for non-admins', () => {
    expect(liveTVNavVisible(caps({ live_tv: true, live_tv_configured: true }), false)).toBe(true);
    expect(liveTVNavVisible(caps({ live_tv: true, live_tv_configured: false }), false)).toBe(false);
    expect(liveTVNavVisible(null, false)).toBe(false);
  });

  it('falls back to live_tv on a server older than live_tv_configured', () => {
    expect(liveTVNavVisible(caps({ live_tv: true }), false)).toBe(true);
    expect(liveTVNavVisible(caps({ live_tv: false }), false)).toBe(false);
  });

  it('always shows for admins', () => {
    expect(liveTVNavVisible(caps({ live_tv_configured: false }), true)).toBe(true);
    expect(liveTVNavVisible(null, true)).toBe(true);
  });
});

describe('onlineSubtitlesVisible', () => {
  it('follows features.subtitles_external, off when unknown', () => {
    expect(onlineSubtitlesVisible(caps({ subtitles_external: true }))).toBe(true);
    expect(onlineSubtitlesVisible(caps({ subtitles_external: false }))).toBe(false);
    expect(onlineSubtitlesVisible(null)).toBe(false);
  });
});
