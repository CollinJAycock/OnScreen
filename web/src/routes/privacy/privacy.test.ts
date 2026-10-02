import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';

// The privacy page's text, tags and comments out, spaces as read. The page
// is static, so its source is what it says.
const source = readFileSync(join(import.meta.dirname, '+page.svelte'), 'utf8');
const text = source
  .replace(/<script[\s\S]*?<\/script>/g, '')
  .replace(/<style[\s\S]*?<\/style>/g, '')
  .replace(/<!--[\s\S]*?-->/g, '')
  .replace(/<[^>]+>/g, ' ')
  .replace(/&amp;/g, '&')
  .replace(/\s+/g, ' ');

describe('privacy policy', () => {
  it('keeps its date', () => {
    expect(text).toContain('Last updated: 2026-10-02');
  });

  // internal/scrobble: the server sends plays to each service the user
  // links under Settings → Scrobbling.
  it('names every scrobbling service the server can send plays to', () => {
    expect(text).toContain('ListenBrainz (optional).');
    expect(text).toContain('Last.fm (optional).');
    expect(text).toContain('Trakt (optional).');
  });

  // lastfm.go lastFMParams: artist, track, album, track number, duration,
  // MusicBrainz id; scrobble adds the start time.
  it('says what goes to Last.fm', () => {
    const lastfm = text.slice(text.indexOf('Last.fm (optional).'));
    expect(lastfm).toContain('title, artist, album, track number, length and MusicBrainz ID');
    expect(lastfm).toContain('the developer receives none of it');
  });

  // trakt.go traktBody / historyBody: title, year, TMDB/TVDB/IMDb ids,
  // season and episode number, progress; history adds the watched time.
  it('says what goes to Trakt', () => {
    const trakt = text.slice(text.indexOf('Trakt (optional).'));
    expect(trakt).toContain('title, year and TMDB, TVDB or IMDb IDs');
    expect(trakt).toContain('how far into it you are');
    expect(trakt).toContain('mark it watched');
    expect(trakt).toContain('the developer receives none of it');
  });
});
