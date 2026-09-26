import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import type { UpcomingItem, UpcomingResponse, UpcomingService } from '$lib/api';
import Page from './+page.svelte';
import Upcoming from './Upcoming.svelte';
import {
  addDays, agendaDays, buildMonthGrid, entryDayKey, entryText, fetchRange, filterItems,
  formatDayKey, formatWhen, groupByDay, initialView, monthTitle, relativeDay,
  safePosterUrl, shiftMonth, visibleRange,
} from './upcoming';

const mockGoto = vi.hoisted(() => vi.fn());
const mockGetUser = vi.hoisted(() => vi.fn());
const mockUpcoming = vi.hoisted(() => vi.fn());
const mockMine = vi.hoisted(() => vi.fn());
const mockQueue = vi.hoisted(() => vi.fn());

vi.mock('$app/navigation', () => ({ goto: mockGoto }));
vi.mock('$lib/api', () => ({
  api: { getUser: mockGetUser },
  upcomingApi: { get: mockUpcoming },
  requestsApi: { list: mockMine, cancel: vi.fn() },
  requestsAdminApi: { list: mockQueue, approve: vi.fn(), decline: vi.fn(), del: vi.fn() },
}));

function entry(over: Partial<UpcomingItem> & Pick<UpcomingItem, 'id' | 'title' | 'date'>): UpcomingItem {
  return {
    kind: 'movie',
    subtitle: '',
    release_type: 'digital',
    all_day: true,
    status: 'upcoming',
    poster_url: null,
    year: 2026,
    overview: '',
    certification: '',
    network: '',
    service: 'Radarr',
    item_id: null,
    ...over,
  };
}

function episode(over: Partial<UpcomingItem> & Pick<UpcomingItem, 'id' | 'title' | 'date'>): UpcomingItem {
  return entry({ kind: 'episode', release_type: 'airing', all_day: false, service: 'Sonarr', ...over });
}

const bothOk: UpcomingService[] = [
  { name: 'Radarr', kind: 'radarr', ok: true, error: '' },
  { name: 'Sonarr', kind: 'sonarr', ok: true, error: '' },
];

function response(items: UpcomingItem[], services: UpcomingService[] = bothOk): UpcomingResponse {
  return { from: '', to: '', items, services };
}

// An instant at a wall-clock time in the test machine's own timezone.
function localIso(y: number, m: number, d: number, h: number, min = 0): string {
  return new Date(y, m, d, h, min).toISOString();
}

describe('entryDayKey', () => {
  it('reads all-day release dates by their UTC date part', () => {
    expect(entryDayKey({ date: '2026-09-28T00:00:00Z', all_day: true })).toBe('2026-09-28');
    expect(entryDayKey({ date: '2026-09-28T23:59:59Z', all_day: true })).toBe('2026-09-28');
  });

  it('reads air times on the viewer’s local day', () => {
    expect(entryDayKey({ date: localIso(2026, 8, 28, 23, 30), all_day: false })).toBe('2026-09-28');
    expect(entryDayKey({ date: localIso(2026, 8, 28, 0, 15), all_day: false })).toBe('2026-09-28');
  });

  it('is null for a date that does not parse', () => {
    expect(entryDayKey({ date: 'not a date', all_day: true })).toBeNull();
    expect(entryDayKey({ date: '', all_day: false })).toBeNull();
  });
});

