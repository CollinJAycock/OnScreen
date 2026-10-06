import { afterEach, describe, expect, it, vi } from 'vitest';

vi.mock('./client', async (orig) => {
  const real = await orig<typeof import('./client')>();
  return { ...real, api: { ...real.api, getOrigin: () => 'https://media.example.com' } };
});

import { pair } from './endpoints';

describe('pair.poll', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('polls with GET, the method the server routes (a POST is 405 forever)', async () => {
    const fetchMock = vi.fn(async () => new Response('{"status":"pending"}', { status: 202 }));
    vi.stubGlobal('fetch', fetchMock);
    expect(await pair.poll('dev-token')).toEqual({ status: 'pending' });
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe('https://media.example.com/api/v1/auth/pair/poll');
    expect(init.method).toBe('GET');
    expect((init.headers as Record<string, string>).Authorization).toBe('Bearer dev-token');
    expect(init.body).toBeUndefined();
    expect(init.redirect).toBe('manual');
  });

  it('reads the token pair on 200 and reports an expired code on 410', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('{"data":{"access_token":"a","refresh_token":"r"}}', { status: 200 })));
    expect(await pair.poll('d')).toEqual({ status: 'done', pair: { access_token: 'a', refresh_token: 'r' } });
    vi.stubGlobal('fetch', vi.fn(async () => new Response('', { status: 410 })));
    expect(await pair.poll('d')).toEqual({ status: 'expired' });
  });
});
