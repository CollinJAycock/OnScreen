// AVPlay behind the media-element surface the player page uses.
//
// The player (routes/watch/[id]) is the webOS app's, written against an
// HTMLVideoElement: it sets `src` and `currentTime`, calls play() / pause(),
// reads `paused`, `readyState`, `seekable`, `duration`, and lives on the
// element's events (loadedmetadata, timeupdate, playing, waiting, seeking,
// seeked, pause, ended, error). On a Samsung TV video goes through AVPlay
// instead: the firmware's player, which decodes HEVC / HDR / 4K in hardware
// where the webview's <video> can't, and draws on a plane behind the page
// (see enableCompositing). This class is that element for AVPlay: the same
// properties, methods and events, so the page's whole playback logic (the
// content timeline, re-issues, Up Next, recovery) runs unchanged. Music and
// audiobooks stay on the real <video> element (no picture to draw, and
// AVPlay has no playback rate for the audiobook speed).
//
// What it maps:
//   - src = url        open + prepareAsync (no autoplay: the page calls
//                      play() once 'loadedmetadata' says it may);
//                      'loadstart', then 'durationchange' + 'loadedmetadata'
//                      + 'loadeddata' when prepared.
//   - currentTime = t  seekTo; a seek before the first buffered frame is
//                      held and applied then (seekTo that early snaps to 0
//                      or does nothing on Samsung firmware).
//   - play() / pause() play / pause, with 'play' / 'playing' / 'pause'.
//   - oncurrentplaytime  'timeupdate' (the clock), readyState 4.
//   - onbufferingstart / complete  'waiting' / 'canplay' (+ 'playing').
//   - onstreamcompleted  'pause' + 'ended'.
//   - onerror          error.code (2 network, 4 not supported) + 'error'.
//   - seekable / duration  from the playlist itself (lib/player/playlist):
//                      AVPlay reports neither for a growing server session,
//                      and the page needs both to tell a seek in the stream
//                      from one that re-opens the session.

import type { AvPlayApi, AvPlayListener } from './avplay';
import { firstVariantUrl, mediaPlaylistSpan, startsAtBeginning, type PlaylistSpan } from './playlist';

/** HTMLMediaElement's readyState values. */
export const HAVE_NOTHING = 0;
export const HAVE_METADATA = 1;
export const HAVE_CURRENT_DATA = 2;
export const HAVE_FUTURE_DATA = 3;
export const HAVE_ENOUGH_DATA = 4;

/** MediaError codes (MEDIA_ERR_*). */
export const MEDIA_ERR_NETWORK = 2;
export const MEDIA_ERR_DECODE = 3;
export const MEDIA_ERR_SRC_NOT_SUPPORTED = 4;

/** How often the playlist is read while the session grows. */
export const PLAYLIST_POLL_MS = 4000;
/** The fewest milliseconds between two 'timeupdate's (AVPlay ticks faster
 *  on some firmware; the element's own rate is ~4 Hz). */
export const TIMEUPDATE_MIN_GAP_MS = 200;
/** A source that opened this close to its start needs no seek to 0. */
export const START_SLACK_MS = 1000;
/** The longest a playlist read may take. The server holds a session's first
 *  read up to 60 s for segment 0; past this the connection is taken for
 *  dead (it would otherwise leave the page on "Starting playbackâ€¦" with no
 *  error), the read fails, and AVPlay is prepared to report for itself. */
export const PLAYLIST_READ_TIMEOUT_MS = 75_000;

/** An AVPlay error's MediaError code: a connection or HTTP failure is a
 *  network error; anything the player refuses (a codec, a container, a
 *  stream it can't open) is "not supported", which is what makes the page
 *  demote a codec claim and re-issue. */
export function errorCode(e: unknown): number {
  const s = errorText(e).toUpperCase();
  if (/CONNECTION|NETWORK|TIMEOUT|TIMED_OUT|HTTP|SERVER|DISCONNECT/.test(s)) return MEDIA_ERR_NETWORK;
  // Only a refusal of the stream itself is "not supported": the page takes
  // that as the codec's fault and demotes it for good. Anything else
  // (PLAYER_ERROR_GENEREIC, INVALID_STATE, an unreadable error) is a decode
  // failure, which a remux falls back from without demoting anything.
  if (/NOT_SUPPORTED|UNSUPPORTED/.test(s)) return MEDIA_ERR_SRC_NOT_SUPPORTED;
  return MEDIA_ERR_DECODE;
}

/** An AVPlay error's text. A WebAPIException's fields may not be
 *  enumerable (JSON.stringify gives '{}'), so they're read by name. */
