import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import Page from './+page.svelte';

const mockGet = vi.hoisted(() => vi.fn());
const mockUpdate = vi.hoisted(() => vi.fn());
const mockPhotoGet = vi.hoisted(() => vi.fn());
const mockPhotoUpdate = vi.hoisted(() => vi.fn());
const mockDeleteFaceData = vi.hoisted(() => vi.fn());
const mockConfirm = vi.hoisted(() => vi.fn());

vi.mock('$lib/api', () => ({
  libraryApi: {
    get: mockGet,
    update: mockUpdate,
    del: vi.fn(),
    detectIntros: vi.fn(),
    trickplayStatus: vi.fn(),
    generateTrickplay: vi.fn(),
  },
  subtitleAutoApi: { getLibrary: vi.fn(), updateLibrary: vi.fn() },
  photoSettingsApi: { get: mockPhotoGet, update: mockPhotoUpdate },
  photoPeopleApi: { deleteFaceData: mockDeleteFaceData },
}));
vi.mock('$lib/native', () => ({ confirmAction: mockConfirm }));

vi.mock('$app/stores', async () => {
  const { readable } = await import('svelte/store');
  return {
    page: readable({ url: new URL('http://localhost/libraries/lib-1/settings'), params: { id: 'lib-1' } }),
  };
});

const photoLib = {
  id: 'lib-1', name: 'Family', type: 'photo', scan_paths: ['/media/photos'], agent: 'tmdb',
  language: 'en', scan_interval_minutes: 60, is_private: false, auto_grant_new_users: false,
  created_at: '', updated_at: '',
};

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.setItem('onscreen_user', JSON.stringify({ id: 'u1', is_admin: true }));
  mockGet.mockResolvedValue(photoLib);
  mockUpdate.mockResolvedValue(photoLib);
  mockPhotoGet.mockResolvedValue({ library_id: 'lib-1', raw_jpeg_pairing: true });
  mockPhotoUpdate.mockImplementation(async (_id: string, on: boolean) => ({ library_id: 'lib-1', raw_jpeg_pairing: on }));
});

describe('Library settings — photo libraries', () => {
  it('saves the RAW + JPEG pairing option only when it changed', async () => {
    render(Page);
    const box = (await screen.findByTestId('raw-pairing')) as HTMLInputElement;
    expect(box.checked).toBe(true);

    await fireEvent.click(screen.getByRole('button', { name: 'Save Changes' }));
    await waitFor(() => expect(mockUpdate).toHaveBeenCalled());
    expect(mockPhotoUpdate).not.toHaveBeenCalled();

    await fireEvent.click(box);
    await fireEvent.click(screen.getByRole('button', { name: 'Save Changes' }));
    await waitFor(() => expect(mockPhotoUpdate).toHaveBeenCalledWith('lib-1', false));
  });

  it('deletes face data after confirming', async () => {
    mockConfirm.mockResolvedValueOnce(false).mockResolvedValueOnce(true);
    mockDeleteFaceData.mockResolvedValue(undefined);
    render(Page);
    const btn = await screen.findByTestId('delete-face-data');
    await fireEvent.click(btn);
    await waitFor(() => expect(mockConfirm).toHaveBeenCalledTimes(1));
    expect(mockDeleteFaceData).not.toHaveBeenCalled();

    await fireEvent.click(btn);
    await waitFor(() => expect(mockDeleteFaceData).toHaveBeenCalledWith('lib-1'));
    expect(await screen.findByText('Face data deleted.')).toBeTruthy();
  });

  it('hides the photo options for other library types', async () => {
    mockGet.mockResolvedValue({ ...photoLib, type: 'movie' });
    render(Page);
    await screen.findByRole('button', { name: 'Save Changes' });
    expect(screen.queryByTestId('raw-pairing')).toBeNull();
    expect(screen.queryByTestId('delete-face-data')).toBeNull();
    expect(mockPhotoGet).not.toHaveBeenCalled();
  });
});
