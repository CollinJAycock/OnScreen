// The console's own player, standing in for the watch page's <video>.
//
// The Xbox web view decodes neither HEVC, AV1 nor Dolby audio, and can't put
// the TV into HDR (see lib/api/capabilities). The shell can: it plays a stream
// in the console's MediaPlayerElement behind the transparent web view
// (shell/NativePlayer.cs). NativeVideo is that player in the shape of the
// HTMLVideoElement the watch page drives — src, play(), pause(), currentTime,
// paused, ended, readyState, error and the media events in the order a
// browser fires them — so the page's player screen, pickers, subtitles and
// progress reporting run on it unchanged. Commands go to the shell as
// "native" messages; its "nativeEvent" answers are played back as events,
// each load tagged with a generation so an old source's late events are
// dropped. A playlist URL is fine: the console plays HLS itself, so the page
// takes its native-HLS branch (canPlayType says so) instead of hls.js.

import { onNativeEvent, postNative, type NativeEvent } from '../shell';

export interface NativeTransport {
  post(command: Record<string, unknown>): boolean;
  subscribe(fn: (e: NativeEvent) => void): () => void;
}

const shellTransport: NativeTransport = { post: postNative, subscribe: onNativeEvent };

/** A TimeRanges from the shell's flat [start, end, start, end, ...]. */
export class Ranges {
  private readonly r: number[];
  constructor(flat: unknown) {
    this.r = Array.isArray(flat) ? flat.filter((n): n is number => typeof n === 'number' && Number.isFinite(n)) : [];
    if (this.r.length % 2) this.r.pop();
  }
  get length(): number {
    return this.r.length / 2;
  }
  start(i: number): number {
    return this.r[i * 2];
  }
  end(i: number): number {
    return this.r[i * 2 + 1];
  }
}

const NO_RANGES = new Ranges([]);

/** HTMLMediaElement's readyState values. */
const HAVE_NOTHING = 0;
const HAVE_METADATA = 1;
const HAVE_CURRENT_DATA = 2;
const HAVE_ENOUGH_DATA = 4;

export class NativeVideo extends EventTarget {
  /** Whether the next load is HDR10 for the TV (the shell switches to an
   *  HDR10 display mode for it). Set before src. */
  hdr = false;
  /** Whether the next load carries Dolby Vision (profile 8.1, or 7 the server
   *  converts): the shell asks for a Dolby Vision display mode, else HDR10.
   *  Set before src. */
  dolbyVision = false;
  /** Called with true while the native player has a source (the page clears
   *  its background then, so the video behind it shows). */
  onActive: ((active: boolean) => void) | null = null;

  private url = '';
  private gen = 0;
  private time = 0;
  private dur = NaN;
  private rate = 1;
  private seekableRanges = NO_RANGES;
  private bufferedRanges = NO_RANGES;
  private state = {
    paused: true,
    ended: false,
    seeking: false,
    readyState: HAVE_NOTHING as number,
    error: null as { code: number; message: string } | null,
  };
  private readonly off: () => void;

  constructor(private readonly transport: NativeTransport = shellTransport) {
    super();
    this.off = transport.subscribe((e) => this.onEvent(e));
  }

  /** Stop listening (the page is going). */
  dispose(): void {
    this.off();
  }

  // ── HTMLMediaElement surface ────────────────────────────────────────

  get src(): string {
    return this.url;
  }
  set src(value: string) {
    this.url = value;
    this.reset();
    if (!value) {
      this.transport.post({ op: 'stop' });
      this.onActive?.(false);
      return;
    }
    this.transport.post({ op: 'load', url: value, gen: this.gen, hdr: this.hdr, dv: this.dolbyVision });
    this.onActive?.(true);
    this.fire('emptied');
    this.fire('loadstart');
  }
  get currentSrc(): string {
    return this.url;
  }
  getAttribute(name: string): string | null {
    return name === 'src' && this.url ? this.url : null;
  }
  removeAttribute(name: string): void {
    if (name === 'src' && this.url) this.src = '';
  }
  /** With no src: the source is released (removeAttribute already did). */
  load(): void {}

  get currentTime(): number {
    return this.time;
  }
  set currentTime(sec: number) {
    if (!Number.isFinite(sec)) return;
    this.time = Math.max(0, sec);
    this.state.seeking = true;
    this.transport.post({ op: 'seek', t: this.time });
    this.fire('seeking');
  }
  get duration(): number {
    return this.dur;
  }
  /** How far the stream reaches (a server session's playlist grows). */
  get seekable(): Ranges {
    return this.seekableRanges;
  }
  get buffered(): Ranges {
    return this.bufferedRanges;
  }
  get paused(): boolean {
    return this.state.paused;
  }
  get ended(): boolean {
    return this.state.ended;
  }
  get seeking(): boolean {
    return this.state.seeking;
  }
  get readyState(): number {
    return this.state.readyState;
  }
  get error(): { code: number; message: string } | null {
    return this.state.error;
  }
  get playbackRate(): number {
    return this.rate;
  }
  set playbackRate(r: number) {
    if (!Number.isFinite(r) || r <= 0 || r === this.rate) return;
    this.rate = r;
    this.transport.post({ op: 'rate', rate: r });
    this.fire('ratechange');
  }
  defaultPlaybackRate = 1;
  /** The element is invisible either way: the picture is behind the page. */
  hidden = false;