function errorText(e: unknown): string {
  if (typeof e === 'string') return e;
  if (e && typeof e === 'object') {
    const o = e as { name?: unknown; message?: unknown; code?: unknown };
    const parts = [o.name, o.message, o.code].filter((x) => x !== undefined && x !== null).map(String);
    let json = '';
    try {
      json = JSON.stringify(e);
    } catch { /* cyclic */ }
    return [...parts, json].join(' ');
  }
  return String(e);
}

/** A TimeRanges with zero or one range. */
function ranges(end: number | null): TimeRanges {
  const has = end !== null && end > 0;
  return {
    length: has ? 1 : 0,
    start: () => 0,
    end: () => (has ? (end as number) : 0),
  } as unknown as TimeRanges;
}

type Listener = (e: Event) => void;

export interface AvplayMediaOptions {
  /** webapis.avplay, or null outside a Tizen webview. */
  api: AvPlayApi | null;
  /** The <object type="application/avplayer"> the picture is placed by. */
  anchor: () => HTMLElement | null;
  /** Reads a playlist (fetch, in the app). */
  fetchText?: (url: string) => Promise<string>;
  now?: () => number;
  setTimer?: (fn: () => void, ms: number) => unknown;
  clearTimer?: (id: unknown) => void;
}

export class AvplayMedia {
  // â”€â”€ The element surface â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
  paused = true;
  ended = false;
  seeking = false;
  readyState = HAVE_NOTHING;
  networkState = 3; // NETWORK_NO_SOURCE
  error: { code: number; message: string } | null = null;
  playbackRate = 1;
  defaultPlaybackRate = 1;
  hidden = false;

  private srcUrl = '';
  private timeSec = 0;
  private prepared = false;
  // The first frame is buffered: seekTo is reliable from here on.
  private flowing = false;
  private pendingSeekSec: number | null = null;
  private span: PlaylistSpan | null = null;
  private mediaPlaylistUrl = '';
  private pollTimer: unknown = null;
  private lastTimeupdateAt = -Infinity;
  // Bumped on every new source: callbacks from an older open are dropped.
  private gen = 0;
  private listeners = new Map<string, Set<Listener>>();
  // Playlist reads in flight (the default fetchText), aborted on close.
  private reads = new Set<AbortController>();
  private readonly fetchText: (url: string) => Promise<string>;
  private readonly now: () => number;
  private readonly setTimer: (fn: () => void, ms: number) => unknown;
  private readonly clearTimer: (id: unknown) => void;

  constructor(private readonly opts: AvplayMediaOptions) {
    this.fetchText = opts.fetchText ?? ((url) => this.fetchWithTimeout(url));
    this.now = opts.now ?? (() => Date.now());
    this.setTimer = opts.setTimer ?? ((fn, ms) => setTimeout(fn, ms));
    this.clearTimer = opts.clearTimer ?? ((id) => clearTimeout(id as ReturnType<typeof setTimeout>));
  }

  /** Whether AVPlay is here at all (false in a desktop browser). */
  get available(): boolean {
    return this.opts.api !== null;
  }

  get src(): string {
    return this.srcUrl;
  }
  set src(url: string) {
    if (!url) {
      this.removeAttribute('src');
      return;
    }
    this.open(url);
  }

  get currentSrc(): string {
    return this.srcUrl;
  }

  getAttribute(name: string): string | null {
    return name === 'src' && this.srcUrl ? this.srcUrl : null;
  }

  setAttribute(name: string, value: string): void {
    if (name === 'src') this.src = value;
  }

  /** Dropping the source (then load(), as the page does) closes AVPlay. */
  removeAttribute(name: string): void {
    if (name !== 'src' || !this.srcUrl) return;
    this.close();
    this.dispatch('emptied');
  }

  load(): void {
    if (this.srcUrl) this.open(this.srcUrl);
  }

  canPlayType(type: string): CanPlayTypeResult {
    return /mpegurl|dash\+xml|mp4/i.test(type) && this.available ? 'maybe' : '';
  }

  get currentTime(): number {
    return this.pendingSeekSec ?? this.timeSec;
  }
  set currentTime(sec: number) {
    if (!Number.isFinite(sec) || sec < 0) return;
    const api = this.opts.api;
    if (!api || !this.srcUrl) return;
    this.ended = false;
    if (!this.prepared || !this.flowing) {
      // Too early for seekTo: applied on the first buffered frame.
      this.pendingSeekSec = sec;
      return;
    }
    this.seekNow(sec);
  }

