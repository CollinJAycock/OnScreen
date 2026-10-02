import { describe, expect, it } from 'vitest';
import {
  LANGUAGE_OPTIONS,
  LANG_SAVE_FAILED,
  LANG_SAVE_REVERTED,
  SUBTITLE_LANGUAGE_OPTIONS,
  findLangOption,
  langLabel,
  languageSaver,
  nextLangValue,
  qualityProfileBody,
  type LangPair,
} from './settingsPrefs';
import type { UserPreferences } from './api/endpoints';

describe('language options', () => {
  it('writes the 639-2/T codes Android writes, Hindi and Arabic included', () => {
    const codes = LANGUAGE_OPTIONS.map((o) => o.value);
    expect(codes).toEqual(['', 'eng', 'spa', 'fra', 'deu', 'ita', 'por', 'jpn', 'kor', 'zho', 'rus', 'hin', 'ara']);
    expect(SUBTITLE_LANGUAGE_OPTIONS[0]).toEqual({ value: 'off', label: 'Off' });
  });

  it('finds the row for an Android-written T code', () => {
    expect(langLabel(LANGUAGE_OPTIONS, 'fra')).toBe('French');
    expect(langLabel(LANGUAGE_OPTIONS, 'deu')).toBe('German');
    expect(langLabel(LANGUAGE_OPTIONS, 'zho')).toBe('Chinese');
  });

  it('still finds the row for the B codes this page used to write', () => {
    expect(langLabel(LANGUAGE_OPTIONS, 'fre')).toBe('French');
    expect(langLabel(LANGUAGE_OPTIONS, 'ger')).toBe('German');
    expect(langLabel(LANGUAGE_OPTIONS, 'chi')).toBe('Chinese');
  });

  it('matches two-letter and region-tagged codes too', () => {
    expect(langLabel(LANGUAGE_OPTIONS, 'en')).toBe('English');
    expect(langLabel(LANGUAGE_OPTIONS, 'pt-BR')).toBe('Portuguese');
    expect(langLabel(LANGUAGE_OPTIONS, 'ENG')).toBe('English');
  });

  it('shows Auto for no preference and Off for subtitles off', () => {
    expect(langLabel(LANGUAGE_OPTIONS, '')).toBe('Auto / file default');
    expect(langLabel(LANGUAGE_OPTIONS, null)).toBe('Auto / file default');
    expect(langLabel(SUBTITLE_LANGUAGE_OPTIONS, 'off')).toBe('Off');
    expect(langLabel(SUBTITLE_LANGUAGE_OPTIONS, '')).toBe('Auto / file default');
  });

  it('shows a set code no row covers as the code, not as Auto', () => {
    expect(langLabel(LANGUAGE_OPTIONS, 'nld')).toBe('nld');
    expect(findLangOption(LANGUAGE_OPTIONS, 'nld')).toBe(-1);
  });

  it('cycles from the matched row, so a B code moves on rather than restarting', () => {
    // fre is French; the next row after French is German.
    expect(nextLangValue(LANGUAGE_OPTIONS, 'fre')).toBe('deu');
    expect(nextLangValue(LANGUAGE_OPTIONS, 'chi')).toBe('rus');
    expect(nextLangValue(LANGUAGE_OPTIONS, 'ara')).toBe('');
    expect(nextLangValue(LANGUAGE_OPTIONS, '')).toBe('eng');
    // Unknown codes start over at the first row.
    expect(nextLangValue(LANGUAGE_OPTIONS, 'nld')).toBe('');
    expect(nextLangValue(SUBTITLE_LANGUAGE_OPTIONS, 'off')).toBe('');
    expect(nextLangValue(SUBTITLE_LANGUAGE_OPTIONS, 'ara')).toBe('off');
  });
});

describe('qualityProfileBody', () => {
  const prefs: UserPreferences = {
    preferred_audio_lang: 'eng',
    max_video_bitrate_kbps: 8000,
    max_audio_bitrate_kbps: 320,
    max_video_height: 1080,
    preferred_video_codec: 'hevc',
    forced_subtitles_only: false,
    episode_use_show_poster: false,
  };

  it('echoes the other quality fields and sets forced-only', () => {
    expect(qualityProfileBody(prefs, true)).toEqual({
      max_video_bitrate_kbps: 8000,
      max_audio_bitrate_kbps: 320,
      max_video_height: 1080,
      preferred_video_codec: 'hevc',
      forced_subtitles_only: true,
    });
  });

  it('sends null for caps that are not set (the endpoint writes every field)', () => {
    const body = qualityProfileBody({ forced_subtitles_only: true, episode_use_show_poster: false }, false);
    expect(body).toEqual({
      max_video_bitrate_kbps: null,
      max_audio_bitrate_kbps: null,
      max_video_height: null,
      preferred_video_codec: null,
      forced_subtitles_only: false,
    });
    // Every key present: an omitted one would reach the server as null too,
    // but the body says so explicitly.
    expect(Object.keys(body).sort()).toEqual([
      'forced_subtitles_only',
      'max_audio_bitrate_kbps',
      'max_video_bitrate_kbps',
      'max_video_height',
      'preferred_video_codec',
    ]);
  });

  it('keeps an empty codec and drops one the endpoint would refuse', () => {
    expect(qualityProfileBody({ ...prefs, preferred_video_codec: '' }, true).preferred_video_codec).toBe('');
    expect(qualityProfileBody({ ...prefs, preferred_video_codec: 'h.264' }, true).preferred_video_codec).toBeNull();
  });
});

function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

// Lets every pending promise callback run.
const flush = () => new Promise<void>((r) => setTimeout(r, 0));

