// Tizen AVPlay wrapper.
//
// AVPlay is the Samsung TV firmware's hardware-accelerated video
// playback API — handles HLS, DASH, MP4, MKV, and HEVC/AV1 directly
// against the silicon decoders. It's much better than HTML5 <video>
// for TV use because:
//
// - HLS / DASH demuxing happens in firmware (no need for hls.js's
//   ~150 KB of JS, no MediaSource Extensions performance cliff).
// - Hardware HEVC + AV1 decode (no CPU-fallback heat).
// - Native 4K + HDR pipeline (HTML5 video on Tizen tops out at
//   1080p SDR for compatibility-mode pages).
//
// The trade-off: AVPlay is a globally-singleton C++ object exposed
// through a polluted JS namespace (`webapis.avplay.*`). It manages
// one window-sized DisplayWindow at a time and doesn't compose with
// CSS — the video sits behind the webview as a hardware overlay.
// Set the display rect with setDisplayRect to position it.
//
// API reference: developer.samsung.com → "AVPlay API" (5.5+).

/* eslint-disable @typescript-eslint/no-explicit-any */

interface AvPlayApi {
  open(url: string): void;
  setListener(listener: AvPlayListener): void;
  setStreamingProperty(name: string, value: string): void;
  setDisplayRect(x: number, y: number, w: number, h: number): void;
  setDisplayMethod(method: 'PLAYER_DISPLAY_MODE_LETTER_BOX' | 'PLAYER_DISPLAY_MODE_FULL_SCREEN'): void;
  prepare(): void;
  prepareAsync(success?: () => void, fail?: (e: unknown) => void): void;
  play(): void;
  pause(): void;
  stop(): void;
  close(): void;
  seekTo(positionMs: number, success?: () => void, fail?: (e: unknown) => void): void;
  getCurrentTime(): number;
  getDuration(): number;
  getState(): string;
  /** Track listing — entries carry { type: 'TEXT'|'AUDIO'|'VIDEO',
   *  index, extra_info }. AVPlay's own per-type numbering, NOT the
   *  source file's stream index. */
  getTotalTrackInfo?(): AvTrackInfo[];
  /** Select a track by AVPlay's own index for the given type. */
  setSelectTrack?(type: 'TEXT' | 'AUDIO' | 'VIDEO', index: number): void;
}

interface AvTrackInfo {
  type: string;
  index: number;
  extra_info?: string;
}

interface AvPlayListener {
  onbufferingstart?: () => void;
  onbufferingprogress?: (percent: number) => void;
  onbufferingcomplete?: () => void;
  oncurrentplaytime?: (currentTime: number) => void;
  onstreamcompleted?: () => void;
  onerror?: (error: string) => void;
}

declare global {
  interface Window {
    webapis?: { avplay?: AvPlayApi };
  }
}

export interface PlaySource {
  url: string;
  /** "HLS" | "DASH" — set so AVPlay picks the right demuxer.
   *  Mirrors the Roku scaffold's guessStreamFormat. */
  streamingMode?: 'HLS' | 'DASH';
  /** Bearer token, appended as `?token=<paseto>` since AVPlay
   *  can't attach an Authorization header to its segment fetches.
   *  The Go server's asset-route middleware accepts this. */
  bearer?: string;
  /** Resume position in milliseconds. Applied via seekTo after
   *  the stream is prepared. */
  startMs?: number;
  /** The DOM element that anchors AVPlay's hardware overlay. Must
   *  be an `<object type="application/avplayer">` — Tizen's
   *  firmware uses both the element's presence (as a webview-side
   *  placement marker) AND its offsetLeft/Top/Width/Height to
   *  composite the video. Hardcoded dimensions land in the wrong
   *  place on 4K panels whose webview ≠ declared resolution. */
  anchor: HTMLElement;
}

export interface PlayHandlers {
  onProgress?: (currentMs: number, durationMs: number) => void;
  onEnded?: () => void;
  onError?: (message: string) => void;
  /** Rebuffering started — the picture is paused while the firmware
   *  refills its buffer. Caller re-shows the loading overlay. */
  onBufferingStart?: () => void;
  /** Rebuffering progress, 0..100. */
  onBufferingProgress?: (percent: number) => void;
  /** Buffer refilled — the firmware is about to resume the picture.
   *  Caller hides the loading overlay. */
  onBufferingComplete?: () => void;
}

/** Singleton wrapper around webapis.avplay. Returns no-op stubs in
 *  non-Tizen environments (browser dev) so the same import works
 *  during `vite dev` against a desktop browser. */
export class AvPlay {
  private api: AvPlayApi | null;
  private prepared = false;
  // Last open() args, kept so a transient onerror can re-open/prepare
  // the same source once without the caller rebuilding everything.
  private lastSource: PlaySource | null = null;
  private lastHandlers: PlayHandlers = {};
  // One automatic re-prepare per open() — reset on each fresh open()
  // so a later session gets its own retry budget. Avoids a flapping
  // stream looping open→error→open forever.
  private retried = false;
  // Pending resume seek. seekTo() before the first frame is decoded is
  // unreliable on Samsung firmware, so a startMs is applied on the first
  // onbufferingcomplete after play() rather than inside prepareAsync.
  private pendingSeekMs = 0;
  // Source/handlers stashed by suspend() (app backgrounded) so restore()
  // can re-open the exact session at the suspended position. Kept apart
  // from lastSource because close() (called inside suspend) clears that.
  private suspendedSource: PlaySource | null = null;
  private suspendedHandlers: PlayHandlers = {};

