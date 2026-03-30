import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import Page from './+page.svelte';

// vi.mock is hoisted to top of file — use vi.hoisted() so vars are ready.
const mockGoto = vi.hoisted(() => vi.fn());
const mockRegister = vi.hoisted(() => vi.fn());
const mockLogin = vi.hoisted(() => vi.fn());
const mockCreateLib = vi.hoisted(() => vi.fn());
const mockSetUser = vi.hoisted(() => vi.fn());

vi.mock('$app/navigation', () => ({ goto: mockGoto }));
vi.mock('$lib/api', () => ({
  authApi: { register: mockRegister, login: mockLogin },
  libraryApi: { create: mockCreateLib },
  api: { setUser: mockSetUser }
}));

describe('Setup page', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
  });

  // ── Step 1: Create Account ─────────────────────────────────────────────────

  it('starts on step 1 — Create Account', () => {
    render(Page);
    expect(screen.getByText('Create Admin Account')).toBeInTheDocument();
  });

  it('shows all three step labels', () => {
    render(Page);
    expect(screen.getByText(/1\. Create Account/)).toBeInTheDocument();
    expect(screen.getByText(/2\. Add Library/)).toBeInTheDocument();
    expect(screen.getByText(/3\. Done/)).toBeInTheDocument();
  });

  it('shows password mismatch error without calling API', async () => {
    render(Page);
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

    await fireEvent.input(screen.getByLabelText(/^username/i), { target: { value: 'admin' } });
    await fireEvent.input(screen.getByLabelText(/^password$/i), { target: { value: 'secret' } });
    await fireEvent.input(screen.getByLabelText(/confirm password/i), { target: { value: 'secret' } });
    await fireEvent.submit(screen.getByText('Create Account').closest('form')!);

    await waitFor(() => expect(screen.getByText(/Add Your First Library/)).toBeInTheDocument());
  });

  it('shows registration error when API fails', async () => {
    mockRegister.mockRejectedValue(new Error('Username taken'));
    render(Page);

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

    await fireEvent.input(screen.getByLabelText(/^password$/i), { target: { value: 'p' } });
    await fireEvent.input(screen.getByLabelText(/confirm password/i), { target: { value: 'p' } });
    await fireEvent.submit(screen.getByText('Create Account').closest('form')!);

    await waitFor(() => expect(mockSetUser).toHaveBeenCalledWith({
      user_id: '1', username: 'admin', is_admin: true
    }));
  });

  // ── Step 2: Add Library ────────────────────────────────────────────────────

  async function advanceToStep2() {
    mockRegister.mockResolvedValue({ id: '1', username: 'admin' });
    mockLogin.mockResolvedValue({
      access_token: 'tok', refresh_token: 'ref',
      expires_at: '', user_id: '1', username: 'admin', is_admin: true
    });
    await fireEvent.input(screen.getByLabelText(/^password$/i), { target: { value: 'p' } });
    await fireEvent.input(screen.getByLabelText(/confirm password/i), { target: { value: 'p' } });
    await fireEvent.submit(screen.getByText('Create Account').closest('form')!);
    await waitFor(() => screen.getByText(/Add Your First Library/));
  }

  it('skip button advances to step 3', async () => {
    render(Page);
    await advanceToStep2();
    await fireEvent.click(screen.getByRole('button', { name: /skip/i }));
    await waitFor(() => expect(screen.getByText('Setup Complete')).toBeInTheDocument());
  });

  it('adding a library advances to step 3', async () => {
    mockCreateLib.mockResolvedValue({ id: '1', name: 'Movies' });
    render(Page);
    await advanceToStep2();

    await fireEvent.input(screen.getByPlaceholderText('My Movies'), { target: { value: 'My Movies' } });
    await fireEvent.input(screen.getByPlaceholderText('/media/movies'), { target: { value: '/media/movies' } });
    await fireEvent.submit(screen.getByText('Add Library').closest('form')!);

    await waitFor(() => expect(screen.getByText('Setup Complete')).toBeInTheDocument());
    expect(mockCreateLib).toHaveBeenCalledWith(
      expect.objectContaining({ name: 'My Movies', scan_paths: ['/media/movies'] })
    );
  });

  it('shows library error when create fails', async () => {
    mockCreateLib.mockRejectedValue(new Error('Path not found'));
    render(Page);
    await advanceToStep2();

    await fireEvent.input(screen.getByPlaceholderText('My Movies'), { target: { value: 'Movies' } });
    await fireEvent.input(screen.getByPlaceholderText('/media/movies'), { target: { value: '/bad' } });
    await fireEvent.submit(screen.getByText('Add Library').closest('form')!);

    await waitFor(() => expect(screen.getByText('Path not found')).toBeInTheDocument());
  });

  // ── Step 3: Done ───────────────────────────────────────────────────────────

  it('Go to Dashboard button navigates to /', async () => {
    render(Page);
    await advanceToStep2();
    await fireEvent.click(screen.getByRole('button', { name: /skip/i }));
    await waitFor(() => screen.getByText('Setup Complete'));
    await fireEvent.click(screen.getByRole('button', { name: /go to dashboard/i }));
    expect(mockGoto).toHaveBeenCalledWith('/');
  });
});
