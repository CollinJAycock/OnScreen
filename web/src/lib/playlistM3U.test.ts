import { describe, expect, it, vi } from 'vitest';
import {
  importSummary,
  isPlaylistFileName,
  playlistNameFromFile,
  unmatchedReason,
} from './playlistM3U';
import { opdsCatalogUrl, saveBlob } from './fileDownload';
import { filenameFromDisposition } from './api';

describe('playlist import helpers', () => {
  it('accepts .m3u and .m3u8 only', () => {
    expect(isPlaylistFileName('Road Trip.m3u8')).toBe(true);
    expect(isPlaylistFileName('old.M3U')).toBe(true);
    expect(isPlaylistFileName('list.txt')).toBe(false);
    expect(isPlaylistFileName('m3u8')).toBe(false);
  });

  it('names the playlist after the file', () => {
    expect(playlistNameFromFile('Road Trip.m3u8')).toBe('Road Trip');
    expect(playlistNameFromFile('x.M3U')).toBe('x');
  });

  it('summarises matches by method, skipping empty ones', () => {
    expect(importSummary({ total: 50, matched: 42, by_method: { path: 40, relative: 0, suffix: 1, tags: 1 } }))
      .toBe('Matched 42 of 50 entries. 40 by path, 1 by folder and file name, 1 by artist and title.');
    expect(importSummary({ total: 1, matched: 0, by_method: { path: 0, relative: 0, suffix: 0, tags: 0 } }))
      .toBe('Matched 0 of 1 entry.');
  });

  it('explains unmatched lines', () => {
    expect(unmatchedReason('url')).toMatch(/web address/);
    expect(unmatchedReason('not_found')).toMatch(/no match/);
  });
});

describe('file downloads', () => {
  it('reads the file name from Content-Disposition', () => {
    expect(filenameFromDisposition(`attachment; filename="Road trip.m3u8"; filename*=UTF-8''Road%20trip%20%E2%99%AB.m3u8`))
      .toBe('Road trip ♫.m3u8');
    expect(filenameFromDisposition('attachment; filename="data.zip"')).toBe('data.zip');
    expect(filenameFromDisposition(null)).toBeNull();
    expect(filenameFromDisposition('attachment')).toBeNull();
  });

  it('builds the OPDS address for the browser and the desktop app', () => {
    expect(opdsCatalogUrl('/api/v1', 'https://media.example.com')).toBe('https://media.example.com/opds/');
    expect(opdsCatalogUrl('https://nas.lan:7070/api/v1', 'tauri://localhost')).toBe('https://nas.lan:7070/opds/');
  });

  it('saves a blob through a temporary link', () => {
    const click = vi.fn();
    const a = { click, remove: vi.fn(), href: '', download: '' };
    const doc = {
      createElement: () => a,
      body: { appendChild: vi.fn() },
    } as unknown as Document;
    const create = vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:x');
    saveBlob(new Blob(['x']), 'export.zip', doc);
    expect(a.download).toBe('export.zip');
    expect(a.href).toBe('blob:x');
    expect(click).toHaveBeenCalled();
    create.mockRestore();
  });
});
