import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { ApiClient, authApi, libraryApi, userApi, notificationApi, subtitleApi, api } from './api';

// ── helpers ───────────────────────────────────────────────────────────────────

function mockFetch(status: number, body: unknown) {
  return vi.fn().mockResolvedValue({
    ok: status >= 200 && status < 300,
    status,
    json: () => Promise.resolve(body)
  });
}

// ── ApiClient ─────────────────────────────────────────────────────────────────

describe('ApiClient', () => {
  let client: ApiClient;

  beforeEach(() => {
    client = new ApiClient();
    localStorage.clear();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  describe('setUser / getUser', () => {
    it('stores user metadata in localStorage', () => {
      client.setUser({ user_id: '1', username: 'admin', is_admin: true });
      const raw = localStorage.getItem('onscreen_user');
      expect(raw).not.toBeNull();
      expect(JSON.parse(raw!)).toEqual({ user_id: '1', username: 'admin', is_admin: true });
    });

    it('removes user metadata from localStorage when null', () => {
      client.setUser({ user_id: '1', username: 'admin', is_admin: true });
      client.setUser(null);
      expect(localStorage.getItem('onscreen_user')).toBeNull();
    });

    it('getUser reads from localStorage', () => {
      client.setUser({ user_id: '2', username: 'bob', is_admin: false });
      const user = client.getUser();
      expect(user).toEqual({ user_id: '2', username: 'bob', is_admin: false });
    });

    it('getUser returns null when nothing stored', () => {
      expect(client.getUser()).toBeNull();
    });
  });

  describe('request', () => {
    it('sends GET with correct URL', async () => {
      const fetch = mockFetch(200, { data: { id: 1 } });
      vi.stubGlobal('fetch', fetch);
      await client.get('/libraries');
      expect(fetch).toHaveBeenCalledWith('/api/v1/libraries', expect.objectContaining({ method: 'GET' }));
    });

    it('returns data field on success', async () => {
      vi.stubGlobal('fetch', mockFetch(200, { data: { name: 'Movies' } }));
      const result = await client.get<{ name: string }>('/libraries/1');
      expect(result.name).toBe('Movies');
    });

    it('does not send Authorization header (cookies handle auth)', async () => {
      const fetch = mockFetch(200, { data: null });
      vi.stubGlobal('fetch', fetch);
      await client.get('/test');
      const [, opts] = fetch.mock.calls[0] as [string, RequestInit & { headers: Record<string, string> }];
      expect(opts.headers['Authorization']).toBeUndefined();
    });

    it('includes credentials: same-origin', async () => {
      const fetch = mockFetch(200, { data: null });
      vi.stubGlobal('fetch', fetch);
      await client.get('/test');
      const [, opts] = fetch.mock.calls[0] as [string, RequestInit];
      expect(opts.credentials).toBe('same-origin');
    });

    it('returns undefined for 204 No Content', async () => {
      vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
        ok: true, status: 204, json: () => Promise.resolve(null)
      }));
      const result = await client.del('/libraries/1');
      expect(result).toBeUndefined();
    });

    it('throws with error message on non-ok response', async () => {
      vi.stubGlobal('fetch', mockFetch(403, {
        error: { code: 'FORBIDDEN', message: 'Access denied', request_id: 'req1' }
      }));
      await expect(client.get('/protected')).rejects.toThrow('Access denied');
    });

    it('falls back to HTTP status when error.message missing', async () => {
      vi.stubGlobal('fetch', mockFetch(500, {}));
      await expect(client.get('/broken')).rejects.toThrow('HTTP 500');
    });

    it('sends POST body as JSON', async () => {
      const fetch = mockFetch(200, { data: { id: '1' } });
      vi.stubGlobal('fetch', fetch);
      await client.post('/auth/login', { username: 'a', password: 'b' });
      const [, opts] = fetch.mock.calls[0] as [string, RequestInit];
      expect(opts.body).toBe(JSON.stringify({ username: 'a', password: 'b' }));
    });

    it('sends PATCH with correct method', async () => {
      const fetch = mockFetch(200, { data: { name: 'Updated' } });
      vi.stubGlobal('fetch', fetch);
      await client.patch('/libraries/1', { name: 'Updated' });
      const [, opts] = fetch.mock.calls[0] as [string, RequestInit];
      expect(opts.method).toBe('PATCH');
    });

    it('sends DELETE without body', async () => {
      vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
        ok: true, status: 204, json: () => Promise.resolve(null)
      }));
      await client.del('/libraries/1');
    });
  });
});