  constructor() {
    this.api = (typeof window !== 'undefined' && window.webapis?.avplay) || null;
  }

  /** True when running inside the Tizen webview with AVPlay
   *  available — false in `vite dev` against a desktop browser.
   *  Player UI should branch on this and fall back to HTML5
   *  `<video>` when needed. */
  available(): boolean {
    return this.api !== null;
  }

  open(source: PlaySource, handlers: PlayHandlers = {}): void {
    if (!this.api) return;
    // Remember the source/handlers + reset the retry budget so a
    // transient onerror can re-open exactly this session once.
    this.lastSource = source;
    this.lastHandlers = handlers;
    this.retried = false;
    this.reopen(source, handlers);
  }

  /** Internal (re)open. Shared by the public open() and the one-shot
   *  transient-error retry so both build the identical session. */
  private reopen(source: PlaySource, handlers: PlayHandlers): void {
    if (!this.api) return;
    this.pendingSeekMs = source.startMs && source.startMs > 0 ? source.startMs : 0;
    const url = source.bearer
      ? `${source.url}${source.url.includes('?') ? '&' : '?'}token=${encodeURIComponent(source.bearer)}`
      : source.url;

    // Defensive close — prepareAsync misbehaves if a prior session
    // is still bound (e.g., switching audio tracks). Matches
    // Jellyfin's fork (extras/avplayVideoPlayer.js:614).
    try { this.api.close(); } catch { /* idle is fine */ }

    this.api.open(url);

    // Buffering ceiling first (Jellyfin's order). 60 s gives slow-
    // start transcodes room to deliver the first segment without the
    // firmware giving up early.
    try {
      (this.api as unknown as { setTimeoutForBuffering?: (s: number) => void })
        .setTimeoutForBuffering?.(60);
    } catch { /* older firmware lacks this — fall back to default */ }

    this.api.setListener({
      oncurrentplaytime: (ms) => handlers.onProgress?.(ms, this.api!.getDuration()),
      onstreamcompleted: () => handlers.onEnded?.(),
      // Rebuffering: re-show the loading overlay (distinct from a hard
      // stall — these fire as a pair around a mid-stream refill).
      onbufferingstart: () => handlers.onBufferingStart?.(),
      onbufferingprogress: (percent) => handlers.onBufferingProgress?.(percent),
      onbufferingcomplete: () => {
        // Apply a deferred resume seek now that real data is buffered —
        // seekTo() before the first decoded frame is unreliable on
        // Samsung firmware (snaps to 0 / no-ops). Fire-and-forget once.
        if (this.pendingSeekMs > 0) {
          const target = this.pendingSeekMs;
          this.pendingSeekMs = 0;
          try { this.api!.seekTo(target); } catch { /* best-effort */ }
        }
        handlers.onBufferingComplete?.();
      },
      onerror: (e) => this.handleError(e)
    });

    // Position AVPlay's hardware overlay where the anchor `<object>`
    // element sits in the DOM. Element-offset based (not hardcoded
    // 1920×1080) so the rect matches on 4K panels whose webview runs
    // at native resolution. Matches Jellyfin's fork at line 624.
    this.api.setDisplayRect(
      source.anchor.offsetLeft,
      source.anchor.offsetTop,
      source.anchor.offsetWidth,
      source.anchor.offsetHeight,
    );

    // ADAPTIVE_INFO carries the streaming-mode hint AVPlay uses to
    // pick its demuxer. Most firmware infers from the URL suffix
    // (.m3u8 vs .mpd), but explicit hints make startup ~50–100 ms
    // faster and avoid the rare misclassification on tunneled URLs.
    this.api.setStreamingProperty(
      'ADAPTIVE_INFO',
      source.streamingMode ? `STREAMING_FORMAT=${source.streamingMode}` : '',
    );
    // Allocate the 4K decode pipeline for HEVC 2160p sources. Safe
    // no-op on 1080p (firmware ignores). Matches Samsung's
    // TVDemoAvPlayer reference.
    try {
      this.api.setStreamingProperty('SET_MODE_4K', 'TRUE');
    } catch { /* older firmware lacks this property — ignore */ }

    this.api.prepareAsync(
      () => {
        this.prepared = true;
        // Don't seek here — the first frame isn't decoded yet and
        // seekTo() is unreliable pre-frame on Samsung firmware. play()
        // first; the deferred startMs is applied on the first
        // onbufferingcomplete (see setListener above).
        this.api!.play();
      },
      // prepare failures are routed through the same transient-retry
      // path as runtime onerror — a flaky first segment often succeeds
      // on a single re-prepare.
      (e) => this.handleError(e)
    );
  }

