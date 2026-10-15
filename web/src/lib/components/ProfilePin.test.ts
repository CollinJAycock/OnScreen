import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import ProfilePin from './ProfilePin.svelte';

const mockGetPrefs = vi.hoisted(() => vi.fn());
const mockSetPin = vi.hoisted(() => vi.fn());
const mockClearPin = vi.hoisted(() => vi.fn());

vi.mock('$lib/api', () => ({
  userApi: { getPreferences: mockGetPrefs, setPin: mockSetPin, clearPin: mockClearPin },
}));
vi.mock('$lib/stores/toast', () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

beforeEach(() => {
  vi.clearAllMocks();
  mockSetPin.mockResolvedValue(undefined);
  mockClearPin.mockResolvedValue(undefined);
});

describe('ProfilePin', () => {
  it('sets a PIN with the current password', async () => {
    mockGetPrefs.mockResolvedValue({ has_pin: false, has_password: true });
    render(ProfilePin);
    await fireEvent.click(await screen.findByRole('button', { name: 'Set PIN' }));
    await fireEvent.input(screen.getByLabelText('4-digit PIN'), { target: { value: '12a34' } });
    await fireEvent.input(screen.getByLabelText('Current password'), { target: { value: 'pw' } });
    // A literal pattern="\d{4}" in the template made Svelte read {4} as an
    // expression (pattern "\d4"), so the browser refused every PIN.
    expect(screen.getByLabelText('4-digit PIN').getAttribute('pattern')).toBe('\\d{4}');
    await fireEvent.click(screen.getByRole('button', { name: 'Set PIN' }));

    await waitFor(() => expect(mockSetPin).toHaveBeenCalledWith('1234', 'pw'));
    expect(await screen.findByText('PIN is set')).toBeTruthy();
  });

  it("doesn't ask an account with no OnScreen password for one", async () => {
    mockGetPrefs.mockResolvedValue({ has_pin: true, has_password: false });
    render(ProfilePin);
    await fireEvent.click(await screen.findByRole('button', { name: 'Clear PIN' }));

    expect(screen.queryByLabelText('Current password')).toBeNull();
    await fireEvent.click(screen.getByRole('button', { name: 'Remove PIN' }));
    await waitFor(() => expect(mockClearPin).toHaveBeenCalledWith(''));
  });
});