// ── authApi ───────────────────────────────────────────────────────────────────

describe('authApi', () => {
  afterEach(() => vi.restoreAllMocks());

  it('setupStatus calls GET /setup/status', async () => {
    const fetch = mockFetch(200, { data: { setup_required: true } });
    vi.stubGlobal('fetch', fetch);
    const result = await authApi.setupStatus();
    expect(result.setup_required).toBe(true);
    expect(fetch.mock.calls[0][0]).toBe('/api/v1/setup/status');
  });

  it('login calls POST /auth/login', async () => {
    const pair = {
      access_token: 'tok', refresh_token: 'ref',
      expires_at: '', user_id: '1', username: 'admin', is_admin: true
    };
    vi.stubGlobal('fetch', mockFetch(200, { data: pair }));
    const result = await authApi.login('admin', 'pass');
    expect(result.access_token).toBe('tok');
  });

  it('register calls POST /auth/register', async () => {
    vi.stubGlobal('fetch', mockFetch(200, { data: { id: '1', username: 'admin' } }));
    const result = await authApi.register('admin', 'pass');
    expect(result.username).toBe('admin');
  });

  it('logout calls POST /auth/logout with no body', async () => {
    const fetch = vi.fn().mockResolvedValue({
      ok: true, status: 204, json: () => Promise.resolve(null)
    });
    vi.stubGlobal('fetch', fetch);
    await authApi.logout();
    expect(fetch.mock.calls[0][0]).toBe('/api/v1/auth/logout');
    const [, opts] = fetch.mock.calls[0] as [string, RequestInit];
    expect(opts.body).toBeUndefined();
  });

});

// ── libraryApi ────────────────────────────────────────────────────────────────

describe('libraryApi', () => {
  afterEach(() => vi.restoreAllMocks());

  it('list calls GET /libraries', async () => {
    const libs = [{ id: '1', name: 'Movies' }];
    vi.stubGlobal('fetch', mockFetch(200, { data: libs }));
    const result = await libraryApi.list();
    expect(result).toHaveLength(1);
    expect(result[0].name).toBe('Movies');
  });

  it('get calls GET /libraries/:id', async () => {
    vi.stubGlobal('fetch', mockFetch(200, { data: { id: '1', name: 'Movies' } }));
    const result = await libraryApi.get('1');
    expect(result.id).toBe('1');
  });

  it('create calls POST /libraries with body', async () => {
    const fetch = mockFetch(200, { data: { id: '2', name: 'Shows' } });
    vi.stubGlobal('fetch', fetch);
    await libraryApi.create({ name: 'Shows', type: 'show' });
    expect(fetch.mock.calls[0][0]).toBe('/api/v1/libraries');
    const body = JSON.parse((fetch.mock.calls[0] as [string, RequestInit])[1].body as string);
    expect(body).toMatchObject({ name: 'Shows' });
  });

  it('update calls PATCH /libraries/:id', async () => {
    const fetch = mockFetch(200, { data: { id: '1', name: 'Updated' } });
    vi.stubGlobal('fetch', fetch);
    await libraryApi.update('1', { name: 'Updated' });
    expect(fetch.mock.calls[0][0]).toBe('/api/v1/libraries/1');
    expect((fetch.mock.calls[0] as [string, RequestInit])[1].method).toBe('PATCH');
  });

  it('del calls DELETE /libraries/:id', async () => {
    const fetch = vi.fn().mockResolvedValue({
      ok: true, status: 204, json: () => Promise.resolve(null)
    });
    vi.stubGlobal('fetch', fetch);
    await libraryApi.del('1');
    expect(fetch.mock.calls[0][0]).toBe('/api/v1/libraries/1');
    expect((fetch.mock.calls[0] as [string, RequestInit])[1].method).toBe('DELETE');
  });

  it('scan calls POST /libraries/:id/scan', async () => {
    const fetch = vi.fn().mockResolvedValue({
      ok: true, status: 204, json: () => Promise.resolve(null)
    });
    vi.stubGlobal('fetch', fetch);
    await libraryApi.scan('42');
    expect(fetch.mock.calls[0][0]).toBe('/api/v1/libraries/42/scan');
  });
});

