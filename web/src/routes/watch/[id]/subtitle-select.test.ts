import { describe, it, expect } from 'vitest';
import { normalizeLang, langMatches, pickPreferredSubtitle, isForeignAudio, subtitleModeOf } from './subtitle-select';

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
  const subs = [
    { language: 'eng', forced: false, title: 'Signs' },
    { language: 'eng', forced: false, title: 'Dialogue', default: true },
  ];
  it("shows the file's default track (API streams carry it as default)", () => {
    expect(pickPreferredSubtitle(subs, null, 'always')?.title).toBe('Dialogue');
  });
  it('stays off with no default, and in the forced modes unless the default is forced', () => {
    expect(pickPreferredSubtitle(subs.map((s) => ({ ...s, default: false })), null, 'always')).toBeNull();
    expect(pickPreferredSubtitle(subs, null, 'forced')).toBeNull();
    expect(pickPreferredSubtitle(subs, null, 'foreign', 'jpn')).toBeNull();
  });
});

describe('pickPreferredSubtitle', () => {
  const subs = [
    { language: 'eng', forced: false },
    { language: 'eng', forced: true },
    { language: 'spa', forced: false },
  ];

  it('matches preferred language across code forms', () => {
    // pref "en" must match stream "eng"
    expect(pickPreferredSubtitle(subs, 'en', 'always')?.language).toBe('eng');
  });

  it('"always" picks the full track before the forced one', () => {
    expect(pickPreferredSubtitle(subs, 'en', 'always')?.forced).toBe(false);
    expect(pickPreferredSubtitle(subs.filter((s) => s.forced), 'en', 'always')?.forced).toBe(true);
  });

  it('"always" picks a plain full track before an SDH one', () => {
    const withSdh = [
      { language: 'eng', forced: false, sdh: true, title: 'SDH' },
      { language: 'eng', forced: false, title: 'Plain' },
    ];
    expect(pickPreferredSubtitle(withSdh, 'eng', 'always')?.title).toBe('Plain');
    expect(pickPreferredSubtitle(withSdh.slice(0, 1), 'eng', 'always')?.title).toBe('SDH');
  });

  it('"forced" returns only a forced in-language track', () => {
    expect(pickPreferredSubtitle(subs, 'en', 'forced')?.forced).toBe(true);
    // Spanish has no forced track → stays off
    expect(pickPreferredSubtitle(subs, 'es', 'forced')).toBeNull();
  });

  it('returns null when the language is absent', () => {
    expect(pickPreferredSubtitle(subs, 'de', 'always')).toBeNull();
  });
});

// docs/foreign-audio-subtitles.md §2, row by row.
describe('pickPreferredSubtitle in the "foreign" mode', () => {
  const full = { language: 'eng', forced: false, title: 'full' };
  const forced = { language: 'eng', forced: true, title: 'forced' };
  const spanish = { language: 'spa', forced: false, title: 'spanish' };
  const cases: [string, string | null, { title: string }[], string | null][] = [
    ['English audio, full + forced', 'eng', [full, forced], 'forced'],
    ['English audio ("en"), full only', 'en', [full], null],
    ['Japanese audio, full + forced', 'jpn', [full, forced], 'full'],
    ['Japanese audio, full only', 'jpn', [full], 'full'],
    ['Japanese audio, forced only', 'jpn', [forced], 'forced'],
    ['Japanese audio, Spanish only', 'jpn', [spanish], null],
    ['untagged audio, full', 'und', [full], null],
    ['no audio language at all, full', '', [full], null],
    ['audio unknown to the client, full', null, [full], null],
  ];
  for (const [name, audio, tracks, want] of cases) {
    it(name, () => {
      const got = pickPreferredSubtitle(tracks as { language: string; forced: boolean; title: string }[], 'eng', 'foreign', audio);
      expect(got?.title ?? null).toBe(want);
    });
  }
});

describe('isForeignAudio', () => {
  it('needs a known language that differs from the subtitles', () => {
    expect(isForeignAudio('jpn', 'eng')).toBe(true);
    expect(isForeignAudio('ja', 'en')).toBe(true);
    expect(isForeignAudio('eng', 'en')).toBe(false);
    expect(isForeignAudio('und', 'eng')).toBe(false);
    expect(isForeignAudio('mul', 'eng')).toBe(false);
    expect(isForeignAudio(undefined, 'eng')).toBe(false);
  });
});

describe('subtitleModeOf', () => {
  it('reads subtitle_mode, else the forced-only flag of an older server', () => {
    expect(subtitleModeOf({ subtitle_mode: 'foreign', forced_subtitles_only: true })).toBe('foreign');
    expect(subtitleModeOf({ subtitle_mode: 'always', forced_subtitles_only: true })).toBe('always');
    expect(subtitleModeOf({ forced_subtitles_only: true })).toBe('forced');
    expect(subtitleModeOf({ forced_subtitles_only: false })).toBe('always');
    expect(subtitleModeOf({ subtitle_mode: 'sometimes' })).toBe('always');
  });
});
