import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import LanguagePicker from './LanguagePicker.svelte';
import { locale, setLocale, storedLocale } from '$lib/i18n/index.svelte';

const { mockSetUILanguage } = vi.hoisted(() => ({ mockSetUILanguage: vi.fn() }));
vi.mock('$lib/api', () => ({ userApi: { setUILanguage: mockSetUILanguage } }));

beforeEach(async () => {
  localStorage.clear();
  mockSetUILanguage.mockReset().mockResolvedValue(undefined);
  Object.defineProperty(navigator, 'languages', { value: ['en-US'], configurable: true });
  await setLocale('en');
});

describe('LanguagePicker', () => {
  it('lists device language plus each language in its own name', () => {
    render(LanguagePicker);
    const options = [...screen.getByLabelText('Language').querySelectorAll('option')].map((o) => o.textContent);
    expect(options).toEqual(['Device language', 'English', 'Español', '简体中文']);
  });

  it('switches this device only on the sign-in screen', async () => {
    render(LanguagePicker, { compact: true });
    await fireEvent.change(screen.getByLabelText('Language'), { target: { value: 'es' } });
    await waitFor(() => expect(locale()).toBe('es'));
    expect(storedLocale()).toBe('es');
    expect(mockSetUILanguage).not.toHaveBeenCalled();
    // The picker's own label follows the new language.
    expect(screen.getByLabelText('Idioma')).toBeTruthy();
  });

  it('saves to the account on the Account page, and says so if that fails', async () => {
    render(LanguagePicker, { saveToAccount: true });
    await fireEvent.change(screen.getByLabelText('Language'), { target: { value: 'zh-Hans' } });
    await waitFor(() => expect(mockSetUILanguage).toHaveBeenCalledWith('zh-Hans'));
    expect(locale()).toBe('zh-Hans');

    mockSetUILanguage.mockRejectedValueOnce(new Error('offline'));
    await fireEvent.change(screen.getByLabelText('语言'), { target: { value: 'es' } });
    expect(await screen.findByRole('alert')).toHaveTextContent('No se pudo guardar el idioma');
    expect(locale()).toBe('es'); // still used on this device
  });

  it('"Device language" clears the choice on the device and the account', async () => {
    await setLocale('es', { persist: true });
    render(LanguagePicker, { saveToAccount: true });
    await fireEvent.change(screen.getByLabelText('Idioma'), { target: { value: '' } });
    await waitFor(() => expect(mockSetUILanguage).toHaveBeenCalledWith(null));
    expect(storedLocale()).toBeNull();
    expect(locale()).toBe('en');
  });
});
