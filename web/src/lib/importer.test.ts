import { describe, it, expect, vi, afterEach } from 'vitest';
import { importApi, type ImportJob } from './api';
import {
  initialMapping,
  mappingList,
  defaultOptions,
  anySelected,
  countsSummary,
  matchSummary,
  statusLabel,
  kindLabel
} from './importer';

function job(over: Partial<ImportJob> = {}): ImportJob {
  return {
    id: 'j', source_kind: 'plex', source_url: 'http://x', status: 'succeeded', dry_run: false,
    options: {}, mapping: [], phase: '', stats: {}, unmatched: [], created_at: '', updated_at: '', ...over
  };
}

describe('import helpers', () => {
  it('maps accounts to same-named users and drops the skipped ones', () => {
    const map = initialMapping([
      { id: '1', name: 'Alice', full_state: true, suggested_user_id: 'u-alice' },
      { id: '2', name: 'Guest', full_state: false }
    ]);
    expect(map).toEqual({ '1': 'u-alice', '2': '' });
    expect(mappingList(map)).toEqual([{ account_id: '1', user_id: 'u-alice' }]);
    expect(mappingList({ ...map, '2': 'u-bob' })).toHaveLength(2);
  });

  it('selects everything the source has (Plex has no favorites)', () => {
    expect(defaultOptions('plex').favorites).toBe(false);
    expect(defaultOptions('jellyfin')).toEqual({ watched: true, ratings: true, favorites: true, playlists: true, history: true });
    expect(anySelected({ watched: false, ratings: false, favorites: false, playlists: false, history: false })).toBe(false);
    expect(anySelected({ ...defaultOptions('emby'), watched: false })).toBe(true);
  });

  it('summarizes the non-zero counts', () => {
    expect(countsSummary(undefined)).toBe('');
    expect(countsSummary({ watched: 12, resume_points: 1, plays: 0, ratings: 3, favorites: 0, playlists: 1, playlist_items: 2 }))
      .toBe('12 watched · 1 resume point · 3 ratings · 1 playlist · 2 playlist items');
  });

  it('describes a job', () => {
    expect(matchSummary(job())).toBe('');
    expect(matchSummary(job({ stats: { referenced: 1000, matched: 940 } }))).toBe(`Matched 940 of ${(1000).toLocaleString()} items`);
    expect(statusLabel(job({ status: 'running', dry_run: true }))).toBe('Previewing');
    expect(statusLabel(job({ dry_run: true }))).toBe('Preview');
    expect(statusLabel(job())).toBe('Imported');
    expect(statusLabel(job({ status: 'failed' }))).toBe('Failed');
    expect(kindLabel('emby')).toBe('Emby');
  });
});

function mockFetch(body: unknown, status = 200) {
  return vi.fn().mockResolvedValue({ ok: status < 400, status, json: () => Promise.resolve(body) });
}

describe('importApi', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('connects and starts with the token in the body, never the URL', async () => {
    const fetch = mockFetch({ data: { kind: 'plex', server_name: 'Den', version: '1', accounts: [] } });
    vi.stubGlobal('fetch', fetch);
    await importApi.connect({ kind: 'plex', url: 'http://10.0.0.5:32400', token: 'secret' });
    expect(fetch).toHaveBeenCalledWith('/api/v1/admin/imports/connect', expect.objectContaining({ method: 'POST' }));
    expect(fetch.mock.calls[0][0]).not.toContain('secret');
    expect(JSON.parse(fetch.mock.calls[0][1].body)).toEqual({ kind: 'plex', url: 'http://10.0.0.5:32400', token: 'secret' });

    await importApi.start({
      kind: 'plex', url: 'http://10.0.0.5:32400', token: 'secret', dry_run: true,
      mapping: [{ account_id: '1', user_id: 'u' }], options: defaultOptions('plex')
    });
    expect(fetch.mock.calls[1][0]).toBe('/api/v1/admin/imports');
    expect(JSON.parse(fetch.mock.calls[1][1].body)).toMatchObject({ dry_run: true, mapping: [{ account_id: '1', user_id: 'u' }] });
  });

  it('lists, reads and cancels imports', async () => {
    const fetch = mockFetch({ data: [], meta: { total: 0 } });
    vi.stubGlobal('fetch', fetch);
    await importApi.list();
    await importApi.get('j-1');
    await importApi.cancel('j-1');
    expect(fetch.mock.calls.map((c) => [c[0], c[1].method])).toEqual([
      ['/api/v1/admin/imports', 'GET'],
      ['/api/v1/admin/imports/j-1', 'GET'],
      ['/api/v1/admin/imports/j-1/cancel', 'POST']
    ]);
  });
});
