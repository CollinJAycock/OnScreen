import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import type { User } from '$lib/api';
import Page from './+page.svelte';

const mockGetUser = vi.hoisted(() => vi.fn());
const mockUserList = vi.hoisted(() => vi.fn());
const mockSetPerms = vi.hoisted(() => vi.fn());
const mockProfiles = vi.hoisted(() => vi.fn());
const mockSettingsGet = vi.hoisted(() => vi.fn());
const mockSettingsUpdate = vi.hoisted(() => vi.fn());
const mockToastSuccess = vi.hoisted(() => vi.fn());
const mockToastError = vi.hoisted(() => vi.fn());

vi.mock('$lib/api', () => ({
  api: { getUser: mockGetUser, setViewAs: vi.fn() },
  userApi: { list: mockUserList, setRequestPermissions: mockSetPerms },
  inviteApi: { list: vi.fn().mockResolvedValue([]) },
  profileApi: { list: mockProfiles },
  settingsApi: { get: mockSettingsGet, update: mockSettingsUpdate },
}));
vi.mock('$lib/stores/toast', () => ({
  toast: { success: mockToastSuccess, error: mockToastError },
}));

function user(over: Partial<User> & Pick<User, 'id' | 'username'>): User {
  return {
    is_admin: false,
    created_at: '2026-01-01T00:00:00Z',
    auto_approve_movies: false,
    auto_approve_tv: false,
    ...over,
  };
}

const sw = (name: string) => screen.getByRole('switch', { name }) as HTMLButtonElement;

beforeEach(() => {
  vi.clearAllMocks();
  mockGetUser.mockReturnValue({ user_id: 'admin', username: 'boss', is_admin: true });
  mockUserList.mockResolvedValue({
    items: [
      user({ id: 'admin', username: 'boss', is_admin: true }),
      user({ id: 'u1', username: 'alice', auto_approve_movies: true }),
      user({ id: 'kid', username: 'kiddo', auto_approve_tv: true }),
    ],
    total: 3,
  });
  mockProfiles.mockResolvedValue([
    { id: 'kid', username: 'kiddo', has_pin: false, created_at: '', max_content_rating: 'PG', inherit_library_access: true },
  ]);
  mockSettingsGet.mockResolvedValue({
    requests: { default_auto_approve_movies: false, default_auto_approve_tv: true },
  });
  mockSetPerms.mockResolvedValue(undefined);
  mockSettingsUpdate.mockResolvedValue(undefined);
});

