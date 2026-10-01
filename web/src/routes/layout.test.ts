import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { tick } from 'svelte';
import { writable, type Writable } from 'svelte/store';
import { page } from '$app/stores';
import { api, authApi, type CapabilitiesResponse, type RequestQuota, type UserMeta } from '$lib/api';
import { audio } from '$lib/stores/audio';
import { initNotifications, playbackTransfers, stopNotifications } from '$lib/stores/notifications';
import { startJobsPolling } from '$lib/stores/jobs';
import Layout from './+layout.svelte';

const ALICE: UserMeta = { user_id: 'u1', username: 'alice', is_admin: false };

const mockGoto = vi.hoisted(() => vi.fn());
const mockItemGet = vi.hoisted(() => vi.fn());
const mockIsTauri = vi.hoisted(() => vi.fn(() => false));
const mockGetUser = vi.hoisted(() => vi.fn());
const mockRefreshSession = vi.hoisted(() => vi.fn());
const mockTrackAuthBootstrap = vi.hoisted(() => vi.fn(<T>(refresh: Promise<T>) => refresh));

// Svelte 5 components are plain functions; a no-op one stands in for the
// heavy children (player, notification bell, jobs banner) this test doesn't
// exercise.
const Stub = vi.hoisted(() => () => {});

vi.mock('$app/navigation', () => ({ goto: mockGoto }));
// Writable, so a test can land the layout on an SSO-callback URL.
vi.mock('$app/stores', async () => {
  const { writable } = await import('svelte/store');
  return {
    page: writable({ url: new URL('http://localhost/'), params: {}, route: { id: null }, status: 200, error: null, data: {}, form: null, state: {} }),
    navigating: writable(null),
    updated: { subscribe: writable(false).subscribe, check: () => Promise.resolve(false) },
  };
});
vi.mock('$lib/api', () => ({
  api: {
    getUser: mockGetUser,
    setUser: vi.fn(),
    getViewAs: () => null,
    setViewAs: vi.fn(),
    refreshSession: mockRefreshSession,
  },
  authApi: { setupStatus: vi.fn().mockResolvedValue({ setup_required: false }), logout: vi.fn() },
  userApi: { listSwitchable: vi.fn().mockResolvedValue([]), pinSwitch: vi.fn() },
  setApiBase: vi.fn(),
  setBearerToken: vi.fn(),
  trackAuthBootstrap: mockTrackAuthBootstrap,
  itemApi: { get: mockItemGet },
  getClientName: () => 'This Browser',
}));
vi.mock('$lib/native', () => ({
  isTauri: mockIsTauri,
  getServerUrl: vi.fn(),
  setServerUrl: vi.fn(),
  getStoredTokens: vi.fn().mockResolvedValue({}),
}));
vi.mock('$lib/stores/notifications', () => ({
  initNotifications: vi.fn(),
  stopNotifications: vi.fn(),
  playbackTransfers: writable(null),
  liveNotifications: writable(null),
}));
vi.mock('$lib/stores/pendingRequests', () => ({
  pendingRequestCount: writable(0),
  refreshPendingRequests: vi.fn(),
  resetPendingRequests: vi.fn(),
  changesPendingQueue: () => false,
  pendingBadgeText: (n: number) => String(n),
}));
vi.mock('$lib/stores/jobs', () => ({ startJobsPolling: vi.fn(), stopJobsPolling: vi.fn() }));
// Writable, so a test can set what the server advertises / allows.
const gateStores = vi.hoisted(() => ({}) as {
  capabilities: Writable<CapabilitiesResponse | null>;
  requestQuota: Writable<RequestQuota | null | undefined>;
});
vi.mock('$lib/stores/capabilities', async () => {
  const { writable } = await import('svelte/store');
  gateStores.capabilities = writable(null);
  return { capabilities: gateStores.capabilities, loadCapabilities: vi.fn().mockResolvedValue(undefined) };
});
const mockLoadQuota = vi.hoisted(() => vi.fn());
vi.mock('$lib/stores/requestAccess', async () => {
  const { writable } = await import('svelte/store');
  gateStores.requestQuota = writable(undefined);
  return { requestQuota: gateStores.requestQuota, loadRequestQuota: mockLoadQuota, resetRequestQuota: vi.fn() };
});
vi.mock('$lib/components/AudioPlayer.svelte', () => ({ default: Stub }));
vi.mock('$lib/components/NotificationBell.svelte', () => ({ default: Stub }));
vi.mock('$lib/components/NotificationPanel.svelte', () => ({ default: Stub }));
vi.mock('$lib/components/JobsBanner.svelte', () => ({ default: Stub }));

