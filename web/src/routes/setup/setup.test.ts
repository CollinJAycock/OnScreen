import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import Page from './+page.svelte';

// vi.mock is hoisted to top of file — use vi.hoisted() so vars are ready.
const mockGoto = vi.hoisted(() => vi.fn());
const mockRegister = vi.hoisted(() => vi.fn());
const mockLogin = vi.hoisted(() => vi.fn());
const mockSetUser = vi.hoisted(() => vi.fn());
const mockSetupStatus = vi.hoisted(() => vi.fn());
const mockSettingsUpdate = vi.hoisted(() => vi.fn());

vi.mock('$app/navigation', () => ({ goto: mockGoto }));

vi.mock('$lib/api', () => ({
  authApi: { register: mockRegister, login: mockLogin, setupStatus: mockSetupStatus },
  settingsApi: { update: mockSettingsUpdate },
  api: { setUser: mockSetUser }
}));

describe('Setup page', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    mockSetupStatus.mockResolvedValue({ setup_required: true });
  });

  // ── Step 1: Create Account ─────────────────────────────────────────────────

  it('starts on step 1 — Create Account', async () => {
    render(Page);
    await waitFor(() => expect(screen.getByText('Create Admin Account')).toBeInTheDocument());
  });

  it('shows all three step labels', async () => {
    // Step labels live in the indicator strip at the top of the card.
    // The flow is Account → API Keys → Done; library creation moved out of
    // setup to Settings ▸ Libraries (see the step 3 copy).
    render(Page);
    await waitFor(() => expect(screen.getByText('Account')).toBeInTheDocument());
    expect(screen.getByText('API Keys')).toBeInTheDocument();
    expect(screen.getByText('Done')).toBeInTheDocument();
  });

  it('shows password mismatch error without calling API', async () => {
    render(Page);
    await waitFor(() => screen.getByLabelText(/^username/i));
    await fireEvent.input(screen.getByLabelText(/^username/i), { target: { value: 'admin' } });
    await fireEvent.input(screen.getByLabelText(/^password$/i), { target: { value: 'abc' } });
    await fireEvent.input(screen.getByLabelText(/confirm password/i), { target: { value: 'xyz' } });
    await fireEvent.submit(screen.getByText('Create Account').closest('form')!);

    await waitFor(() => expect(screen.getByText('Passwords do not match.')).toBeInTheDocument());
    expect(mockRegister).not.toHaveBeenCalled();
  });

  it('advances to step 2 on successful registration', async () => {
    mockRegister.mockResolvedValue({ id: '1', username: 'admin' });
    mockLogin.mockResolvedValue({
      access_token: 'tok', refresh_token: 'ref',
      expires_at: '', user_id: '1', username: 'admin', is_admin: true
    });
    render(Page);
    await waitFor(() => screen.getByLabelText(/^username/i));

    await fireEvent.input(screen.getByLabelText(/^username/i), { target: { value: 'admin' } });
    await fireEvent.input(screen.getByLabelText(/^password$/i), { target: { value: 'secret' } });
    await fireEvent.input(screen.getByLabelText(/confirm password/i), { target: { value: 'secret' } });
    await fireEvent.submit(screen.getByText('Create Account').closest('form')!);

    await waitFor(() => expect(screen.getByText(/Metadata API Keys/)).toBeInTheDocument());
  });

  it('shows registration error when API fails', async () => {
    mockRegister.mockRejectedValue(new Error('Username taken'));
    render(Page);
    await waitFor(() => screen.getByLabelText(/^password$/i));

    await fireEvent.input(screen.getByLabelText(/^password$/i), { target: { value: 'x' } });
    await fireEvent.input(screen.getByLabelText(/confirm password/i), { target: { value: 'x' } });
    await fireEvent.submit(screen.getByText('Create Account').closest('form')!);

    await waitFor(() => expect(screen.getByText('Username taken')).toBeInTheDocument());
  });

  it('auto-logs in after registration and stores refresh token', async () => {
    mockRegister.mockResolvedValue({ id: '1', username: 'admin' });
    mockLogin.mockResolvedValue({
      access_token: 'tok', refresh_token: 'my-refresh',
      expires_at: '', user_id: '1', username: 'admin', is_admin: true
    });
    render(Page);
    await waitFor(() => screen.getByLabelText(/^password$/i));

    await fireEvent.input(screen.getByLabelText(/^password$/i), { target: { value: 'p' } });
    await fireEvent.input(screen.getByLabelText(/confirm password/i), { target: { value: 'p' } });
    await fireEvent.submit(screen.getByText('Create Account').closest('form')!);

    await waitFor(() => expect(mockSetUser).toHaveBeenCalledWith({
      user_id: '1', username: 'admin', is_admin: true
    }));
  });

  // ── Step 2: API Keys ───────────────────────────────────────────────────────

  async function advanceToStep2() {
    mockRegister.mockResolvedValue({ id: '1', username: 'admin' });
    mockLogin.mockResolvedValue({
      access_token: 'tok', refresh_token: 'ref',
      expires_at: '', user_id: '1', username: 'admin', is_admin: true
    });
    await waitFor(() => screen.getByLabelText(/^password$/i));
    await fireEvent.input(screen.getByLabelText(/^password$/i), { target: { value: 'p' } });
    await fireEvent.input(screen.getByLabelText(/confirm password/i), { target: { value: 'p' } });
    await fireEvent.submit(screen.getByText('Create Account').closest('form')!);
    await waitFor(() => screen.getByText(/Metadata API Keys/));
  }

  it('empty-keys Continue advances to step 3 without calling settings', async () => {
    // No keys filled in → the primary button reads "Continue" and just
    // jumps to the done step. No settingsApi call is made — admins can
    // add keys later from Settings ▸ Metadata.
    render(Page);
    await advanceToStep2();

    await fireEvent.click(screen.getByRole('button', { name: /^continue$/i }));
    await waitFor(() => expect(screen.getByText(/You're all set/)).toBeInTheDocument());
    expect(mockSettingsUpdate).not.toHaveBeenCalled();
  });

  it('Skip button (visible once a key is typed) advances to step 3', async () => {
    // The Skip button only renders once at least one key field has
    // content — otherwise the primary "Continue" button is the skip path.
    render(Page);
    await advanceToStep2();

    await fireEvent.input(screen.getByPlaceholderText(/v3 API key/i), { target: { value: 'k' } });
    await fireEvent.click(screen.getByRole('button', { name: /^skip$/i }));
    await waitFor(() => expect(screen.getByText(/You're all set/)).toBeInTheDocument());
    expect(mockSettingsUpdate).not.toHaveBeenCalled();
  });

  it('saving keys advances to step 3', async () => {
    mockSettingsUpdate.mockResolvedValue({});
    render(Page);
    await advanceToStep2();

    await fireEvent.input(screen.getByPlaceholderText(/v3 API key/i), { target: { value: 'tmdb-key' } });
    await fireEvent.input(screen.getByPlaceholderText(/UUID-format/i), { target: { value: 'tvdb-key' } });
    await fireEvent.submit(screen.getByText(/Save & Continue/).closest('form')!);

    await waitFor(() => expect(screen.getByText(/You're all set/)).toBeInTheDocument());
    expect(mockSettingsUpdate).toHaveBeenCalledWith(
      expect.objectContaining({ tmdb_api_key: 'tmdb-key', tvdb_api_key: 'tvdb-key' })
    );
  });

  it('shows keys error when settings update fails', async () => {
    mockSettingsUpdate.mockRejectedValue(new Error('Invalid TMDB key'));
    render(Page);
    await advanceToStep2();

    await fireEvent.input(screen.getByPlaceholderText(/v3 API key/i), { target: { value: 'bad' } });
    await fireEvent.submit(screen.getByText(/Save & Continue/).closest('form')!);

    await waitFor(() => expect(screen.getByText('Invalid TMDB key')).toBeInTheDocument());
    // Stay on step 2 so the admin can correct the value.
    expect(screen.getByText(/Metadata API Keys/)).toBeInTheDocument();
  });

  // ── Step 3: Done ───────────────────────────────────────────────────────────

  it('Go to Dashboard button navigates to /', async () => {
    render(Page);
    await advanceToStep2();
    await fireEvent.click(screen.getByRole('button', { name: /^continue$/i }));
    await waitFor(() => screen.getByText(/You're all set/));
    await fireEvent.click(screen.getByRole('button', { name: /go to dashboard/i }));
    expect(mockGoto).toHaveBeenCalledWith('/');
  });
});
