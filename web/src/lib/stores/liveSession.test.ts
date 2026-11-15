// The layout syncs the live feeds on every navigation; they must start once
// per signed-in user, stop on sign-out and restart for a different user.
const n = vi.hoisted(() => ({ init: vi.fn(), stop: vi.fn() }));
const j = vi.hoisted(() => ({ start: vi.fn(), stop: vi.fn() }));
vi.mock('./notifications', () => ({ initNotifications: n.init, stopNotifications: n.stop }));
vi.mock('./jobs', () => ({ startJobsPolling: j.start, stopJobsPolling: j.stop }));

import { liveSessionUser, syncLiveSession } from './liveSession';

afterEach(() => {
  syncLiveSession(null);
  vi.clearAllMocks();
});

describe('syncLiveSession', () => {
  it('starts the feeds once for a signed-in user, however often it is synced', () => {
    syncLiveSession(null); // the initial mount before sign-in
    expect(n.init).not.toHaveBeenCalled();
    syncLiveSession('u1'); // the goto after sign-in
    syncLiveSession('u1'); // later navigations
    syncLiveSession('u1');
    expect(n.init).toHaveBeenCalledTimes(1);
    expect(j.start).toHaveBeenCalledTimes(1);
    expect(n.stop).not.toHaveBeenCalled();
    expect(liveSessionUser()).toBe('u1');
  });

  it('stops them on sign-out', () => {
    syncLiveSession('u1');
    syncLiveSession(null);
    expect(n.stop).toHaveBeenCalledTimes(1);
    expect(j.stop).toHaveBeenCalledTimes(1);
    expect(liveSessionUser()).toBeNull();
    syncLiveSession(null);
    expect(n.stop).toHaveBeenCalledTimes(1);
  });

  it('restarts them for a different user', () => {
    syncLiveSession('u1');
    syncLiveSession('u2');
    expect(n.stop).toHaveBeenCalledTimes(1);
    expect(j.stop).toHaveBeenCalledTimes(1);
    expect(n.init).toHaveBeenCalledTimes(2);
    expect(j.start).toHaveBeenCalledTimes(2);
    expect(liveSessionUser()).toBe('u2');
  });
});
