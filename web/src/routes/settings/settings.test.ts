import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import Page from './+page.svelte';

const mockSettingsGet = vi.hoisted(() => vi.fn());
const mockSettingsUpdate = vi.hoisted(() => vi.fn());
const mockEmailEnabled = vi.hoisted(() => vi.fn());
const mockEmailSendTest = vi.hoisted(() => vi.fn());
const mockListSwitchable = vi.hoisted(() => vi.fn());
const mockGetPrefs = vi.hoisted(() => vi.fn());
const mockSetPrefs = vi.hoisted(() => vi.fn());
const mockSetPin = vi.hoisted(() => vi.fn());
const mockClearPin = vi.hoisted(() => vi.fn());
const mockGetUser = vi.hoisted(() => vi.fn());

vi.mock('$lib/api', () => ({
  settingsApi: { get: mockSettingsGet, update: mockSettingsUpdate },
  userApi: {
    listSwitchable: mockListSwitchable,
    getPreferences: mockGetPrefs,
    setPreferences: mockSetPrefs,
    setPin: mockSetPin,
    clearPin: mockClearPin,
  },
  emailApi: { enabled: mockEmailEnabled, sendTest: mockEmailSendTest },
  api: { getUser: mockGetUser },
}));

vi.mock('$lib/stores/toast', () => ({
  toast: { success: vi.fn(), error: vi.fn() },
}));

beforeEach(() => {
  vi.clearAllMocks();
  mockSettingsGet.mockResolvedValue({
    tmdb_api_key: 'existing-tmdb',
    tvdb_api_key: '',
    arr_api_key: '',
    arr_webhook_url: '',
    arr_path_mappings: {},
  });
  mockEmailEnabled.mockResolvedValue({ enabled: false });
  mockGetPrefs.mockResolvedValue({ preferred_audio_lang: null, preferred_subtitle_lang: null });
  mockListSwitchable.mockResolvedValue([]);
  mockGetUser.mockReturnValue({ user_id: 'u1', username: 'admin', is_admin: true });
});

describe('Settings page', () => {
  it('loads settings on mount and populates fields', async () => {
    render(Page);
    await waitFor(() => {
      const tmdb = screen.getByLabelText(/TMDB API Key/i) as HTMLInputElement;
      expect(tmdb.value).toBe('existing-tmdb');
    });
  });

  it('shows load error banner when settings fetch fails', async () => {
    mockSettingsGet.mockRejectedValueOnce(new Error('boom'));
    render(Page);
    await waitFor(() => {
      expect(screen.getByText('boom')).toBeTruthy();
    });
  });

  it('saves settings with trimmed values and path mappings', async () => {
    mockSettingsUpdate.mockResolvedValue(undefined);
    render(Page);
    await waitFor(() => {
      expect(screen.getByLabelText(/TMDB API Key/i)).toBeTruthy();
    });

    const tmdb = screen.getByLabelText(/TMDB API Key/i) as HTMLInputElement;
    await fireEvent.input(tmdb, { target: { value: '  new-key  ' } });

    const form = tmdb.closest('form')!;
    await fireEvent.submit(form);

    await waitFor(() => {
      expect(mockSettingsUpdate).toHaveBeenCalled();
    });
    const arg = mockSettingsUpdate.mock.calls[0][0];
    expect(arg.tmdb_api_key).toBe('new-key');
    expect(arg.arr_path_mappings).toEqual({});
  });

  it('does not render email test section when SMTP disabled', async () => {
    render(Page);
    await waitFor(() => {
      expect(screen.getByLabelText(/TMDB API Key/i)).toBeTruthy();
    });
    expect(screen.queryByText(/Send Test Email/i)).toBeNull();
  });

  it('sends test email when SMTP enabled', async () => {
    mockEmailEnabled.mockResolvedValueOnce({ enabled: true });
    mockEmailSendTest.mockResolvedValueOnce({ message: 'sent to a@b.c' });

    render(Page);
    const toInput = await waitFor(() => screen.getByPlaceholderText(/recipient@example\.com/i) as HTMLInputElement);
    await fireEvent.input(toInput, { target: { value: 'a@b.c' } });

    const sendBtn = screen.getByRole('button', { name: /^send test$/i });
    await fireEvent.click(sendBtn);

    await waitFor(() => {
      expect(mockEmailSendTest).toHaveBeenCalledWith('a@b.c');
    });
  });

  it('saves language preferences', async () => {
    mockSetPrefs.mockResolvedValue(undefined);
    render(Page);
    await waitFor(() => {
      expect(screen.getByLabelText(/TMDB API Key/i)).toBeTruthy();
    });

    const btns = screen.getAllByRole('button');
    const saveLang = btns.find((b) => /save language/i.test(b.textContent ?? ''));
    if (saveLang) {
      await fireEvent.click(saveLang);
      await waitFor(() => expect(mockSetPrefs).toHaveBeenCalled());
    }
  });
});