// ── userApi ──────────────────────────────────────────────────────────────────

describe('userApi', () => {
  afterEach(() => vi.restoreAllMocks());

  it('getPreferences calls GET /users/me/preferences', async () => {
    const prefs = { preferred_audio_lang: 'en', preferred_subtitle_lang: null, max_content_rating: 'PG-13' };
    const fetch = mockFetch(200, { data: prefs });
    vi.stubGlobal('fetch', fetch);
    const result = await userApi.getPreferences();
    expect(fetch.mock.calls[0][0]).toBe('/api/v1/users/me/preferences');
    expect(result.preferred_audio_lang).toBe('en');
    expect(result.max_content_rating).toBe('PG-13');
  });

  it('setPreferences calls PUT /users/me/preferences', async () => {
    const fetch = vi.fn().mockResolvedValue({
      ok: true, status: 204, json: () => Promise.resolve(null)
    });
    vi.stubGlobal('fetch', fetch);
    await userApi.setPreferences({
      preferred_audio_lang: 'ja',
      preferred_subtitle_lang: 'en',
      max_content_rating: null
    });
    expect(fetch.mock.calls[0][0]).toBe('/api/v1/users/me/preferences');
    const [, opts] = fetch.mock.calls[0] as [string, RequestInit];
    expect(opts.method).toBe('PUT');
    const body = JSON.parse(opts.body as string);
    expect(body.preferred_audio_lang).toBe('ja');
    expect(body.preferred_subtitle_lang).toBe('en');
  });

  it('setContentRating calls PUT /users/:id/content-rating', async () => {
    const fetch = vi.fn().mockResolvedValue({
      ok: true, status: 204, json: () => Promise.resolve(null)
    });
    vi.stubGlobal('fetch', fetch);
    await userApi.setContentRating('user-123', 'PG-13');
    expect(fetch.mock.calls[0][0]).toBe('/api/v1/users/user-123/content-rating');
    const body = JSON.parse((fetch.mock.calls[0] as [string, RequestInit])[1].body as string);
    expect(body.max_content_rating).toBe('PG-13');
  });

  it('setContentRating sends null to clear restriction', async () => {
    const fetch = vi.fn().mockResolvedValue({
      ok: true, status: 204, json: () => Promise.resolve(null)
    });
    vi.stubGlobal('fetch', fetch);
    await userApi.setContentRating('user-123', null);
    const body = JSON.parse((fetch.mock.calls[0] as [string, RequestInit])[1].body as string);
    expect(body.max_content_rating).toBeNull();
  });

  it('listSwitchable calls GET /users/switchable', async () => {
    const users = [{ id: '1', username: 'alice', is_admin: false, has_pin: true }];
    vi.stubGlobal('fetch', mockFetch(200, { data: users }));
    const result = await userApi.listSwitchable();
    expect(result).toHaveLength(1);
    expect(result[0].username).toBe('alice');
  });
});

// ── notificationApi ─────────────────────────────────────────────────────────

describe('notificationApi', () => {
  afterEach(() => vi.restoreAllMocks());

  it('list calls GET /notifications with pagination', async () => {
    const notifs = [{ id: '1', type: 'system', title: 'Test', body: '', read: false, created_at: 1000 }];
    const fetch = mockFetch(200, { data: notifs });
    vi.stubGlobal('fetch', fetch);
    const result = await notificationApi.list(10, 5);
    expect(fetch.mock.calls[0][0]).toBe('/api/v1/notifications?limit=10&offset=5');
    expect(result).toHaveLength(1);
    expect(result[0].title).toBe('Test');
  });

  it('list uses default pagination', async () => {
    const fetch = mockFetch(200, { data: [] });
    vi.stubGlobal('fetch', fetch);
    await notificationApi.list();
    expect(fetch.mock.calls[0][0]).toBe('/api/v1/notifications?limit=20&offset=0');
  });

  it('unreadCount calls GET /notifications/unread-count', async () => {
    vi.stubGlobal('fetch', mockFetch(200, { data: { count: 3 } }));
    const result = await notificationApi.unreadCount();
    expect(result.count).toBe(3);
  });

  it('markRead calls POST /notifications/:id/read', async () => {
    const fetch = vi.fn().mockResolvedValue({
      ok: true, status: 204, json: () => Promise.resolve(null)
    });
    vi.stubGlobal('fetch', fetch);
    await notificationApi.markRead('notif-abc');
    expect(fetch.mock.calls[0][0]).toBe('/api/v1/notifications/notif-abc/read');
    expect((fetch.mock.calls[0] as [string, RequestInit])[1].method).toBe('POST');
  });

  it('markAllRead calls POST /notifications/read-all', async () => {
    const fetch = vi.fn().mockResolvedValue({
      ok: true, status: 204, json: () => Promise.resolve(null)
    });
    vi.stubGlobal('fetch', fetch);
    await notificationApi.markAllRead();
    expect(fetch.mock.calls[0][0]).toBe('/api/v1/notifications/read-all');
    expect((fetch.mock.calls[0] as [string, RequestInit])[1].method).toBe('POST');
  });
});

