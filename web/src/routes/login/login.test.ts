import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import Page from './+page.svelte';

// vi.mock is hoisted to top of file — use vi.hoisted() so vars are ready.
const mockGoto = vi.hoisted(() => vi.fn());
const mockLogin = vi.hoisted(() => vi.fn());
const mockSetUser = vi.hoisted(() => vi.fn());

vi.mock('$app/navigation', () => ({ goto: mockGoto }));
const mockSetupStatus = vi.hoisted(() => vi.fn());
const mockOidcEnabled = vi.hoisted(() => vi.fn());
const mockLdapEnabled = vi.hoisted(() => vi.fn());
const mockSamlEnabled = vi.hoisted(() => vi.fn());
const mockForgotEnabled = vi.hoisted(() => vi.fn());

vi.mock('$lib/api', () => ({
  authApi: {
    login: mockLogin,
    setupStatus: mockSetupStatus,
    oidcEnabled: mockOidcEnabled,
    ldapEnabled: mockLdapEnabled,
    samlEnabled: mockSamlEnabled,
    forgotPasswordEnabled: mockForgotEnabled,
  },
  api: { setUser: mockSetUser }
}));

describe('Login page', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    mockSetupStatus.mockResolvedValue({ setup_required: false });
    mockOidcEnabled.mockResolvedValue({ enabled: false, display_name: '' });
    mockLdapEnabled.mockResolvedValue({ enabled: false, display_name: '' });
    mockSamlEnabled.mockResolvedValue({ enabled: false, display_name: '' });
    mockForgotEnabled.mockResolvedValue({ enabled: false });
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('renders username and password fields', () => {
    render(Page);
    expect(screen.getByRole('textbox', { name: /username/i })).toBeInTheDocument();
    // password input is not a textbox role — find by label text
    expect(screen.getByLabelText(/password/i)).toBeInTheDocument();
  });

  it('renders the Sign In button', () => {
    render(Page);
    expect(screen.getByRole('button', { name: /sign in/i })).toBeInTheDocument();
  });

  it('renders setup link when setup is required', async () => {
    mockSetupStatus.mockResolvedValue({ setup_required: true });
    render(Page);
    await waitFor(() => expect(screen.getByText(/set up onscreen/i)).toBeInTheDocument());
  });

  it('calls login with entered credentials', async () => {
    mockLogin.mockResolvedValue({
      access_token: 'tok', refresh_token: 'ref',
      expires_at: '', user_id: '1', username: 'admin', is_admin: true
    });
    render(Page);

    await fireEvent.input(screen.getByLabelText(/^username/i), { target: { value: 'admin' } });
    await fireEvent.input(screen.getByLabelText(/password/i), { target: { value: 'secret' } });
    await fireEvent.submit(screen.getByRole('button', { name: /sign in/i }).closest('form')!);

    await waitFor(() => expect(mockLogin).toHaveBeenCalledWith('admin', 'secret'));
  });

  it('navigates to / on successful login', async () => {
    mockLogin.mockResolvedValue({
      access_token: 'tok', refresh_token: 'ref',
      expires_at: '', user_id: '1', username: 'admin', is_admin: true
    });
    render(Page);
    await fireEvent.submit(screen.getByRole('button', { name: /sign in/i }).closest('form')!);
    await waitFor(() => expect(mockGoto).toHaveBeenCalledWith('/'));
  });

  it('stores user metadata in localStorage on success', async () => {
    mockLogin.mockResolvedValue({
      access_token: 'tok', refresh_token: 'my-refresh',
      expires_at: '', user_id: '1', username: 'admin', is_admin: true
    });
    render(Page);
    await fireEvent.submit(screen.getByRole('button', { name: /sign in/i }).closest('form')!);
    await waitFor(() => expect(mockSetUser).toHaveBeenCalledWith({
      user_id: '1', username: 'admin', is_admin: true
    }));
  });

  it('shows error message on failed login', async () => {
    mockLogin.mockRejectedValue(new Error('Invalid credentials'));
    render(Page);
    await fireEvent.submit(screen.getByRole('button', { name: /sign in/i }).closest('form')!);
    await waitFor(() => expect(screen.getByText('Invalid credentials')).toBeInTheDocument());
  });

  it('shows generic error when non-Error thrown', async () => {
    mockLogin.mockRejectedValue('unexpected');
    render(Page);
    await fireEvent.submit(screen.getByRole('button', { name: /sign in/i }).closest('form')!);
    await waitFor(() => expect(screen.getByText('Login failed.')).toBeInTheDocument());
  });

  it('does not navigate on failed login', async () => {
    mockLogin.mockRejectedValue(new Error('bad'));
    render(Page);
    await fireEvent.submit(screen.getByRole('button', { name: /sign in/i }).closest('form')!);
    await waitFor(() => screen.getByText('bad'));
    expect(mockGoto).not.toHaveBeenCalled();
  });

  // Regression guard for the bug where SAML was configured server-side
  // (admin form, /auth/saml/enabled returning true) but the login page
  // never queried it — leaving users with no way to start the flow.
  it('renders the SAML button when /auth/saml/enabled returns enabled', async () => {
    mockSamlEnabled.mockResolvedValue({ enabled: true, display_name: 'Company SAML' });
    render(Page);
    await waitFor(() =>
      expect(screen.getByRole('button', { name: /company saml/i })).toBeInTheDocument(),
    );
  });

  it('does not render the SAML button when disabled', async () => {
    render(Page);
    // Wait for the enabled-fetches to settle, then assert no SAML button.
    await waitFor(() => expect(mockSamlEnabled).toHaveBeenCalled());
    expect(screen.queryByRole('button', { name: /saml/i })).toBeNull();
  });
});
