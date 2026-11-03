// The Xbox shell around this app.
//
// On Xbox the TV app is not installed on the console: a small UWP app (the
// shell, clients/xbox/shell) asks for the user's OnScreen server, then loads
// this page from that server at <server>/tvapp/ in a WebView2. So the page and
// the API share one origin: no CORS, no mixed content for a plain-http LAN
// server, and the app updates with the server. The shell passes what only
// native code can know on the page's query string, read once at boot:
//
//   ?shell=xbox&device=Xbox%20Series%20X&native=1&hdr=1&uhd=1
//
// and takes requests back over WebView2's message channel
// (window.chrome.webview.postMessage): "exit" (the exit popup's Exit) and
// "changeServer" (Settings' Forget server: back to the shell's server prompt).
// The shell sends the controller's keys the other way ("key" messages): the
// console gives the web view none of its own (found on a Series X).
//
// Outside the shell (a desktop browser at <server>/tvapp/, which is how the app
// is tested on a PC) everything degrades: no device name, display answers
// from the browser, and no exit or server change.

import { GAMEPAD, isGamepadKeyCode } from './focus/keys';

const PARAMS_KEY = 'onscreen.shell_params';

export interface ShellParams {
  /** "xbox" inside the shell; null in a plain browser. */
  shell: string | null;
  /** The console model, e.g. "Xbox Series X"; '' when unknown. */
  device: string;
  /** The display shows HDR10 (HdmiDisplayInformation); null when unknown. */
  hdr: boolean | null;
  /** The display takes 2160p; null when unknown. */
  uhd: boolean | null;
  /** The shell has the console's own player (lib/player/nativeVideo), for
   *  the video the web view can't decode. */
  native: boolean;
}

const NONE: ShellParams = { shell: null, device: '', hdr: null, uhd: null, native: false };

function flag(v: string | null): boolean | null {
  if (v === '1' || v === 'true') return true;
  if (v === '0' || v === 'false') return false;
  return null;
}

/** The shell's parameters in a query string ('' or '?…'). */
export function parseShellParams(search: string): ShellParams {
  let q: URLSearchParams;
  try {
    q = new URLSearchParams(search);
  } catch {
    return NONE;
  }
  const shell = q.get('shell');
  if (!shell) return NONE;
  return {
    shell: shell.toLowerCase().slice(0, 16),
    device: (q.get('device') ?? '').trim().slice(0, 40),
    hdr: flag(q.get('hdr')),
    uhd: flag(q.get('uhd')),
    native: flag(q.get('native')) === true,
  };
}

interface Store {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
}

/** The parameters for this launch: the query string's, else what an earlier
 *  page load of this session kept (a reload, or SvelteKit rewriting the URL,
 *  can drop the query). */
export function resolveShellParams(search: string, store: Store | null): ShellParams {
  const fresh = parseShellParams(search);
  if (fresh.shell) {
    try {
      store?.setItem(PARAMS_KEY, JSON.stringify(fresh));
    } catch {
      /* storage blocked: this load still has them */
    }
    return fresh;
  }
  try {
    const saved = JSON.parse(store?.getItem(PARAMS_KEY) ?? 'null') as Partial<ShellParams> | null;
    if (saved && typeof saved.shell === 'string') {
      return {
        shell: saved.shell,
        device: typeof saved.device === 'string' ? saved.device : '',
        hdr: typeof saved.hdr === 'boolean' ? saved.hdr : null,
        uhd: typeof saved.uhd === 'boolean' ? saved.uhd : null,
        native: saved.native === true,
      };
    }
  } catch {
    /* unreadable: no shell */
  }
  return NONE;
}

/**
 * The server this page came from, which is the API's origin: the page is
 * served at <origin>/tvapp/. null when the page isn't served over http(s) by a
 * server (vite dev, where Setup still asks for one, or a test).
 */
export function servedOrigin(loc: { protocol: string; origin: string } | null, dev: boolean): string | null {
  if (dev || !loc) return null;
  if (loc.protocol !== 'http:' && loc.protocol !== 'https:') return null;
  return loc.origin;
}

interface WebViewChannel {
  postMessage(message: unknown): void;
}

function channel(): WebViewChannel | null {
  try {
    const wv = (globalThis as { chrome?: { webview?: WebViewChannel } }).chrome?.webview;
    return wv && typeof wv.postMessage === 'function' ? wv : null;
  } catch {
    return null;
  }
}

/**
 * Tell or ask the shell something; false when there is no shell. "exit" and
 * "changeServer" as above; "nativeKeys": the controller reaches the page as
 * key events (lib/gamepad saw one), so the shell must stop forwarding the
 * system's Back request as a "back" message, or B would act twice.
 */
/** A command for the shell's native player (lib/player/nativeVideo). */
export function postNative(command: Record<string, unknown>): boolean {
  const c = channel();
  if (!c) return false;
  try {
    c.postMessage({ type: 'native', ...command });
    return true;
  } catch {
    return false;
  }
}

/** An event from the shell's native player: what happened (ev), to which
 *  load (gen), where (t) and of how long (d), in seconds. */
export interface NativeEvent {
  ev: string;
  gen: number;
  t: number;
  d: number;
  [k: string]: unknown;
}

/** A "nativeEvent" message as a NativeEvent; null for anything else. */
export function nativeEvent(data: unknown): NativeEvent | null {
  if (!data || typeof data !== 'object') return null;
  const m = data as Record<string, unknown>;
  if (m.type !== 'nativeEvent' || typeof m.ev !== 'string') return null;
  const num = (v: unknown) => (typeof v === 'number' && Number.isFinite(v) ? v : 0);
  return { ...m, ev: m.ev, gen: num(m.gen), t: num(m.t), d: num(m.d) };
}