  get duration(): number {
    if (!this.prepared) return NaN;
    if (this.span?.ended) return this.span.producedEndSec;
    // A growing session (or no playlist read yet): the element reports
    // Infinity until ENDLIST, and the page reads that as "not ended".
    if (this.mediaPlaylistUrl || /\.m3u8(\?|$)/i.test(this.srcUrl)) return Infinity;
    const ms = this.call(() => this.opts.api!.getDuration(), 0);
    return ms > 0 ? ms / 1000 : NaN;
  }

  get seekable(): TimeRanges {
    if (!this.prepared) return ranges(null);
    if (this.span) return ranges(this.span.producedEndSec);
    const d = this.duration;
    return ranges(Number.isFinite(d) ? d : null);
  }

  get buffered(): TimeRanges {
    return ranges(null);
  }

  play(): Promise<void> {
    const api = this.opts.api;
    if (!api || !this.srcUrl) return Promise.resolve();
    if (!this.paused) return Promise.resolve();
    this.paused = false;
    this.ended = false;
    this.dispatch('play');
    if (this.prepared) {
      this.call(() => api.play(), undefined);
      if (this.flowing && this.readyState >= HAVE_FUTURE_DATA) this.dispatch('playing');
    }
    return Promise.resolve();
  }

  pause(): void {
    const api = this.opts.api;
    if (!api || this.paused) return;
    this.paused = true;
    if (this.prepared) this.call(() => api.pause(), undefined);
    this.dispatch('pause');
  }

  addEventListener(type: string, fn: Listener, opts?: boolean | AddEventListenerOptions): void {
    const once = typeof opts === 'object' && !!opts.once;
    const wrapped: Listener = once
      ? (e) => {
          this.removeEventListener(type, wrapped);
          fn(e);
        }
      : fn;
    let set = this.listeners.get(type);
    if (!set) this.listeners.set(type, (set = new Set()));
    set.add(wrapped);
  }

  removeEventListener(type: string, fn: Listener): void {
    this.listeners.get(type)?.delete(fn);
  }

  /** Close AVPlay and forget the source, without an event (teardown). */
  close(): void {
    this.gen++;
    this.stopPolling();
    // An old source's reads go too: left open they'd hold connections the
    // next source needs (six per host).
    for (const c of this.reads) c.abort();
    this.reads.clear();
    const api = this.opts.api;
    if (api) {
      this.call(() => api.stop(), undefined);
      this.call(() => api.close(), undefined);
    }
    this.srcUrl = '';
    this.mediaPlaylistUrl = '';
    this.span = null;
    this.prepared = false;
    this.flowing = false;
    this.pendingSeekSec = null;
    this.timeSec = 0;
    this.paused = true;
    this.ended = false;
    this.seeking = false;
    this.readyState = HAVE_NOTHING;
    this.networkState = 3;
  }

  // â”€â”€ AVPlay â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

  private open(url: string): void {
    const api = this.opts.api;
    this.close();
    this.srcUrl = url;
    this.error = null;
    const gen = this.gen;
    if (!api) {
      this.failLater(gen, 'AVPlay is not available');
      return;
    }
    this.networkState = 2; // NETWORK_LOADING
    this.dispatch('loadstart');
    try {
      api.open(url);
      try {
        (api as AvPlayApi & { setTimeoutForBuffering?: (s: number) => void }).setTimeoutForBuffering?.(60);
      } catch { /* older firmware */ }
      api.setListener(this.listener(gen));
      const a = this.opts.anchor();
      if (a) api.setDisplayRect(a.offsetLeft, a.offsetTop, a.offsetWidth || 1920, a.offsetHeight || 1080);
      if (/\.m3u8(\?|$)/i.test(url)) api.setStreamingProperty('ADAPTIVE_INFO', 'STREAMING_FORMAT=HLS');
      try {
        api.setStreamingProperty('SET_MODE_4K', 'TRUE');
      } catch { /* older firmware lacks it */ }
    } catch (e) {
      this.failLater(gen, e);
      return;
    }
    // An HLS session's first playlist read is held by the server until the
    // first segment exists, which takes up to a minute (~30 s for a 4K
    // remux on QA). AVPlay gives up on a connection after ~30 s
    // (PLAYER_ERROR_CONNECTION_FAILED), which failed remuxes over to a
    // transcode that then hit the same wait. Our own read has no such limit:
    // AVPlay is prepared once it has answered, so it only ever opens a
    // playlist that's ready. Answered either way: on a failed read AVPlay
    // tries for itself and reports what it gets.
    if (/\.m3u8(\?|$)/i.test(url)) void this.readPlaylist(gen, url, () => this.prepare(gen));
    else this.prepare(gen);
  }

