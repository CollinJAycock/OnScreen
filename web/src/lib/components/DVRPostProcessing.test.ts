import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import DVRPostProcessing from './DVRPostProcessing.svelte';

const mockGet = vi.hoisted(() => vi.fn());
const mockUpdate = vi.hoisted(() => vi.fn());
const mockToast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn(), info: vi.fn() }));

vi.mock('$lib/api', () => ({
  settingsApi: { getDVR: mockGet, updateDVR: mockUpdate },
}));
vi.mock('$lib/stores/toast', () => ({ toast: mockToast }));

const defaults = {
  remux: false, remux_container: 'mkv', transcode: false, transcode_codec: 'h264',
  transcode_quality: 'balanced', delete_original: false, commercials: 'off',
  script_configured: false, comskip_configured: false, commercial_detector: 'builtin',
};

beforeEach(() => {
  vi.clearAllMocks();
});

describe('DVRPostProcessing', () => {
  it('shows the env-only script read-only', async () => {
    mockGet.mockResolvedValue({ ...defaults, script_configured: true, script_path: '/opt/post.sh' });
    render(DVRPostProcessing);
    expect(await screen.findByText('/opt/post.sh')).toBeTruthy();
    expect(screen.getAllByText(/configured via environment/).length).toBe(2);
    // There is no input for an executable path.
    expect(screen.queryByDisplayValue('/opt/post.sh')).toBeNull();
  });

  it('only offers cutting once remux or transcode is on', async () => {
    mockGet.mockResolvedValue({ ...defaults });
    render(DVRPostProcessing);
    const cut = (await screen.findByRole('option', { name: /Cut them out/ })) as HTMLOptionElement;
    expect(cut.disabled).toBe(true);
    await fireEvent.click(screen.getByLabelText(/Remux recordings/));
    expect(cut.disabled).toBe(false);
  });

  it('saves the editable fields only', async () => {
    mockGet.mockResolvedValue({ ...defaults, script_configured: true, script_path: '/opt/post.sh' });
    mockUpdate.mockResolvedValue(undefined);
    render(DVRPostProcessing);
    await fireEvent.click(await screen.findByLabelText(/Remux recordings/));
    await fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(mockUpdate).toHaveBeenCalled());
    expect(mockUpdate).toHaveBeenCalledWith({
      remux: true, remux_container: 'mkv', transcode: false, transcode_codec: 'h264',
      transcode_quality: 'balanced', delete_original: false, commercials: 'off',
    });
    expect(mockToast.success).toHaveBeenCalled();
  });
});
