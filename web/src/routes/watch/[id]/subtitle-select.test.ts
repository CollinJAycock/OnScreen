import { describe, it, expect } from 'vitest';
import { normalizeLang, langMatches, pickPreferredSubtitle } from './subtitle-select';

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
});