  play(): Promise<void> {
    if (!this.url) return Promise.reject(new DOMException('no source', 'NotSupportedError'));
    if (this.state.ended) this.state.ended = false;
    this.transport.post({ op: 'play' });
    if (this.state.paused) {
      this.state.paused = false;
      this.fire('play');
    }
    return Promise.resolve();
  }
  pause(): void {
    if (this.url) this.transport.post({ op: 'pause' });
    if (!this.state.paused) {
      this.state.paused = true;
      this.fire('pause');
    }
  }
  /** HLS (the console plays it itself) and the files a direct play hands
   *  over. */
  canPlayType(type: string): '' | 'maybe' {
    return /mpegurl|mp4|matroska|mkv|quicktime/i.test(type) ? 'maybe' : '';
  }

  // ── The shell's events ─────────────────────────────────────────────

  private onEvent(e: NativeEvent): void {
    if (e.gen !== this.gen || !this.url) return;
    if ('s' in e) this.seekableRanges = new Ranges(e.s);
    if ('b' in e) this.bufferedRanges = new Ranges(e.b);
    if (Number.isFinite(e.d) && e.d > 0 && e.d !== this.dur) {
      this.dur = e.d;
      if (this.state.readyState >= HAVE_METADATA) this.fire('durationchange');
    }
    switch (e.ev) {
      case 'loadedmetadata':
        this.state.readyState = HAVE_METADATA;
        this.fire('durationchange');
        this.fire('loadedmetadata');
        this.state.readyState = HAVE_ENOUGH_DATA;
        this.fire('loadeddata');
        this.fire('canplay');
        break;
      case 'state':
        this.onState(String(e.state));
        break;
      case 'seeked':
        this.time = e.t;
        this.state.seeking = false;
        this.fire('seeked');
        this.fire('timeupdate');
        break;
      case 'time':
        // While a seek is out the shell still reports the old point.
        if (this.state.seeking || this.state.readyState < HAVE_METADATA || e.t === this.time) return;
        this.time = e.t;
        this.fire('timeupdate');
        break;
      case 'ended':
        this.time = Number.isFinite(this.dur) ? this.dur : e.t;
        this.state.ended = true;
        this.fire('timeupdate');
        if (!this.state.paused) {
          this.state.paused = true;
          this.fire('pause');
        }
        this.fire('ended');
        break;
      case 'error':
        this.state.error = {
          code: typeof e.code === 'number' ? e.code : 4,
          message: typeof e.msg === 'string' ? e.msg : 'native playback failed',
        };
        this.fire('error');
        break;
    }
  }

  private onState(s: string): void {
    if (s === 'playing') {
      this.state.readyState = HAVE_ENOUGH_DATA;
      if (this.state.paused) {
        this.state.paused = false;
        this.fire('play');
      }
      this.fire('playing');
    } else if (s === 'paused') {
      // Before the first play the shell sits paused: not a 'pause'.
      if (!this.state.paused && !this.state.ended) {
        this.state.paused = true;
        this.fire('pause');
      }
    } else if (s === 'buffering') {
      if (this.state.readyState >= HAVE_METADATA) {
        this.state.readyState = HAVE_CURRENT_DATA;
        this.fire('waiting');
      }
    }
  }

  private reset(): void {
    this.gen++;
    this.time = 0;
    this.dur = NaN;
    this.rate = 1;
    this.seekableRanges = NO_RANGES;
    this.bufferedRanges = NO_RANGES;
    this.state = { paused: true, ended: false, seeking: false, readyState: HAVE_NOTHING, error: null };
  }

  private fire(type: string): void {
    this.dispatchEvent(new Event(type));
  }
}

/**
 * Whether a server origin is on the home network: a private or loopback
 * address, a .local name or a bare host name. The native player only takes a
 * file as it is (direct play) from such a server. It downloads a file as fast
 * as the link allows, which over the internet (through a proxy such as
 * Cloudflare, which pulls the whole file from the server) filled the server's
 * uplink for a minute and stalled the app's own requests; a server session's
 * HLS is fetched a segment at a time instead.
 */
export function onHomeNetwork(origin: string): boolean {
  let host: string;
  try {
    host = new URL(origin).hostname.toLowerCase().replace(/^\[|\]$/g, '');
  } catch {
    return false;
  }
  if (host === 'localhost' || host.endsWith('.local') || (!host.includes('.') && !host.includes(':'))) return true;
  const v4 = /^(\d+)\.(\d+)\.(\d+)\.(\d+)$/.exec(host);
  if (v4) {
    const [a, b] = [Number(v4[1]), Number(v4[2])];
    return a === 10 || a === 127 || (a === 192 && b === 168) || (a === 172 && b >= 16 && b <= 31) || (a === 169 && b === 254);
  }
  // IPv6 loopback, unique-local (fc00::/7) and link-local (fe80::/10).
  return host === '::1' || /^f[cd][0-9a-f]{2}:/.test(host) || /^fe[89ab][0-9a-f]:/.test(host);
}
