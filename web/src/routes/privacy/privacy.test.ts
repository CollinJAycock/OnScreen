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

  // clients/webos: appinfo.json's requiredACG (systemconfig.query, for
  // lib/platform's getConfigs probe), lib/device's model name from
  // deviceInfo, and the web view's storage and cache.
  it('has a section on what the LG webOS TV app uses on the TV', () => {
    const at = text.indexOf('What the LG webOS TV app uses on the TV');
    expect(at).toBeGreaterThan(-1);
    const lg = text.slice(at, text.indexOf('Third-party services', at));
    expect(lg).toContain('systemconfig.query');
    expect(lg).toContain('whether the screen shows HDR and whether it is 4K');
    expect(lg).toContain('model name');
    expect(lg).toContain('Magic Remote');
    expect(lg).toContain('web view');
    expect(lg).toContain('deleted when you uninstall the app');
    expect(lg).toContain('does not use the camera, microphone, location');
  });

  it("doesn't describe the image cache in Android terms only", () => {
    const cache = text.slice(text.indexOf('Image and trickplay cache.'));
    expect(cache.slice(0, 400)).toContain('on LG webOS TV, the web view');
  });
});
