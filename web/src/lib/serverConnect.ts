/**
 * Desktop (Tauri) server connection: turns what the user typed into a
 * server URL the app can actually use, or a message that says why not.
 *
 * The rules live in Rust (`resolve_server_input` / `set_server_url` in
 * clients/desktop/src-tauri/src/lib.rs): a bare host or IP is tried over
 * https:// first, then http:// — and plain http:// is allowed for
 * local-network hosts only. This module probes each candidate from the
 * webview, because that is the request path every later API call takes
 * (the webview's certificate store, the server's CORS policy). When the
 * webview can't reach any candidate, the Rust-side probe (not subject to
 * CORS) tells "server down / wrong address" apart from "server up but it
 * doesn't allow the desktop app's origin", which a webview fetch reports
 * identically as "Failed to fetch".
 */

export type ServerCandidate = { url: string; cleartext: boolean; local: boolean };
export type ServerProbe = { url: string; name: string; version: string };
export type ConnectResult = { url: string; cleartext: boolean };

type Invoke = <T>(cmd: string, args?: Record<string, unknown>) => Promise<T>;

export interface ConnectDeps {
  invoke?: Invoke;
  fetch?: typeof fetch;
  /** The webview's own origin, named in the CORS advice. */
  origin?: string;
  /** Called with a short progress line ("Trying https://…"). */
  onStatus?: (status: string) => void;
  /** Per-attempt timeout; the https:// try of a bare host gets the first. */
  timeouts?: { first: number; other: number };
}

const CAPS_PATH = '/api/v1/system/capabilities';
const DEFAULT_TIMEOUTS = { first: 8000, other: 10000 };

/** The webview got no readable reply (down, refused, TLS, CORS, timeout). */
class NoReply extends Error {}
/** The webview got a readable reply that isn't a healthy OnScreen server. */
class BadReply extends Error {}

/** The origin of a server URL, or null when there is none to compare. */
export function originOf(u: string | null | undefined): string | null {
  if (!u) return null;
  try { return new URL(u.trim()).origin; } catch { return null; }
}

/** A Tauri command rejection is the Rust `Err(String)` itself, not an Error. */
export function errorText(e: unknown): string {
  if (typeof e === 'string') return e;
  if (e instanceof Error) return e.message;
  return String(e);
}

function isOnScreenCapabilities(body: unknown): boolean {
  const server = (body as { data?: { server?: unknown } } | null)?.data?.server;
  return typeof server === 'object' && server !== null;
}

/**
 * When probing an http:// URL ended on https:// on the SAME host (a
 * reverse proxy forcing TLS), use the https:// URL: fetch follows the
 * redirect for this GET transparently, but every later POST (sign-in)
 * would be turned into a GET by it. Never a downgrade, never another host.
 */
function adoptHttpsUpgrade(base: string, finalUrl: string): string {
  try {
    const asked = new URL(base);
    const answered = new URL(finalUrl);
    if (
      asked.protocol === 'http:' &&
      answered.protocol === 'https:' &&
      asked.hostname === answered.hostname &&
      answered.pathname.endsWith(CAPS_PATH)
    ) {
      const prefix = answered.pathname.slice(0, -CAPS_PATH.length).replace(/\/+$/, '');
      return `${answered.origin}${prefix}`;
    }
  } catch {
    // keep what was asked for
  }
  return base;
}

async function webviewProbe(base: string, fetchFn: typeof fetch, timeoutMs: number): Promise<string> {
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), timeoutMs);
  try {
    let resp: Response;
    try {
      resp = await fetchFn(base + CAPS_PATH, {
        signal: ctrl.signal,
        credentials: 'omit',
        cache: 'no-store',
      });
    } catch (e) {
      throw new NoReply(ctrl.signal.aborted ? 'timed out' : errorText(e));
    }
    if (!resp.ok) {
      throw new BadReply(
        `${base} answered with HTTP ${resp.status} — is this an OnScreen server's address?`,
      );
    }
    let body: unknown = null;
    try {
      body = await resp.json();
    } catch {
      // The timeout also covers the body: an abort here is no answer, not
      // a wrong one.
      if (ctrl.signal.aborted) throw new NoReply('timed out');
      // not JSON: falls through to the shape check
    }
    if (!isOnScreenCapabilities(body)) {
      throw new BadReply(
        `${base} answered, but not like an OnScreen server — check the address and port.`,
      );
    }
    return adoptHttpsUpgrade(base, resp.url || base + CAPS_PATH);
  } finally {
    clearTimeout(timer);
  }
}

async function defaultInvoke(): Promise<Invoke> {
  const { invoke } = await import('@tauri-apps/api/core');
  return invoke as Invoke;
}

