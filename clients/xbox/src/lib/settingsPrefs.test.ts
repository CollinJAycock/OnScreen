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
  QUALITY_ROWS,
  QUALITY_SAVE_FAILED,
  QUALITY_SAVE_INVALID,
  qualityLabel,
  qualityOptions,
  qualityProfileBody,
  qualityProfileUpdate,
  qualitySaver,
  qualityValue,
  type LangPair,
  type QualityField,
  type QualityRow,
} from './settingsPrefs';
import type { QualityProfileUpdate, UserPreferences } from './api/endpoints';

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

const row = (field: QualityField): QualityRow => QUALITY_ROWS.find((r) => r.field === field) as QualityRow;

describe('Playback quality choices', () => {
  it("offers the web app's choices, values in the profile's units", () => {
    const values = (f: QualityField) => row(f).options.map((o) => o.value);
    expect(values('max_video_height')).toEqual([null, 2160, 1080, 720, 480]);
    expect(values('max_video_bitrate_kbps')).toEqual([null, 40000, 20000, 12000, 8000, 4000, 2000, 1000]);
    expect(values('max_audio_bitrate_kbps')).toEqual([null, 320, 256, 192, 128, 96]);
    expect(values('preferred_video_codec')).toEqual([null, 'h264', 'hevc']);
    expect(qualityLabel(row('max_video_height'), 2160)).toBe('4K');
    expect(qualityLabel(row('max_video_bitrate_kbps'), null)).toBe('No limit');
    expect(qualityLabel(row('max_audio_bitrate_kbps'), null)).toBe('Original');
    expect(qualityLabel(row('preferred_video_codec'), null)).toBe('Automatic');
  });

  it('shows a value set elsewhere as itself, not as "no limit", and lists it', () => {
    expect(qualityLabel(row('max_video_height'), 1440)).toBe('1440p');
    expect(qualityLabel(row('max_video_bitrate_kbps'), 15000)).toBe('15 Mbps');
    expect(qualityLabel(row('max_video_bitrate_kbps'), 2500)).toBe('2.5 Mbps');
    expect(qualityLabel(row('max_audio_bitrate_kbps'), 160)).toBe('160 kbps');
    expect(qualityLabel(row('preferred_video_codec'), 'av1')).toBe('av1');
    const opts = qualityOptions(row('max_video_height'), 1440);
    expect(opts).toHaveLength(6);
    expect(opts[5]).toEqual({ value: 1440, label: '1440p' });
    expect(qualityOptions(row('max_video_height'), 1080)).toHaveLength(5);
  });

  it('reads a missing, 0 or empty cap as no limit', () => {
    const p = { max_video_height: 0, max_video_bitrate_kbps: null, preferred_video_codec: '' };
    expect(qualityValue(p, 'max_video_height')).toBeNull();
    expect(qualityValue(p, 'max_video_bitrate_kbps')).toBeNull();
    expect(qualityValue(p, 'max_audio_bitrate_kbps')).toBeNull();
    expect(qualityValue(p, 'preferred_video_codec')).toBeNull();
    expect(qualityValue({ max_video_height: 720 }, 'max_video_height')).toBe(720);
  });
});

describe('qualityProfileUpdate', () => {
  const stored: UserPreferences = {
    max_video_bitrate_kbps: 8000,
    max_audio_bitrate_kbps: 320,
    max_video_height: 1080,
    preferred_video_codec: 'hevc',
    forced_subtitles_only: true,
    episode_use_show_poster: false,
  };

  it('sends all five fields: the change, the rest echoed from the read', () => {
    expect(qualityProfileUpdate(stored, { max_video_height: 720 })).toEqual({
      max_video_bitrate_kbps: 8000,
      max_audio_bitrate_kbps: 320,
      max_video_height: 720,
      preferred_video_codec: 'hevc',
      forced_subtitles_only: true,
    });
  });

  it('clears a cap with null', () => {
    const body = qualityProfileUpdate(stored, { max_video_bitrate_kbps: null, preferred_video_codec: null });
    expect(body.max_video_bitrate_kbps).toBeNull();
    expect(body.preferred_video_codec).toBeNull();
    expect(body.max_video_height).toBe(1080);
  });
});

