// Ported from web/src/routes/watch/[id]/subtitle-select.test.ts (the contract
// must stay in lock-step with the web helper), plus pickPreferredAudio.
import { describe, it, expect } from 'vitest';
import {
  normalizeLang,
  langMatches,
  isForeignAudio,
  pickPreferredAudio,
  pickPreferredSubtitle,
  subtitleModeOf,
} from './subtitleSelect';

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
    expect(pickPreferredSubtitle(subs, null, 'always')?.title).toBe('Dialogue');
    expect(pickPreferredSubtitle(subs, '', 'always')?.title).toBe('Dialogue');
  });
  it('stays off with no default track, and in the forced modes unless the default is forced', () => {
    expect(pickPreferredSubtitle(subs.map((s) => ({ ...s, isDefault: false })), null, 'always')).toBeNull();
    expect(pickPreferredSubtitle(subs, null, 'forced')).toBeNull();
    expect(pickPreferredSubtitle(subs, null, 'foreign', 'jpn')).toBeNull();
    expect(pickPreferredSubtitle([{ language: 'eng', forced: true, isDefault: true }], null, 'forced')?.forced).toBe(true);
  });
  it('lets a preference win over the default, and leaves a preference with no match off', () => {
    const multi = [...subs, { language: 'spa', forced: false, title: 'Español' }];
    expect(pickPreferredSubtitle(multi, 'es', 'always')?.title).toBe('Español');
    expect(pickPreferredSubtitle(multi, 'de', 'always')).toBeNull();
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

  it('picks across embedded and downloaded rows alike (the player offers both)', () => {
    const rows = [
      { key: 'emb:3', language: 'eng', forced: false },
      { key: 'ext:a1', language: 'pt-BR', forced: false },
    ];
    expect(pickPreferredSubtitle(rows, 'por', 'always')?.key).toBe('ext:a1');
    // The settings page's "Off" value matches nothing.
    expect(pickPreferredSubtitle(rows, 'off', 'always')).toBeNull();
    expect(pickPreferredSubtitle(rows, 'off', 'foreign', 'jpn')).toBeNull();
  });
});

// docs/foreign-audio-subtitles.md §2, row by row (the web's table).
describe('pickPreferredSubtitle in the "foreign" mode', () => {
  const full = { language: 'eng', forced: false, title: 'full' };
  const forced = { language: 'eng', forced: true, title: 'forced' };
  const spanish = { language: 'spa', forced: false, title: 'spanish' };
  const cases: [string, string | null, { language: string; forced: boolean; title: string }[], string | null][] = [
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
      expect(pickPreferredSubtitle(tracks, 'eng', 'foreign', audio)?.title ?? null).toBe(want);
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