describe('Users page — request auto-approval', () => {
  it('shows each user’s movie and TV switches from the list', async () => {
    render(Page);
    await waitFor(() => expect(screen.getByText('alice')).toBeTruthy());

    expect(sw('Auto-approve movies for alice').getAttribute('aria-checked')).toBe('true');
    expect(sw('Auto-approve TV for alice').getAttribute('aria-checked')).toBe('false');
  });

  it('tells the admin their own requests are always approved instead of showing switches', async () => {
    render(Page);
    await waitFor(() => expect(screen.getByText('alice')).toBeTruthy());

    expect(screen.getByText("Admins' requests are always approved")).toBeTruthy();
    expect(screen.queryByRole('switch', { name: /for boss/ })).toBeNull();
  });

  it('saves both values when one switch flips, and updates the row', async () => {
    render(Page);
    await waitFor(() => expect(screen.getByText('alice')).toBeTruthy());

    await fireEvent.click(sw('Auto-approve TV for alice'));
    await waitFor(() => expect(mockSetPerms).toHaveBeenCalledTimes(1));
    expect(mockSetPerms).toHaveBeenCalledWith('u1', { auto_approve_movies: true, auto_approve_tv: true });
    await waitFor(() => expect(sw('Auto-approve TV for alice').getAttribute('aria-checked')).toBe('true'));
    expect(mockToastSuccess).toHaveBeenCalledWith('TV requests from "alice" are now approved automatically');

    await fireEvent.click(sw('Auto-approve movies for alice'));
    await waitFor(() => expect(mockSetPerms).toHaveBeenCalledTimes(2));
    expect(mockSetPerms).toHaveBeenLastCalledWith('u1', { auto_approve_movies: false, auto_approve_tv: true });
    await waitFor(() => expect(sw('Auto-approve movies for alice').getAttribute('aria-checked')).toBe('false'));
  });

  it('leaves the switch as it was when the save fails', async () => {
    mockSetPerms.mockRejectedValueOnce(new Error('nope'));
    render(Page);
    await waitFor(() => expect(screen.getByText('alice')).toBeTruthy());

    await fireEvent.click(sw('Auto-approve movies for alice'));
    await waitFor(() => expect(mockToastError).toHaveBeenCalledWith('nope'));
    expect(sw('Auto-approve movies for alice').getAttribute('aria-checked')).toBe('true');
  });

  it('explains that a rating-limited profile always needs approval, but still stores its switches', async () => {
    render(Page);
    await waitFor(() => expect(screen.getByText('Rating-limited profiles always need approval')).toBeTruthy());

    // Only the rated profile gets the note, and its switches are dimmed.
    expect(screen.getAllByText('Rating-limited profiles always need approval')).toHaveLength(1);
    const kidMovies = sw('Auto-approve movies for kiddo');
    expect(kidMovies.closest('.perm-toggles')?.classList.contains('limited')).toBe(true);
    expect(sw('Auto-approve movies for alice').closest('.perm-toggles')?.classList.contains('limited')).toBe(false);
    expect(kidMovies.getAttribute('aria-describedby')).toBe('perm-note-kid');

    await fireEvent.click(kidMovies);
    await waitFor(() => expect(mockSetPerms).toHaveBeenCalledWith('kid', { auto_approve_movies: true, auto_approve_tv: true }));
    expect(mockToastSuccess).toHaveBeenCalledWith(expect.stringContaining('only if the rating limit is removed'));
  });

  it('uses a ceiling carried on the user list too', async () => {
    mockProfiles.mockResolvedValue([]);
    mockUserList.mockResolvedValue({
      items: [user({ id: 'u2', username: 'teen', max_content_rating: 'PG-13' })],
      total: 1,
    });
    render(Page);
    await waitFor(() => expect(screen.getByText('Rating-limited profiles always need approval')).toBeTruthy());
  });

  it('still renders the switches when the profile list fails to load', async () => {
    mockProfiles.mockRejectedValue(new Error('boom'));
    render(Page);
    await waitFor(() => expect(screen.getByText('alice')).toBeTruthy());
    expect(sw('Auto-approve movies for kiddo')).toBeTruthy();
    expect(screen.queryByText('Rating-limited profiles always need approval')).toBeNull();
  });
});

describe('Users page — new-user defaults', () => {
  it('loads the defaults from server settings with the explanatory note', async () => {
    render(Page);
    await waitFor(() => expect(sw('Auto-approve movies')).toBeTruthy());

    expect(sw('Auto-approve movies').getAttribute('aria-checked')).toBe('false');
    expect(sw('Auto-approve TV').getAttribute('aria-checked')).toBe('true');
    expect(screen.getByText('Applies to accounts created from now on. Household profiles start with auto-approve off.')).toBeTruthy();
  });

  it('saves a flipped default through PATCH /settings with the whole requests object', async () => {
    render(Page);
    await waitFor(() => expect(sw('Auto-approve movies')).toBeTruthy());

    await fireEvent.click(sw('Auto-approve movies'));
    await waitFor(() => expect(mockSettingsUpdate).toHaveBeenCalledTimes(1));
    expect(mockSettingsUpdate).toHaveBeenCalledWith({
      requests: { default_auto_approve_movies: true, default_auto_approve_tv: true },
    });
    await waitFor(() => expect(sw('Auto-approve movies').getAttribute('aria-checked')).toBe('true'));
    expect(mockToastSuccess).toHaveBeenCalledWith('New-user defaults saved');
  });

  it('keeps the old value when saving the default fails', async () => {
    mockSettingsUpdate.mockRejectedValueOnce(new Error('denied'));
    render(Page);
    await waitFor(() => expect(sw('Auto-approve TV')).toBeTruthy());

    await fireEvent.click(sw('Auto-approve TV'));
    await waitFor(() => expect(mockToastError).toHaveBeenCalledWith('denied'));
    expect(sw('Auto-approve TV').getAttribute('aria-checked')).toBe('true');
  });

  it('treats a settings response without a requests block as both off', async () => {
    mockSettingsGet.mockResolvedValue({});
    render(Page);
    await waitFor(() => expect(sw('Auto-approve movies')).toBeTruthy());
    expect(sw('Auto-approve movies').getAttribute('aria-checked')).toBe('false');
    expect(sw('Auto-approve TV').getAttribute('aria-checked')).toBe('false');
  });

  it('shows the error when the defaults cannot be loaded', async () => {
    mockSettingsGet.mockRejectedValue(new Error('settings down'));
    render(Page);
    await waitFor(() => expect(screen.getByText('settings down')).toBeTruthy());
    expect(screen.queryByRole('switch', { name: 'Auto-approve movies' })).toBeNull();
  });
});

