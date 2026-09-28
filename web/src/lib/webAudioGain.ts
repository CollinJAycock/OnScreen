// Web Audio gain stage for the browser <audio> elements.
//
// The AudioPlayer routes each of its two gapless <audio> elements through
//   MediaElementAudioSourceNode -> GainNode -> destination
// on one shared, lazily-created AudioContext, so ReplayGain (and the user
// volume) can be applied as a single float multiply per element.
//
// Two browser rules shape this module:
//
//  1. createMediaElementSource() is one-way. Once an element is tapped its
//     audio ONLY reaches the speakers through the graph — there is no
//     un-tap. So we tap only when we're confident the graph will run:
//       - the AudioContext is actually 'running' (autoplay policy: it has
//         to be created/resumed from a user gesture, see unlock()), and
//       - the media is same-origin. A tapped element whose response is
//         cross-origin without CORS outputs silence (the spec zeroes it).
//         A same-origin URL isn't enough on its own: with an object-storage
//         media store the server 302s /media/stream/* to a presigned
//         bucket URL, which is cross-origin. probeStream() asks once per
//         session with redirect:'manual' and disables the path on a
//         redirect.
//     Anything uncertain -> don't tap; playback stays exactly as it was,
//     just without ReplayGain (logged once).
//
//  2. An AudioContext created outside a user gesture starts 'suspended'.
//     unlock() is wired to pointerdown/keydown/click by the player, so the
//     click that starts an album is also the gesture that starts the graph.

export type ProbeVerdict = 'safe' | 'redirect' | 'unknown';

export interface GainRouterDeps {
  /** Build the shared context. Default: AudioContext({latencyHint:'playback'}). */
  createContext?: () => AudioContext | null;
  /** Classify a media URL. Default: probeStream (1-byte ranged GET, no redirect follow). */
  probe?: (url: string) => Promise<ProbeVerdict>;
  /** Page origin used for the same-origin check. Default: location.origin. */
  origin?: () => string;
  /** Sticky user activation. Default: navigator.userActivation.hasBeenActive (true when unsupported). */
  hasBeenActive?: () => boolean;
  /** How long to wait for a suspended context to start before giving up. */
  startTimeoutMs?: number;
  log?: (msg: string) => void;
}

type WebkitWindow = Window & typeof globalThis & { webkitAudioContext?: typeof AudioContext };

export function webAudioSupported(): boolean {
  if (typeof window === 'undefined') return false;
  const w = window as WebkitWindow;
  return typeof (w.AudioContext ?? w.webkitAudioContext) === 'function';
}

function defaultCreateContext(): AudioContext | null {
  if (typeof window === 'undefined') return null;
  const w = window as WebkitWindow;
  const Ctor = w.AudioContext ?? w.webkitAudioContext;
  if (!Ctor) return null;
  // No sampleRate option on purpose: the context then runs at the output
  // device's rate, so the media element's audio is resampled once (source
  // rate -> device rate) — the same single conversion the browser's own
  // <audio> output path does. latencyHint 'playback' = bigger buffers,
  // fewer underruns, lower CPU; latency is irrelevant for music.
  try {
    return new Ctor({ latencyHint: 'playback' });
  } catch {
    try {
      return new Ctor();
    } catch {
      return null;
    }
  }
}

export function isSameOrigin(url: string, origin: string): boolean {
  try {
    return new URL(url, origin).origin === origin;
  } catch {
    return false;
  }
}

/** One 1-byte ranged GET with redirect:'manual'. The request is the same
 *  one the <audio> element is about to make for this file (same auth, same
 *  server-side accounting), so it has no side effects of its own. */
export async function probeStream(url: string): Promise<ProbeVerdict> {
  const ctrl = typeof AbortController !== 'undefined' ? new AbortController() : null;
  try {
    const res = await fetch(url, {
      method: 'GET',
      headers: { Range: 'bytes=0-0' },
      redirect: 'manual',
      credentials: 'same-origin',
      cache: 'no-store',
      signal: ctrl?.signal,
    });
    if (res.type === 'opaqueredirect' || (res.status >= 300 && res.status < 400)) return 'redirect';
    return res.ok ? 'safe' : 'unknown';
  } catch {
    return 'unknown';
  } finally {
    // Drop the body — we only needed the status line.
    ctrl?.abort();
  }
}

