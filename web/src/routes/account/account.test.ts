import { render, screen, waitFor } from '@testing-library/svelte';
import Page from './+page.svelte';

// The Account page is where every user — not only admins, who also have
// Settings — reaches their own sign-in settings. These tests cover which of
// those cards show; each card's own behaviour is tested with its component.

const mockGetPrefs = vi.hoisted(() => vi.fn());
const Stub = vi.hoisted(() => () => {});

vi.mock('$lib/api', () => ({
  getApiBase: () => '/api/v1',
  userApi: { getPreferences: mockGetPrefs, exportMyData: vi.fn() },
}));
vi.mock('$lib/stores/capabilities', async () => {
  const { writable } = await import('svelte/store');
  return { capabilities: writable(null), ensureCapabilities: vi.fn() };
});
vi.mock('$lib/stores/toast', () => ({ toast: { success: vi.fn(), error: vi.fn() } }));
vi.mock('$lib/components/SignedInDevices.svelte', () => ({ default: Stub }));
vi.mock('$lib/components/MyDownloads.svelte', () => ({ default: Stub }));
vi.mock('$lib/components/ApiKeys.svelte', () => ({ default: Stub }));
vi.mock('$lib/components/PlaybackQuality.svelte', () => ({ default: Stub }));
vi.mock('$lib/components/DigestOptIn.svelte', () => ({ default: Stub }));
vi.mock('$lib/components/ChangePassword.svelte', () => ({ default: Stub }));
vi.mock('$lib/components/TwoFactor.svelte', () => ({ default: Stub }));
vi.mock('$lib/components/LanguagePrefs.svelte', () => ({ default: Stub }));

beforeEach(() => {
  vi.clearAllMocks();
});

describe('Account page', () => {
  it('offers password, two-factor, languages and scrobbling to an account with a password', async () => {
    mockGetPrefs.mockResolvedValue({ has_password: true, has_pin: false });
    render(Page);

    await waitFor(() => expect(screen.getByRole('heading', { name: 'Password' })).toBeTruthy());
    expect(screen.getByText(/signs you out on your other devices/i)).toBeTruthy();
    // An account's own PIN isn't used for switching (only managed profiles').
    expect(screen.queryByRole('heading', { name: 'Profile PIN' })).toBeNull();
    expect(screen.getByRole('heading', { name: 'Languages' })).toBeTruthy();
    expect(screen.getByRole('link', { name: /link your accounts/i }).getAttribute('href')).toBe('/account/scrobble');
  });

  it('explains instead of offering a password change when the account has no OnScreen password', async () => {
    mockGetPrefs.mockResolvedValue({ has_password: false });
    render(Page);

    await waitFor(() => expect(screen.getByText(/no OnScreen password to change/i)).toBeTruthy());
    expect(screen.queryByText(/signs you out on your other devices/i)).toBeNull();
  });

  it('treats an older server that does not report has_password as having one', async () => {
    mockGetPrefs.mockResolvedValue({});
    render(Page);

    await waitFor(() => expect(screen.getByText(/signs you out on your other devices/i)).toBeTruthy());
  });
});