beforeEach(() => {
  vi.clearAllMocks();
  mockIsTauri.mockReturnValue(false);
  mockGetUser.mockImplementation(() => ALICE);
  mockRefreshSession.mockReset();
  playbackTransfers.set(null);
  gateStores.capabilities.set(null);
  gateStores.requestQuota.set(undefined);
});

async function renderShell() {
  render(Layout);
  // The shell (and the transfer subscription) come up once setup status resolves.
  return screen.findByRole('link', { name: /Audio/ });
}

const caps = (features: Partial<CapabilitiesResponse['features']>) =>
  ({ features: { requests: false, live_tv: true, dvr: true, ...features } }) as CapabilitiesResponse;
const quota = (can_request: boolean): RequestQuota => ({
  can_request,
  window_days: 7,
  movies: { limit: 0, used: 0, remaining: null },
  tv: { limit: 0, used: 0, remaining: null },
});

describe('layout sidebar', () => {
  it('links the Audio settings page in browsers, not only the desktop app', async () => {
    const link = await renderShell();
    expect(link.getAttribute('href')).toBe('/native/audio');
  });

  // The store-review server: no TMDB key, no tuners, a reviewer that can't
  // request. Neither link may show, nor flash up while things load.
  it('hides Requests and Live TV from a non-admin until the server says they apply', async () => {
    await renderShell();
    expect(screen.queryByRole('link', { name: /Requests/ })).toBeNull();
    expect(screen.queryByRole('link', { name: /Live TV/ })).toBeNull();
    expect(mockLoadQuota).toHaveBeenCalled();

    gateStores.capabilities.set(caps({ requests: false, live_tv_configured: false }));
    gateStores.requestQuota.set(quota(false));
    await tick();
    expect(screen.queryByRole('link', { name: /Requests/ })).toBeNull();
    expect(screen.queryByRole('link', { name: /Live TV/ })).toBeNull();

    // Requests on, but this account may not request: still hidden.
    gateStores.capabilities.set(caps({ requests: true, live_tv_configured: false }));
    await tick();
    expect(screen.queryByRole('link', { name: /Requests/ })).toBeNull();

    gateStores.requestQuota.set(quota(true));
    gateStores.capabilities.set(caps({ requests: true, live_tv_configured: true }));
    await tick();
    expect(screen.getByRole('link', { name: /Requests/ }).getAttribute('href')).toBe('/requests');
    expect(screen.getByRole('link', { name: /Live TV/ }).getAttribute('href')).toBe('/tv/guide');
  });

  it('falls back to live_tv on a server older than live_tv_configured', async () => {
    gateStores.capabilities.set(caps({ live_tv: true }));
    await renderShell();
    expect(screen.getByRole('link', { name: /Live TV/ })).toBeTruthy();
  });

  it('keeps both links for an admin', async () => {
    mockGetUser.mockImplementation(() => ({ ...ALICE, is_admin: true }));
    gateStores.capabilities.set(caps({ requests: false, live_tv_configured: false }));
    await renderShell();
    expect(screen.getByRole('link', { name: /Requests/ })).toBeTruthy();
    expect(screen.getByRole('link', { name: /Live TV/ })).toBeTruthy();
  });
});

