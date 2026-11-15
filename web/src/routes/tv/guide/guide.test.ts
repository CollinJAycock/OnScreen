// The Live TV guide: one fetch per window, and the newest window wins when
// Earlier / Later are clicked faster than the server answers.
import { render, screen, waitFor, fireEvent } from '@testing-library/svelte';

const guide = vi.hoisted(() => vi.fn());
const channels = vi.hoisted(() => vi.fn());
vi.mock('$app/navigation', () => ({ goto: vi.fn() }));
vi.mock('$lib/stores/toast', () => ({ toast: { success: vi.fn(), error: vi.fn() } }));
vi.mock('$lib/api', () => ({
  liveTvApi: { channels, guide, createSchedule: vi.fn() },
}));

import Page from './+page.svelte';

function program(id: string, title: string, from: string) {
  const start = new Date(from);
  return {
    id, channel_id: 'ch-1', title,
    starts_at: start.toISOString(),
    ends_at: new Date(start.getTime() + 30 * 60_000).toISOString(),
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.setItem('onscreen_user', JSON.stringify({ user_id: 'u1', username: 'viewer' }));
  channels.mockResolvedValue({ items: [{ id: 'ch-1', number: '2.1', name: 'News' }], total: 1 });
  guide.mockImplementation((from: string) => Promise.resolve({ items: [program(`p-${from}`, `Show at ${from}`, from)], total: 1 }));
});

describe('Live TV guide', () => {
  it('loads the window once per visit', async () => {
    render(Page);
    await waitFor(() => expect(guide).toHaveBeenCalled());
    await new Promise((r) => setTimeout(r, 30));
    expect(guide).toHaveBeenCalledTimes(1);
    expect(channels).toHaveBeenCalledTimes(1);
  });

  it("shows the newest window when an older window's answer lands last", async () => {
    render(Page);
    await waitFor(() => expect(guide).toHaveBeenCalledTimes(1));
    let releaseOlder!: () => void;
    guide.mockImplementationOnce((from: string) => new Promise((resolve) => {
      releaseOlder = () => resolve({ items: [program('older', 'Older Window Show', from)], total: 1 });
    }));
    const later = screen.getByText('Later →');
    await fireEvent.click(later);
    await fireEvent.click(later);
    await waitFor(() => expect(guide).toHaveBeenCalledTimes(3));
    const newestFrom = guide.mock.calls[2][0] as string;
    expect(await screen.findByText(`Show at ${newestFrom}`)).toBeTruthy();
    releaseOlder();
    await new Promise((r) => setTimeout(r, 30));
    expect(screen.queryByText('Older Window Show')).toBeNull();
  });
});
