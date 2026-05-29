import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import Page from './+page.svelte';

const mockStatus = vi.hoisted(() => vi.fn());
const mockSet = vi.hoisted(() => vi.fn());

vi.mock('$lib/api', () => ({
  scrobbleApi: { status: mockStatus, setListenBrainz: mockSet },
}));

vi.mock('$lib/stores/toast', () => ({
  toast: { success: vi.fn(), error: vi.fn() },
}));

beforeEach(() => {
  vi.clearAllMocks();
  mockSet.mockResolvedValue(undefined);
});

describe('Scrobbling settings page', () => {
  it('shows the link form when no account is linked', async () => {
    mockStatus.mockResolvedValue({ listenbrainz_linked: false, listenbrainz_enabled: false });
    render(Page);

    await waitFor(() => {
      expect(screen.getByPlaceholderText(/ListenBrainz user token/i)).toBeTruthy();
    });
    expect(screen.getByRole('button', { name: /link account/i })).toBeTruthy();
  });

  it('submits a trimmed token and enables on link', async () => {
    // First load is unlinked; after linking, status reports linked.
    mockStatus
      .mockResolvedValueOnce({ listenbrainz_linked: false, listenbrainz_enabled: false })
      .mockResolvedValue({ listenbrainz_linked: true, listenbrainz_enabled: true });
    render(Page);

    const input = (await screen.findByPlaceholderText(/ListenBrainz user token/i)) as HTMLInputElement;
    await fireEvent.input(input, { target: { value: '  tok123  ' } });
    await fireEvent.submit(input.closest('form')!);

    await waitFor(() => expect(mockSet).toHaveBeenCalledWith('tok123', true));
  });

  it('does not submit an empty token from the link form', async () => {
    mockStatus.mockResolvedValue({ listenbrainz_linked: false, listenbrainz_enabled: false });
    render(Page);

    const input = (await screen.findByPlaceholderText(/ListenBrainz user token/i)) as HTMLInputElement;
    await fireEvent.input(input, { target: { value: '   ' } });
    await fireEvent.submit(input.closest('form')!);

    // Whitespace-only is treated as empty — link() bails before calling the API.
    expect(mockSet).not.toHaveBeenCalled();
  });

  it('shows the linked state and unlinks with an empty token', async () => {
    mockStatus.mockResolvedValue({ listenbrainz_linked: true, listenbrainz_enabled: true });
    render(Page);

    const unlink = await screen.findByRole('button', { name: /unlink account/i });
    await fireEvent.click(unlink);

    await waitFor(() => expect(mockSet).toHaveBeenCalledWith('', false));
  });

  it('surfaces a load failure without crashing', async () => {
    mockStatus.mockRejectedValueOnce(new Error('boom'));
    render(Page);

    await waitFor(() => {
      expect(screen.getByText('boom')).toBeTruthy();
    });
  });
});
