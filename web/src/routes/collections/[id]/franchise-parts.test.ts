import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import type { CollectionItem, FranchisePart } from '$lib/api';
import FranchiseParts from './FranchiseParts.svelte';

const mockCreate = vi.hoisted(() => vi.fn());
const mockQuota = vi.hoisted(() => vi.fn());

vi.mock('$lib/api', () => ({
  requestsApi: { create: mockCreate, quota: mockQuota },
  assetUrl: (p: string) => `http://srv${p}`,
}));
const capsStore = vi.hoisted(() => ({}) as { set: (v: unknown) => void });
vi.mock('$lib/stores/capabilities', async () => {
  const { writable } = await import('svelte/store');
  const capabilities = writable<unknown>(null);
  capsStore.set = capabilities.set;
  return { capabilities };
});

const parts: FranchisePart[] = [
  { tmdb_id: 348, title: 'Alien', year: 1979, item_id: 'i-1', request_status: null },
  { tmdb_id: 679, title: 'Aliens', year: 1986, item_id: null, request_status: null },
  { tmdb_id: 8077, title: 'Alien³', year: 1992, item_id: null, request_status: 'approved' },
];
const items: CollectionItem[] = [{ id: 'i-1', title: 'Alien', type: 'movie', year: 1979, poster_path: 'movies/alien.jpg' }];

const quota = { can_request: true, window_days: 7, movies: { limit: 0, used: 0, remaining: null }, tv: { limit: 0, used: 0, remaining: null } };

function card(title: string): HTMLElement {
  const el = screen.getByText(title, { selector: '.title' }).closest('.card');
  if (!el) throw new Error(`no card for ${title}`);
  return el as HTMLElement;
}

beforeEach(() => {
  vi.clearAllMocks();
  mockQuota.mockResolvedValue(quota);
  capsStore.set({ features: { requests: true } });
});

describe('FranchiseParts', () => {
  it('renders owned parts as links and missing parts greyed with their request state', async () => {
    render(FranchiseParts, { parts, items });

    const owned = card('Alien');
    expect(owned.tagName).toBe('A');
    expect(owned.getAttribute('href')).toBe('/watch/i-1');
    expect(owned.querySelector('img')?.getAttribute('src')).toBe('http://srv/artwork/movies/alien.jpg?w=300');

    const missing = card('Aliens');
    expect(missing.classList.contains('missing')).toBe(true);
    expect(missing.querySelector('button')?.textContent?.trim()).toBe('Request');

    // Already requested: status instead of a button.
    const requested = card('Alien³');
    expect(requested.querySelector('button')).toBeNull();
    expect(requested.textContent).toContain('Approved');

    expect(screen.getByText(/1 of 3 films/)).toBeTruthy();
  });

  it('requests a missing movie by TMDB id and flips the row', async () => {
    mockCreate.mockResolvedValue({ id: 'r-1', status: 'pending', auto_approved: false });
    render(FranchiseParts, { parts, items });
    await waitFor(() => expect(mockQuota).toHaveBeenCalled());

    await fireEvent.click(screen.getByRole('button', { name: 'Request' }));

    await waitFor(() => expect(screen.getByRole('status').textContent).toContain('Requested: Aliens'));
    expect(mockCreate).toHaveBeenCalledWith({ type: 'movie', tmdb_id: 679 });
    expect(card('Aliens').querySelector('button')).toBeNull();
    expect(card('Aliens').textContent).toContain('Requested');
  });

  it('says so when the request went over quota', async () => {
    mockCreate.mockResolvedValue({ id: 'r-1', status: 'pending', auto_approved: false, over_quota: true });
    render(FranchiseParts, { parts, items });
    await fireEvent.click(screen.getByRole('button', { name: 'Request' }));
    await waitFor(() => expect(screen.getByRole('status').textContent).toContain('reached your limit'));
  });

  it("shows the server's message when the request is refused", async () => {
    mockCreate.mockRejectedValue(Object.assign(new Error('You have 25 pending requests — wait for some to be handled'), { status: 403, code: 'FORBIDDEN' }));
    render(FranchiseParts, { parts, items });
    await fireEvent.click(screen.getByRole('button', { name: 'Request' }));
    const alert = await screen.findByRole('alert');
    expect(alert.textContent).toContain('You have 25 pending requests');
    // Still requestable.
    expect(card('Aliens').querySelector('button')?.hasAttribute('disabled')).toBe(false);
  });

  it('drops Request when requesting is turned off for the account', async () => {
    mockQuota.mockResolvedValue({ ...quota, can_request: false });
    render(FranchiseParts, { parts, items });
    await waitFor(() => expect(card('Aliens').querySelector('button')).toBeNull());
    // Still listed, greyed out — just nothing to press.
    expect(card('Aliens').classList.contains('missing')).toBe(true);
    expect(screen.queryByText(/turned off/)).toBeNull();
  });

  it('drops Request when the server has requests off', async () => {
    capsStore.set({ features: { requests: false } });
    render(FranchiseParts, { parts, items });
    expect(card('Aliens').querySelector('button')).toBeNull();
  });

  it('drops Request after a REQUESTS_DISABLED refusal', async () => {
    mockCreate.mockRejectedValue(Object.assign(new Error('requests disabled'), { status: 403, code: 'REQUESTS_DISABLED' }));
    render(FranchiseParts, { parts, items });
    await waitFor(() => expect(mockQuota).toHaveBeenCalled());
    await fireEvent.click(screen.getByRole('button', { name: 'Request' }));
    await waitFor(() => expect(card('Aliens').querySelector('button')).toBeNull());
    expect((await screen.findByRole('alert')).textContent).toContain('Requesting is turned off');
  });

  it('skips the quota lookup when nothing is missing', async () => {
    render(FranchiseParts, { parts: [parts[0]], items });
    await new Promise((r) => setTimeout(r, 0));
    expect(mockQuota).not.toHaveBeenCalled();
    expect(screen.queryByRole('button')).toBeNull();
  });
});
