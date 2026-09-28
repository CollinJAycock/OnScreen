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