  /** A failure found while `src` is being set, reported as the element does:
   *  after the setter returns. Dispatched inside it, the page (still opening
   *  the stream) dropped it and stayed on "Starting playbackâ€¦". */
  private failLater(gen: number, e: unknown): void {
    this.setTimer(() => {
      if (gen === this.gen) this.fail(e);
    }, 0);
  }

  /** A playlist read with a deadline, aborted when the source closes. */
  private fetchWithTimeout(url: string): Promise<string> {
    const c = new AbortController();
    this.reads.add(c);
    const timer = this.setTimer(() => c.abort(), PLAYLIST_READ_TIMEOUT_MS);
    return fetch(url, { signal: c.signal })
      .then((r) => (r.ok ? r.text() : ''))
      .finally(() => {
        this.clearTimer(timer);
        this.reads.delete(c);
      });
  }

  private prepare(gen: number): void {
    const api = this.opts.api;
    if (!api || gen !== this.gen) return;
    api.prepareAsync(
      () => {
        if (gen !== this.gen) return;
        // play() called before the prepare finished: start now. Read before
        // 'loadedmetadata', whose listener may call play() itself (which
        // then plays, being prepared): AVPlay must not be told twice.
        const playWaiting = !this.paused;
        this.prepared = true;
        this.networkState = 1; // NETWORK_IDLE
        if (playWaiting) this.call(() => api.play(), undefined);
        this.readyState = HAVE_METADATA;
        this.dispatch('durationchange');
        this.dispatch('loadedmetadata');
        this.readyState = HAVE_CURRENT_DATA;
        this.dispatch('loadeddata');
      },
      (e) => {
        if (gen === this.gen) this.fail(e);
      },
    );
  }

  private listener(gen: number): AvPlayListener {
    return {
      oncurrentplaytime: (ms) => {
        if (gen !== this.gen) return;
        if (this.readyState < HAVE_ENOUGH_DATA) this.readyState = HAVE_ENOUGH_DATA;
        // The first tick may be the first frame (some firmware sends no
        // buffering events): a held seek goes now, and this tick (still the
        // old position) isn't reported.
        if (!this.flowing) {
          // With no buffering events, this tick is the only sign playback
          // began: say so, or the page never arms its progress reporting.
          if (!this.startFlowing() && !this.paused) this.dispatch('playing');
        }
        if (this.seeking || this.pendingSeekSec !== null) return;
        this.timeSec = Math.max(0, ms) / 1000;
        const t = this.now();
        if (t - this.lastTimeupdateAt >= TIMEUPDATE_MIN_GAP_MS) {
          this.lastTimeupdateAt = t;
          this.dispatch('timeupdate');
        }
      },
      onbufferingstart: () => {
        if (gen !== this.gen) return;
        this.readyState = HAVE_CURRENT_DATA;
        this.dispatch('waiting');
      },
      onbufferingcomplete: () => {
        if (gen !== this.gen) return;
        this.readyState = HAVE_ENOUGH_DATA;
        this.startFlowing();
        this.dispatch('canplay');
        if (!this.paused && !this.seeking) this.dispatch('playing');
      },
      onstreamcompleted: () => {
        if (gen !== this.gen) return;
        const d = this.duration;
        if (Number.isFinite(d)) this.timeSec = d;
        this.ended = true;
        if (!this.paused) {
          this.paused = true;
          this.dispatch('pause');
        }
        this.dispatch('ended');
      },
      onerror: (e) => {
        if (gen === this.gen) this.fail(e);
      },
    };
  }

  /** The first frame is in: a seek held for it goes now. True when it did
   *  (its 'seeked' brings 'playing'). */
  private startFlowing(): boolean {
    if (this.flowing) return false;
    this.flowing = true;
    const held = this.pendingSeekSec;
    if (held === null) return false;
    // Already at the start (a session that opened there): no seek.
    if (held === 0 && this.call(() => this.opts.api!.getCurrentTime(), Infinity) < START_SLACK_MS) {
      this.pendingSeekSec = null;
      return false;
    }
    this.seekNow(held);
    return true;
  }

  private seekNow(sec: number): void {
    const api = this.opts.api!;
    const gen = this.gen;
    this.pendingSeekSec = null;
    this.seeking = true;
    this.timeSec = sec;
    this.dispatch('seeking');
    const done = () => {
      if (gen !== this.gen) return;
      this.seeking = false;
      this.dispatch('timeupdate');
      this.dispatch('seeked');
      if (!this.paused) this.dispatch('playing');
    };
    try {
      api.seekTo(Math.round(sec * 1000), done, done);
    } catch {
      done();
    }
  }