describe('"Play on…" receiver', () => {
  it('starts transferred audio at the sent position', async () => {
    const play = vi.spyOn(audio, 'play').mockImplementation(() => {});
    mockItemGet.mockResolvedValue({
      id: 't1', type: 'track', title: 'Song', duration_ms: 240_000, files: [{ id: 'f1' }],
    });
    await renderShell();

    playbackTransfers.set({ itemId: 't1', positionMs: 93_500, targetClientName: 'This Browser' });

    // (The root layout never unsubscribes — it lives as long as the app — so
    // layouts rendered by earlier tests may answer too; every call must agree.)
    await waitFor(() => expect(play).toHaveBeenCalled());
    for (const [queue, index, startMS] of play.mock.calls) {
      expect(queue).toEqual([expect.objectContaining({ id: 't1', fileId: 'f1', title: 'Song' })]);
      expect(index).toBe(0);
      expect(startMS).toBe(93_500);
    }
    expect(mockGoto).not.toHaveBeenCalled();
    play.mockRestore();
  });

  it('opens video on the watch page at the sent position', async () => {
    mockItemGet.mockResolvedValue({ id: 'm1', type: 'movie', title: 'Film', files: [] });
    await renderShell();

    playbackTransfers.set({ itemId: 'm1', positionMs: 61_000, targetClientName: 'This Browser' });

    await waitFor(() => expect(mockGoto).toHaveBeenCalledWith('/watch/m1?at=61000'));
  });

  it('ignores transfers aimed at another device', async () => {
    await renderShell();
    playbackTransfers.set({ itemId: 'm1', positionMs: 5, targetClientName: 'Living Room TV' });
    await new Promise((r) => setTimeout(r, 20));
    expect(mockItemGet).not.toHaveBeenCalled();
  });
});

// SSO/SAML/OIDC callbacks land on "/?<x>_auth=1" with fresh cookies but no
// stored user; the layout learns who signed in through api.refreshSession
// (never a raw fetch — see +layout.svelte).
describe('SSO-callback bootstrap', () => {
  function land(search: string) {
    window.history.replaceState({}, '', '/' + search);
    (page as unknown as Writable<{ url: URL }>).update((p) => ({ ...p, url: new URL('http://localhost/' + search) }));
  }

  afterEach(() => land(''));

  const switchUserButton = () => screen.getByRole('button', { name: 'Switch user' });

  it('refreshes once, registers it for the home gate, and shows whoever it stored', async () => {
    land('?oidc_auth=1&keep=1');
    let stored: UserMeta | null = null;
    mockGetUser.mockImplementation(() => stored);
    mockRefreshSession.mockImplementation(async () => {
      stored = { user_id: 'u2', username: 'carol', is_admin: true };
      return true;
    });

    render(Layout);

    await waitFor(() => expect(switchUserButton().textContent).toContain('carol'));
    expect(screen.getByRole('link', { name: 'Settings' })).toBeTruthy();
    expect(mockRefreshSession).toHaveBeenCalledTimes(1);
    expect(mockTrackAuthBootstrap).toHaveBeenCalledWith(mockRefreshSession.mock.results[0].value);
    expect(initNotifications).toHaveBeenCalledTimes(1);
    expect(startJobsPolling).toHaveBeenCalledTimes(1);
    // Markers stripped (so a reload doesn't bootstrap again); the rest kept.
    expect(window.location.search).toBe('?keep=1');
  });

  it.each([
    ['resolves false', () => mockRefreshSession.mockResolvedValue(false)],
    ['rejects', () => mockRefreshSession.mockRejectedValue(new Error('auth refresh: timed out waiting for another tab'))],
  ])('sets nothing when the refresh %s, and still strips the markers', async (_name, arrange) => {
    land('?saml_auth=1&google_auth=1');
    mockGetUser.mockReturnValue(null);
    arrange();

    render(Layout);

    await waitFor(() => expect(window.location.search).toBe(''));
    expect(mockRefreshSession).toHaveBeenCalledTimes(1);
    expect(switchUserButton().textContent?.trim()).toBe('User');
    expect(screen.queryByRole('link', { name: 'Settings' })).toBeNull();
    expect(initNotifications).not.toHaveBeenCalled();
    expect(startJobsPolling).not.toHaveBeenCalled();
  });

  it('does not refresh on an ordinary load', async () => {
    await renderShell();
    expect(mockRefreshSession).not.toHaveBeenCalled();
    expect(mockTrackAuthBootstrap).not.toHaveBeenCalled();
  });
});