class SignedOut extends Error {}

// The settings page around languageSaver: the rows on screen, every PUT and
// re-read held until the test settles it.
function settingsPage() {
  const screen: LangPair = { audio: 'eng', sub: '' };
  const puts: { langs: LangPair; done: ReturnType<typeof deferred<void>> }[] = [];
  const reads: ReturnType<typeof deferred<LangPair>>[] = [];
  const page = {
    screen,
    puts,
    reads,
    error: '',
    saving: false,
    shown: 0,
  };
  const save = languageSaver({
    current: () => ({ ...screen }),
    put: (langs) => {
      const done = deferred<void>();
      puts.push({ langs, done });
      return done.promise;
    },
    read: () => {
      const d = deferred<LangPair>();
      reads.push(d);
      return d.promise;
    },
    show: (langs) => {
      page.shown++;
      screen.audio = langs.audio;
      screen.sub = langs.sub;
    },
    saving: (on) => {
      page.saving = on;
    },
    error: (text) => {
      page.error = text;
    },
    fatal: (e) => e instanceof SignedOut,
  });
  // A press on the Audio row: the row shows the new value, then saves.
  const pressAudio = (audio: string) => {
    screen.audio = audio;
    return save();
  };
  return { page, pressAudio };
}

describe('languageSaver', () => {
  it('saves a press, with no re-read', async () => {
    const { page, pressAudio } = settingsPage();
    const run = pressAudio('spa');
    expect(page.saving).toBe(true);
    expect(page.puts.map((p) => p.langs)).toEqual([{ audio: 'spa', sub: '' }]);
    page.puts[0].done.resolve();
    await run;
    expect(page.saving).toBe(false);
    expect(page.error).toBe('');
    expect(page.reads).toHaveLength(0);
  });

  it('shows what the server kept when a save fails with nothing queued', async () => {
    const { page, pressAudio } = settingsPage();
    const run = pressAudio('spa');
    page.puts[0].done.reject(new Error('500'));
    await flush();
    expect(page.error).toBe(LANG_SAVE_FAILED);
    expect(page.reads).toHaveLength(1);
    page.reads[0].resolve({ audio: 'eng', sub: 'off' });
    await run;
    expect(page.screen).toEqual({ audio: 'eng', sub: 'off' });
    expect(page.error).toBe(LANG_SAVE_REVERTED);
  });

  it('skips the re-read when a press is queued, and saves that press', async () => {
    const { page, pressAudio } = settingsPage();
    const first = pressAudio('spa');
    const second = pressAudio('fra');
    // spa's PUT gets a transient 500.
    page.puts[0].done.reject(new Error('500'));
    await flush();
    // No re-read: it would put "eng" back on screen and the follow-up would
    // save "eng", losing fra.
    expect(page.reads).toHaveLength(0);
    expect(page.screen.audio).toBe('fra');
    expect(page.puts.map((p) => p.langs.audio)).toEqual(['spa', 'fra']);
    // The error stays up while the follow-up is out...
    expect(page.error).toBe(LANG_SAVE_FAILED);
    page.puts[1].done.resolve();
    await Promise.all([first, second]);
    // ...and goes once a save succeeded.
    expect(page.error).toBe('');
    expect(page.screen.audio).toBe('fra');
  });

  it("drops the re-read's answer when a press arrives while it is out", async () => {
    const { page, pressAudio } = settingsPage();
    const first = pressAudio('spa');
    page.puts[0].done.reject(new Error('500'));
    await flush();
    expect(page.reads).toHaveLength(1);
    const second = pressAudio('fra');
    page.reads[0].resolve({ audio: 'eng', sub: '' });
    await flush();
    expect(page.shown).toBe(0);
    expect(page.screen.audio).toBe('fra');
    expect(page.puts.map((p) => p.langs.audio)).toEqual(['spa', 'fra']);
    page.puts[1].done.resolve();
    await Promise.all([first, second]);
    expect(page.error).toBe('');
  });

  it('keeps the error up across a later run until a save succeeds', async () => {
    const { page, pressAudio } = settingsPage();
    const first = pressAudio('spa');
    page.puts[0].done.reject(new Error('500'));
    await flush();
    page.reads[0].resolve({ audio: 'eng', sub: '' });
    await first;
    expect(page.error).toBe(LANG_SAVE_REVERTED);
    // The next press starts a new run: the message is still there.
    const second = pressAudio('ita');
    expect(page.saving).toBe(true);
    expect(page.error).toBe(LANG_SAVE_REVERTED);
    page.puts[1].done.reject(new Error('500'));
    await flush();
    expect(page.error).toBe(LANG_SAVE_FAILED);
    page.reads[1].resolve({ audio: 'eng', sub: '' });
    await second;
    const third = pressAudio('jpn');
    page.puts[2].done.resolve();
    await third;
    expect(page.error).toBe('');
  });

  it('keeps the unsaved choice under the plain error when the re-read fails too', async () => {
    const { page, pressAudio } = settingsPage();
    const run = pressAudio('spa');
    page.puts[0].done.reject(new Error('500'));
    await flush();
    page.reads[0].reject(new Error('offline'));
    await run;
    expect(page.screen.audio).toBe('spa');
    expect(page.error).toBe(LANG_SAVE_FAILED);
    expect(page.saving).toBe(false);
  });

  it('stops on a failure the page handles itself (signed out)', async () => {
    const { page, pressAudio } = settingsPage();
    const run = pressAudio('spa');
    page.puts[0].done.reject(new SignedOut());
    await run;
    expect(page.reads).toHaveLength(0);
    expect(page.error).toBe('');
    expect(page.saving).toBe(false);
  });
});
