import { describe, it, expect, vi } from 'vitest';
import { connectToServer, cleartextWarning, errorText, type ServerCandidate } from './serverConnect';

// The Rust side (resolve_server_input / probe_server_url) is faked here with
// the candidates its own unit tests pin down (clients/desktop/src-tauri
// lib.rs server_url_tests); this suite covers what the webview does with
// them: probe order, the https upgrade, and which message the user gets.

const CAPS = { data: { server: { name: 'OnScreen', version: '2.5.0', api_version: 'v1' } } };
const ORIGIN = 'http://tauri.localhost';

function okResponse(url: string, body: unknown = CAPS, status = 200): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    url,
    json: async () => {
      if (typeof body === 'string') throw new SyntaxError('Unexpected token <');
      return body;
    },
  } as unknown as Response;
}

type Route = (url: string) => Response | Error;

/** fetch fake: routes by URL; an Error result rejects like a network failure. */
function fakeFetch(route: Route) {
  return vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    expect(init?.credentials).toBe('omit');
    const r = route(url);
    if (r instanceof Error) throw r;
    return r;
  });
}

function fakeInvoke(
  candidates: ServerCandidate[] | string,
  probe: (url: string) => { url: string; name: string; version: string } | string = (u) =>
    `${u}: the connection was refused — is the server running, and is the port right?`,
) {
  return vi.fn(async (cmd: string, args?: Record<string, unknown>) => {
    if (cmd === 'resolve_server_input') {
      if (typeof candidates === 'string') throw candidates; // Rust Err(String)
      return candidates;
    }
    if (cmd === 'probe_server_url') {
      const r = probe(String(args?.url));
      if (typeof r === 'string') throw r;
      return r;
    }
    throw new Error(`unexpected command ${cmd}`);
  }) as unknown as (<T>(cmd: string, args?: Record<string, unknown>) => Promise<T>) & ReturnType<typeof vi.fn>;
}

const bareLan: ServerCandidate[] = [
  { url: 'https://10.0.0.66:7070', cleartext: false, local: true },
  { url: 'http://10.0.0.66:7070', cleartext: true, local: true },
];
const barePublic: ServerCandidate[] = [
  { url: 'https://onscreen.example.com', cleartext: false, local: false },
];

const failed = () => new TypeError('Failed to fetch');