  private fail(e: unknown): void {
    const message = errorText(e);
    this.error = { code: errorCode(e), message };
    this.networkState = 3;
    this.stopPolling();
    this.dispatch('error');
  }

  // â”€â”€ The playlist (seekable, duration) â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

  private async readPlaylist(gen: number, url: string, onFirstRead?: () => void): Promise<void> {
    let text = '';
    try {
      let target = this.mediaPlaylistUrl || url;
      text = await this.fetchText(target);
      if (gen !== this.gen) return;
      // Only a playlist that answered: an error page would pin the master
      // URL as the media playlist, and no span or ENDLIST would ever show.
      if (!this.mediaPlaylistUrl && text) {
        const variant = firstVariantUrl(text, url);
        if (variant) {
          target = variant;
          text = await this.fetchText(variant);
          if (gen !== this.gen) return;
        }
        if (text) this.mediaPlaylistUrl = target;
      }
      const span = mediaPlaylistSpan(text);
      if (span) {
        const was = this.span;
        this.span = span;
        if (span.ended && !was?.ended) this.dispatch('durationchange');
      }
    } catch {
      // Unreadable this time: the next poll tries again.
    }
    if (gen !== this.gen) return;
    if (onFirstRead) {
      // A server session's playlist (EVENT, or VOD / ENDLIST) starts at its
      // beginning, as the element would: AVPlay opens a growing playlist at
      // its live edge, three segments from the end, and a remux has written
      // dozens by then, so the start was skipped. The page asks for a seek
      // only past 0, so one for 0 is held (unless the page asked for one)
      // and applied on the first frame. Not for a live sliding window (Live
      // TV): that one plays at its live edge, and its oldest segment is
      // already being deleted.
      if (startsAtBeginning(text) && this.pendingSeekSec === null && !this.flowing) this.pendingSeekSec = 0;
      onFirstRead();
    }
    if (this.span?.ended) return;
    this.pollTimer = this.setTimer(() => void this.readPlaylist(gen, url), PLAYLIST_POLL_MS);
  }

  private stopPolling(): void {
    if (this.pollTimer !== null) this.clearTimer(this.pollTimer);
    this.pollTimer = null;
  }

  // â”€â”€ Plumbing â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

  private dispatch(type: string): void {
    const set = this.listeners.get(type);
    if (!set) return;
    const e = { type, target: this, currentTarget: this } as unknown as Event;
    for (const fn of [...set]) {
      try {
        fn(e);
      } catch (err) {
        console.error('[avplay]', type, err);
      }
    }
  }

  private call<T>(fn: () => T, fallback: T): T {
    try {
      return fn();
    } catch {
      return fallback;
    }
  }
}

// â”€â”€ The picture plane â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
//
// AVPlay draws behind the webview, where the page's own background would
// hide it: while video plays, html, body and the app's root are transparent
// (the `player-route` class, app.css), and an <object
// type="application/avplayer"> at the top of <body> marks where the picture
// goes. Nested inside the app's overflow:hidden root the firmware's chroma
// reservation can be clipped or not engage (Samsung's samples and Jellyfin
// put it on <body>). Not on the audio views: an empty AVPlay object paints
// firmware black over the now-playing screen.

let anchorEl: HTMLObjectElement | null = null;

/** Make the page see-through and place the anchor (idempotent). */
export function enableCompositing(doc: Document = document): HTMLObjectElement {
  doc.documentElement.classList.add('player-route');
  doc.body.classList.add('player-route');
  if (anchorEl && doc.body.contains(anchorEl)) return anchorEl;
  const a = doc.createElement('object') as HTMLObjectElement;
  a.type = 'application/avplayer';
  a.setAttribute('width', '1920');
  a.setAttribute('height', '1080');
  a.style.position = 'absolute';
  a.style.left = '0';
  a.style.top = '0';
  a.style.width = '1920px';
  a.style.height = '1080px';
  a.style.zIndex = '0';
  doc.body.insertBefore(a, doc.body.firstChild);
  anchorEl = a;
  return a;
}

/** Undo enableCompositing. */
export function disableCompositing(doc: Document = document): void {
  doc.documentElement.classList.remove('player-route');
  doc.body.classList.remove('player-route');
  if (anchorEl?.parentNode) anchorEl.parentNode.removeChild(anchorEl);
  anchorEl = null;
}

/** The anchor, while compositing is on. */
export function compositingAnchor(): HTMLObjectElement | null {
  return anchorEl;
}