// A browser sign-out queues behind other tabs' session refreshes
// (api.revokeSession) and can take a while.
describe('Sign out', () => {
  it('shows "Signing out…" and ignores repeat clicks until the logout finishes, then goes to /login', async () => {
    let finish!: () => void;
    vi.mocked(authApi.logout).mockImplementationOnce(() => new Promise<void>((resolve) => { finish = resolve; }));
    await renderShell();
    const button = screen.getByRole('button', { name: 'Sign out' }) as HTMLButtonElement;

    // A second click straight after the first. Svelte has disabled the button
    // by then, and the DOM drops a click on a disabled button — that, not the
    // handler's `signingOut` guard, is what ignores it (the guard has its own
    // test below).
    void fireEvent.click(button);
    await fireEvent.click(button);

    expect(button.textContent?.trim()).toBe('Signing out…');
    expect(button.getAttribute('aria-label')).toBe('Signing out…');
    expect(button.disabled).toBe(true);
    expect(authApi.logout).toHaveBeenCalledTimes(1);
    expect(stopNotifications).toHaveBeenCalledTimes(1);
    // Still on the page, still signed in locally, while the logout runs.
    expect(mockGoto).not.toHaveBeenCalled();
    expect(api.setUser).not.toHaveBeenCalled();

    finish();
    await waitFor(() => expect(mockGoto).toHaveBeenCalledWith('/login'));
    expect(api.setUser).toHaveBeenCalledWith(null);
    // Usable again for the next session (the layout outlives the sign-out).
    await waitFor(() => expect(button.disabled).toBe(false));
    expect(button.textContent?.trim()).toBe('Sign out');
  });

  it('a click that gets past the disabled button anyway is ignored while the logout runs', async () => {
    let finish!: () => void;
    vi.mocked(authApi.logout).mockImplementationOnce(() => new Promise<void>((resolve) => { finish = resolve; }));
    await renderShell();
    const button = screen.getByRole('button', { name: 'Sign out' }) as HTMLButtonElement;

    await fireEvent.click(button);
    expect(button.disabled).toBe(true);
    // The attribute stripped behind Svelte's back (devtools, an extension):
    // the click now reaches the handler, and its own guard must hold.
    button.disabled = false;
    await fireEvent.click(button);

    expect(authApi.logout).toHaveBeenCalledTimes(1);
    expect(stopNotifications).toHaveBeenCalledTimes(1);
    expect(mockGoto).not.toHaveBeenCalled();

    finish();
    await waitFor(() => expect(mockGoto).toHaveBeenCalledTimes(1));
  });

  it('a logout that fails still signs out locally and goes to /login', async () => {
    vi.mocked(authApi.logout).mockRejectedValueOnce(new Error('Failed to fetch'));
    await renderShell();

    await fireEvent.click(screen.getByRole('button', { name: 'Sign out' }));

    await waitFor(() => expect(mockGoto).toHaveBeenCalledWith('/login'));
    expect(api.setUser).toHaveBeenCalledWith(null);
    expect(authApi.logout).toHaveBeenCalledTimes(1);
  });

  it('a logout that never settles is given up after 60 s: signs out locally, goes to /login, button usable again', async () => {
    vi.mocked(authApi.logout).mockImplementationOnce(() => new Promise<void>(() => {}));
    await renderShell();
    const button = screen.getByRole('button', { name: 'Sign out' }) as HTMLButtonElement;
    vi.useFakeTimers();
    try {
      await fireEvent.click(button);
      await vi.advanceTimersByTimeAsync(59_000);
      expect(button.disabled).toBe(true);
      expect(api.setUser).not.toHaveBeenCalled();
      expect(mockGoto).not.toHaveBeenCalled();

      await vi.advanceTimersByTimeAsync(1_000);
      expect(api.setUser).toHaveBeenCalledWith(null);
      expect(mockGoto).toHaveBeenCalledWith('/login');
      expect(button.disabled).toBe(false);
      expect(button.textContent?.trim()).toBe('Sign out');
    } finally {
      vi.useRealTimers();
    }
  });
});
