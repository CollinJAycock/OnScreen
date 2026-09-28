import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import SeasonPicker from './SeasonPicker.svelte';

const mockSeasons = vi.hoisted(() => vi.fn());

vi.mock('$lib/api', () => ({
  discoverApi: { seasons: mockSeasons },
}));

const S = (n: number, over: Record<string, unknown> = {}) => ({
  season_number: n,
  name: n === 0 ? 'Specials' : `Season ${n}`,
  episode_count: 10,
  aired_episodes: 10,
  air_date: '2020-01-01',
  owned_episodes: 0,
  requested: null,
  ...over,
});

function box(name: RegExp): HTMLInputElement {
  return screen.getByRole('checkbox', { name }) as HTMLInputElement;
}

function submitButton(): HTMLButtonElement {
  return screen.getByRole('button', { name: /^Request|Sending/ }) as HTMLButtonElement;
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe('SeasonPicker', () => {
  it('shows owned and requested seasons locked, specials last and unticked', async () => {
    mockSeasons.mockResolvedValue([
      S(1, { owned_episodes: 10 }),
      S(2, { requested: 'pending' }),
      S(3),
      S(0, { episode_count: 2, aired_episodes: 2 }),
    ]);
    render(SeasonPicker, { tmdbId: 1399, title: 'Show (2011)', onsubmit: vi.fn(), onclose: vi.fn() });
    await waitFor(() => expect(mockSeasons).toHaveBeenCalledWith(1399));
    await screen.findByText('In library');

    expect(screen.getByText('Show (2011)')).toBeTruthy();
    expect(box(/Season 1/).disabled).toBe(true);
    expect(box(/Season 2/).disabled).toBe(true);
    expect(screen.getByText('Requested · Pending')).toBeTruthy();
    expect(box(/Season 3/).checked).toBe(true);
    expect(box(/Specials/).checked).toBe(false);

    const labels = screen.getAllByRole('listitem').map((li) => li.textContent ?? '');
    expect(labels[labels.length - 1]).toMatch(/Specials/);
    expect(submitButton().textContent).toContain('Request season 3');
  });

  it('sends the picked seasons and closes on success', async () => {
    mockSeasons.mockResolvedValue([S(1, { owned_episodes: 10 }), S(2), S(3), S(0)]);
    const onsubmit = vi.fn().mockResolvedValue(null);
    const onclose = vi.fn();
    render(SeasonPicker, { tmdbId: 7, title: 'Show', onsubmit, onclose });
    await screen.findByText('In library');

    await fireEvent.click(box(/Season 3/)); // untick 3
    await fireEvent.click(box(/Specials/)); // tick specials
    expect(submitButton().textContent).toContain('Request season 2 + specials');
    await fireEvent.click(submitButton());
    await waitFor(() => expect(onsubmit).toHaveBeenCalledWith([0, 2]));
    await waitFor(() => expect(onclose).toHaveBeenCalledTimes(1));
  });

  it('omits seasons for a fresh show with everything picked', async () => {
    mockSeasons.mockResolvedValue([S(1), S(2), S(0)]);
    const onsubmit = vi.fn().mockResolvedValue(null);
    render(SeasonPicker, { tmdbId: 7, title: 'Show', onsubmit, onclose: vi.fn() });
    await screen.findByText(/Season 2/);
    expect(submitButton().textContent).toContain('Request all seasons');
    await fireEvent.click(submitButton());
    await waitFor(() => expect(onsubmit).toHaveBeenCalledWith(undefined));
  });

  it('"All seasons" clears and restores the regular seasons', async () => {
    mockSeasons.mockResolvedValue([S(1), S(2), S(0)]);
    render(SeasonPicker, { tmdbId: 7, title: 'Show', onsubmit: vi.fn(), onclose: vi.fn() });
    await screen.findByText(/Season 2/);
    const all = box(/All seasons/);
    expect(all.checked).toBe(true);
    await fireEvent.click(all);
    expect(box(/Season 1/).checked).toBe(false);
    expect(box(/Season 2/).checked).toBe(false);
    expect(submitButton().disabled).toBe(true);
    await fireEvent.click(all);
    expect(box(/Season 1/).checked).toBe(true);
  });

  it('keeps the picker open with the error when the request fails', async () => {
    mockSeasons.mockResolvedValue([S(1), S(2)]);
    const onsubmit = vi.fn().mockResolvedValue('you already have an active request for one of these seasons');
    const onclose = vi.fn();
    render(SeasonPicker, { tmdbId: 7, title: 'Show', onsubmit, onclose });
    await screen.findByText(/Season 2/);
    await fireEvent.click(submitButton());
    const alert = await screen.findByRole('alert');
    expect(alert.textContent).toContain('one of these seasons');
    expect(onclose).not.toHaveBeenCalled();
  });

  it('offers "Request all seasons" when the season list can’t be loaded', async () => {
    mockSeasons.mockRejectedValue(new Error("couldn't read the season list from TMDB"));
    const onsubmit = vi.fn().mockResolvedValue(null);
    render(SeasonPicker, { tmdbId: 7, title: 'Show', onsubmit, onclose: vi.fn() });
    expect((await screen.findByRole('alert')).textContent).toContain('season list');
    await fireEvent.click(screen.getByRole('button', { name: 'Request all seasons' }));
    await waitFor(() => expect(onsubmit).toHaveBeenCalledWith(undefined));
  });

  it('cancel closes without requesting', async () => {
    mockSeasons.mockResolvedValue([S(1)]);
    const onsubmit = vi.fn();
    const onclose = vi.fn();
    render(SeasonPicker, { tmdbId: 7, title: 'Show', onsubmit, onclose });
    await screen.findByText(/Season 1/);
    await fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(onclose).toHaveBeenCalledTimes(1);
    expect(onsubmit).not.toHaveBeenCalled();
  });
});
