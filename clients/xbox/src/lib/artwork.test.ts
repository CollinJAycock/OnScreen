import { describe, expect, it } from 'vitest';
import { logoArtworkPath, showsLogo } from './artwork';

describe('logoArtworkPath', () => {
  it('asks for the logo as stored, never resized (a resize re-encodes the PNG as JPEG)', () => {
    expect(logoArtworkPath('abc/logo.png')).toBe('/artwork/abc/logo.png');
    expect(logoArtworkPath('abc/logo.png')).not.toMatch(/[?&][wh]=/);
  });

  it('is empty without a logo', () => {
    expect(logoArtworkPath(undefined)).toBe('');
    expect(logoArtworkPath(null)).toBe('');
    expect(logoArtworkPath('  ')).toBe('');
  });
});

describe('showsLogo', () => {
  it('shows the logo for movies and shows only', () => {
    expect(showsLogo('movie')).toBe(true);
    expect(showsLogo('show')).toBe(true);
    expect(showsLogo('episode')).toBe(false);
    expect(showsLogo('artist')).toBe(false);
    expect(showsLogo(undefined)).toBe(false);
  });
});
