import { render, screen, waitFor } from '@testing-library/svelte';
import { writable } from 'svelte/store';
import { audio } from '$lib/stores/audio';
import { playbackTransfers } from '$lib/stores/notifications';
import Layout from './+layout.svelte';

const mockGoto = vi.hoisted(() => vi.fn());
const mockItemGet = vi.hoisted(() => vi.fn());
const mockIsTauri = vi.hoisted(() => vi.fn(() => false));

// Svelte 5 components are plain functions; a no-op one stands in for the
// heavy children (player, notification bell, jobs banner) this test doesn't
// exercise.
const Stub = vi.hoisted(() => () => {});

vi.mock('$app/navigation', () => ({ goto: mockGoto }));
vi.mock('$lib/api', () => ({
  api: {
    getUser: () => ({ user_id: 'u1', username: 'alice', is_admin: false }),
    setUser: vi.fn(),
    getViewAs: () => null,
    setViewAs: vi.fn(),
  },
  authApi: { setupStatus: vi.fn().mockResolvedValue({ setup_required: false }), logout: vi.fn() },
  userApi: { listSwitchable: vi.fn().mockResolvedValue([]), pinSwitch: vi.fn() },
  setApiBase: vi.fn(),
  setBearerToken: vi.fn(),
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
vi.mock('$lib/stores/capabilities', () => ({ loadCapabilities: vi.fn().mockResolvedValue(undefined) }));
vi.mock('$lib/components/AudioPlayer.svelte', () => ({ default: Stub }));
vi.mock('$lib/components/NotificationBell.svelte', () => ({ default: Stub }));
vi.mock('$lib/components/NotificationPanel.svelte', () => ({ default: Stub }));
vi.mock('$lib/components/JobsBanner.svelte', () => ({ default: Stub }));

beforeEach(() => {
  vi.clearAllMocks();
  mockIsTauri.mockReturnValue(false);
  playbackTransfers.set(null);
});

async function renderShell() {
  render(Layout);
  // The shell (and the transfer subscription) come up once setup status resolves.
  return screen.findByRole('link', { name: /Audio/ });
}

describe('layout sidebar', () => {
  it('links the Audio settings page in browsers, not only the desktop app', async () => {
    const link = await renderShell();
    expect(link.getAttribute('href')).toBe('/native/audio');
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
