import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import MyDownloads from './MyDownloads.svelte';

const mockList = vi.hoisted(() => vi.fn());
const mockRemove = vi.hoisted(() => vi.fn());

vi.mock('$lib/api', () => ({
  assetUrl: (p: string) => p,
  downloadsApi: { listMine: mockList, remove: mockRemove },
  ApiRequestError: class extends Error {},
}));
vi.mock('$lib/stores/toast', () => ({ toast: { success: vi.fn(), error: vi.fn() } }));
vi.mock('$lib/downloads', async (orig) => ({
  ...(await orig<typeof import('$lib/downloads')>()),
  DOWNLOAD_POLL_MS: 5,
}));

const job = (over: Record<string, unknown>) => ({
  id: 'j1', item_id: 'item-1', file_id: 'f1', title: 'Heat', item_type: 'movie',
  quality: '720p_4mbps', quality_label: '720p · 4 Mbps', status: 'ready', progress: 1,
  created_at: new Date().toISOString(), ...over,
});

beforeEach(() => vi.clearAllMocks());

describe('MyDownloads', () => {
  it('says when there are none', async () => {
    mockList.mockResolvedValue([]);
    render(MyDownloads);
    expect(await screen.findByText(/No downloads yet/)).toBeTruthy();
  });

  it('lists ready files with a link and size, and removes one', async () => {
    mockList.mockResolvedValue([
      job({ size_bytes: 730 * 1024 * 1024, file_url: '/api/v1/downloads/j1/file' }),
      job({ id: 'j2', title: 'Ronin', status: 'failed', error: 'damaged', progress: 0 }),
    ]);
    mockRemove.mockResolvedValue(undefined);
    render(MyDownloads);
    const link = (await screen.findByRole('link', { name: 'Download' })) as HTMLAnchorElement;
    expect(link.getAttribute('href')).toBe('/api/v1/downloads/j1/file');
    expect(screen.getByText(/730 MB/)).toBeTruthy();
    expect(screen.getByText('Failed: damaged')).toBeTruthy();
    await fireEvent.click(screen.getByRole('button', { name: /Remove Ronin/ }));
    await waitFor(() => expect(mockRemove).toHaveBeenCalledWith('j2'));
    await waitFor(() => expect(screen.queryByText('Ronin')).toBeNull());
  });

  it('refreshes while a download is being prepared', async () => {
    mockList
      .mockResolvedValueOnce([job({ status: 'running', progress: 0.3 })])
      .mockResolvedValue([job({ file_url: '/api/v1/downloads/j1/file' })]);
    render(MyDownloads);
    expect(await screen.findByText('Preparing · 30%')).toBeTruthy();
    expect(screen.getByRole('progressbar')).toBeTruthy();
    expect(await screen.findByRole('link', { name: 'Download' })).toBeTruthy();
    expect(mockList.mock.calls.length).toBeGreaterThanOrEqual(2);
  });
});