describe('connectToServer', () => {
  it('a bare LAN address falls back from https:// to http:// and flags it unencrypted', async () => {
    const fetch = fakeFetch((u) => (u.startsWith('https://') ? failed() : okResponse(u)));
    const invoke = fakeInvoke(bareLan);
    const status = vi.fn();

    const r = await connectToServer('10.0.0.66:7070', { invoke, fetch, origin: ORIGIN, onStatus: status });

    expect(r).toEqual({ url: 'http://10.0.0.66:7070', cleartext: true });
    expect(fetch.mock.calls.map((c) => String(c[0]))).toEqual([
      'https://10.0.0.66:7070/api/v1/system/capabilities',
      'http://10.0.0.66:7070/api/v1/system/capabilities',
    ]);
    expect(invoke).toHaveBeenCalledWith('resolve_server_input', { input: '10.0.0.66:7070' });
    expect(invoke).not.toHaveBeenCalledWith('probe_server_url', expect.anything());
    expect(status).toHaveBeenCalledWith('Trying https://10.0.0.66:7070…');
  });

  it('prefers https:// when it answers and never tries http://', async () => {
    const fetch = fakeFetch((u) => okResponse(u));
    const r = await connectToServer('10.0.0.66:7070', { invoke: fakeInvoke(bareLan), fetch, origin: ORIGIN });

    expect(r).toEqual({ url: 'https://10.0.0.66:7070', cleartext: false });
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it("shows the shell's reason when the input is refused (e.g. cleartext to a public host)", async () => {
    const msg =
      "OnScreen only connects over plain http:// to servers on your local network, and onscreen.example.com isn't one";
    const fetch = fakeFetch(() => okResponse('x'));
    await expect(
      connectToServer('http://onscreen.example.com', { invoke: fakeInvoke(msg), fetch, origin: ORIGIN }),
    ).rejects.toThrow(msg);
    expect(fetch).not.toHaveBeenCalled();
  });

  it('a server that answers Rust but not the webview is a CORS problem, and says how to fix it', async () => {
    const fetch = fakeFetch(() => failed());
    const invoke = fakeInvoke(barePublic, (u) => ({ url: u, name: 'OnScreen', version: '2.4.1' }));

    const err = await connectToServer('onscreen.example.com', { invoke, fetch, origin: ORIGIN }).catch((e) => e);

    expect(err).toBeInstanceOf(Error);
    expect(err.message).toContain('https://onscreen.example.com is running');
    expect(err.message).toContain('CORS');
    expect(err.message).toContain(ORIGIN);
    expect(err.message).toContain('2.5.0');
  });

  it("an unreachable server gets Rust's reason for each address tried", async () => {
    const fetch = fakeFetch(() => failed());
    const invoke = fakeInvoke(bareLan, (u) =>
      u.startsWith('https://')
        ? `${u}: a secure (https://) connection couldn't be set up (corrupt message).`
        : `${u}: the connection was refused — is the server running, and is the port right?`,
    );

    const err = await connectToServer('10.0.0.66:7070', { invoke, fetch, origin: ORIGIN }).catch((e) => e);

    expect(err.message).toMatch(/^Couldn't connect to the server\./);
    expect(err.message).toContain("https://10.0.0.66:7070: a secure (https://) connection couldn't be set up");
    expect(err.message).toContain('http://10.0.0.66:7070: the connection was refused');
    expect(err.message).not.toContain('only tried for servers on your local network');
  });

  it('a bare public host that fails explains why http:// was not tried', async () => {
    const fetch = fakeFetch(() => failed());
    const err = await connectToServer('onscreen.example.com', {
      invoke: fakeInvoke(barePublic),
      fetch,
      origin: ORIGIN,
    }).catch((e) => e);

    expect(err.message).toContain('only tried for servers on your local network');
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it('something that answers but is not OnScreen is reported as such, without the CORS advice', async () => {
    const invoke = fakeInvoke([{ url: 'http://192.168.1.1', cleartext: true, local: true }]);
    const html = fakeFetch((u) => okResponse(u, '<html>router login</html>'));
    await expect(connectToServer('http://192.168.1.1', { invoke, fetch: html, origin: ORIGIN })).rejects.toThrow(
      /not like an OnScreen server/,
    );

    const notFound = fakeFetch((u) => okResponse(u, { error: 'nope' }, 404));
    await expect(
      connectToServer('http://192.168.1.1', { invoke, fetch: notFound, origin: ORIGIN }),
    ).rejects.toThrow(/answered with HTTP 404/);
    expect(invoke).not.toHaveBeenCalledWith('probe_server_url', expect.anything());
  });

  it('adopts a same-host redirect to https:// (keeping a path prefix), never another host', async () => {
    const invoke = fakeInvoke([{ url: 'http://nas.local/onscreen', cleartext: true, local: true }]);
    const upgraded = fakeFetch(() => okResponse('https://nas.local/onscreen/api/v1/system/capabilities'));
    expect(await connectToServer('http://nas.local/onscreen', { invoke, fetch: upgraded, origin: ORIGIN })).toEqual({
      url: 'https://nas.local/onscreen',
      cleartext: false,
    });

    const elsewhere = fakeFetch(() => okResponse('https://evil.example/api/v1/system/capabilities'));
    expect(await connectToServer('http://nas.local/onscreen', { invoke, fetch: elsewhere, origin: ORIGIN })).toEqual({
      url: 'http://nas.local/onscreen',
      cleartext: true,
    });
  });

  it('retries the https:// URL Rust was redirected to before blaming CORS', async () => {
    const invoke = fakeInvoke([{ url: 'http://nas.local', cleartext: true, local: true }], () => ({
      url: 'https://nas.local',
      name: 'OnScreen',
      version: '2.5.0',
    }));
    // The redirect itself carries no CORS headers (a proxy), so the http://
    // probe fails in the webview; the https:// URL works directly.
    const fetch = fakeFetch((u) => (u.startsWith('http://') ? failed() : okResponse(u)));

    expect(await connectToServer('http://nas.local', { invoke, fetch, origin: ORIGIN })).toEqual({
      url: 'https://nas.local',
      cleartext: false,
    });
  });

  it('gives up on a silent https:// attempt after its timeout and moves on to http://', async () => {
    const fetch = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.startsWith('http://')) return Promise.resolve(okResponse(url));
      return new Promise<Response>((_, reject) => {
        init?.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')));
      });
    });

    const r = await connectToServer('10.0.0.66:7070', {
      invoke: fakeInvoke(bareLan),
      fetch,
      origin: ORIGIN,
      timeouts: { first: 5, other: 1000 },
    });
    expect(r.url).toBe('http://10.0.0.66:7070');
  });
});

describe('cleartextWarning', () => {
  it('warns for http:// across the network only', () => {
    expect(cleartextWarning('http://10.0.0.66:7070')).toMatch(/^Not encrypted/);
    expect(cleartextWarning('HTTP://nas.local')).toMatch(/^Not encrypted/);
    expect(cleartextWarning('https://onscreen.example.com')).toBe('');
    expect(cleartextWarning(null)).toBe('');
    // Loopback never leaves this machine.
    expect(cleartextWarning('http://localhost:7070')).toBe('');
    expect(cleartextWarning('http://127.0.0.1:7070')).toBe('');
    expect(cleartextWarning('http://[::1]:7070')).toBe('');
  });
});

describe('errorText', () => {
  it('reads a Rust Err(String) rejection as well as an Error', () => {
    expect(errorText('plain string')).toBe('plain string');
    expect(errorText(new Error('boom'))).toBe('boom');
  });
});
