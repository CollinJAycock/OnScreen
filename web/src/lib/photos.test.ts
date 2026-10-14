import {
  extOf,
  faceCropStyle,
  isRawPath,
  loadMemorySelection,
  localISODate,
  originalFile,
  padBox,
  photoImageUrl,
  saveMemorySelection,
  serverImageUrl,
} from './photos';
import {
  memoryKey,
  memoryKeyDate,
  photoViewerHref,
  viewerCloseHref,
  viewerContextFromURL,
} from './photoViewerContext';
import type { ItemFile } from './api';

describe('extOf / isRawPath', () => {
  it('reads the extension case-insensitively', () => {
    expect(extOf('2023/IMG_0001.CR2')).toBe('cr2');
    expect(extOf('a.b/c')).toBe('');
    expect(isRawPath('x/DSCF0001.RAF')).toBe(true);
    expect(isRawPath('x/IMG_0001.JPG')).toBe(false);
    expect(isRawPath(undefined)).toBe(false);
  });
});

describe('photoImageUrl', () => {
  it('keeps browser-drawable originals on /artwork', () => {
    const u = photoImageUrl({ id: 'p1', poster_path: '2023/IMG 1.jpg', updated_at: 5 }, { w: 300 });
    expect(u).toBe('/artwork/2023/IMG%201.jpg?v=5&w=300');
  });

  it('sends RAW, HEIC and TIFF through the server render', () => {
    expect(photoImageUrl({ id: 'p1', poster_path: 'a/x.NEF' }, { w: 300 })).toBe('/api/v1/items/p1/image?w=300');
    expect(photoImageUrl({ id: 'p1', poster_path: 'a/x.heic', updated_at: 2 })).toBe('/api/v1/items/p1/image?w=4096&h=4096&v=2');
    expect(photoImageUrl({ id: 'p1' }, { w: 200, fit: 'cover' })).toBe('/api/v1/items/p1/image?w=200&fit=cover');
  });

  it('builds the server render URL', () => {
    expect(serverImageUrl('a b', { w: 800 })).toBe('/api/v1/items/a%20b/image?w=800');
  });
});

describe('originalFile', () => {
  const f = (id: string, container: string) => ({ id, container }) as ItemFile;
  it('prefers the RAW of a pair', () => {
    expect(originalFile([f('j', 'jpg'), f('r', 'cr3')])?.id).toBe('r');
    expect(originalFile([f('j', 'jpg')])?.id).toBe('j');
    expect(originalFile([])).toBeUndefined();
  });
});

describe('face crops', () => {
  it('pads and clamps the box', () => {
    expect(padBox({ x: 0, y: 0, w: 0.2, h: 0.2 }, 0.5)).toEqual({ x: 0, y: 0, w: 0.30000000000000004, h: 0.30000000000000004 });
    const b = padBox({ x: 0.9, y: 0.9, w: 0.1, h: 0.1 }, 0.5);
    expect(b.x + b.w).toBeCloseTo(1);
  });

  it('scales and positions the background', () => {
    const s = faceCropStyle('/img.jpg', { x: 0.25, y: 0.5, w: 0.5, h: 0.25 }, 0);
    expect(s).toContain('background-size:200.00% 400.00%');
    expect(s).toContain('background-position:50.00% 66.67%');
    expect(s).toContain('url("/img.jpg")');
  });
});

describe('memories', () => {
  it('formats the local date', () => {
    expect(localISODate(new Date(2026, 0, 5))).toBe('2026-01-05');
  });

  it('remembers a memory selection per tab', () => {
    saveMemorySelection('2026-10-14-0', ['a', 'b']);
    expect(loadMemorySelection('2026-10-14-0')).toEqual(['a', 'b']);
    expect(loadMemorySelection('2026-10-14-1')).toBeNull();
  });

  it('round-trips the viewer context', () => {
    const key = memoryKey('2026-10-14', 2);
    expect(memoryKeyDate(key)).toBe('2026-10-14');
    const href = photoViewerHref('p1', { kind: 'memory', key });
    expect(href).toBe('/photos/p1?memory=2026-10-14-2');
    expect(viewerContextFromURL(new URL(`http://x${href}`))).toEqual({ kind: 'memory', key });
    expect(viewerCloseHref({ kind: 'memory', key })).toBe('/photos/memories?date=2026-10-14');
    expect(viewerContextFromURL(new URL('http://x/photos/p1?memory=../x'))).toEqual({ kind: 'library' });
  });

  it('carries a person context', () => {
    const href = photoViewerHref('p1', { kind: 'person', personId: 'per-1' });
    expect(viewerContextFromURL(new URL(`http://x${href}`))).toEqual({ kind: 'person', personId: 'per-1' });
    expect(viewerCloseHref({ kind: 'person', personId: 'per-1' })).toBe('/photos/people/per-1');
  });
});
