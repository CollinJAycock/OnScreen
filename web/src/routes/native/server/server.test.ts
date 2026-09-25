import { describe, it, expect, vi, beforeEach, afterEach, type Mock } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import Page from './+page.svelte';

// Records the order of the side effects that matter: validate -> revoke
// (old server) -> persist (new server).
const calls = vi.hoisted(() => [] as string[]);
const mockInvoke = vi.hoisted(() => vi.fn());
const mockLogout = vi.hoisted(() => vi.fn());
const mockSetUser = vi.hoisted(() => vi.fn());
const mockSetServerUrl = vi.hoisted(() => vi.fn());
const mockGetServerUrl = vi.hoisted(() => vi.fn());
const mockGetStoredTokens = vi.hoisted(() => vi.fn());

vi.mock('@tauri-apps/api/core', () => ({ invoke: mockInvoke }));
vi.mock('$lib/native', () => ({
  isTauri: () => true,
  getServerUrl: mockGetServerUrl,
  setServerUrl: mockSetServerUrl,
  clearServerUrl: vi.fn(),
  getStoredTokens: mockGetStoredTokens,
  clearStoredTokens: vi.fn(),
}));
vi.mock('$lib/api', () => ({
  api: { setUser: mockSetUser },
  authApi: { logout: mockLogout },
}));

async function submitUrl(url: string) {
  const button = await screen.findByRole('button', { name: /save & reload/i });
  await fireEvent.input(screen.getByRole('textbox'), { target: { value: url } });
  await fireEvent.submit(button.closest('form')!);
}

describe('Native server page: changing the server URL', () => {
  let reload: Mock<() => void>;

  beforeEach(() => {
    vi.clearAllMocks();
    calls.length = 0;
    reload = vi.fn<() => void>(() => { calls.push('reload'); });
    vi.spyOn(window.location, 'reload').mockImplementation(reload);
    mockGetServerUrl.mockResolvedValue('https://old.example');
    mockGetStoredTokens.mockResolvedValue({ access_token: 'a', refresh_token: 'r', asset_token: null });
    mockInvoke.mockImplementation(async (cmd: string) => { calls.push(cmd); });
    mockLogout.mockImplementation(async () => { calls.push('logout'); });
    mockSetUser.mockImplementation(() => { calls.push('setUser'); });
    mockSetServerUrl.mockImplementation(async () => { calls.push('set_server_url'); });
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('a URL the shell rejects leaves the session alone', async () => {
    mockInvoke.mockRejectedValue('server URL must start with https:// or http:// (e.g. https://nas:7070)');
    render(Page);
    await submitUrl('nas:7070');

    expect(await screen.findByText(/must start with https:\/\//)).toBeInTheDocument();
    expect(mockInvoke).toHaveBeenCalledWith('validate_server_url', { url: 'nas:7070' });
    expect(mockLogout).not.toHaveBeenCalled();
    expect(mockSetUser).not.toHaveBeenCalled();
    expect(mockSetServerUrl).not.toHaveBeenCalled();
    expect(reload).not.toHaveBeenCalled();
  });

  it('switching servers validates, then revokes on the old server, then saves', async () => {
    render(Page);
    await submitUrl('  https://new.example  ');

    await waitFor(() => expect(reload).toHaveBeenCalled());
    expect(calls).toEqual(['validate_server_url', 'logout', 'setUser', 'set_server_url', 'reload']);
    expect(mockSetServerUrl).toHaveBeenCalledWith('https://new.example');
  });

  it('an unreachable old server does not block the switch', async () => {
    mockLogout.mockRejectedValue(new TypeError('Failed to fetch'));
    render(Page);
    await submitUrl('https://new.example');

    await waitFor(() => expect(reload).toHaveBeenCalled());
    expect(mockSetServerUrl).toHaveBeenCalledWith('https://new.example');
  });

  it('the same origin keeps the session', async () => {
    render(Page);
    await submitUrl('https://old.example/');

    await waitFor(() => expect(reload).toHaveBeenCalled());
    expect(mockLogout).not.toHaveBeenCalled();
    expect(calls).toEqual(['validate_server_url', 'set_server_url', 'reload']);
  });
});
