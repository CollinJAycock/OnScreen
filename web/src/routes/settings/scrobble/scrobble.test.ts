import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import Page from './+page.svelte';

const mockStatus = vi.hoisted(() => vi.fn());
const mockSet = vi.hoisted(() => vi.fn());
const mockStartLastFM = vi.hoisted(() => vi.fn());
const mockCompleteLastFM = vi.hoisted(() => vi.fn());
const mockUnlinkLastFM = vi.hoisted(() => vi.fn());
const mockStartTrakt = vi.hoisted(() => vi.fn());
const mockCompleteTrakt = vi.hoisted(() => vi.fn());
const mockUnlinkTrakt = vi.hoisted(() => vi.fn());
// The poll loop has its own tests; here each test settles a link by calling
// the onDone / onError the page handed it.
const mockPoll = vi.hoisted(() => vi.fn());

vi.mock('$lib/api', () => ({
  scrobbleApi: {
    status: mockStatus,
    setListenBrainz: mockSet,
    startLastFM: mockStartLastFM,
    completeLastFM: mockCompleteLastFM,
    unlinkLastFM: mockUnlinkLastFM,
    startTrakt: mockStartTrakt,
    completeTrakt: mockCompleteTrakt,
    unlinkTrakt: mockUnlinkTrakt,
  },
}));

vi.mock('$lib/scrobbleLink', () => ({ pollLink: mockPoll }));

vi.mock('$lib/stores/toast', () => ({
  toast: { success: vi.fn(), error: vi.fn() },
}));

beforeEach(() => {
  vi.clearAllMocks();
  mockSet.mockResolvedValue(undefined);
  mockPoll.mockReturnValue(() => {});
});

const available = {
  listenbrainz_linked: false,
  listenbrainz_enabled: false,
  lastfm_available: true,
  lastfm_linked: false,
  trakt_available: true,
  trakt_linked: false,
};

/** The options the page passed to its most recent pollLink call. */
function lastPoll() {
  return mockPoll.mock.calls[mockPoll.mock.calls.length - 1][0];
}

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

  it('says Last.fm and Trakt need an admin when the server has no credentials', async () => {
    // An older server omits the fields entirely.
    mockStatus.mockResolvedValue({ listenbrainz_linked: false, listenbrainz_enabled: false });
    render(Page);

    await screen.findByText(/Last.fm isn't set up on this server/i);
    expect(screen.getByText(/Trakt isn't set up on this server/i)).toBeTruthy();
    expect(screen.queryByRole('button', { name: /connect/i })).toBeNull();
  });

  it('links Last.fm through its approval page', async () => {
    mockStatus
      .mockResolvedValueOnce(available)
      .mockResolvedValue({ ...available, lastfm_linked: true, lastfm_username: 'rj' });
    mockStartLastFM.mockResolvedValue({ auth_url: 'https://www.last.fm/api/auth/?token=t', pending: 'sealed' });
    mockCompleteLastFM.mockResolvedValue({ status: 'pending' });
    render(Page);

    await fireEvent.click(await screen.findByRole('button', { name: /connect last.fm/i }));

    const open = await screen.findByRole('link', { name: /open last.fm/i });
    expect(open.getAttribute('href')).toBe('https://www.last.fm/api/auth/?token=t');
    expect(open.getAttribute('target')).toBe('_blank');
    expect(screen.getByText(/waiting for last.fm/i)).toBeTruthy();

    // The poll completes with the sealed handle from the start call.
    await lastPoll().complete();
    expect(mockCompleteLastFM).toHaveBeenCalledWith('sealed');

    await lastPoll().onDone('linked', 'rj');
    await screen.findByText('rj');
    expect(screen.getByRole('button', { name: /disconnect last.fm/i })).toBeTruthy();
  });

  it('links Trakt with a device code and reports a decline', async () => {
    mockStatus.mockResolvedValue(available);
    mockStartTrakt.mockResolvedValue({
      user_code: 'ABCD1234', verification_url: 'https://trakt.tv/activate',
      expires_in: 600, interval: 5, pending: 'sealed-t',
    });
    render(Page);

    await fireEvent.click(await screen.findByRole('button', { name: /connect trakt/i }));

    await screen.findByText('ABCD1234');
    const activate = screen.getByRole('link', { name: 'trakt.tv/activate' });
    expect(activate.getAttribute('href')).toBe('https://trakt.tv/activate');
    // Trakt's own cadence and lifetime drive the poll.
    expect(lastPoll().intervalMs).toBe(5000);
    expect(lastPoll().timeoutMs).toBe(600_000);

    await lastPoll().onDone('denied');
    await screen.findByText(/access was declined on trakt/i);
    expect(screen.queryByText('ABCD1234')).toBeNull();
    expect(screen.getByRole('button', { name: /connect trakt/i })).toBeTruthy();
  });

  it('shows a start failure instead of a code', async () => {
    mockStatus.mockResolvedValue(available);
    mockStartTrakt.mockRejectedValue(new Error("couldn't reach Trakt"));
    render(Page);

    await fireEvent.click(await screen.findByRole('button', { name: /connect trakt/i }));

    await screen.findByText("couldn't reach Trakt");
    expect(mockPoll).not.toHaveBeenCalled();
  });

  it('disconnects a linked service', async () => {
    mockStatus
      .mockResolvedValueOnce({ ...available, trakt_linked: true, trakt_username: 'tr' })
      .mockResolvedValue(available);
    mockUnlinkTrakt.mockResolvedValue(undefined);
    render(Page);

    await fireEvent.click(await screen.findByRole('button', { name: /disconnect trakt/i }));

    await waitFor(() => expect(mockUnlinkTrakt).toHaveBeenCalled());
    await screen.findByRole('button', { name: /connect trakt/i });
  });
});
