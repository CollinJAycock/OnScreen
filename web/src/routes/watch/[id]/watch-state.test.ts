// Show / season / movie detail: the up-next Play button, the season it
// opens on, and the watched toggles. The player itself isn't exercised —
// these pages render the detail view and never start a stream.
import { render, screen, waitFor, fireEvent } from '@testing-library/svelte';
import { readable } from 'svelte/store';

const mockGoto = vi.hoisted(() => vi.fn());
const mockGet = vi.hoisted(() => vi.fn());
const mockChildren = vi.hoisted(() => vi.fn());
const mockUpNext = vi.hoisted(() => vi.fn());
const mockMarkWatched = vi.hoisted(() => vi.fn());
const mockMarkUnwatched = vi.hoisted(() => vi.fn());
const mockRemove = vi.hoisted(() => vi.fn());
const mockToastError = vi.hoisted(() => vi.fn());
const pageId = vi.hoisted(() => ({ id: 'show-1', search: '' }));

vi.mock('$app/navigation', () => ({ goto: mockGoto }));
vi.mock('$app/stores', () => ({
  page: {
    subscribe: (fn: (v: unknown) => void) =>
      readable({ url: new URL(`http://localhost/watch/${pageId.id}${pageId.search}`), params: { id: pageId.id }, state: {} }).subscribe(fn),
  },
}));
vi.mock('$lib/api', () => ({
  itemApi: {
    get: mockGet,
    children: mockChildren,
    upNext: mockUpNext,
    markWatched: mockMarkWatched,
    markUnwatched: mockMarkUnwatched,
    remove: mockRemove,
    getWatchStatus: vi.fn().mockRejectedValue(new Error('404')),
  },
  peopleApi: { credits: vi.fn().mockResolvedValue([]) },
  userApi: {
    getPreferences: vi.fn().mockResolvedValue({}),
    getMyWatchLimit: vi.fn().mockResolvedValue({ allowed: true }),
  },
  mediaApi: {},
  libraryApi: {},
  transcodeApi: {},
  subtitleApi: {},
  assetUrl: (p: string) => p,
  apiBeacon: vi.fn(),
  ApiRequestError: class extends Error {},
}));
vi.mock('$lib/stores/toast', () => ({
  toast: { success: vi.fn(), error: mockToastError },
}));
const mockConfirm = vi.hoisted(() => vi.fn());
vi.mock('$lib/native', async (importOriginal) => ({
  ...(await importOriginal<typeof import('$lib/native')>()),
  confirmAction: mockConfirm,
}));

import Page from './+page.svelte';

const show = {
  id: 'show-1', library_id: 'lib', title: 'Lost', type: 'show', genres: [],
  view_offset_ms: 0, updated_at: 1, is_favorite: false, files: [],
};
const seasons = [
  { id: 's1', title: 'Season 1', type: 'season', index: 1, created_at: '', updated_at: 1 },
  { id: 's2', title: 'Season 2', type: 'season', index: 2, created_at: '', updated_at: 1 },
];
const episodes: Record<string, unknown[]> = {
  s1: [
    { id: 'e11', title: 'Pilot', type: 'episode', index: 1, watched: true, created_at: '', updated_at: 1 },
    { id: 'e12', title: 'Tabula Rasa', type: 'episode', index: 2, watched: false, created_at: '', updated_at: 1 },
  ],
  s2: [
    { id: 'e21', title: 'Man of Science', type: 'episode', index: 1, watched: true, created_at: '', updated_at: 1 },
    { id: 'e22', title: 'Adrift', type: 'episode', index: 2, watched: false, created_at: '', updated_at: 1 },
  ],
};

function childrenImpl(id: string) {
  if (id === 'show-1') return Promise.resolve({ items: seasons, total: 2 });
  return Promise.resolve({ items: episodes[id] ?? [], total: 0 });
}

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  localStorage.setItem('onscreen_user', JSON.stringify({ id: '1', username: 'u' }));
  // Keep the page from injecting the Cast SDK <script>.
  if (!document.querySelector('script[data-cast-sdk]')) {
    const s = document.createElement('script');
    s.setAttribute('data-cast-sdk', '1');
    document.head.appendChild(s);
  }
  pageId.id = 'show-1';
  pageId.search = '';
  mockGet.mockResolvedValue(show);
  mockChildren.mockImplementation(childrenImpl);
});