describe('buildMonthGrid', () => {
  const inMonth = (y: number, m: number) => buildMonthGrid(y, m).filter(d => d.inMonth);

  it('is six Sunday-first weeks', () => {
    for (const [y, m] of [[2026, 8], [2026, 1], [2024, 1], [2027, 0], [2015, 1]]) {
      const grid = buildMonthGrid(y, m);
      expect(grid).toHaveLength(42);
      grid.forEach((d, i) => expect(new Date(`${d.key}T00:00:00Z`).getUTCDay()).toBe(i % 7));
      // Consecutive days, no gaps or repeats.
      grid.slice(1).forEach((d, i) => expect(d.key).toBe(addDays(grid[i].key, 1)));
    }
  });

  it('starts on the Sunday on or before the 1st and dims the neighbouring months', () => {
    // 1 Sep 2026 is a Tuesday.
    const sep = buildMonthGrid(2026, 8);
    expect(sep[0]).toEqual({ key: '2026-08-30', day: 30, inMonth: false });
    expect(sep[2]).toEqual({ key: '2026-09-01', day: 1, inMonth: true });
    expect(sep[41]).toEqual({ key: '2026-10-10', day: 10, inMonth: false });
    expect(inMonth(2026, 8).map(d => d.day)).toEqual(Array.from({ length: 30 }, (_, i) => i + 1));

    // 1 Feb 2026 is itself a Sunday, so the grid opens on it.
    expect(buildMonthGrid(2026, 1)[0]).toEqual({ key: '2026-02-01', day: 1, inMonth: true });

    // 1 Aug 2026 is a Saturday: a 31-day month that needs all six weeks.
    const aug = buildMonthGrid(2026, 7);
    expect(aug[0].key).toBe('2026-07-26');
    expect(aug[36]).toEqual({ key: '2026-08-31', day: 31, inMonth: true });
  });

  it('handles leap years, including the century rules', () => {
    expect(inMonth(2024, 1)).toHaveLength(29);
    expect(buildMonthGrid(2024, 1).find(d => d.key === '2024-02-29')?.inMonth).toBe(true);
    expect(buildMonthGrid(2024, 1).find(d => d.key === '2024-03-01')?.inMonth).toBe(false);
    expect(inMonth(2026, 1)).toHaveLength(28);
    expect(buildMonthGrid(2026, 1).some(d => d.key === '2026-02-29')).toBe(false);
    expect(inMonth(2000, 1)).toHaveLength(29);
    expect(inMonth(2100, 1)).toHaveLength(28);
  });

  it('crosses year boundaries and normalises an out-of-range month', () => {
    const dec = buildMonthGrid(2026, 11);
    expect(dec[0].key).toBe('2026-11-29');
    expect(dec[41].key).toBe('2027-01-09');
    expect(buildMonthGrid(2027, 0)[0].key).toBe('2026-12-27');
    expect(buildMonthGrid(2026, 12)).toEqual(buildMonthGrid(2027, 0));
  });
});

describe('date-range helpers', () => {
  it('addDays is plain calendar arithmetic', () => {
    expect(addDays('2024-02-28', 1)).toBe('2024-02-29');
    expect(addDays('2023-02-28', 1)).toBe('2023-03-01');
    expect(addDays('2026-12-31', 1)).toBe('2027-01-01');
    expect(addDays('2026-01-01', -1)).toBe('2025-12-31');
    expect(addDays('2026-03-08', 1)).toBe('2026-03-09'); // US DST change
    expect(addDays('2026-09-26', 30)).toBe('2026-10-26');
  });

  it('shiftMonth rolls the year', () => {
    expect(shiftMonth({ year: 2026, month: 11 }, 1)).toEqual({ year: 2027, month: 0 });
    expect(shiftMonth({ year: 2026, month: 0 }, -1)).toEqual({ year: 2025, month: 11 });
    expect(shiftMonth({ year: 2026, month: 8 }, 0)).toEqual({ year: 2026, month: 8 });
  });

  it('visibleRange covers the grid in month view and today + 30 days in agenda view', () => {
    expect(visibleRange('month', { year: 2026, month: 8 }, '2026-09-26'))
      .toEqual({ from: '2026-08-30', to: '2026-10-10' });
    expect(visibleRange('agenda', { year: 2020, month: 0 }, '2026-09-26'))
      .toEqual({ from: '2026-09-26', to: '2026-10-26' });
  });

  it('fetchRange pads a day each side and always stays inside the 62-day cap', () => {
    expect(fetchRange({ from: '2026-08-30', to: '2026-10-10' })).toEqual({ from: '2026-08-29', to: '2026-10-11' });
    const span = (r: { from: string; to: string }) => (Date.parse(r.to) - Date.parse(r.from)) / 86400000 + 1;
    for (let y = 2024; y <= 2030; y++) {
      for (let m = 0; m < 12; m++) {
        expect(span(fetchRange(visibleRange('month', { year: y, month: m }, '2026-09-26')))).toBe(44);
      }
    }
    expect(span(fetchRange(visibleRange('agenda', { year: 2026, month: 8 }, '2026-09-26')))).toBe(33);
  });

  it('initialView opens narrow screens on the agenda', () => {
    expect(initialView(375)).toBe('agenda');
    expect(initialView(699)).toBe('agenda');
    expect(initialView(700)).toBe('month');
    expect(initialView(1440)).toBe('month');
  });

  it('relativeDay labels today and tomorrow only', () => {
    expect(relativeDay('2026-09-26', '2026-09-26')).toBe('Today');
    expect(relativeDay('2026-09-27', '2026-09-26')).toBe('Tomorrow');
    expect(relativeDay('2026-09-28', '2026-09-26')).toBe('');
    expect(relativeDay('2026-09-25', '2026-09-26')).toBe('');
  });
});

