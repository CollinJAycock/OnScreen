import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import LanguagePrefs from './LanguagePrefs.svelte';

const api = vi.hoisted(() => ({
  getPreferences: vi.fn(),
  setPreferences: vi.fn(),
  setQualityProfile: vi.fn(),
}));
const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }));

vi.mock('$lib/api', () => ({ userApi: api }));
vi.mock('$lib/stores/toast', () => ({ toast }));

// What a new account reads after migration 00066, plus stream limits set on
// the Playback page, which the quality-profile PUT must not wipe.
const prefs = {
  preferred_audio_lang: 'eng',
  preferred_subtitle_lang: 'eng',
  subtitle_mode: 'foreign',
  forced_subtitles_only: true,
  max_video_height: 1080,
  max_video_bitrate_kbps: 8000,
  max_audio_bitrate_kbps: null,
  preferred_video_codec: 'hevc',
  episode_use_show_poster: true,
};

beforeEach(() => {
  vi.clearAllMocks();
  api.getPreferences.mockResolvedValue({ ...prefs });
  api.setPreferences.mockResolvedValue(undefined);
  api.setQualityProfile.mockResolvedValue(undefined);
});

const mode = () => screen.getByLabelText('Show subtitles') as HTMLSelectElement;
const save = () => fireEvent.click(screen.getByRole('button', { name: /save/i }));

describe('LanguagePrefs', () => {
  it('shows the English defaults in the "foreign" mode', async () => {
    render(LanguagePrefs);
    await waitFor(() => expect(mode().value).toBe('foreign'));
    expect((screen.getByLabelText('Preferred Subtitle Language') as HTMLSelectElement).value).toBe('eng');
  });

  it("reads an older server's forced-only flag as 'Only forced subtitles'", async () => {
    api.getPreferences.mockResolvedValue({ ...prefs, subtitle_mode: undefined, forced_subtitles_only: true });
    render(LanguagePrefs);
    await waitFor(() => expect(mode().value).toBe('forced'));
  });

  it('a changed mode is saved with the stream limits kept', async () => {
    render(LanguagePrefs);
    await waitFor(() => expect(mode().value).toBe('foreign'));
    await fireEvent.change(mode(), { target: { value: 'always' } });
    await save();

    await waitFor(() =>
      expect(api.setQualityProfile).toHaveBeenCalledWith({
        max_video_height: 1080,
        max_video_bitrate_kbps: 8000,
        max_audio_bitrate_kbps: null,
        preferred_video_codec: 'hevc',
        forced_subtitles_only: false,
        subtitle_mode: 'always',
      }),
    );
    expect(api.setPreferences).toHaveBeenCalledWith(
      expect.objectContaining({ preferred_audio_lang: 'eng', preferred_subtitle_lang: 'eng' }),
    );
  });

  it('leaves the quality profile alone when the mode did not change', async () => {
    render(LanguagePrefs);
    await waitFor(() => expect(mode().value).toBe('foreign'));
    await save();
    await waitFor(() => expect(api.setPreferences).toHaveBeenCalled());
    expect(api.setQualityProfile).not.toHaveBeenCalled();
  });

  it("saves 'None' as null, which means the file's own default tracks", async () => {
    render(LanguagePrefs);
    await waitFor(() => expect(mode().value).toBe('foreign'));
    await fireEvent.change(screen.getByLabelText('Preferred Subtitle Language'), { target: { value: '' } });
    await save();
    await waitFor(() =>
      expect(api.setPreferences).toHaveBeenCalledWith(expect.objectContaining({ preferred_subtitle_lang: null })),
    );
  });
});
