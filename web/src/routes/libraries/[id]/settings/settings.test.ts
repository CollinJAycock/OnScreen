import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import Page from './+page.svelte';

const mockGet = vi.hoisted(() => vi.fn());
const mockUpdate = vi.hoisted(() => vi.fn());
const mockTpStatus = vi.hoisted(() => vi.fn());
const mockTpGenerate = vi.hoisted(() => vi.fn());

vi.mock('$lib/api', () => ({
  libraryApi: {
    get: mockGet,
    update: mockUpdate,
    del: vi.fn(),
    detectIntros: vi.fn(),
    trickplayStatus: mockTpStatus,
    generateTrickplay: mockTpGenerate,
  },
}));

vi.mock('$app/stores', async () => {
  const { readable } = await import('svelte/store');
  return {
    page: readable({ url: new URL('http://localhost/libraries/lib-1/settings'), params: { id: 'lib-1' } }),
  };
});

function lib(overrides: Record<string, unknown> = {}) {
  return {
    id: 'lib-1',
    name: 'Movies',
    type: 'movie',
    scan_paths: ['/media/movies'],
    agent: 'tmdb',
    language: 'en',
    scan_interval_minutes: 60,
    is_private: false,
    auto_grant_new_users: false,
    trickplay_enabled: true,
    created_at: '',
    updated_at: '',
    ...overrides,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.setItem('onscreen_user', JSON.stringify({ id: 'u1', is_admin: true }));
  mockTpStatus.mockResolvedValue({ enabled: true, total: 20, done: 12, pending: 7, failed: 1 });
  mockTpGenerate.mockResolvedValue({ status: 'queued', queued: 7 });
});

describe('Library settings — seek-bar thumbnails', () => {
  it('shows the switch (reflecting the library) and the progress counts', async () => {
    mockGet.mockResolvedValue(lib());
    render(Page);

    const toggle = (await screen.findByLabelText(/generate seek-bar thumbnails/i)) as HTMLInputElement;
    expect(toggle.checked).toBe(true);
    await waitFor(() => {
      expect(screen.getByTestId('trickplay-progress').textContent)
        .toBe('12 of 20 videos have thumbnails · 7 pending · 1 failed');
    });
    expect(mockTpStatus).toHaveBeenCalledWith('lib-1');
  });

  it('saves the toggle with the rest of the form', async () => {
    mockGet.mockResolvedValue(lib());
    mockUpdate.mockImplementation(async (_id: string, body: Record<string, unknown>) => lib(body));
    render(Page);

    const toggle = (await screen.findByLabelText(/generate seek-bar thumbnails/i)) as HTMLInputElement;
    await fireEvent.click(toggle);
    await fireEvent.click(screen.getByRole('button', { name: /save changes/i }));

    await waitFor(() => expect(mockUpdate).toHaveBeenCalled());
    const [id, body] = mockUpdate.mock.calls[0];
    expect(id).toBe('lib-1');
    expect(body.trickplay_enabled).toBe(false);
  });

  it('queues generation and refreshes the counts', async () => {
    mockGet.mockResolvedValue(lib());
    render(Page);

    const btn = await screen.findByRole('button', { name: /generate now/i });
    await fireEvent.click(btn);

    await waitFor(() => expect(mockTpGenerate).toHaveBeenCalledWith('lib-1'));
    expect(await screen.findByText(/^Queued 7 videos\./)).toBeTruthy();
    await waitFor(() => expect(mockTpStatus).toHaveBeenCalledTimes(2));
  });

  it('surfaces a failed generate request', async () => {
    mockGet.mockResolvedValue(lib());
    mockTpGenerate.mockRejectedValueOnce(new Error('forbidden'));
    render(Page);

    await fireEvent.click(await screen.findByRole('button', { name: /generate now/i }));
    expect(await screen.findByText('forbidden')).toBeTruthy();
  });

  it('hides the section for non-video libraries and leaves their flag alone', async () => {
    mockGet.mockResolvedValue(lib({ type: 'music', trickplay_enabled: false }));
    mockUpdate.mockImplementation(async (_id: string, body: Record<string, unknown>) => lib(body));
    render(Page);

    await screen.findByRole('button', { name: /save changes/i });
    expect(screen.queryByLabelText(/generate seek-bar thumbnails/i)).toBeNull();
    expect(screen.queryByRole('button', { name: /generate now/i })).toBeNull();
    expect(mockTpStatus).not.toHaveBeenCalled();

    await fireEvent.click(screen.getByRole('button', { name: /save changes/i }));
    await waitFor(() => expect(mockUpdate).toHaveBeenCalled());
    expect('trickplay_enabled' in mockUpdate.mock.calls[0][1]).toBe(false);
  });

  it('still renders when the status endpoint is unavailable', async () => {
    mockGet.mockResolvedValue(lib());
    mockTpStatus.mockRejectedValue(new Error('404'));
    render(Page);

    expect(await screen.findByLabelText(/generate seek-bar thumbnails/i)).toBeTruthy();
    await waitFor(() => expect(mockTpStatus).toHaveBeenCalled());
    expect(screen.getByTestId('trickplay-progress').textContent).toBe('');
  });
});