describe('filtering and grouping', () => {
  const movie = entry({ id: 'm', title: 'Movie', date: '2026-09-28T00:00:00Z' });
  const ep = episode({ id: 'e', title: 'Show', date: localIso(2026, 8, 28, 20) });

  it('filterItems keeps movies, TV or both', () => {
    expect(filterItems([movie, ep], 'all')).toEqual([movie, ep]);
    expect(filterItems([movie, ep], 'movie')).toEqual([movie]);
    expect(filterItems([movie, ep], 'episode')).toEqual([ep]);
    expect(filterItems([], 'movie')).toEqual([]);
  });

  it('groupByDay puts all-day releases first, then air times, then titles', () => {
    const late = episode({ id: 'late', title: 'A Late Show', date: localIso(2026, 8, 28, 22) });
    const early = episode({ id: 'early', title: 'Z Early Show', date: localIso(2026, 8, 28, 9) });
    const same = episode({ id: 'same', title: 'B Late Show', date: localIso(2026, 8, 28, 22) });
    const film = entry({ id: 'film', title: 'Zzz Film', date: '2026-09-28T00:00:00Z' });
    const byDay = groupByDay([late, same, early, film]);
    expect([...byDay.keys()]).toEqual(['2026-09-28']);
    expect(byDay.get('2026-09-28')!.map(i => i.id)).toEqual(['film', 'early', 'late', 'same']);
  });

  it('groupByDay drops days outside the range and unparseable dates', () => {
    const before = entry({ id: 'b', title: 'Before', date: '2026-09-29T00:00:00Z' });
    const first = entry({ id: 'f', title: 'First', date: '2026-09-30T00:00:00Z' });
    const last = entry({ id: 'l', title: 'Last', date: '2026-10-10T00:00:00Z' });
    const after = entry({ id: 'a', title: 'After', date: '2026-10-11T00:00:00Z' });
    const broken = entry({ id: 'x', title: 'Broken', date: 'soon' });
    const byDay = groupByDay([before, first, last, after, broken], { from: '2026-09-30', to: '2026-10-10' });
    expect([...byDay.keys()]).toEqual(['2026-09-30', '2026-10-10']);
  });

  it('agendaDays lists only days with entries, in date order', () => {
    const a = entry({ id: 'a', title: 'A', date: '2026-10-03T00:00:00Z' });
    const b = entry({ id: 'b', title: 'B', date: '2026-09-27T00:00:00Z' });
    const c = entry({ id: 'c', title: 'C', date: '2026-10-03T00:00:00Z' });
    expect(agendaDays(groupByDay([a, b, c])).map(d => [d.key, d.items.map(i => i.id)]))
      .toEqual([['2026-09-27', ['b']], ['2026-10-03', ['a', 'c']]]);
    expect(agendaDays(new Map())).toEqual([]);
  });
});

