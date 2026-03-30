import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import Page from './+page.svelte';

// vi.mock is hoisted to top of file — use vi.hoisted() so vars are ready.
const mockGoto = vi.hoisted(() => vi.fn());
const mockLogin = vi.hoisted(() => vi.fn());
const mockSetUser = vi.hoisted(() => vi.fn());

vi.mock('$app/navigation', () => ({ goto: mockGoto }));
vi.mock('$lib/api', () => ({
  authApi: { login: mockLogin },
  api: { setUser: mockSetUser }
}));

describe('Login page', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
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

  it('renders setup link', () => {
    render(Page);
    expect(screen.getByText(/set up onscreen/i)).toBeInTheDocument();
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
});