interface Chain {
  gain: GainNode;
  target: number;
}

/** Time constant for level changes on an element that's already playing
 *  (volume drag, mode switch). ~15 ms is below audibility as a fade but
 *  long enough to avoid zipper clicks. */
const SMOOTH_TAU_S = 0.015;

export class MediaGainRouter {
  private ctx: AudioContext | null = null;
  private chains = new Map<HTMLMediaElement, Chain>();
  private probeOk = false;
  private probing: Promise<ProbeVerdict> | null = null;
  private logged = new Set<string>();
  // After a failed start we stop waiting on every track (that would put a
  // gap between gapless tracks) until the user interacts again.
  private startBlocked = false;
  /** Non-empty once the path is permanently off for this session. */
  disabledReason = '';

  constructor(private deps: GainRouterDeps = {}) {}

  get disabled(): boolean {
    return this.disabledReason !== '';
  }

  get running(): boolean {
    return this.ctx?.state === 'running';
  }

  isRouted(el: HTMLMediaElement): boolean {
    return this.chains.has(el);
  }

  /** Create/resume the context. Call from a user-gesture handler. */
  unlock(): void {
    this.startBlocked = false;
    if (this.disabled) return;
    if (!this.ctx) this.ctx = this.makeContext();
    this.resume();
  }

  /** Best-effort resume without a gesture — fine for a context that has
   *  already been allowed to run (e.g. after a Safari 'interrupted'). */
  resume(): void {
    const c = this.ctx;
    if (c && c.state !== 'running' && c.state !== 'closed') {
      void c.resume().catch(() => {});
    }
  }

  /** Whether elements may be tapped for this media URL. Resolves false
   *  (and leaves playback untouched) on any doubt. */
  async canRoute(url: string): Promise<boolean> {
    if (this.disabled) return false;
    const origin = this.deps.origin?.() ?? (typeof location !== 'undefined' ? location.origin : '');
    if (!isSameOrigin(url, origin)) {
      this.disable(
        'media is served from another origin',
        'the media URL is cross-origin; a MediaElementSource without CORS outputs silence',
      );
      return false;
    }
    if (!(await this.ensureRunning())) return false;
    const verdict = await this.probeOnce(url);
    if (verdict === 'redirect') {
      this.disable(
        'media streams are redirected to external storage',
        'the server redirects /media/stream to another origin (object storage); tapping it would output silence',
      );
      return false;
    }
    return verdict === 'safe';
  }

  /** Tap an element. Idempotent. initialGain should be the level the
   *  element is meant to have right now (the caller pins element.volume
   *  to 1 in the same task). */
  attach(el: HTMLMediaElement, initialGain: number): boolean {
    if (this.chains.has(el)) return true;
    const c = this.ctx;
    if (!c || this.disabled || c.state === 'closed') return false;
    let gain: GainNode;
    try {
      gain = c.createGain();
      gain.gain.value = sanitize(initialGain);
    } catch (e) {
      this.logOnce('attach', `could not create a gain node: ${String(e)}`);
      return false;
    }
    let source: MediaElementAudioSourceNode;
    try {
      source = c.createMediaElementSource(el);
    } catch (e) {
      // InvalidStateError: something else already tapped this element
      // (another AudioContext, an e2e analyser). It's not ours to route.
      this.logOnce('attach', `element already has a media source node: ${String(e)}`);
      return false;
    }
    try {
      source.connect(gain);
      gain.connect(c.destination);
    } catch (e) {
      // Tapped but not wired would be silent — fall back to a direct
      // connection so the element is at least audible at unity.
      this.logOnce('attach', `gain wiring failed, routing direct: ${String(e)}`);
      try {
        source.connect(c.destination);
      } catch {
        /* nothing more we can do */
      }
      return false;
    }
    this.chains.set(el, { gain, target: sanitize(initialGain) });
    return true;
  }