describe('display helpers', () => {
  it('entryText is the full, untruncated label', () => {
    expect(entryText(episode({ id: 'e', title: 'Show', subtitle: 'S02E05 · Title', date: '' })))
      .toBe('Show — S02E05 · Title');
    expect(entryText(episode({ id: 'e', title: 'Show', subtitle: '', date: '' }))).toBe('Show');
    expect(entryText(entry({ id: 'm', title: 'Film', release_type: 'physical', date: '' })))
      .toBe('Film (Physical release)');
  });

  it('safePosterUrl only lets https through', () => {
    expect(safePosterUrl('https://image.tmdb.org/p.jpg')).toBe('https://image.tmdb.org/p.jpg');
    expect(safePosterUrl('http://image.tmdb.org/p.jpg')).toBeNull();
    expect(safePosterUrl('javascript:alert(1)')).toBeNull();
    expect(safePosterUrl('')).toBeNull();
    expect(safePosterUrl(null)).toBeNull();
  });
});

describe('Upcoming tab', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    // Pin "today" to 15 Sep 2026 (a Tuesday) at local noon; timers stay real.
    vi.useFakeTimers({ toFake: ['Date'] });
    vi.setSystemTime(new Date(2026, 8, 15, 12, 0));
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  const cell = (container: HTMLElement, key: string) =>
    container.querySelector(`.month-grid [data-day="${key}"]`) as HTMLElement;

  const items = [
    entry({
      id: 'radarr:1:10:digital', title: 'Arrival Day', date: '2026-09-18T00:00:00Z',
      item_id: 'movie-uuid', certification: 'PG-13', overview: 'A film about arriving.',
      poster_url: 'https://image.tmdb.org/arrival.jpg',
    }),
    episode({
      id: 'sonarr:2:20', title: 'Show One', subtitle: 'S02E05 · The One',
      date: localIso(2026, 8, 20, 21), network: 'HBO', item_id: 'show-uuid',
    }),
    episode({
      id: 'sonarr:2:19', title: 'Old Show', subtitle: 'S01E01', date: localIso(2026, 8, 10, 20),
      status: 'downloaded',
    }),
  ];

  it('fetches the padded month grid and puts each entry on its day', async () => {
    mockUpcoming.mockResolvedValue(response(items));
    const { container } = render(Upcoming, { props: { isAdmin: false } });

    await waitFor(() => expect(screen.getByText('Arrival Day')).toBeTruthy());
    expect(mockUpcoming).toHaveBeenCalledTimes(1);
    expect(mockUpcoming).toHaveBeenCalledWith('2026-08-29', '2026-10-11');
    expect(screen.getByRole('heading', { name: monthTitle(2026, 8) })).toBeTruthy();

    expect(container.querySelectorAll('.month-grid .cell')).toHaveLength(42);
    expect(cell(container, '2026-09-15').classList.contains('today')).toBe(true);
    expect(cell(container, '2026-08-30').classList.contains('outside')).toBe(true);
    expect(cell(container, '2026-09-01').classList.contains('outside')).toBe(false);

    const movieCell = within(cell(container, '2026-09-18'));
    expect(movieCell.getByText('Arrival Day')).toBeTruthy();
    expect(movieCell.getByText('Digital')).toBeTruthy();

    const epCell = within(cell(container, '2026-09-20'));
    expect(epCell.getByText('S02E05 · The One')).toBeTruthy();
    const epButton = epCell.getByText('Show One').closest('button')!;
    expect(epButton.classList.contains('st-upcoming')).toBe(true);
    expect(epButton.getAttribute('title')).toBe('Show One — S02E05 · The One');

    const oldButton = within(cell(container, '2026-09-10')).getByText('Old Show').closest('button')!;
    expect(oldButton.classList.contains('st-downloaded')).toBe(true);

    // Legend names all three statuses.
    const legend = within(screen.getByRole('list', { name: 'Status colours' }));
    for (const label of ['Downloaded', 'Missing', 'Upcoming']) expect(legend.getByText(label)).toBeTruthy();
    expect(screen.queryByText('Nothing expected in this range')).toBeNull();
  });

  it('filters to movies or TV without refetching', async () => {
    mockUpcoming.mockResolvedValue(response(items));
    render(Upcoming);
    await waitFor(() => expect(screen.getByText('Show One')).toBeTruthy());

    await fireEvent.click(screen.getByRole('button', { name: 'TV' }));
    expect(screen.queryByText('Arrival Day')).toBeNull();
    expect(screen.getByText('Show One')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'TV' }).getAttribute('aria-pressed')).toBe('true');

    await fireEvent.click(screen.getByRole('button', { name: 'Movies' }));
    expect(screen.getByText('Arrival Day')).toBeTruthy();
    expect(screen.queryByText('Show One')).toBeNull();

    await fireEvent.click(screen.getByRole('button', { name: 'All' }));
    expect(screen.getByText('Show One')).toBeTruthy();
    expect(mockUpcoming).toHaveBeenCalledTimes(1);
  });

  it('opens a detail panel with a link into OnScreen, and Escape closes it', async () => {
    mockUpcoming.mockResolvedValue(response(items));
    render(Upcoming);
    await waitFor(() => expect(screen.getByText('Arrival Day')).toBeTruthy());

    const trigger = screen.getByText('Arrival Day').closest('button')!;
    await fireEvent.click(trigger);
    const dialog = within(screen.getByRole('dialog'));
    expect(dialog.getByRole('heading', { name: /Arrival Day/ })).toBeTruthy();
    expect(dialog.getByText('PG-13')).toBeTruthy();
    expect(dialog.getByText('Radarr')).toBeTruthy();
    expect(dialog.getByText('A film about arriving.')).toBeTruthy();
    expect(dialog.getByText(formatWhen(items[0]))).toBeTruthy();
    expect(dialog.getByRole('link', { name: 'Open in OnScreen' }).getAttribute('href')).toBe('/watch/movie-uuid');
    expect(screen.getByRole('dialog').querySelector('img')?.getAttribute('src'))
      .toBe('https://image.tmdb.org/arrival.jpg');
    await waitFor(() => expect(document.activeElement?.getAttribute('aria-label')).toBe('Close'));

    await fireEvent.keyDown(window, { key: 'Escape' });
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(document.activeElement).toBe(trigger);
  });

  it('shows an episode’s network and hides the link when it is not in OnScreen', async () => {
    mockUpcoming.mockResolvedValue(response([
      episode({ id: 'e', title: 'Unmatched Show', subtitle: 'S01E02', date: localIso(2026, 8, 16, 20), network: 'AMC' }),
    ]));
    render(Upcoming);
    await waitFor(() => expect(screen.getByText('Unmatched Show')).toBeTruthy());

    await fireEvent.click(screen.getByText('Unmatched Show').closest('button')!);
    const dialog = within(screen.getByRole('dialog'));
    expect(dialog.getByText('AMC')).toBeTruthy();
    expect(dialog.getByText('Sonarr')).toBeTruthy();
    expect(dialog.queryByRole('link', { name: 'Open in OnScreen' })).toBeNull();

    await fireEvent.click(dialog.getByRole('button', { name: 'Close' }));
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('switches to an agenda of the next 30 days, grouped by day', async () => {
    mockUpcoming.mockResolvedValue(response(items));
    const { container } = render(Upcoming);
    await waitFor(() => expect(screen.getByText('Arrival Day')).toBeTruthy());

    await fireEvent.click(screen.getByRole('button', { name: 'Agenda' }));
    await waitFor(() => expect(mockUpcoming).toHaveBeenLastCalledWith('2026-09-14', '2026-10-16'));
    await waitFor(() => expect(container.querySelector('.agenda')).toBeTruthy());

    expect(screen.getByRole('heading', { name: 'Next 30 days' })).toBeTruthy();
    const days = [...container.querySelectorAll('.agenda-day')];
    expect(days.map(d => d.querySelector('.agenda-date')?.textContent?.trim()))
      .toEqual([formatDayKey('2026-09-18'), formatDayKey('2026-09-20')]);
    expect(within(days[1] as HTMLElement).getByText('HBO')).toBeTruthy();
    // 10 Sep is before today, so it isn't in the agenda.
    expect(screen.queryByText('Old Show')).toBeNull();
    // No month navigation in the agenda.
    expect(screen.queryByRole('button', { name: 'Next month' })).toBeNull();
  });

  it('opens on the agenda on a narrow screen', async () => {
    const original = Object.getOwnPropertyDescriptor(window, 'innerWidth');
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 500 });
    try {
      mockUpcoming.mockResolvedValue(response([]));
      render(Upcoming);
      await waitFor(() => expect(screen.getByText('Nothing expected in this range')).toBeTruthy());
      expect(mockUpcoming).toHaveBeenCalledWith('2026-09-14', '2026-10-16');
      expect(screen.getByRole('button', { name: 'Agenda' }).getAttribute('aria-pressed')).toBe('true');
    } finally {
      if (original) Object.defineProperty(window, 'innerWidth', original);
      else delete (window as { innerWidth?: number }).innerWidth;
    }
  });

  it('pages between months and comes back with Today', async () => {
    mockUpcoming.mockResolvedValue(response([]));
    render(Upcoming);
    await waitFor(() => expect(mockUpcoming).toHaveBeenCalledTimes(1));

    await fireEvent.click(screen.getByRole('button', { name: 'Next month' }));
    // 1 Oct 2026 is a Thursday: grid 27 Sep – 7 Nov, padded a day each side.
    expect(mockUpcoming).toHaveBeenLastCalledWith('2026-09-26', '2026-11-08');
    expect(screen.getByRole('heading', { name: monthTitle(2026, 9) })).toBeTruthy();

    await fireEvent.click(screen.getByRole('button', { name: 'Previous month' }));
    await fireEvent.click(screen.getByRole('button', { name: 'Previous month' }));
    expect(mockUpcoming).toHaveBeenLastCalledWith('2026-07-25', '2026-09-06');

    await fireEvent.click(screen.getByRole('button', { name: 'Today' }));
    expect(mockUpcoming).toHaveBeenLastCalledWith('2026-08-29', '2026-10-11');
    expect(screen.getByRole('heading', { name: monthTitle(2026, 8) })).toBeTruthy();
  });

  it('shows the empty state for a range with nothing in it', async () => {
    mockUpcoming.mockResolvedValue(response([]));
    render(Upcoming);
    await waitFor(() => expect(screen.getByText('Nothing expected in this range')).toBeTruthy());
  });

  it('points admins at the arr settings when nothing is connected', async () => {
    mockUpcoming.mockResolvedValue(response([], []));
    render(Upcoming, { props: { isAdmin: true } });
    await waitFor(() => expect(screen.getByText(/No Radarr or Sonarr connected/)).toBeTruthy());
    expect(screen.getByRole('link', { name: 'Connect one in Settings' }).getAttribute('href'))
      .toBe('/settings/arr-services');
    expect(screen.queryByText('Nothing expected in this range')).toBeNull();
  });

  it('tells other users to ask an admin when nothing is connected', async () => {
    mockUpcoming.mockResolvedValue(response([], []));
    render(Upcoming, { props: { isAdmin: false } });
    await waitFor(() => expect(screen.getByText(/No Radarr or Sonarr connected/)).toBeTruthy());
    expect(screen.getByText('Ask an admin to connect one.')).toBeTruthy();
    expect(screen.queryByRole('link', { name: 'Connect one in Settings' })).toBeNull();
  });

  it('names a service that could not be reached', async () => {
    mockUpcoming.mockResolvedValue(response([items[0]], [
      { name: 'Radarr', kind: 'radarr', ok: true, error: '' },
      { name: 'Sonarr 4K', kind: 'sonarr', ok: false, error: 'unreachable' },
    ]));
    render(Upcoming);
    await waitFor(() => expect(screen.getByText(/Couldn’t reach Sonarr 4K/)).toBeTruthy());
    expect(screen.getByText('Arrival Day')).toBeTruthy();
  });

  it('does not claim "nothing expected" when every service failed', async () => {
    mockUpcoming.mockResolvedValue(response([], [{ name: 'Radarr', kind: 'radarr', ok: false, error: 'timeout' }]));
    render(Upcoming);
    await waitFor(() => expect(screen.getByText(/Couldn’t reach Radarr/)).toBeTruthy());
    expect(screen.queryByText('Nothing expected in this range')).toBeNull();
  });

  it('shows a friendly error with a retry when the load fails', async () => {
    mockUpcoming.mockRejectedValueOnce(new Error('boom: http://radarr:7878'));
    mockUpcoming.mockResolvedValueOnce(response(items));
    render(Upcoming);
    await waitFor(() => expect(screen.getByText(/Couldn’t load upcoming releases/)).toBeTruthy());
    expect(screen.queryByText(/radarr:7878/)).toBeNull();

    await fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
    await waitFor(() => expect(screen.getByText('Arrival Day')).toBeTruthy());
    expect(screen.queryByText(/Couldn’t load upcoming releases/)).toBeNull();
  });
});