describe('show page up-next', () => {
  it('opens the season holding the up-next episode and plays it', async () => {
    mockUpNext.mockResolvedValue({
      mode: 'next',
      episode: { id: 'e22', title: 'Adrift', season_id: 's2', season_number: 2, episode_number: 2 },
    });
    render(Page);
    const btn = await screen.findByRole('button', { name: /Play S2 · E2/ });
    // Season 2's episodes are listed, not season 1's.
    await waitFor(() => expect(screen.getByText('Adrift')).toBeTruthy());
    expect(screen.queryByText('Tabula Rasa')).toBeNull();
    expect(mockChildren).toHaveBeenCalledWith('s2');
    expect(mockChildren).not.toHaveBeenCalledWith('s1');
    await fireEvent.click(btn);
    expect(mockGoto).toHaveBeenCalledWith('/watch/e22');
  });

  it('labels resume and rewatch', async () => {
    mockUpNext.mockResolvedValue({
      mode: 'resume',
      episode: { id: 'e12', title: 'Tabula Rasa', season_id: 's1', season_number: 1, episode_number: 2, view_offset_ms: 5000 },
    });
    const { unmount } = render(Page);
    expect(await screen.findByRole('button', { name: /Resume S1 · E2/ })).toBeTruthy();
    unmount();

    mockUpNext.mockResolvedValue({
      mode: 'rewatch',
      episode: { id: 'e11', title: 'Pilot', season_id: 's1', season_number: 1, episode_number: 1 },
    });
    render(Page);
    expect(await screen.findByRole('button', { name: /Watch again/ })).toBeTruthy();
    // Everything's watched: only the unwatched mark makes sense.
    expect(screen.queryByRole('button', { name: /Mark all watched/ })).toBeNull();
    expect(screen.getByRole('button', { name: /Mark all unwatched/ })).toBeTruthy();
  });

  it('falls back to the first season and no Play button when up-next fails', async () => {
    mockUpNext.mockRejectedValue(new Error('404'));
    render(Page);
    await waitFor(() => expect(screen.getByText('Tabula Rasa')).toBeTruthy());
    expect(screen.queryByRole('button', { name: /^Play S/ })).toBeNull();
    expect(screen.getByRole('button', { name: /Mark all watched/ })).toBeTruthy();
    expect(screen.getByRole('button', { name: /Mark all unwatched/ })).toBeTruthy();
  });

  it('shows no Play button when there is nothing to play', async () => {
    mockUpNext.mockResolvedValue({ mode: 'none' });
    render(Page);
    await waitFor(() => expect(screen.getByText('Tabula Rasa')).toBeTruthy());
    expect(screen.queryByRole('button', { name: /^(Play|Resume|Watch again)/ })).toBeNull();
  });
});

describe('show page watched marks', () => {
  beforeEach(() => {
    mockUpNext.mockResolvedValue({
      mode: 'next',
      episode: { id: 'e12', title: 'Tabula Rasa', season_id: 's1', season_number: 1, episode_number: 2 },
    });
  });

  it('confirms before marking a whole show unwatched, then refreshes', async () => {
    mockConfirm.mockResolvedValue(false);
    mockMarkUnwatched.mockResolvedValue(undefined);
    render(Page);
    const btn = await screen.findByRole('button', { name: /Mark all unwatched/ });
    await fireEvent.click(btn);
    await waitFor(() => expect(mockConfirm).toHaveBeenCalled());
    expect(mockMarkUnwatched).not.toHaveBeenCalled();

    mockConfirm.mockResolvedValue(true);
    const upNextCalls = mockUpNext.mock.calls.length;
    await fireEvent.click(btn);
    await waitFor(() => expect(mockMarkUnwatched).toHaveBeenCalledWith('show-1'));
    // Episode lists and up-next are re-read.
    await waitFor(() => expect(mockUpNext.mock.calls.length).toBeGreaterThan(upNextCalls));
    expect(mockChildren.mock.calls.filter(([id]) => id === 's1').length).toBeGreaterThan(1);
  });

  it('marks all watched without confirming', async () => {
    mockMarkWatched.mockResolvedValue(undefined);
    render(Page);
    await fireEvent.click(await screen.findByRole('button', { name: /Mark all watched/ }));
    await waitFor(() => expect(mockMarkWatched).toHaveBeenCalledWith('show-1'));
    expect(mockConfirm).not.toHaveBeenCalled();
  });

  it('toggles one episode from its row, rolling back on failure', async () => {
    mockMarkWatched.mockRejectedValue(new Error('nope'));
    render(Page);
    const toggle = await screen.findByRole('button', { name: 'Mark watched: episode 2, Tabula Rasa' });
    const row = screen.getByText('Tabula Rasa').closest('a')!;
    expect(row.classList.contains('ep-watched')).toBe(false);
    await fireEvent.click(toggle);
    expect(mockMarkWatched).toHaveBeenCalledWith('e12');
    await waitFor(() => expect(mockToastError).toHaveBeenCalledWith('nope'));
    await waitFor(() => expect(screen.getByText('Tabula Rasa').closest('a')!.classList.contains('ep-watched')).toBe(false));
  });

  it('episode toggle refreshes the season and up-next on success', async () => {
    mockMarkUnwatched.mockResolvedValue(undefined);
    render(Page);
    const toggle = await screen.findByRole('button', { name: 'Mark unwatched: episode 1, Pilot' });
    const before = mockUpNext.mock.calls.length;
    await fireEvent.click(toggle);
    await waitFor(() => expect(mockMarkUnwatched).toHaveBeenCalledWith('e11'));
    await waitFor(() => expect(mockUpNext.mock.calls.length).toBeGreaterThan(before));
  });
});