/**
 * Resolves `input` ("10.0.0.66:7070", "https://media.example.com", …) to
 * the server URL to save. Throws an Error whose message is ready to show.
 * Persists nothing — the caller saves the result with `setServerUrl`.
 */
export async function connectToServer(input: string, deps: ConnectDeps = {}): Promise<ConnectResult> {
  const invoke = deps.invoke ?? (await defaultInvoke());
  const fetchFn = deps.fetch ?? ((...args: Parameters<typeof fetch>) => fetch(...args));
  const origin = deps.origin ?? (typeof window !== 'undefined' ? window.location.origin : '');
  const timeouts = deps.timeouts ?? DEFAULT_TIMEOUTS;
  const status = deps.onStatus ?? (() => {});

  let candidates: ServerCandidate[];
  try {
    candidates = await invoke<ServerCandidate[]>('resolve_server_input', { input });
  } catch (e) {
    throw new Error(errorText(e));
  }
  if (!candidates.length) throw new Error("Enter your OnScreen server's address.");

  const unreached: { c: ServerCandidate; timedOut: boolean }[] = [];
  const badReplies: string[] = [];
  for (const [i, c] of candidates.entries()) {
    status(`Trying ${c.url}…`);
    const ms = candidates.length > 1 && i === 0 ? timeouts.first : timeouts.other;
    try {
      const url = await webviewProbe(c.url, fetchFn, ms);
      return { url, cleartext: url.startsWith('http://') };
    } catch (e) {
      if (e instanceof BadReply) badReplies.push(e.message);
      else unreached.push({ c, timedOut: e instanceof NoReply && e.message === 'timed out' });
    }
  }

  // Nothing the webview could use. Ask Rust why: it isn't subject to CORS,
  // so a server Rust reaches but the webview can't is one that doesn't
  // allow this app's origin.
  status('Checking why the server could not be reached…');
  const reasons: string[] = [];
  for (const { c, timedOut } of unreached) {
    let probe: ServerProbe;
    try {
      probe = await invoke<ServerProbe>('probe_server_url', { url: c.url });
    } catch (e) {
      reasons.push(errorText(e));
      continue;
    }
    // Before blaming CORS, give the webview one more go: at the https:// URL
    // an http:// one redirects to, or with the full timeout when its first
    // try only ran out of time (Rust's probe waits longer than a bare
    // host's https:// try, so a slow server can answer Rust alone).
    if (probe.url !== c.url || timedOut) {
      try {
        const url = await webviewProbe(probe.url, fetchFn, timeouts.other);
        return { url, cleartext: url.startsWith('http://') };
      } catch (e) {
        if (e instanceof NoReply && e.message === 'timed out') {
          throw new Error(
            `The OnScreen server at ${probe.url} is running, but it answered too slowly. Try again.`,
          );
        }
        // fall through to the CORS explanation
      }
    }
    throw new Error(
      `The OnScreen server at ${probe.url} is running, but it doesn't let this app connect: ` +
        `its CORS settings don't allow the desktop app (${origin || 'this app'}). ` +
        `OnScreen server 2.5.0 and later allow the desktop app automatically — update the ` +
        `server, or have an admin add ${origin || 'the app'} under Settings → General → ` +
        'CORS Allowed Origins and restart it, then try again.',
    );
  }

  if (badReplies.length) throw new Error(badReplies[0]);

  let message = `Couldn't connect to the server. ${reasons.join(' ')}`.trim();
  const typedScheme = /^[a-z][a-z0-9+.-]*:\/\//i.test(input.trim());
  if (!typedScheme && candidates.length === 1 && !candidates[0].local) {
    message +=
      ' (Plain http:// is only tried for servers on your local network, such as ' +
      '192.168.1.50:7070 or nas.local.)';
  }
  throw new Error(message);
}

/** Warning to show next to a saved server URL, or '' when it's encrypted
 *  (or plain http:// to this same machine, which never leaves it). */
export function cleartextWarning(url: string | null | undefined): string {
  if (!url || !/^http:\/\//i.test(url)) return '';
  try {
    const host = new URL(url).hostname.toLowerCase().replace(/^\[|\]$/g, '');
    if (host === 'localhost' || host.endsWith('.localhost') || /^127\./.test(host) || host === '::1') {
      return '';
    }
  } catch {
    // unparseable: warn
  }
  return (
    'Not encrypted: this server is reached over plain http://, so your password and ' +
    'what you watch can be seen by anyone on the same network. That is fine on a ' +
    'trusted home network; use the https:// address when one is available.'
  );
}
