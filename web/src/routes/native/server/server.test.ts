import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import Page from './+page.svelte';

// Records the order of the side effects that matter: test the new server ->
// revoke (old server) -> persist (new server) -> reload.
const calls = vi.hoisted(() => [] as string[]);
const mockConnect = vi.hoisted(() => vi.fn());
const mockLogout = vi.hoisted(() => vi.fn());
const mockSetUser = vi.hoisted(() => vi.fn());
const mockGetUser = vi.hoisted(() => vi.fn());
const mockSetServerUrl = vi.hoisted(() => vi.fn());
const mockClearServerUrl = vi.hoisted(() => vi.fn());
const mockClearTokens = vi.hoisted(() => vi.fn());
const mockGetServerUrl = vi.hoisted(() => vi.fn());
const mockGetStoredTokens = vi.hoisted(() => vi.fn());
const mockReloadApp = vi.hoisted(() => vi.fn());

vi.mock('$lib/native', () => ({
  isTauri: () => true,
  getServerUrl: mockGetServerUrl,
  setServerUrl: mockSetServerUrl,
  clearServerUrl: mockClearServerUrl,
  getStoredTokens: mockGetStoredTokens,
  clearStoredTokens: mockClearTokens,
  reloadApp: mockReloadApp,
}));
vi.mock('$lib/api', () => ({
  api: { setUser: mockSetUser, getUser: mockGetUser },
  authApi: { logout: mockLogout },
}));
// The probing itself is covered by $lib/serverConnect's own suite.
vi.mock('$lib/serverConnect', async () => {
  const actual = await vi.importActual<typeof import('$lib/serverConnect')>('$lib/serverConnect');
  return { ...actual, connectToServer: mockConnect };
});

async function submitUrl(url: string) {
  const button = await screen.findByRole('button', { name: /save and reconnect/i });
  await fireEvent.input(screen.getByRole('textbox', { name: /server address/i }), { target: { value: url } });
  await fireEvent.submit(button.closest('form')!);
}

describe('Native server page: changing the server', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    calls.length = 0;
    mockGetServerUrl.mockResolvedValue('https://old.example');
    mockGetStoredTokens.mockResolvedValue({ access_token: 'a', refresh_token: 'r', asset_token: null });
    mockGetUser.mockReturnValue({ user_id: 'u1', username: 'me', is_admin: false });
    mockConnect.mockImplementation(async (input: string) => {
      calls.push('connect');
      const typed = input.trim();
      return { url: typed.includes('://') ? typed.replace(/\/$/, '') : `http://${typed}`, cleartext: !typed.startsWith('https://') };
    });
    mockLogout.mockImplementation(async () => { calls.push('logout'); });
    mockSetUser.mockImplementation(() => { calls.push('setUser'); });
    mockSetServerUrl.mockImplementation(async () => { calls.push('set_server_url'); });
    mockReloadApp.mockImplementation(() => { calls.push('reload'); });
  });

  it('a server that fails the test leaves the session alone', async () => {
    mockConnect.mockRejectedValue(new Error("Couldn't connect to the server. http://nas:7070: the connection was refused"));
    render(Page);
    await submitUrl('nas:7070');

    expect(await screen.findByRole('alert')).toHaveTextContent(/connection was refused/);
    expect(mockConnect).toHaveBeenCalledWith('nas:7070', expect.anything());
    // Focus is back in the field, ready for a corrected address.
    await waitFor(() => expect(document.activeElement).toBe(screen.getByRole('textbox', { name: /server address/i })));
    expect(mockLogout).not.toHaveBeenCalled();
    expect(mockSetUser).not.toHaveBeenCalled();
    expect(mockSetServerUrl).not.toHaveBeenCalled();
    expect(mockReloadApp).not.toHaveBeenCalled();
  });

  it('an empty address is refused without testing anything', async () => {
    render(Page);
    await submitUrl('   ');

    expect(await screen.findByRole('alert')).toHaveTextContent(/Enter your OnScreen server's address/);
    expect(mockConnect).not.toHaveBeenCalled();
  });

  it('a bare LAN address is accepted: tested, then the old session revoked, then saved', async () => {
    render(Page);
    await submitUrl('  10.0.0.66:7070  ');

    await waitFor(() => expect(mockReloadApp).toHaveBeenCalled());
    expect(calls).toEqual(['connect', 'logout', 'setUser', 'set_server_url', 'reload']);
    // The URL the test resolved is what gets saved, not the raw input.
    expect(mockSetServerUrl).toHaveBeenCalledWith('http://10.0.0.66:7070');
  });

  it('an unreachable old server does not block the switch', async () => {
    mockLogout.mockRejectedValue(new TypeError('Failed to fetch'));
    render(Page);
    await submitUrl('https://new.example');

    await waitFor(() => expect(mockReloadApp).toHaveBeenCalled());
    expect(mockSetServerUrl).toHaveBeenCalledWith('https://new.example');
  });

  it('the same origin keeps the session', async () => {
    render(Page);
    await submitUrl('https://old.example/');

    await waitFor(() => expect(mockReloadApp).toHaveBeenCalled());
    expect(mockLogout).not.toHaveBeenCalled();
    expect(calls).toEqual(['connect', 'set_server_url', 'reload']);
  });

  it('flags a plain http:// server as unencrypted', async () => {
    mockGetServerUrl.mockResolvedValue('http://10.0.0.66:7070');
    render(Page);

    expect(await screen.findByRole('note')).toHaveTextContent(/^Not encrypted/);
  });

  it('signed out, it offers the way back to sign-in and hides signed-in settings', async () => {
    mockGetUser.mockReturnValue(null);
    mockGetStoredTokens.mockResolvedValue({ access_token: null, refresh_token: null, asset_token: null });
    render(Page);

    expect(await screen.findByRole('link', { name: /back to sign in/i })).toHaveAttribute('href', '/login');
    expect(screen.queryByText(/native audio engine/i)).not.toBeInTheDocument();
  });
});