describe('season page', () => {
  it('uses the season’s own up-next and does not confirm mark-all-unwatched', async () => {
    pageId.id = 's2';
    mockGet.mockResolvedValue({ ...show, id: 's2', title: 'Season 2', type: 'season', parent_id: 'show-1', index: 2 });
    mockUpNext.mockResolvedValue({
      mode: 'next',
      episode: { id: 'e22', title: 'Adrift', season_id: 's2', season_number: 2, episode_number: 2 },
    });
    mockMarkUnwatched.mockResolvedValue(undefined);
    render(Page);
    expect(await screen.findByRole('button', { name: /Play S2 · E2/ })).toBeTruthy();
    expect(mockUpNext).toHaveBeenCalledWith('s2');
    await fireEvent.click(screen.getByRole('button', { name: /Mark all unwatched/ }));
    await waitFor(() => expect(mockMarkUnwatched).toHaveBeenCalledWith('s2'));
    expect(mockConfirm).not.toHaveBeenCalled();
  });
});

describe('movie page', () => {
  it('toggles watched from the detail view', async () => {
    pageId.id = 'm1';
    mockGet.mockResolvedValue({
      ...show, id: 'm1', title: 'Alien', type: 'movie', view_offset_ms: 60_000,
      files: [{ id: 'f1', stream_url: '/s', container: 'mkv' }],
    });
    mockMarkWatched.mockResolvedValue(undefined);
    render(Page);
    const btn = await screen.findByRole('button', { name: 'Mark watched' });
    expect(screen.getByRole('button', { name: /Resume/ })).toBeTruthy();
    await fireEvent.click(btn);
    await waitFor(() => expect(mockMarkWatched).toHaveBeenCalledWith('m1'));
    expect(await screen.findByRole('button', { name: 'Mark unwatched' })).toBeTruthy();
    // Resume point cleared with it.
    await waitFor(() => expect(screen.queryByRole('button', { name: /Resume/ })).toBeNull());
    expect(mockUpNext).not.toHaveBeenCalled();
  });

  it('resumes at the ?at= of a "Play on…" transfer instead of the saved point', async () => {
    pageId.id = 'm1';
    pageId.search = '?at=61000';
    mockGet.mockResolvedValue({
      ...show, id: 'm1', title: 'Alien', type: 'movie', view_offset_ms: 5_000,
      files: [{ id: 'f1', stream_url: '/s', container: 'mkv' }],
    });
    render(Page);
    expect(await screen.findByRole('button', { name: 'Resume · 1:01' })).toBeTruthy();
  });

  // window.alert shows nothing in the desktop app, so failures go to a toast.
  it('reports a failed Remove with a toast', async () => {
    localStorage.setItem('onscreen_user', JSON.stringify({ id: '1', username: 'u', is_admin: true }));
    pageId.id = 'm1';
    mockGet.mockResolvedValue({
      ...show, id: 'm1', title: 'Alien', type: 'movie',
      files: [{ id: 'f1', stream_url: '/s', container: 'mkv' }],
    });
    mockConfirm.mockResolvedValue(true);
    mockRemove.mockRejectedValue(new Error('Item is locked'));
    render(Page);
    await fireEvent.click(await screen.findByRole('button', { name: 'Remove' }));
    await waitFor(() => expect(mockToastError).toHaveBeenCalledWith('Item is locked'));
    expect(mockRemove).toHaveBeenCalledWith('m1');
    expect(mockGoto).not.toHaveBeenCalled();
  });
});