const nativeListeners = new Set<(e: NativeEvent) => void>();

/** Hear the native player's events; returns the unsubscribe. */
export function onNativeEvent(fn: (e: NativeEvent) => void): () => void {
  nativeListeners.add(fn);
  return () => nativeListeners.delete(fn);
}

export function postToShell(type: 'exit' | 'changeServer' | 'nativeKeys' | 'webKeys'): boolean {
  const c = channel();
  if (!c) return false;
  try {
    c.postMessage({ type });
    return true;
  } catch {
    return false;
  }
}

/**
 * Where to go after Forget server / Change server: the shell's own server
 * prompt (null: the shell has taken over), else Sign in — a browser can't
 * point a page another server's /tvapp/ — or Setup in vite dev, where the
 * stored origin is still the API's.
 */
export function afterForgetServer(dev: boolean = import.meta.env.DEV): '#/setup' | '#/login' | null {
  if (postToShell('changeServer')) return null;
  return dev ? '#/setup' : '#/login';
}

// ── Runtime ─────────────────────────────────────────────────────────────────

let live: ShellParams | null = null;

/** This launch's shell parameters (read once). */
export function shellParams(): ShellParams {
  if (live) return live;
  let search = '';
  let store: Store | null = null;
  try {
    search = globalThis.location?.search ?? '';
  } catch {
    /* no location (tests) */
  }
  try {
    store = globalThis.sessionStorage ?? null;
  } catch {
    /* storage blocked */
  }
  live = resolveShellParams(search, store);
  return live;
}

/** Inside the Xbox shell (it can exit the app and change the server). */
export function inShell(): boolean {
  return shellParams().shell !== null && channel() !== null;
}

/** What a message from the shell asks the page to do; null for anything
 *  else. "back": the system's Back request (the B button, when Xbox raises it
 *  as navigation rather than a key), played to the app as B. */
export function shellCommand(data: unknown): 'back' | null {
  return data && typeof data === 'object' && (data as { type?: unknown }).type === 'back' ? 'back' : null;
}

export interface ShellKey {
  type: 'keydown' | 'keyup';
  /** The GAMEPAD name ('GamepadA', ...). */
  key: string;
  /** The VK_GAMEPAD_* code (195-218). */
  code: number;
  repeat: boolean;
}

/** A controller key the shell passed on ("key" message); null for anything
 *  else, or a code outside the gamepad range. */
export function shellKey(data: unknown): ShellKey | null {
  if (!data || typeof data !== 'object') return null;
  const m = data as { type?: unknown; event?: unknown; code?: unknown; repeat?: unknown };
  if (m.type !== 'key' || (m.event !== 'keydown' && m.event !== 'keyup')) return null;
  if (typeof m.code !== 'number' || !isGamepadKeyCode(m.code)) return null;
  const key = GAMEPAD_NAME_BY_CODE.get(m.code) ?? `Gamepad${m.code}`;
  return { type: m.event, key, code: m.code, repeat: m.event === 'keydown' && m.repeat === true };
}

const GAMEPAD_NAME_BY_CODE = new Map(Object.entries(GAMEPAD).map(([name, g]) => [g.code, name]));

/** Key events this page made from the shell's "key" messages. */
const fromShell = new WeakSet<Event>();

/** A key event made from the shell's "key" message, not the web view's own. */
export function isShellKeyEvent(e: Event): boolean {
  return fromShell.has(e);
}

/** The shell's key as a key event, with the code a web view would carry
 *  (a KeyboardEvent can't be constructed with one), so lib/gamepad takes it
 *  for the platform's own and stops polling. */
function dispatchShellKey(k: ShellKey): void {
  const e = new KeyboardEvent(k.type, { key: k.key, repeat: k.repeat, bubbles: true, cancelable: true });
  Object.defineProperty(e, 'keyCode', { get: () => k.code });
  Object.defineProperty(e, 'which', { get: () => k.code });
  fromShell.add(e);
  (document.activeElement ?? document.body).dispatchEvent(e);
}

let listening = false;

/** Act on the shell's messages; idempotent, a no-op without a shell. */
export function listenToShell(): void {
  if (listening) return;
  let wv: (WebViewChannel & { addEventListener?(t: string, f: (e: { data: unknown }) => void): void }) | null;
  try {
    wv = (globalThis as { chrome?: { webview?: WebViewChannel } }).chrome?.webview ?? null;
  } catch {
    wv = null;
  }
  if (!wv || typeof wv.addEventListener !== 'function') return;
  listening = true;
  // A gamepad key event the shell didn't make: the web view has its own,
  // so the shell's would arrive twice. It stops sending them.
  let ownKeys = false;
  window.addEventListener(
    'keydown',
    (e) => {
      if (ownKeys || fromShell.has(e) || !isGamepadKeyCode(e.keyCode)) return;
      ownKeys = true;
      postToShell('webKeys');
    },
    true,
  );
  wv.addEventListener('message', (e) => {
    const ne = nativeEvent(e.data);
    if (ne) {
      for (const fn of nativeListeners) fn(ne);
      return;
    }
    const key = shellKey(e.data);
    if (key) {
      if (!ownKeys) dispatchShellKey(key);
      return;
    }
    if (shellCommand(e.data) !== 'back') return;
    const target = document.activeElement ?? document.body;
    for (const type of ['keydown', 'keyup']) {
      target.dispatchEvent(new KeyboardEvent(type, { key: 'GamepadB', bubbles: true, cancelable: true }));
    }
  });
}