// The settings page around qualitySaver: what the rows show, every read and
// PUT held until the test settles it.
function qualityPage(initial: QualityProfileUpdate) {
  const reads: ReturnType<typeof deferred<UserPreferences>>[] = [];
  const puts: { body: QualityProfileUpdate; done: ReturnType<typeof deferred<void>> }[] = [];
  const page = { screen: { ...initial }, reads, puts, error: '', saving: false };
  const save = qualitySaver({
    read: () => {
      const d = deferred<UserPreferences>();
      reads.push(d);
      return d.promise;
    },
    put: (body) => {
      const d = deferred<void>();
      puts.push({ body, done: d });
      return d.promise;
    },
    show: (stored, pending) => {
      for (const k of Object.keys(stored) as (keyof QualityProfileUpdate)[]) {
        if (!pending.has(k)) (page.screen as Record<string, unknown>)[k] = stored[k];
      }
    },
    saving: (on) => (page.saving = on),
    error: (text) => (page.error = text),
    fatal: (e) => e instanceof SignedOut,
  });
  const press = (change: Partial<QualityProfileUpdate>) => {
    Object.assign(page.screen, change);
    return save(change);
  };
  return { page, press };
}

describe('qualitySaver', () => {
  const server = (p: Partial<UserPreferences> = {}): UserPreferences => ({
    max_video_bitrate_kbps: null,
    max_audio_bitrate_kbps: null,
    max_video_height: null,
    preferred_video_codec: null,
    forced_subtitles_only: false,
    episode_use_show_poster: false,
    ...p,
  });
  const start: QualityProfileUpdate = qualityProfileUpdate(server());

  it('echoes the fields it does not change from a read made right before the PUT', async () => {
    const { page, press } = qualityPage(start);
    const run = press({ max_video_height: 720 });
    // Another device set a bitrate cap since the page opened: it is kept.
    page.reads[0].resolve(server({ max_video_bitrate_kbps: 4000 }));
    await flush();
    expect(page.puts[0].body).toEqual({ ...start, max_video_bitrate_kbps: 4000, max_video_height: 720 });
    page.puts[0].done.resolve();
    await run;
    expect(page.screen.max_video_bitrate_kbps).toBe(4000);
    expect(page.screen.max_video_height).toBe(720);
    expect(page.error).toBe('');
    expect(page.saving).toBe(false);
  });

  it('folds presses made during a save into one follow-up save, keeping both', async () => {
    const { page, press } = qualityPage(start);
    const first = press({ max_video_height: 1080 });
    const second = press({ preferred_video_codec: 'h264' });
    const third = press({ forced_subtitles_only: true });
    page.reads[0].resolve(server());
    await flush();
    expect(page.puts).toHaveLength(1);
    page.puts[0].done.resolve();
    await flush();
    // The follow-up reads again (the server now has the first change).
    page.reads[1].resolve(server({ max_video_height: 1080 }));
    await flush();
    expect(page.puts[1].body).toMatchObject({
      max_video_height: 1080,
      preferred_video_codec: 'h264',
      forced_subtitles_only: true,
    });
    page.puts[1].done.resolve();
    await Promise.all([first, second, third]);
    expect(page.puts).toHaveLength(2);
    expect(page.screen.preferred_video_codec).toBe('h264');
  });

  it("puts the server's value back on a refused (400) save", async () => {
    const { page, press } = qualityPage({ ...start, max_audio_bitrate_kbps: 320 });
    const run = press({ max_audio_bitrate_kbps: 96 });
    page.reads[0].resolve(server({ max_audio_bitrate_kbps: 320 }));
    await flush();
    page.puts[0].done.reject(Object.assign(new Error('invalid'), { status: 400 }));
    await run;
    expect(page.error).toBe(QUALITY_SAVE_INVALID);
    expect(page.screen.max_audio_bitrate_kbps).toBe(320);
  });

  it('keeps a change whose read failed for the next save', async () => {
    const { page, press } = qualityPage(start);
    const run = press({ max_video_height: 480 });
    page.reads[0].reject(new Error('offline'));
    await run;
    expect(page.error).toBe(QUALITY_SAVE_FAILED);
    // Still on screen, and the next press carries it too.
    expect(page.screen.max_video_height).toBe(480);
    const next = press({ forced_subtitles_only: true });
    page.reads[1].resolve(server());
    await flush();
    expect(page.puts[0].body).toMatchObject({ max_video_height: 480, forced_subtitles_only: true });
    page.puts[0].done.resolve();
    await next;
    expect(page.error).toBe('');
  });

  it('stops on a failure the page handles itself (signed out)', async () => {
    const { page, press } = qualityPage(start);
    const run = press({ max_video_height: 720 });
    page.reads[0].reject(new SignedOut());
    await run;
    expect(page.error).toBe('');
    expect(page.saving).toBe(false);
  });
});
