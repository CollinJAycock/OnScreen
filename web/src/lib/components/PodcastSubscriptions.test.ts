import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import PodcastSubscriptions from './PodcastSubscriptions.svelte';

const mockList = vi.hoisted(() => vi.fn());
const mockSubscribe = vi.hoisted(() => vi.fn());
const mockToast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn(), info: vi.fn() }));

vi.mock('$lib/api', () => ({
  podcastApi: { listFeeds: mockList, subscribe: mockSubscribe },
}));
vi.mock('$lib/stores/toast', () => ({ toast: mockToast }));

beforeEach(() => {
  vi.clearAllMocks();
});

describe('PodcastSubscriptions', () => {
  it("lists the library's feeds with their counts", async () => {
    mockList.mockResolvedValue({
      items: [{ id: 'f-1', title: 'Night Radio', author: 'Jane', image_url: 'https://a/art.jpg',
        episode_count: 12, downloaded_count: 3, auto_download: true, keep_latest: 0 }],
      total: 1,
    });
    render(PodcastSubscriptions, { libraryId: 'lib-1' });
    const link = await screen.findByRole('link', { name: /Night Radio/ });
    expect(mockList).toHaveBeenCalledWith('lib-1');
    expect(link.getAttribute('href')).toBe('/podcasts/feeds/f-1');
    expect(link.textContent).toContain('12 episodes · 3 downloaded');
  });

  it('explains the empty state', async () => {
    mockList.mockResolvedValue({ items: [], total: 0 });
    render(PodcastSubscriptions, { libraryId: 'lib-1' });
    expect(await screen.findByText('No subscriptions yet')).toBeTruthy();
  });

  it('subscribes with the form settings and reloads', async () => {
    mockList.mockResolvedValue({ items: [], total: 0 });
    mockSubscribe.mockResolvedValue({ id: 'f-2', title: 'New Show' });
    const onchange = vi.fn();
    render(PodcastSubscriptions, { libraryId: 'lib-1', onchange });
    await screen.findByText('No subscriptions yet');

    await fireEvent.input(screen.getByLabelText('RSS feed URL'), { target: { value: ' https://a/feed.xml ' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Subscribe' }));

    await waitFor(() => expect(mockSubscribe).toHaveBeenCalled());
    expect(mockSubscribe).toHaveBeenCalledWith({
      library_id: 'lib-1', feed_url: 'https://a/feed.xml', auto_download: true, keep_latest: 0, download_latest: 1,
    });
    await waitFor(() => expect(onchange).toHaveBeenCalled());
    expect(mockList).toHaveBeenCalledTimes(2);
    expect(mockToast.success).toHaveBeenCalledWith(expect.stringContaining('New Show'));
  });

  it('shows why a subscription was refused', async () => {
    mockList.mockResolvedValue({ items: [], total: 0 });
    mockSubscribe.mockRejectedValue(new Error("the URL isn't an RSS podcast feed"));
    render(PodcastSubscriptions, { libraryId: 'lib-1' });
    await screen.findByText('No subscriptions yet');
    await fireEvent.input(screen.getByLabelText('RSS feed URL'), { target: { value: 'https://a/page' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Subscribe' }));
    const alert = await screen.findByRole('alert');
    expect(alert.textContent).toContain("isn't an RSS podcast feed");
  });
});
