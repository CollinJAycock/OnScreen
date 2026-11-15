// Tizen AVPlay: the firmware API's types. The player is
// lib/player/avplayMedia (the media-element surface the watch page drives,
// one per page that plays video: the player, Live TV); this file held a
// second, older wrapper around the same global player as well, whose last
// caller (the layout's background suspend / restore) did nothing, since
// nothing opened through it.
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

export interface AvPlayApi {
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

export interface AvTrackInfo {
  type: string;
  index: number;
  extra_info?: string;
}

export interface AvPlayListener {
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