// ── subtitleApi.ocr (job-queued, v2.1+) ───────────────────────────────────────
//
// v2.0 was synchronous (POST returned 201 with the row inline) but 524'd
// behind reverse proxies with sub-multi-minute response timeouts on
// feature-length PGS tracks. v2.1 returns 202 + a job descriptor, and
// clients poll the GET endpoint until status reaches a terminal state.

describe('subtitleApi.ocr', () => {
  afterEach(() => vi.restoreAllMocks());

  it('POSTs to /items/:id/subtitles/ocr with the job body', async () => {
    const fetch = vi.fn().mockResolvedValue({
      ok: true,
      status: 202,
      json: () =>
        Promise.resolve({
          data: { job_id: 'job-1', status: 'running', file_id: 'f', stream_index: 3, started_at: '2026-04-27T13:00:00Z' },
        }),
    });
    vi.stubGlobal('fetch', fetch);

    const job = await subtitleApi.ocr('item-1', { file_id: 'f', stream_index: 3, language: 'eng' });

    expect(fetch.mock.calls[0][0]).toBe('/api/v1/items/item-1/subtitles/ocr');
    const init = fetch.mock.calls[0][1] as RequestInit;
    expect(init.method).toBe('POST');
    expect(JSON.parse(init.body as string)).toEqual({ file_id: 'f', stream_index: 3, language: 'eng' });
    expect(job.job_id).toBe('job-1');
    expect(job.status).toBe('running');
  });
});

describe('subtitleApi.ocrStatus', () => {
  afterEach(() => vi.restoreAllMocks());

  it('GETs to /items/:id/subtitles/ocr/:jobId and returns the job', async () => {
    const fetch = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: () =>
        Promise.resolve({
          data: {
            job_id: 'job-1',
            status: 'completed',
            file_id: 'f',
            stream_index: 3,
            started_at: '2026-04-27T13:00:00Z',
            completed_at: '2026-04-27T13:02:00Z',
            subtitle: { id: 'sub-1', file_id: 'f', language: 'eng', source: 'ocr', source_id: 'stream_3', forced: false, sdh: false, url: '/u' },
          },
        }),
    });
    vi.stubGlobal('fetch', fetch);

    const status = await subtitleApi.ocrStatus('item-1', 'job-1');
    expect(fetch.mock.calls[0][0]).toBe('/api/v1/items/item-1/subtitles/ocr/job-1');
    expect((fetch.mock.calls[0] as [string, RequestInit])[1].method).toBe('GET');
    expect(status.status).toBe('completed');
    expect(status.subtitle?.id).toBe('sub-1');
  });

  it('surfaces the error string when the job failed', async () => {
    const fetch = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: () =>
        Promise.resolve({
          data: {
            job_id: 'job-2',
            status: 'failed',
            file_id: 'f',
            stream_index: 3,
            started_at: '2026-04-27T13:00:00Z',
            completed_at: '2026-04-27T13:00:01Z',
            error: 'ocr engine not configured',
          },
        }),
    });
    vi.stubGlobal('fetch', fetch);

    const status = await subtitleApi.ocrStatus('item-1', 'job-2');
    expect(status.status).toBe('failed');
    expect(status.error).toBe('ocr engine not configured');
  });
});

// ── api singleton ─────────────────────────────────────────────────────────────

describe('api singleton', () => {
  it('is an ApiClient instance', () => {
    expect(api).toBeInstanceOf(ApiClient);
  });
});
