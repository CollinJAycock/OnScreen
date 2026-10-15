import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import DownloadMenu from './DownloadMenu.svelte';

const mockOptions = vi.hoisted(() => vi.fn());
const mockCreate = vi.hoisted(() => vi.fn());
const mockGet = vi.hoisted(() => vi.fn());
const mockToast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }));
const mockStart = vi.hoisted(() => vi.fn());

vi.mock('$lib/api', () => ({
  assetUrl: (p: string) => p,
  downloadsApi: { options: mockOptions, create: mockCreate, get: mockGet },
  ApiRequestError: class extends Error {
    constructor(message: string, readonly status: number, readonly code: string) {
      super(message);
    }
  },
}));
vi.mock('$lib/stores/toast', () => ({ toast: mockToast }));
vi.mock('$lib/native', () => ({ isTauri: () => false, nativeDownload: vi.fn() }));
vi.mock('$lib/downloads', async (orig) => ({
  ...(await orig<typeof import('$lib/downloads')>()),
  DOWNLOAD_POLL_MS: 5,
  startBrowserDownload: mockStart,
}));

const props = { itemId: 'item-1', title: 'Heat', fileId: 'file-1', streamToken: 'tok', container: 'mkv' };

const qualities = [
  { id: '1080p_8mbps', label: '1080p · 8 Mbps', bitrate_kbps: 8000, max_height: 1080, allowed: false },
  { id: '720p_4mbps', label: '720p · 4 Mbps', bitrate_kbps: 4000, max_height: 720, allowed: true },
];

const job = (over: Record<string, unknown>) => ({
  id: 'job-1', item_id: 'item-1', file_id: 'file-1', title: 'Heat', item_type: 'movie',
  quality: '720p_4mbps', quality_label: '720p · 4 Mbps', status: 'queued', progress: 0,
  created_at: new Date().toISOString(), ...over,
});

beforeEach(() => {
  vi.clearAllMocks();
  mockOptions.mockResolvedValue({ file_id: 'file-1', qualities });
});

async function openMenu() {
  await fireEvent.click(screen.getByRole('button', { name: /Download/ }));
  await waitFor(() => expect(mockOptions).toHaveBeenCalledWith('item-1', 'file-1'));
}

describe('DownloadMenu', () => {
  it('offers the original and the converted qualities, disabling those above the limit', async () => {
    render(DownloadMenu, props);
    await openMenu();
    const original = screen.getByRole('menuitem', { name: /Original/ }) as HTMLAnchorElement;
    expect(original.getAttribute('href')).toBe('/media/download/file-1?token=tok');
    const hd = await screen.findByRole('menuitem', { name: /1080p · 8 Mbps/ });
    expect((hd as HTMLButtonElement).disabled).toBe(true);
    expect(hd.textContent).toMatch(/above your limit/);
    expect((screen.getByRole('menuitem', { name: /720p · 4 Mbps/ }) as HTMLButtonElement).disabled).toBe(false);
  });

  it('creates a job, follows its progress, then starts the download', async () => {
    mockCreate.mockResolvedValue(job({ status: 'running', progress: 0.25 }));
    mockGet
      .mockResolvedValueOnce(job({ status: 'running', progress: 0.6 }))
      .mockResolvedValueOnce(job({ status: 'ready', progress: 1, file_url: '/api/v1/downloads/job-1/file' }));
    render(DownloadMenu, props);
    await openMenu();
    await fireEvent.click(await screen.findByRole('menuitem', { name: /720p · 4 Mbps/ }));
    expect(mockCreate).toHaveBeenCalledWith('item-1', '720p_4mbps', 'file-1');
    await waitFor(() => expect(mockStart).toHaveBeenCalledWith('/api/v1/downloads/job-1/file'));
    expect(mockGet).toHaveBeenCalledWith('job-1');
    await waitFor(() => expect(screen.getByRole('button', { name: /^\s*Download/ })).toBeTruthy());
  });

  it('starts a ready shared file straight away', async () => {
    mockCreate.mockResolvedValue(job({ status: 'ready', progress: 1, file_url: '/api/v1/downloads/job-1/file' }));
    render(DownloadMenu, props);
    await openMenu();
    await fireEvent.click(await screen.findByRole('menuitem', { name: /720p · 4 Mbps/ }));
    await waitFor(() => expect(mockStart).toHaveBeenCalledTimes(1));
    expect(mockGet).not.toHaveBeenCalled();
  });

  it('reports a failed conversion', async () => {
    mockCreate.mockResolvedValue(job({ status: 'failed', error: 'the file is damaged' }));
    render(DownloadMenu, props);
    await openMenu();
    await fireEvent.click(await screen.findByRole('menuitem', { name: /720p · 4 Mbps/ }));
    await waitFor(() => expect(mockToast.error).toHaveBeenCalledWith(expect.stringMatching(/damaged/)));
    expect(mockStart).not.toHaveBeenCalled();
  });
});