describe('Requests page', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockMine.mockResolvedValue({ items: [], total: 0 });
    mockQueue.mockResolvedValue({ items: [], total: 0 });
    mockUpcoming.mockResolvedValue(response([]));
  });

  it('gives everyone an Upcoming tab, but only admins the Queue', async () => {
    mockGetUser.mockReturnValue({ user_id: 'u1', username: 'kid', is_admin: false });
    render(Page);
    await waitFor(() => expect(screen.getByRole('button', { name: 'Upcoming' })).toBeTruthy());
    expect(screen.queryByRole('button', { name: 'Queue' })).toBeNull();
    expect(mockUpcoming).not.toHaveBeenCalled();

    await fireEvent.click(screen.getByRole('button', { name: 'Upcoming' }));
    await waitFor(() => expect(mockUpcoming).toHaveBeenCalledTimes(1));
    expect(screen.getByRole('group', { name: 'Calendar view' })).toBeTruthy();
  });

  it('keeps the Queue tab for admins alongside Upcoming', async () => {
    mockGetUser.mockReturnValue({ user_id: 'u1', username: 'admin', is_admin: true });
    render(Page);
    await waitFor(() => expect(screen.getByRole('button', { name: 'Queue' })).toBeTruthy());
    expect(screen.getByRole('button', { name: 'Upcoming' })).toBeTruthy();
  });
});

