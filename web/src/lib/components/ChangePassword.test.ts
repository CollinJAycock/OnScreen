import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import ChangePassword from './ChangePassword.svelte';

const mockChange = vi.hoisted(() => vi.fn());
const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }));

vi.mock('$lib/api', () => ({ userApi: { changePassword: mockChange } }));
vi.mock('$lib/stores/toast', () => ({ toast }));

beforeEach(() => {
  vi.clearAllMocks();
});

async function fill(current: string, next: string, confirm: string) {
  await fireEvent.input(screen.getByLabelText('Current password'), { target: { value: current } });
  await fireEvent.input(screen.getByLabelText('New password'), { target: { value: next } });
  await fireEvent.input(screen.getByLabelText('Confirm new password'), { target: { value: confirm } });
}

const button = () => screen.getByRole('button', { name: /change password/i }) as HTMLButtonElement;

describe('ChangePassword', () => {
  it('sends the current and new password and reports the other devices signed out', async () => {
    mockChange.mockResolvedValue({ other_sessions_revoked: 2, signed_out: false });
    render(ChangePassword);
    await fill('old-password', 'a-new-password', 'a-new-password');
    await fireEvent.click(button());

    await waitFor(() => expect(mockChange).toHaveBeenCalledWith('old-password', 'a-new-password'));
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith('Password changed. Your other devices were signed out.'));
    expect((screen.getByLabelText('Current password') as HTMLInputElement).value).toBe('');
  });

  it('stays disabled until the new password is long enough and confirmed', async () => {
    render(ChangePassword);
    await fill('old-password', 'short', 'short');
    expect(button().disabled).toBe(true);
    expect(screen.getByText(/at least 12 characters/i)).toBeTruthy();

    await fill('old-password', 'a-new-password', 'a-different-one');
    expect(button().disabled).toBe(true);
    expect(screen.getByText(/don't match/i)).toBeTruthy();

    await fill('old-password', 'a-new-password', 'a-new-password');
    expect(button().disabled).toBe(false);
  });

  it("shows the server's error, e.g. a wrong current password", async () => {
    mockChange.mockRejectedValue(new Error('your current password is incorrect'));
    render(ChangePassword);
    await fill('guess', 'a-new-password', 'a-new-password');
    await fireEvent.click(button());

    await waitFor(() => expect(screen.getByText('your current password is incorrect')).toBeTruthy());
    expect(toast.success).not.toHaveBeenCalled();
  });
});
