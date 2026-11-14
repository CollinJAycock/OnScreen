// The play decision runs transcode Start's own gates on the server (the
// parental watch limit, an admin stop). A refusal there must end at the same
// message Start's would: the player used to fall back to its local guess,
// which met the refusal one call later — or, for a direct play, as a bare
// media error.
import { render, screen, waitFor } from '@testing-library/svelte';
import { readable } from 'svelte/store';

const mockGet = vi.hoisted(() => vi.fn());
const mockDecide = vi.hoisted(() => vi.fn());
const mockStart = vi.hoisted(() => vi.fn());

vi.mock('$app/navigation', () => ({ goto: vi.fn() }));
vi.mock('$lib/stores/page.svelte', () => ({
  page: {
    subscribe: (fn: (v: unknown) => void) =>
      readable({ url: new URL('http://localhost/watch/ep-1'), params: { id: 'ep-1' }, state: {} }).subscribe(fn),
  },
}));
vi.mock('$lib/api', () => ({
  itemApi: {
    get: mockGet,
    children: vi.fn().mockResolvedValue([]),
    upNext: vi.fn().mockRejectedValue(new Error('none')),
    getWatchStatus: vi.fn().mockRejectedValue(new Error('404')),
  },
  peopleApi: { credits: vi.fn().mockResolvedValue([]) },
  userApi: {
    getPreferences: vi.fn().mockResolvedValue({}),
    // The pre-flight says yes: only the decision refuses.
    getMyWatchLimit: vi.fn().mockResolvedValue({ allowed: true }),
  },
  mediaApi: {},
  libraryApi: {},
  transcodeApi: { decide: mockDecide, start: mockStart },
  subtitleApi: {},
  assetUrl: (p: string) => p,
  apiBeacon: vi.fn(),
  ApiRequestError: class ApiRequestError extends Error {
    constructor(message: string, readonly status: number, readonly code: string) {
      super(message);
    }
  },
}));
vi.mock('$lib/stores/toast', () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

import { ApiRequestError } from '$lib/api';
import Page from './+page.svelte';

const episode = {
  id: 'ep-1', library_id: 'lib', title: 'Pilot', type: 'episode', genres: [],
  view_offset_ms: 0, updated_at: 1, is_favorite: false, duration_ms: 600000,
  files: [{
    id: 'f-1', stream_url: '/media/stream/f-1', container: 'mkv', video_codec: 'h264',
    audio_codec: 'eac3', resolution_w: 1920, resolution_h: 1080, duration_ms: 600000,
    faststart: true,
  }],
};

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.setItem('onscreen_user', JSON.stringify({ id: 'u1', username: 'viewer' }));
  mockGet.mockResolvedValue(structuredClone(episode));
  mockStart.mockReturnValue(new Promise(() => {}));
});

describe('a refusal from the play decision', () => {
  it('shows the parental watch limit and starts nothing', async () => {
    mockDecide.mockRejectedValue(new ApiRequestError('outside_allowed_hours', 403, 'PARENTAL_LIMIT'));
    render(Page);
    expect(await screen.findByText('Watch limit reached')).toBeTruthy();
    expect(screen.getByText(/outside the allowed hours/)).toBeTruthy();
    expect(screen.queryByText('outside_allowed_hours')).toBeNull();
    await new Promise((r) => setTimeout(r, 50));
    expect(mockStart).not.toHaveBeenCalled();
  });

  it('shows an admin stop with the server sentence and starts nothing', async () => {
    const sentence = 'Playback was stopped by the server admin.';
    mockDecide.mockRejectedValue(new ApiRequestError(sentence, 403, 'PLAYBACK_STOPPED'));
    render(Page);
    expect(await screen.findByText('Playback stopped')).toBeTruthy();
    expect(screen.getByText(sentence)).toBeTruthy();
    await new Promise((r) => setTimeout(r, 50));
    expect(mockStart).not.toHaveBeenCalled();
  });

  it('still falls back to the local decision on any other failure', async () => {
    mockDecide.mockRejectedValue(new Error('offline'));
    render(Page);
    // E-AC-3 in Matroska: the local decision remuxes, so Start is asked.
    await waitFor(() => expect(mockStart).toHaveBeenCalled());
    expect(screen.queryByText('Watch limit reached')).toBeNull();
    expect(screen.queryByText('Playback stopped')).toBeNull();
  });
});