describe('Users page — request limits', () => {
  const limitsBtn = (name: string) => screen.getByRole('button', { name: `Request limits for ${name}` }) as HTMLButtonElement;
  const input = (label: string) => screen.getByLabelText(label) as HTMLInputElement;

  beforeEach(() => {
    mockUserList.mockResolvedValue({
      items: [
        user({ id: 'admin', username: 'boss', is_admin: true }),
        user({ id: 'u1', username: 'alice', can_request: true, request_quota_movies: 5, request_quota_tv: null }),
        user({ id: 'u2', username: 'bob', can_request: false }),
      ],
      total: 3,
    });
    mockSettingsGet.mockResolvedValue({
      requests: {
        default_auto_approve_movies: false, default_auto_approve_tv: true,
        quota_movies: 3, quota_tv: 0, quota_window_days: 7,
      },
    });
  });

  it('summarises each row and offers no limits for admins', async () => {
    render(Page);
    await waitFor(() => expect(screen.getByText('alice')).toBeTruthy());
    expect(limitsBtn('alice').textContent).toContain('Movies 5 · TV default');
    expect(limitsBtn('bob').textContent).toContain('Requests off');
    expect(limitsBtn('bob').classList.contains('off')).toBe(true);
    expect(screen.queryByRole('button', { name: 'Request limits for boss' })).toBeNull();
  });

  it('opens an editor with the can-request switch and quota inputs showing the defaults', async () => {
    render(Page);
    await waitFor(() => expect(screen.getByText('alice')).toBeTruthy());
    await fireEvent.click(limitsBtn('alice'));
    expect(limitsBtn('alice').getAttribute('aria-expanded')).toBe('true');
    expect(sw('Can request for alice').getAttribute('aria-checked')).toBe('true');
    expect(input('Movie request limit for alice').value).toBe('5');
    expect(input('TV request limit for alice').value).toBe('');
    // Blank means the server default, which the placeholder spells out.
    expect(input('Movie request limit for alice').placeholder).toBe('Default (3)');
    expect(input('TV request limit for alice').placeholder).toBe('Default (no limit)');
    expect(screen.getByText(/Requests per week\. Blank = server default, 0 = no limit/)).toBeTruthy();
  });

  it('turns requesting off and back on through request-permissions', async () => {
    render(Page);
    await waitFor(() => expect(screen.getByText('alice')).toBeTruthy());
    await fireEvent.click(limitsBtn('alice'));
    await fireEvent.click(sw('Can request for alice'));
    await waitFor(() => expect(mockSetPerms).toHaveBeenCalledWith('u1', { can_request: false }));
    await waitFor(() => expect(sw('Can request for alice').getAttribute('aria-checked')).toBe('false'));
    expect(mockToastSuccess).toHaveBeenCalledWith('"alice" can no longer make requests');
    expect(limitsBtn('alice').textContent).toContain('Requests off');

    await fireEvent.click(sw('Can request for alice'));
    await waitFor(() => expect(mockSetPerms).toHaveBeenLastCalledWith('u1', { can_request: true }));
  });

  it('saves quotas (blank = default) and collapses the editor', async () => {
    render(Page);
    await waitFor(() => expect(screen.getByText('alice')).toBeTruthy());
    await fireEvent.click(limitsBtn('alice'));
    await fireEvent.input(input('Movie request limit for alice'), { target: { value: '' } });
    await fireEvent.input(input('TV request limit for alice'), { target: { value: '0' } });
    const saves = screen.getAllByRole('button', { name: 'Save limits' });
    await fireEvent.click(saves[0]);
    await waitFor(() => expect(mockSetPerms).toHaveBeenCalledWith('u1', { quota_movies: null, quota_tv: 0 }));
    await waitFor(() => expect(limitsBtn('alice').getAttribute('aria-expanded')).toBe('false'));
    expect(limitsBtn('alice').textContent).toContain('Movies default · TV no limit');
    expect(mockToastSuccess).toHaveBeenCalledWith('Request limits saved for "alice"');
  });

  it('refuses a bad quota without calling the server', async () => {
    render(Page);
    await waitFor(() => expect(screen.getByText('alice')).toBeTruthy());
    await fireEvent.click(limitsBtn('alice'));
    await fireEvent.input(input('Movie request limit for alice'), { target: { value: '-2' } });
    await fireEvent.click(screen.getAllByRole('button', { name: 'Save limits' })[0]);
    expect(mockToastError).toHaveBeenCalledWith(expect.stringContaining('Movies: enter a whole number'));
    expect(mockSetPerms).not.toHaveBeenCalled();
  });

  it('edits the server-wide defaults and window', async () => {
    render(Page);
    await waitFor(() => expect(input('Default movie request limit').value).toBe('3'));
    expect(input('Default TV request limit').value).toBe('0');
    expect(input('Request limit window in days').value).toBe('7');

    await fireEvent.input(input('Default TV request limit'), { target: { value: '2' } });
    await fireEvent.input(input('Request limit window in days'), { target: { value: '30' } });
    const saves = screen.getAllByRole('button', { name: 'Save limits' });
    await fireEvent.click(saves[saves.length - 1]);
    await waitFor(() => expect(mockSettingsUpdate).toHaveBeenCalledWith({
      requests: {
        default_auto_approve_movies: false, default_auto_approve_tv: true,
        quota_movies: 3, quota_tv: 2, quota_window_days: 30,
      },
    }));
    expect(mockToastSuccess).toHaveBeenCalledWith('Request limits saved');
  });

  it('rejects an out-of-range window', async () => {
    render(Page);
    await waitFor(() => expect(input('Request limit window in days')).toBeTruthy());
    await fireEvent.input(input('Request limit window in days'), { target: { value: '120' } });
    const saves = screen.getAllByRole('button', { name: 'Save limits' });
    await fireEvent.click(saves[saves.length - 1]);
    expect(mockToastError).toHaveBeenCalledWith(expect.stringContaining('from 1 to 90'));
    expect(mockSettingsUpdate).not.toHaveBeenCalled();
  });

  it('treats an older server without the new fields as allowed with default limits', async () => {
    mockUserList.mockResolvedValue({ items: [user({ id: 'u1', username: 'alice' })], total: 1 });
    mockSettingsGet.mockResolvedValue({ requests: { default_auto_approve_movies: false, default_auto_approve_tv: false } });
    render(Page);
    await waitFor(() => expect(screen.getByText('alice')).toBeTruthy());
    expect(limitsBtn('alice').textContent).toContain('Default limits');
    await fireEvent.click(limitsBtn('alice'));
    expect(sw('Can request for alice').getAttribute('aria-checked')).toBe('true');
    expect(input('Request limit window in days').value).toBe('7');
  });
});
