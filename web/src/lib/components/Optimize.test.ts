import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import OptimizeDialog from './OptimizeDialog.svelte';
import OptimizedVersionsList from './OptimizedVersionsList.svelte';

const mockProfiles = vi.hoisted(() => vi.fn());
const mockCreate = vi.hoisted(() => vi.fn());
const mockList = vi.hoisted(() => vi.fn());
const mockRemove = vi.hoisted(() => vi.fn());
const mockToast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }));
const mockConfirm = vi.hoisted(() => vi.fn());

vi.mock('$lib/api', () => ({
  optimizeApi: { profiles: mockProfiles, create: mockCreate, list: mockList, remove: mockRemove },
  ApiRequestError: class extends Error {
    constructor(message: string, readonly status: number, readonly code: string) {
      super(message);
    }
  },
}));
vi.mock('$lib/stores/toast', () => ({ toast: mockToast }));
vi.mock('$lib/native', () => ({ confirmAction: mockConfirm }));

const profiles = [
  { id: 'mobile_720p', label: 'Mobile · 720p 4 Mbps', max_height: 720, bitrate_kbps: 4000 },
  { id: 'tv_1080p', label: 'TV · 1080p 8 Mbps', max_height: 1080, bitrate_kbps: 8000 },
  { id: 'original_compatible', label: 'Original quality, compatible (H.264/AAC)', max_height: 2160, bitrate_kbps: 40000, keep_resolution: true },
];

const job = (over: Record<string, unknown>) => ({
  id: 'v1', item_id: 'item-1', file_id: 'file-1', profile: 'mobile_720p',
  profile_label: 'Mobile · 720p 4 Mbps', status: 'ready', progress: 1,
  created_at: new Date().toISOString(), ...over,
});

beforeEach(() => {
  vi.clearAllMocks();
  mockProfiles.mockResolvedValue(profiles);
});

describe('OptimizeDialog', () => {
  it('queues the chosen profile for the selection and reports the result', async () => {
    mockCreate.mockResolvedValue({ profile: 'tv_1080p', items: 12, queued: 10, existing: 2, skipped: 0 });
    const onclose = vi.fn();
    const ondone = vi.fn();
    render(OptimizeDialog, { open: true, selection: { library_id: 'lib-1' }, what: 'Movies', onclose, ondone });
    const tv = await screen.findByRole('radio', { name: /TV · 1080p 8 Mbps/ });
    await fireEvent.click(tv);
    await fireEvent.click(screen.getByRole('button', { name: 'Optimize' }));
    await waitFor(() => expect(mockCreate).toHaveBeenCalledWith({ library_id: 'lib-1' }, 'tv_1080p'));
    expect(mockToast.success).toHaveBeenCalledWith('10 queued, 2 already optimized or in progress.');
    expect(ondone).toHaveBeenCalled();
    expect(onclose).toHaveBeenCalled();
  });

  it('renders nothing while closed', () => {
    render(OptimizeDialog, { open: false, selection: { item_ids: ['i'] }, what: 'X' });
    expect(screen.queryByRole('dialog')).toBeNull();
  });
});

describe('OptimizedVersionsList', () => {
  it('lists an item’s versions with progress, and deletes one', async () => {
    mockList.mockResolvedValue({
      jobs: [job({}), job({ id: 'v2', profile: 'tv_1080p', profile_label: 'TV · 1080p 8 Mbps', status: 'running', progress: 0.42 })],
      counts: { ready: 1, running: 1 },
    });
    mockConfirm.mockResolvedValue(true);
    mockRemove.mockResolvedValue(undefined);
    render(OptimizedVersionsList, { itemId: 'item-1' });
    expect(await screen.findByText('Encoding · 42%')).toBeTruthy();
    expect(mockList).toHaveBeenCalledWith(expect.objectContaining({ item_id: 'item-1' }));
    await fireEvent.click(screen.getByRole('button', { name: 'Delete' }));
    await waitFor(() => expect(mockRemove).toHaveBeenCalledWith('v1'));
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeTruthy();
  });

  it('hides itself on a title with no versions', async () => {
    mockList.mockResolvedValue({ jobs: [], counts: {} });
    const { container } = render(OptimizedVersionsList, { itemId: 'item-1', hideWhenEmpty: true });
    await waitFor(() => expect(mockList).toHaveBeenCalled());
    await waitFor(() => expect(container.querySelector('.opt-list')).toBeNull());
  });
});
