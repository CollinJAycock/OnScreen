// Dolby Vision profile 5 (hdr_type 'dolby_vision') in the player: the
// server's play decision says whether it plays (tone-mapped to SDR, where a
// worker can) or is unsupported. The file's own hdr_type stands in only when
// the decision call failed — never while the verdict is still on its way.
import { render, screen, waitFor } from '@testing-library/svelte';
import { readable } from 'svelte/store';

const mockGet = vi.hoisted(() => vi.fn());
const mockDecide = vi.hoisted(() => vi.fn());

vi.mock('$app/navigation', () => ({ goto: vi.fn() }));
vi.mock('$app/stores', () => ({
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
    getMyWatchLimit: vi.fn().mockResolvedValue({ allowed: true }),
  },
  mediaApi: {},
  libraryApi: {},
  // A transcode that never answers: the player stays on its spinner.
  transcodeApi: { decide: mockDecide, start: vi.fn(() => new Promise(() => {})) },
  subtitleApi: {},
  assetUrl: (p: string) => p,
  apiBeacon: vi.fn(),
  ApiRequestError: class extends Error {},
}));
vi.mock('$lib/stores/toast', () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

import Page from './+page.svelte';

const MESSAGE = 'Dolby Vision is not supported';

const episode = {
  id: 'ep-1', library_id: 'lib', title: 'Pilot', type: 'episode', genres: [],
  view_offset_ms: 0, updated_at: 1, is_favorite: false, duration_ms: 600000,
  files: [{
    id: 'f-1', stream_url: '/media/stream/f-1', container: 'mp4', video_codec: 'hevc',
    audio_codec: 'aac', resolution_w: 3840, resolution_h: 2160, duration_ms: 600000,
    hdr_type: 'dolby_vision', dv_profile: 5, video_bit_depth: 10, faststart: true,
  }],
};

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.setItem('onscreen_user', JSON.stringify({ id: 'u1', username: 'viewer' }));
  mockGet.mockResolvedValue(structuredClone(episode));
});

describe('Dolby Vision profile 5 in the player', () => {
  it('plays when the server tone-maps it (verdict transcode)', async () => {
    mockDecide.mockResolvedValue({ file_id: 'f-1', decision: 'transcode' });
    render(Page);
    await waitFor(() => expect(mockDecide).toHaveBeenCalled());
    await new Promise((r) => setTimeout(r, 50));
    expect(screen.queryByText(MESSAGE)).toBeNull();
  });

  it('shows the message when the server says unsupported', async () => {
    mockDecide.mockResolvedValue({ file_id: 'f-1', decision: 'unsupported' });
    render(Page);
    expect(await screen.findByText(MESSAGE)).toBeTruthy();
  });

  it('falls back to the file when the decision call fails', async () => {
    mockDecide.mockRejectedValue(new Error('offline'));
    render(Page);
    expect(await screen.findByText(MESSAGE)).toBeTruthy();
  });

  it('does not show the message while the verdict is on its way', async () => {
    mockDecide.mockReturnValue(new Promise(() => {}));
    render(Page);
    await waitFor(() => expect(mockDecide).toHaveBeenCalled());
    await new Promise((r) => setTimeout(r, 50));
    expect(screen.queryByText(MESSAGE)).toBeNull();
  });
});
