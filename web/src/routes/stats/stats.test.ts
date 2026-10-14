import { render, screen, waitFor, fireEvent } from '@testing-library/svelte';
import Page from './+page.svelte';
import Digest from '$lib/components/DigestOptIn.svelte';

const mockGoto = vi.hoisted(() => vi.fn());
const mockStats = vi.hoisted(() => vi.fn());
const mockGetMine = vi.hoisted(() => vi.fn());
const mockSetMine = vi.hoisted(() => vi.fn());

vi.mock('$app/navigation', () => ({ goto: mockGoto }));
vi.mock('$lib/api', () => ({ api: {}, assetUrl: (p: string) => p }));
vi.mock('$lib/stores/toast', () => ({ toast: { success: vi.fn(), error: vi.fn() } }));
vi.mock('$lib/userStats', async (orig) => {
  const real = await orig<typeof import('$lib/userStats')>();
  return {
    ...real,
    userStatsApi: { get: mockStats, yearInReview: vi.fn() },
    digestApi: { getMine: mockGetMine, setMine: mockSetMine },
  };
});

const totals = {
  plays: 12, watch_ms: 5_400_000, listen_ms: 0, hours_watched: 1.5, hours_listened: 0,
  items_finished: 3, books_read: 1, active_days: 4,
};

function stats(over: Record<string, unknown> = {}) {
  return {
    range: '30d', since: '', until: '', timezone: 'UTC', totals,
    by_type: [{ type: 'movie', plays: 2, ms: 5_400_000 }],
    top_genres: [{ genre: 'Crime', plays: 2, ms: 5_400_000 }],
    top_titles: {
      movies: [{ id: 'm1', title: 'Heat', year: 1995, plays: 2, ms: 1 }],
      shows: [], artists: [], albums: [{ id: 'a1', title: 'Blue', parent_title: 'Joni Mitchell', plays: 5, ms: 1 }],
    },
    by_month: [{ month: '2026-10', plays: 12, ms: 5_400_000 }],
    heatmap: [{ weekday: 1, hour: 20, plays: 3 }],
    longest_streak: { days: 4, start: '2026-10-01', end: '2026-10-04' },
    devices: [{ device: 'Fire TV', plays: 12, ms: 5_400_000 }],
    ...over,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
});

describe('My stats page', () => {
  it('redirects to /login when signed out', () => {
    render(Page);
    expect(mockGoto).toHaveBeenCalledWith('/login');
  });

  it('shows totals, top titles linked to their pages, and devices', async () => {
    localStorage.setItem('onscreen_user', JSON.stringify({ id: '1', username: 'u' }));
    mockStats.mockResolvedValue(stats());
    render(Page);
    await waitFor(() => expect(screen.getByText('Heat')).toBeTruthy());
    expect(mockStats).toHaveBeenCalledWith('30d');
    expect(screen.getByText('1.5')).toBeTruthy();
    expect(screen.getByText('Joni Mitchell')).toBeTruthy();
    expect(screen.getByText('Heat').closest('a')?.getAttribute('href')).toBe('/watch/m1');
    expect(screen.getByText('Blue').closest('a')?.getAttribute('href')).toBe('/albums/a1');
    expect(screen.getByText('Fire TV')).toBeTruthy();
  });

  it('reloads for another range', async () => {
    localStorage.setItem('onscreen_user', JSON.stringify({ id: '1', username: 'u' }));
    mockStats.mockResolvedValue(stats());
    render(Page);
    await waitFor(() => expect(screen.getByText('Heat')).toBeTruthy());
    await fireEvent.click(screen.getByText('All time'));
    expect(mockStats).toHaveBeenLastCalledWith('all');
  });

  it('says so when nothing was played', async () => {
    localStorage.setItem('onscreen_user', JSON.stringify({ id: '1', username: 'u' }));
    mockStats.mockResolvedValue(stats({ totals: { ...totals, plays: 0, books_read: 0 } }));
    render(Page);
    await waitFor(() => expect(screen.getByText(/Nothing played in this range/)).toBeTruthy());
  });
});

describe('Digest opt-in', () => {
  it('saves a frequency', async () => {
    mockGetMine.mockResolvedValue({ frequency: 'off', has_email: true, available: true });
    mockSetMine.mockResolvedValue({ frequency: 'weekly', has_email: true, available: true });
    render(Digest);
    await waitFor(() => expect(screen.getByText('Weekly')).toBeTruthy());
    await fireEvent.click(screen.getByText('Weekly'));
    expect(mockSetMine).toHaveBeenCalledWith('weekly');
    await waitFor(() => expect(screen.getByText('Weekly').getAttribute('aria-checked')).toBe('true'));
  });

  it('warns when the account has no email address', async () => {
    mockGetMine.mockResolvedValue({ frequency: 'monthly', has_email: false, available: true });
    render(Digest);
    await waitFor(() => expect(screen.getByText(/no email address/)).toBeTruthy());
  });

  it('warns when the server is not sending digests', async () => {
    mockGetMine.mockResolvedValue({ frequency: 'weekly', has_email: true, available: false });
    render(Digest);
    await waitFor(() => expect(screen.getByText(/isn’t sending digests/)).toBeTruthy());
  });
});