  /** Single best-effort recovery for a transient AVPlay error: close,
   *  re-open, and re-prepare the same source exactly once. If we've
   *  already retried this session (or have nothing to retry), the error
   *  is surfaced to the caller as fatal. */
  private handleError(e: unknown): void {
    const msg = typeof e === 'string' ? e : JSON.stringify(e);
    if (!this.retried && this.lastSource) {
      this.retried = true;
      const source = this.lastSource;
      const handlers = this.lastHandlers;
      try {
        this.api?.stop();
      } catch { /* may not be prepared — ignore */ }
      this.prepared = false;
      try {
        this.reopen(source, handlers);
        return;
      } catch {
        // Re-open itself threw — fall through to the fatal path.
      }
    }
    this.lastHandlers.onError?.(msg);
  }

  /** Pause. Returns true only when the firmware actually paused (the
   *  session is prepared) so callers don't flip UI state on a no-op
   *  guard-fail. */
  pause(): boolean {
    if (!this.prepared) return false;
    try {
      this.api?.pause();
      return true;
    } catch {
      return false;
    }
  }

  /** Resume. Returns true only when playback actually resumed — see
   *  pause() for the rationale. */
  resume(): boolean {
    if (!this.prepared) return false;
    try {
      this.api?.play();
      return true;
    } catch {
      return false;
    }
  }

  seekTo(positionMs: number): void {
    if (this.prepared) this.api?.seekTo(positionMs);
  }

  /** True when a session is prepared (decoder bound, play() valid). */
  isPrepared(): boolean {
    return this.prepared;
  }

  /** AVPlay's own TEXT (subtitle) tracks, in AVPlay index order. The
   *  Nth entry here is the Nth subtitle the file exposes to AVPlay —
   *  use this index with selectTextTrack(), NOT the server's
   *  subtitle_streams file-stream index (the two numberings differ). */
  textTracks(): AvTrackInfo[] {
    if (!this.prepared || !this.api?.getTotalTrackInfo) return [];
    try {
      return (this.api.getTotalTrackInfo() ?? []).filter((t) => t.type === 'TEXT');
    } catch {
      return [];
    }
  }

  /** Select an AVPlay TEXT track by its AVPlay index (not file-stream
   *  index). Disabling is firmware-dependent; callers pass the index
   *  resolved via textTracks(). */
  selectTextTrack(avplayIndex: number): void {
    if (!this.prepared) return;
    try {
      this.api?.setSelectTrack?.('TEXT', avplayIndex);
    } catch {
      /* firmware-quirk fallback — silent */
    }
  }

  /** App backgrounded: tear the decoder down so it isn't held while
   *  hidden (resume otherwise comes back black/stale). The last source
   *  + the live position are captured so restore() can re-open exactly
   *  where we left off. Returns true if there was an active session to
   *  suspend. */
  suspend(): boolean {
    if (!this.api || !this.lastSource || !this.prepared) return false;
    // Snapshot the resume point before close() clears state.
    const pos = this.currentMs();
    const resumeSource: PlaySource = {
      ...this.lastSource,
      startMs: pos > 0 ? pos : (this.lastSource.startMs ?? 0),
    };
    const resumeHandlers = this.lastHandlers;
    this.close(); // clears lastSource — re-stash for restore() below
    this.suspendedSource = resumeSource;
    this.suspendedHandlers = resumeHandlers;
    return true;
  }

  /** App foregrounded: re-open + re-prepare the suspended source at the
   *  position it was suspended at. No-op if nothing was suspended. */
  restore(): boolean {
    if (!this.api || !this.suspendedSource) return false;
    const source = this.suspendedSource;
    const handlers = this.suspendedHandlers;
    this.suspendedSource = null;
    this.suspendedHandlers = {};
    // The anchor <object> may have been unmounted while suspended —
    // routes that render it conditionally (live TV's mode==='playing'
    // block) or that tore the page down before the foreground event
    // leave a detached node whose offsetLeft/Width are all 0. Re-opening
    // against it composites the video to a 0×0 rect (black screen), so
    // bail instead; the page re-opens its own session when re-entered.
    if (!document.body.contains(source.anchor) || source.anchor.offsetWidth === 0) {
      return false;
    }
    this.open(source, handlers);
    return true;
  }

  currentMs(): number {
    return this.api?.getCurrentTime() ?? 0;
  }

  durationMs(): number {
    return this.api?.getDuration() ?? 0;
  }

  state(): string {
    try {
      return this.api?.getState() ?? 'NO_API';
    } catch {
      return 'EXC';
    }
  }

  close(): void {
    if (!this.api) return;
    // Drop the retry source first so a late onerror fired during/after
    // teardown can't re-open a session we're tearing down.
    this.lastSource = null;
    this.lastHandlers = {};
    this.retried = false;
    this.pendingSeekMs = 0;
    try {
      this.api.stop();
    } catch {
      // AVPlay throws if called before prepare(). Ignore.
    }
    this.api.close();
    this.prepared = false;
  }
}

export const avplay = new AvPlay();
