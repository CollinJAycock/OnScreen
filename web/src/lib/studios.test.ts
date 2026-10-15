import { describe, it, expect } from 'vitest';
import { heroLogoPath, libraryStudioKind, studioHref, studioKindLabel } from './studios';

describe('studios helpers', () => {
  it('links a studio, optionally within one library', () => {
    expect(studioHref({ id: 's1' })).toBe('/studios/s1');
    expect(studioHref({ id: 's1' }, 'lib 1')).toBe('/studios/s1?library_id=lib%201');
  });

  it('labels studios and networks', () => {
    expect(studioKindLabel('studio')).toBe('Studio');
    expect(studioKindLabel('network', true)).toBe('Networks');
  });

  it('knows which libraries have studios or networks', () => {
    expect(libraryStudioKind('movie')).toBe('studio');
    expect(libraryStudioKind('show')).toBe('network');
    expect(libraryStudioKind('anime')).toBe('network');
    expect(libraryStudioKind('music')).toBeNull();
    expect(libraryStudioKind(undefined)).toBeNull();
  });

  it('shows a clear logo on movies and shows only', () => {
    expect(heroLogoPath({ type: 'movie', logo_path: 'Alien/clearlogo.png' })).toBe('Alien/clearlogo.png');
    expect(heroLogoPath({ type: 'show', logo_path: 'BB/logo.png' })).toBe('BB/logo.png');
    expect(heroLogoPath({ type: 'season', logo_path: 'BB/logo.png' })).toBeNull();
    expect(heroLogoPath({ type: 'movie' })).toBeNull();
    expect(heroLogoPath(null)).toBeNull();
  });
});
