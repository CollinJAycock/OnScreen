import { render, screen, waitFor } from '@testing-library/svelte';
import { readable } from 'svelte/store';
import Page from './+page.svelte';

const mockGoto = vi.hoisted(() => vi.fn());
const mockReview = vi.hoisted(() => vi.fn());
const params = vi.hoisted(() => ({ year: '2025' }));

vi.mock('$app/navigation', () => ({ goto: mockGoto }));
vi.mock('$app/stores', () => ({ page: readable({ params, url: new URL('http://localhost/year/2025') }) }));
vi.mock('$lib/api', () => ({ api: {}, assetUrl: (p: string) => p }));
vi.mock('$lib/userStats', async (orig) => {
  const real = await orig<typeof import('$lib/userStats')>();
  return { ...real, userStatsApi: { get: vi.fn(), yearInReview: mockReview } };
});

const totals = (h: number) => ({
  plays: 40, watch_ms: 0, listen_ms: 0, hours_watched: h, hours_listened: 0,
  items_finished: 9, books_read: 2, active_days: 30,
});

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  localStorage.setItem('onscreen_user', JSON.stringify({ id: '1', username: 'u' }));
});

describe('Year in review page', () => {
  it('shows the headline, first play and the change on last year', async () => {
    mockReview.mockResolvedValue({
      year: 2025, timezone: 'UTC', has_data: true, totals: totals(120),
      longest_streak: { days: 6 }, top_movies: [{ id: 'm', title: 'Dune', plays: 3, ms: 1 }],
      top_shows: [], top_artists: [], top_albums: [], top_genres: [{ genre: 'Sci-Fi', plays: 3, ms: 1 }],
      top_device: { device: 'Fire TV', plays: 30, ms: 1 },
      first_play: { id: 'p', type: 'movie', title: 'Paddington', occurred_at: '2025-01-02T10:00:00Z' },
      busiest_day: { date: '2025-03-03', plays: 9, ms: 1 },
      new_artists: { count: 4, top: [{ id: 'a', title: 'Khruangbin', plays: 12, ms: 1 }] },
      by_month: [{ month: '2025-01', plays: 3, ms: 10 }],
      previous_year: { year: 2024, totals: totals(100) },
    });
    render(Page);
    await waitFor(() => expect(screen.getByText('Paddington')).toBeTruthy());
    expect(mockReview).toHaveBeenCalledWith(2025);
    expect(screen.getByText(/20% more than 2024/)).toBeTruthy();
    expect(screen.getByText('Dune').closest('a')?.getAttribute('href')).toBe('/watch/m');
    expect(screen.getByText(/including Khruangbin/)).toBeTruthy();
  });

  it('has an empty state', async () => {
    mockReview.mockResolvedValue({
      year: 2025, timezone: 'UTC', has_data: false, totals: totals(0), longest_streak: { days: 0 },
      top_movies: [], top_shows: [], top_artists: [], top_albums: [], top_genres: [], top_device: null,
      first_play: null, busiest_day: null, new_artists: { count: 0, top: [] }, by_month: [], previous_year: null,
    });
    render(Page);
    await waitFor(() => expect(screen.getByText('Nothing played yet')).toBeTruthy());
  });
});
