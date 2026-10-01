import { describe, expect, it } from 'vitest';
import { audioTrackLabel, languageName, primaryLanguage, subtitleLabel } from './langName';

describe('languageName', () => {
  it('names 639-1, 639-2/T and 639-2/B codes alike', () => {
    expect(languageName('en')).toBe('English');
    expect(languageName('eng')).toBe('English');
    expect(languageName('fra')).toBe('French');
    expect(languageName('fre')).toBe('French');
    expect(languageName('ger')).toBe('German');
    expect(languageName('deu')).toBe('German');
    expect(languageName('chi')).toBe('Chinese');
    expect(languageName('JPN')).toBe('Japanese');
    expect(languageName(' spa ')).toBe('Spanish');
  });

  it('adds the region or script a tag carries', () => {
    expect(languageName('pt-BR')).toBe('Portuguese (Brazil)');
    expect(languageName('pt_br')).toBe('Portuguese (Brazil)');
    expect(languageName('es-419')).toBe('Spanish (Latin America)');
    expect(languageName('zh-TW')).toBe('Chinese (Taiwan)');
    expect(languageName('zh-Hant')).toBe('Chinese (Traditional)');
    // A region the table doesn't name shows as its code, like Locale does.
    expect(languageName('en-XQ')).toBe('English (XQ)');
  });

  it('gives an unknown code back as itself, and nothing for no language', () => {
    expect(languageName('xyz')).toBe('xyz');
    expect(languageName('')).toBeNull();
    expect(languageName(null)).toBeNull();
    expect(languageName('und')).toBeNull();
    expect(languageName('mul')).toBeNull();
    expect(languageName('zxx')).toBeNull();
  });

  it('knows the three-letter-only codes', () => {
    expect(languageName('yue')).toBe('Cantonese');
    expect(languageName('fil')).toBe('Filipino');
  });

  it('reduces any form to its primary subtag', () => {
    expect(primaryLanguage('ger')).toBe('de');
    expect(primaryLanguage('pt-BR')).toBe('pt');
    expect(primaryLanguage('xyz-AB')).toBe('xyz');
    expect(primaryLanguage(undefined)).toBe('');
  });
});

describe('subtitleLabel (Android SubtitleLabel)', () => {
  const base = { title: null, forced: false, sdh: false, downloaded: false, fallback: 'Track 3' };

  it('names the language, then a title of its own', () => {
    expect(subtitleLabel({ ...base, language: 'eng' })).toBe('English');
    expect(subtitleLabel({ ...base, language: 'eng', title: 'Commentary' })).toBe('English · Commentary');
  });

  it('drops a title that only repeats the language', () => {
    expect(subtitleLabel({ ...base, language: 'eng', title: 'English' })).toBe('English');
    expect(subtitleLabel({ ...base, language: 'eng', title: 'eng' })).toBe('English');
    expect(subtitleLabel({ ...base, language: 'pt-BR', title: 'Portuguese' })).toBe('Portuguese (Brazil)');
    expect(subtitleLabel({ ...base, language: 'pt-BR', title: 'portuguese (brazil)' })).toBe('Portuguese (Brazil)');
  });

  it('adds forced / SDH / downloaded unless the title already says so', () => {
    expect(subtitleLabel({ ...base, language: 'spa', forced: true })).toBe('Spanish · forced');
    expect(subtitleLabel({ ...base, language: 'eng', title: 'English (Forced)', forced: true })).toBe(
      'English · English (Forced)',
    );
    expect(subtitleLabel({ ...base, language: 'eng', title: 'English SDH', sdh: true })).toBe('English · English SDH');
    expect(subtitleLabel({ ...base, language: 'eng', title: 'CC', sdh: true })).toBe('English · CC');
    expect(subtitleLabel({ ...base, language: 'eng', sdh: true })).toBe('English · SDH');
    expect(subtitleLabel({ ...base, language: 'pt-BR', downloaded: true, fallback: 'External' })).toBe(
      'Portuguese (Brazil) · downloaded',
    );
    expect(subtitleLabel({ ...base, language: 'fre', forced: true, sdh: true, downloaded: true })).toBe(
      'French · forced · SDH · downloaded',
    );
  });

  it('falls back to the given name when there is neither a language nor a title', () => {
    expect(subtitleLabel({ ...base, language: '' })).toBe('Track 3');
    expect(subtitleLabel({ ...base, language: 'und', forced: true })).toBe('Track 3 · forced');
    expect(subtitleLabel({ ...base, language: '', downloaded: true, fallback: 'External' })).toBe('External · downloaded');
    // An unknown code still beats the fallback.
    expect(subtitleLabel({ ...base, language: 'xyz' })).toBe('xyz');
  });
});

describe('audioTrackLabel', () => {
  it('reads "N. <title|language|Track N> · Nch"', () => {
    expect(audioTrackLabel({ title: 'Commentary', language: 'eng', channels: 2 }, 1)).toBe('2. Commentary · 2ch');
    expect(audioTrackLabel({ title: '', language: 'jpn', channels: 6 }, 0)).toBe('1. Japanese · 6ch');
    expect(audioTrackLabel({ title: '  ', language: 'und', channels: 2 }, 2)).toBe('3. Track 3 · 2ch');
    expect(audioTrackLabel({ language: 'xyz', channels: 0 }, 0)).toBe('1. xyz');
  });
});
