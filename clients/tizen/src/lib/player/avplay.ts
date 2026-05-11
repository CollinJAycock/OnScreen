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
}

/** Singleton wrapper around webapis.avplay. Returns no-op stubs in
 *  non-Tizen environments (browser dev) so the same import works
 *  during `vite dev` against a desktop browser. */
export class AvPlay {
  private api: AvPlayApi | null;
  private prepared = false;

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
      onerror: (e) => handlers.onError?.(typeof e === 'string' ? e : JSON.stringify(e))
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
        if (source.startMs && source.startMs > 0) {
          this.api!.seekTo(source.startMs);
        }
        this.api!.play();
      },
      (e) => handlers.onError?.(typeof e === 'string' ? e : JSON.stringify(e))
    );
  }

  pause(): void {
    if (this.prepared) this.api?.pause();
  }

  resume(): void {
    if (this.prepared) this.api?.play();
  }

  seekTo(positionMs: number): void {
    if (this.prepared) this.api?.seekTo(positionMs);
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