// Last: these move the process timezone, restoring it afterwards.
describe('day boundaries in other timezones', () => {
  const original = process.env.TZ;
  afterEach(() => {
    if (original === undefined) delete process.env.TZ; else process.env.TZ = original;
  });

  it('keeps a release date on its own day west of Greenwich', () => {
    process.env.TZ = 'America/Los_Angeles';
    // Sanity: read locally, UTC midnight really is the previous evening here.
    expect(new Date('2026-09-28T00:00:00Z').getDate()).toBe(27);
    expect(entryDayKey({ date: '2026-09-28T00:00:00Z', all_day: true })).toBe('2026-09-28');
    // The same instant as an air time is the evening of the 27th.
    expect(entryDayKey({ date: '2026-09-28T00:00:00Z', all_day: false })).toBe('2026-09-27');
    expect(formatWhen({ date: '2026-09-28T00:00:00Z', all_day: true })).toContain('28');
    expect(formatDayKey('2026-09-28')).toContain('28');
  });

  it('moves a late-UTC air time onto the next local day east of Greenwich', () => {
    process.env.TZ = 'Asia/Tokyo';
    expect(entryDayKey({ date: '2026-09-28T20:00:00Z', all_day: false })).toBe('2026-09-29');
    expect(entryDayKey({ date: '2026-09-28T20:00:00Z', all_day: true })).toBe('2026-09-28');
  });

  it('fetches the padding day that an evening airing on the last grid day falls in', () => {
    process.env.TZ = 'America/New_York';
    // 10 Oct 2026 21:00 in New York is already 11 Oct in UTC.
    const lateShow = episode({ id: 'e', title: 'Late', date: '2026-10-11T01:00:00Z' });
    const shown = visibleRange('month', { year: 2026, month: 8 }, '2026-09-26');
    expect(shown.to).toBe('2026-10-10');
    expect(fetchRange(shown).to).toBe('2026-10-11');
    expect([...groupByDay([lateShow], shown).keys()]).toEqual(['2026-10-10']);
  });
});