  /** Set an element's gain. `smooth` for elements that are audible right
   *  now; immediate otherwise (a paused element about to start must have
   *  its exact gain from the first sample). */
  setGain(el: HTMLMediaElement, value: number, smooth: boolean): void {
    const chain = this.chains.get(el);
    const c = this.ctx;
    if (!chain || !c) return;
    const v = sanitize(value);
    if (chain.target === v) return;
    chain.target = v;
    const p = chain.gain.gain;
    const t = c.currentTime;
    p.cancelScheduledValues(t);
    if (smooth) {
      p.setValueAtTime(p.value, t);
      p.setTargetAtTime(v, t, SMOOTH_TAU_S);
      // setTarget approaches asymptotically; land exactly on the target
      // once the fade is inaudible so the steady-state gain is exact.
      p.setValueAtTime(v, t + SMOOTH_TAU_S * 8);
    } else {
      p.setValueAtTime(v, t);
    }
  }

  /** Current target gain for a routed element (test/debug aid). */
  gainOf(el: HTMLMediaElement): number | undefined {
    return this.chains.get(el)?.target;
  }

  private makeContext(): AudioContext | null {
    const c = (this.deps.createContext ?? defaultCreateContext)();
    if (!c) this.disable('Web Audio is unavailable', 'could not create an AudioContext');
    return c;
  }

  private async ensureRunning(): Promise<boolean> {
    if (this.ctx?.state === 'running') return true;
    if (this.startBlocked) return false;
    if (!this.ctx) {
      // Without any user activation a fresh context can only start
      // suspended (and Chrome logs an autoplay warning) — don't bother.
      const active = this.deps.hasBeenActive?.() ?? defaultHasBeenActive();
      if (!active) return false;
      this.ctx = this.makeContext();
      if (!this.ctx) return false;
    }
    const c = this.ctx;
    if (c.state === 'closed') {
      this.disable('Web Audio is unavailable', 'the AudioContext was closed');
      return false;
    }
    const timeout = this.deps.startTimeoutMs ?? 1500;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const started = await Promise.race([
      c.resume().then(
        () => true,
        () => false,
      ),
      new Promise<boolean>((r) => {
        timer = setTimeout(() => r(false), timeout);
      }),
    ]);
    clearTimeout(timer);
    if (started && c.state === 'running') return true;
    this.startBlocked = true;
    this.logOnce('start', 'AudioContext could not start (autoplay policy); ReplayGain skipped until the next interaction');
    return false;
  }

  private async probeOnce(url: string): Promise<ProbeVerdict> {
    if (this.probeOk) return 'safe';
    if (!this.probing) {
      const run = this.deps.probe ?? probeStream;
      this.probing = run(url).finally(() => {
        this.probing = null;
      });
    }
    const v = await this.probing;
    // Only a pass is remembered: 'unknown' (network blip, 401 during a
    // token refresh) is retried on the next track.
    if (v === 'safe') this.probeOk = true;
    return v;
  }

  private disable(reason: string, detail: string): void {
    if (this.disabled) return;
    this.disabledReason = reason;
    this.logOnce('disabled', `ReplayGain is off in this browser: ${detail}`);
  }

  private logOnce(key: string, msg: string): void {
    if (this.logged.has(key)) return;
    this.logged.add(key);
    (this.deps.log ?? ((m: string) => console.info(m)))(`[replaygain] ${msg}`);
  }
}

function sanitize(v: number): number {
  return Number.isFinite(v) && v > 0 ? v : 0;
}

function defaultHasBeenActive(): boolean {
  if (typeof navigator === 'undefined') return false;
  const ua = (navigator as Navigator & { userActivation?: { hasBeenActive: boolean } }).userActivation;
  return ua ? ua.hasBeenActive : true;
}
