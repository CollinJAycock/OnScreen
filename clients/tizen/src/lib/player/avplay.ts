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
    this.api.open(url);
    this.api.setStreamingProperty('ADAPTIVE_INFO', '');
    this.api.setListener({
      oncurrentplaytime: (ms) => handlers.onProgress?.(ms, this.api!.getDuration()),
      onstreamcompleted: () => handlers.onEnded?.(),
      onerror: (e) => handlers.onError?.(typeof e === 'string' ? e : JSON.stringify(e))
    });
    // 1920×1080 fullscreen — overlay sits behind the SvelteKit
    // webview which we make fully transparent in the +layout
    // CSS while the player is mounted.
    this.api.setDisplayRect(0, 0, 1920, 1080);
    this.api.setDisplayMethod('PLAYER_DISPLAY_MODE_LETTER_BOX');
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
