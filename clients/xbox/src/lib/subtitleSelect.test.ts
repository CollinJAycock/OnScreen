// Ported from web/src/routes/watch/[id]/subtitle-select.test.ts (the contract
// must stay in lock-step with the web helper), plus pickPreferredAudio.
import { describe, it, expect } from 'vitest';
import { normalizeLang, langMatches, pickPreferredAudio, pickPreferredSubtitle } from './subtitleSelect';

describe('normalizeLang', () => {
  it('maps 639-2/B 3-letter codes to 639-1', () => {
    expect(normalizeLang('eng')).toBe('en');
    expect(normalizeLang('spa')).toBe('es');
    expect(normalizeLang('fre')).toBe('fr');
    expect(normalizeLang('fra')).toBe('fr');
  });
  it('strips region/script subtags and lowercases', () => {
    expect(normalizeLang('en-US')).toBe('en');
    expect(normalizeLang('pt-BR')).toBe('pt');
    expect(normalizeLang('EN')).toBe('en');
    expect(normalizeLang('zh-Hant')).toBe('zh');
  });
  it('passes unknown codes through as their primary subtag', () => {
    expect(normalizeLang('xyz')).toBe('xyz');
    expect(normalizeLang('')).toBe('');
    expect(normalizeLang(null)).toBe('');
  });
});

describe('langMatches', () => {
  it('matches across 2-letter / 3-letter / region forms', () => {
    expect(langMatches('eng', 'en')).toBe(true);
    expect(langMatches('en-US', 'eng')).toBe(true);
    expect(langMatches('EN', 'en')).toBe(true);
  });
  it('does not match different languages or empty', () => {
    expect(langMatches('eng', 'spa')).toBe(false);
    expect(langMatches('', 'en')).toBe(false);
    expect(langMatches('en', null)).toBe(false);
  });
});

describe('pickPreferredSubtitle with no preferred language', () => {
  // The file's default track, as ExoPlayer picks it: an anime release whose
  // English dialogue track is marked default opened with subtitles on the
  // Android TV app and without them here.
  const subs = [
    { language: 'eng', forced: false, title: 'Signs' },
    { language: 'eng', forced: false, title: 'Dialogue', isDefault: true },
  ];
  it("shows the file's default track", () => {
    expect(pickPreferredSubtitle(subs, null, false)?.title).toBe('Dialogue');
    expect(pickPreferredSubtitle(subs, '', false)?.title).toBe('Dialogue');
  });
  it('stays off with no default track, and under forced-only unless the default is forced', () => {
    expect(pickPreferredSubtitle(subs.map((s) => ({ ...s, isDefault: false })), null, false)).toBeNull();
    expect(pickPreferredSubtitle(subs, null, true)).toBeNull();
    expect(pickPreferredSubtitle([{ language: 'eng', forced: true, isDefault: true }], null, true)?.forced).toBe(true);
  });
  it('lets a preference win over the default, and leaves a preference with no match off', () => {
    const multi = [...subs, { language: 'spa', forced: false, title: 'Español' }];
    expect(pickPreferredSubtitle(multi, 'es', false)?.title).toBe('Español');
    expect(pickPreferredSubtitle(multi, 'de', false)).toBeNull();
  });
});
describe('pickPreferredSubtitle', () => {
  const subs = [
    { language: 'eng', forced: false },
    { language: 'eng', forced: true },
    { language: 'spa', forced: false },
  ];

  it('returns null with no preferred language', () => {
    expect(pickPreferredSubtitle(subs, null, false)).toBeNull();
    expect(pickPreferredSubtitle(subs, '', true)).toBeNull();
  });

  it('matches preferred language across code forms', () => {
    // pref "en" must match stream "eng"
    const got = pickPreferredSubtitle(subs, 'en', false);
    expect(got?.language).toBe('eng');
  });

  it('prefers the forced track in-language when not forcedOnly', () => {
    const got = pickPreferredSubtitle(subs, 'en', false);
    expect(got?.forced).toBe(true);
  });

  it('forcedOnly returns only a forced in-language track', () => {
    expect(pickPreferredSubtitle(subs, 'en', true)?.forced).toBe(true);
    // Spanish has no forced track → forcedOnly stays off
    expect(pickPreferredSubtitle(subs, 'es', true)).toBeNull();
  });

  it('falls back to the first full track when no forced exists and not forcedOnly', () => {
    const got = pickPreferredSubtitle(subs, 'es', false);
    expect(got?.language).toBe('spa');
    expect(got?.forced).toBe(false);
  });

  it('returns null when the language is absent', () => {
    expect(pickPreferredSubtitle(subs, 'de', false)).toBeNull();
  });

  it('picks across embedded and downloaded rows alike (the player offers both)', () => {
    const rows = [
      { key: 'emb:3', language: 'eng', forced: false },
      { key: 'ext:a1', language: 'pt-BR', forced: false },
    ];
    expect(pickPreferredSubtitle(rows, 'por', false)?.key).toBe('ext:a1');
    // The settings page's "Off" value matches nothing.
    expect(pickPreferredSubtitle(rows, 'off', false)).toBeNull();
  });
});

describe('pickPreferredAudio', () => {
  const audio = [
    { language: 'jpn' },
    { language: 'eng' },
    { language: 'fra' },
    { language: 'fra' }, // a commentary in the same language
  ];

  it('returns the ordinal of the first track in the language', () => {
    expect(pickPreferredAudio(audio, 'fra')).toBe(2);
    expect(pickPreferredAudio(audio, 'fre')).toBe(2);
    expect(pickPreferredAudio(audio, 'fr')).toBe(2);
    expect(pickPreferredAudio(audio, 'en-US')).toBe(1);
    expect(pickPreferredAudio(audio, 'jpn')).toBe(0);
  });

  it('returns null with no preference, or no track in it (the server default plays)', () => {
    expect(pickPreferredAudio(audio, null)).toBeNull();
    expect(pickPreferredAudio(audio, '')).toBeNull();
    expect(pickPreferredAudio(audio, 'deu')).toBeNull();
    expect(pickPreferredAudio([], 'eng')).toBeNull();
    expect(pickPreferredAudio([{ language: '' }], 'eng')).toBeNull();
  });
});