describe('Native server page: disconnect', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    calls.length = 0;
    mockGetServerUrl.mockResolvedValue('https://old.example');
    mockGetStoredTokens.mockResolvedValue({ access_token: 'a', refresh_token: 'r', asset_token: null });
    mockGetUser.mockReturnValue({ user_id: 'u1', username: 'me', is_admin: false });
    mockLogout.mockImplementation(async () => { calls.push('logout'); });
    mockSetUser.mockImplementation(() => { calls.push('setUser'); });
    mockClearTokens.mockImplementation(async () => { calls.push('clear_tokens'); });
    mockClearServerUrl.mockImplementation(async () => { calls.push('clear_server_url'); });
    mockReloadApp.mockImplementation(() => { calls.push('reload'); });
  });

  it('asks for a second click in the page (window.confirm does not block in the desktop webview)', async () => {
    const confirmSpy = vi.fn(() => true);
    vi.stubGlobal('confirm', confirmSpy);
    render(Page);

    await fireEvent.click(await screen.findByRole('button', { name: /sign out \+ clear server url/i }));
    expect(mockClearServerUrl).not.toHaveBeenCalled();
    expect(screen.getByText(/Sign out and forget https:\/\/old\.example\?/)).toBeInTheDocument();

    await fireEvent.click(screen.getByRole('button', { name: /cancel/i }));
    expect(screen.getByRole('button', { name: /sign out \+ clear server url/i })).toBeInTheDocument();
    expect(mockClearServerUrl).not.toHaveBeenCalled();

    await fireEvent.click(screen.getByRole('button', { name: /sign out \+ clear server url/i }));
    await fireEvent.click(screen.getByRole('button', { name: /yes, disconnect/i }));
    await waitFor(() => expect(mockReloadApp).toHaveBeenCalled());
    expect(calls).toEqual(['logout', 'setUser', 'clear_tokens', 'clear_server_url', 'reload']);
    expect(confirmSpy).not.toHaveBeenCalled();
    vi.unstubAllGlobals();
  });
});
