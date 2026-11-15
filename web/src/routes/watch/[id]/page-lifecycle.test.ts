// The watch page outlives a title (next episode, a pick from the page) and
// can be left at any await. Late answers for a title the viewer has left
// must not land on the next one, and a transcode session that starts that
// late must be stopped rather than orphaned.
import { render, screen, waitFor } from '@testing-library/svelte';
import { tick } from 'svelte';
import type { Writable } from 'svelte/store';

const mockGet = vi.hoisted(() => vi.fn());
const mockStart = vi.hoisted(() => vi.fn());
const mockStop = vi.hoisted(() => vi.fn());
const mockBeacon = vi.hoisted(() => vi.fn());

vi.mock('$app/navigation', () => ({ goto: vi.fn() }));
vi.mock('$lib/stores/page.svelte', async () => {
  const { writable } = await import('svelte/store');
  return {
    page: writable({ url: new URL('http://localhost/watch/ep-1'), params: { id: 'ep-1' }, state: {} }),
  };
});
vi.mock('$lib/api', () => ({
  itemApi: {
    get: mockGet,
    children: vi.fn().mockResolvedValue({ items: [], total: 0 }),
    upNext: vi.fn().mockRejectedValue(new Error('none')),
    getWatchStatus: vi.fn().mockRejectedValue(new Error('404')),
  },
  peopleApi: { credits: vi.fn().mockResolvedValue([]) },
  userApi: {
    getPreferences: vi.fn().mockResolvedValue({}),
    getMyWatchLimit: vi.fn().mockResolvedValue({ allowed: true }),
  },
  mediaApi: {},
  libraryApi: {},
  // No verdict: the local decision remuxes E-AC-3 in Matroska, so Start is asked.
  transcodeApi: { decide: vi.fn().mockRejectedValue(new Error('offline')), start: mockStart, stop: mockStop },
  subtitleApi: {},
  assetUrl: (p: string) => p,
  apiBeacon: mockBeacon,
  ApiRequestError: class ApiRequestError extends Error {
    constructor(message: string, readonly status: number, readonly code: string) {
      super(message);
    }
  },
}));
vi.mock('$lib/stores/toast', () => ({ toast: { success: vi.fn(), error: vi.fn(), info: vi.fn() } }));

import { page } from '$lib/stores/page.svelte';
import Page from './+page.svelte';

const pageStore = page as unknown as Writable<{ url: URL; params: { id: string }; state: object }>;
function goTo(id: string) {
  pageStore.set({ url: new URL(`http://localhost/watch/${id}`), params: { id }, state: {} });
}

function episode(id: string, title: string) {
  return {
    id, library_id: 'lib', title, type: 'episode', genres: [],
    view_offset_ms: 0, updated_at: 1, is_favorite: false, duration_ms: 600000,
    files: [{
      id: `f-${id}`, stream_url: `/media/stream/f-${id}`, container: 'mkv', video_codec: 'h264',
      audio_codec: 'eac3', resolution_w: 1920, resolution_h: 1080, duration_ms: 600000,
      faststart: true,
    }],
  };
}
function movie(id: string, title: string) {
  return {
    id, library_id: 'lib', title, type: 'movie', genres: [],
    view_offset_ms: 0, updated_at: 1, is_favorite: false, duration_ms: 600000, files: [],
  };
}
function session(n: number) {
  return { session_id: `s${n}`, token: `tk${n}`, playlist_url: `/hls/s${n}/index.m3u8`, start_offset_sec: 0 };
}
function deferred<T>() {
  let resolve!: (v: T) => void;
  const promise = new Promise<T>((r) => { resolve = r; });
  return { promise, resolve };
}
async function settle() {
  for (let i = 0; i < 5; i++) {
    await new Promise((r) => setTimeout(r, 0));
    await tick();
  }
}

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.setItem('onscreen_user', JSON.stringify({ user_id: 'u1', username: 'viewer' }));
  goTo('ep-1');
  mockGet.mockImplementation((id: string) => Promise.resolve(episode(id, `Title ${id}`)));
  mockStart.mockReturnValue(new Promise(() => {}));
  mockStop.mockResolvedValue(undefined);
});

describe('a transcode start that lands late', () => {
  it('is stopped when the viewer left the page meanwhile', async () => {
    const start = deferred<ReturnType<typeof session>>();
    mockStart.mockReturnValueOnce(start.promise);
    const { unmount } = render(Page);
    await waitFor(() => expect(mockStart).toHaveBeenCalledTimes(1));
    unmount();
    start.resolve(session(1));
    await waitFor(() => expect(mockStop).toHaveBeenCalledWith('s1', 'tk1'));
  });

  it('is stopped, not attached, when the viewer moved to the next episode', async () => {
    const first = deferred<ReturnType<typeof session>>();
    const second = deferred<ReturnType<typeof session>>();
    mockStart.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
    const { unmount } = render(Page);
    await waitFor(() => expect(mockStart).toHaveBeenCalledTimes(1));
    expect(mockStart.mock.calls[0][0]).toBe('ep-1');

    goTo('ep-2');
    // The next episode starts its own stream: the first start in flight
    // used to swallow it (switchingTranscode) and attach under ep-2.
    await waitFor(() => expect(mockStart).toHaveBeenCalledTimes(2));
    expect(mockStart.mock.calls[1][0]).toBe('ep-2');

    first.resolve(session(1));
    await waitFor(() => expect(mockStop).toHaveBeenCalledWith('s1', 'tk1'));
    second.resolve(session(2));
    await settle();
    expect(mockStop).not.toHaveBeenCalledWith('s2', 'tk2');
    unmount();
  });
});

describe('moving between titles', () => {
  it("never shows a title whose answer lands after the next one's", async () => {
    const late = deferred<ReturnType<typeof movie>>();
    mockGet.mockImplementation((id: string) => {
      if (id === 'm-1') return late.promise;
      return Promise.resolve(movie(id, id === 'm-0' ? 'Opening Night' : 'Second Feature'));
    });
    goTo('m-0');
    render(Page);
    expect(await screen.findByText('Opening Night')).toBeTruthy();

    goTo('m-1');
    await settle();
    goTo('m-2');
    expect(await screen.findByText('Second Feature')).toBeTruthy();

    late.resolve(movie('m-1', 'First Feature'));
    await settle();
    expect(screen.queryByText('First Feature')).toBeNull();
    expect(screen.getByText('Second Feature')).toBeTruthy();
  });
});

describe('player shortcuts', () => {
  it('leave keys typed into a field alone', async () => {
    render(Page);
    await waitFor(() => expect(mockStart).toHaveBeenCalled());
    const input = document.createElement('input');
    document.body.appendChild(input);
    try {
      const typed = new KeyboardEvent('keydown', { key: ' ', bubbles: true, cancelable: true });
      input.dispatchEvent(typed);
      expect(typed.defaultPrevented).toBe(false);
      // The same key outside a field is play/pause.
      const shortcut = new KeyboardEvent('keydown', { key: ' ', bubbles: true, cancelable: true });
      document.body.dispatchEvent(shortcut);
      expect(shortcut.defaultPrevented).toBe(true);
    } finally {
      input.remove();
    }
  });
});

describe('closing the tab', () => {
  it('ends the transcode session with a keepalive request', async () => {
    mockStart.mockResolvedValueOnce(session(1));
    render(Page);
    await waitFor(() => expect(mockStart).toHaveBeenCalled());
    await settle();
    window.dispatchEvent(new Event('pagehide'));
    expect(mockBeacon).toHaveBeenCalledWith('DELETE', '/transcode/sessions/s1?token=tk1');
  });
});

describe('the Cast button', () => {
  const ctx = {
    setOptions: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    getCurrentSession: () => null,
  };
  const w = window as any;

  beforeEach(() => {
    w.cast = {
      framework: {
        CastContext: { getInstance: () => ctx },
        CastContextEventType: { SESSION_STATE_CHANGED: 'sessionstatechanged' },
      },
    };
    w.chrome = { cast: { media: { DEFAULT_MEDIA_RECEIVER_APP_ID: 'CC1AD845' }, AutoJoinPolicy: { ORIGIN_SCOPED: 'origin_scoped' } } };
  });
  afterEach(() => {
    for (const k of ['cast', 'chrome', '__onGCastApiAvailable', '__onscreenCastAvailable', '__onscreenCastConfigured']) delete w[k];
    document.querySelectorAll('script[data-cast-sdk]').forEach((s) => s.remove());
  });

  it('is set up on every visit, not only the first of the page load, and detached on leave', async () => {
    const first = render(Page);
    // The SDK reports once per page load.
    w.__onGCastApiAvailable(true);
    expect(ctx.addEventListener).toHaveBeenCalledTimes(1);
    const listener = ctx.addEventListener.mock.calls[0][1];
    first.unmount();
    expect(ctx.removeEventListener).toHaveBeenCalledWith('sessionstatechanged', listener);

    // A later visit: no second callback from the SDK.
    const second = render(Page);
    await settle();
    expect(ctx.addEventListener).toHaveBeenCalledTimes(2);
    expect(ctx.setOptions).toHaveBeenCalledTimes(1);
    second.unmount();
  });
});
