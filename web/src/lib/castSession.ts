// Chromecast sender: the SDK glue for one cast to Google's Default Media
// Receiver. The decisions (which profile, direct or HLS, the LOAD payload,
// timeline math, how a seek lands) are the pure helpers in $lib/cast; this
// class sequences the server calls and the Cast SDK around them:
//
//   1. playback-decision with the Chromecast's capability profile (not this
//      browser's) → a plan: refuse, cast the file directly, or HLS.
//   2. HLS: transcode-start with the same profile, video_copy when the
//      decision lets the video through, position_ms = where to start; read
//      the playlist once (it blocks until segment 0 exists, so the receiver
//      never waits on it, and it is the authority on the segment container
//      and on whether the stream is a whole-title ABR ladder).
//   3. LOAD on the receiver; a 4K-profile load the receiver can't play is
//      retried once with the HD profile.
//   4. While it plays: report progress as the Chromecast (resume, Now
//      Playing), map seeks (receiver seek or a new session), and on the
//      end stop the server session.
//
// The receiver's segment fetches keep the server session alive (each one
// stamps its activity), and so do these progress reports while it's paused.
//
// `w` is the window holding the Cast SDK (window.cast.framework,
// window.chrome.cast) — injected so tests can drive a fake one.

import {
  CAST_SUBTITLE_TRACK_ID,
  CHROMECAST_CAPS,
  absoluteUrl,
  buildCastHlsMediaInfo,
  buildCastMediaInfo,
  buildCastStartBody,
  castClientName,
  castContentTime,
  castLoadStartSec,
  castProfileForDevice,
  castProgressDecision,
  castSubtitleTrack,
  fallbackCastProfile,
  planCast,
  planCastSeek,
  sniffHlsPlaylist,
  type CastMediaInfo,
  type CastPlan,
  type CastProfile,
  type CastSubtitleChoice,
  type CastableFile,
  type CastableItem,
  type HlsSegmentFormat,
} from './cast';
import type { PlaybackDecision, TranscodeSession, TranscodeStartBody } from './api';

/** The server calls a cast makes. The page wires them to transcodeApi /
 *  itemApi (decideFor / startFor send the Chromecast's caps header). */
export interface CastControllerDeps {
  decide(itemId: string, caps: string, fileId: string): Promise<PlaybackDecision>;
  start(itemId: string, caps: string, body: TranscodeStartBody): Promise<TranscodeSession>;
  stop(sessionId: string, token: string): Promise<unknown>;
  progress(
    itemId: string,
    positionMs: number,
    durationMs: number,
    state: 'playing' | 'paused' | 'stopped',
    decision: 'directPlay' | 'remux' | 'transcode',
    clientName: string,
  ): Promise<unknown>;
  /** GET a same-origin URL as text (the session playlist). */
  fetchText(url: string, signal: AbortSignal): Promise<string>;
  /** The purpose=asset token for the poster URL the receiver loads (it has
   *  no cookies, and /artwork needs auth). Absent or null: no token. */
  assetToken?(): string | null;
}

export interface CastStartOptions {
  item: CastableItem;
  file: CastableFile;
  /** Content position to start at, seconds. */
  positionSec: number;
  /** The title's length, seconds (progress reports, seek clamping). */
  durationSec: number;
  /** Selected audio track (counting audio streams); undefined = default. */
  audioStreamIndex?: number;
  /** Text subtitle to side-load, if one is selected. */
  subtitle?: CastSubtitleChoice | null;
  /** Image subtitle (absolute stream index) burned into a transcode. */
  burnStreamIndex?: number | null;
}

export interface CastState {
  /** Content position, seconds. */
  positionSec: number;
  paused: boolean;
  buffering: boolean;
  /** The receiver played to the end. */
  ended: boolean;
}

/** How long the sender waits for a session's playlist (the server holds
 *  the request up to 60 s for segment 0). */
const PLAYLIST_WAIT_MS = 75_000;
/** Progress heartbeat while casting. */
const PROGRESS_INTERVAL_MS = 10_000;

function isBadRequest(e: unknown): boolean {
  return typeof e === 'object' && e !== null && (e as { status?: unknown }).status === 400;
}

class ReceiverLoadError extends Error {
  constructor(readonly receiverError: unknown) {
    super('receiver load failed');
  }
}

export class CastController {
  private opts: CastStartOptions | null = null;
  private profile: CastProfile = 'hd';
  private plan: CastPlan | null = null;
  private hls: { sid: string; token: string } | null = null;
  private offsetSec = 0;
  private fullTimeline = true;
  private gen = 0;
  private loading = false;
  private retried = false;
  private playbackStarted = false;
  private ended = false;
  private lastContentSec = 0;
  private lastPaused = false;
  private clientName = 'Chromecast';
  private player: any = null;
  private rpc: any = null;
  private media: any = null;
  private progressTimer: ReturnType<typeof setInterval> | null = null;
  private readonly onChange = () => this.handleChange();

  constructor(
    private readonly w: any,
    private readonly origin: string,
    private readonly deps: CastControllerDeps,
    private readonly onState: (s: CastState) => void,
    private readonly onError: (message: string) => void,
  ) {}

  private session(): any {
    return this.w.cast.framework.CastContext.getInstance().getCurrentSession();
  }

  /** Starts the cast on the connected receiver. Resolves true once the
   *  receiver accepted a LOAD; false after a refusal or failure (onError has
   *  said why). */
  async start(opts: CastStartOptions): Promise<boolean> {
    const session = this.session();
    if (!session) return false;
    this.opts = opts;
    this.retried = false;
    this.ended = false;
    this.lastContentSec = opts.positionSec;
    const device = session.getCastDevice?.() ?? {};
    // The Web Sender SDK has no model name; a future one might.
    this.clientName = castClientName(device.friendlyName);
    this.profile = castProfileForDevice(device.modelName ?? device.friendlyName);
    const fw = this.w.cast.framework;
    this.player = new fw.RemotePlayer();
    this.rpc = new fw.RemotePlayerController(this.player);
    this.rpc.addEventListener(fw.RemotePlayerEventType.ANY_CHANGE, this.onChange);
    this.progressTimer = setInterval(() => this.heartbeat(), PROGRESS_INTERVAL_MS);
    return this.loadAt(opts.positionSec, this.profile);
  }

  /** Seeks the cast to content time targetSec: on the receiver when the
   *  stream holds that point, else by starting a new session there. */
  async seek(targetSec: number): Promise<void> {
    if (!this.opts || !this.player || this.loading) return;
    const p = this.player;
    const live = p.liveSeekableRange;
    const seekableEnd =
      live && Number.isFinite(live.end) ? live.end
        : Number.isFinite(p.duration) && p.duration > 0 ? p.duration
          : null;
    const plan = planCastSeek(targetSec, {
      fullTimeline: this.fullTimeline,
      offsetSec: this.offsetSec,
      seekableEndSec: seekableEnd,
      durationSec: this.opts.durationSec,
    });
    if (plan.kind === 'receiver') {
      p.currentTime = plan.streamTime;
      this.rpc.seek();
      this.lastContentSec = this.offsetSec + plan.streamTime;
      this.reportNow(p.isPaused ? 'paused' : 'playing');
      this.emit();
      return;
    }
    await this.loadAt(plan.positionMs / 1000, this.profile);
  }

  togglePlay(): void {
    this.rpc?.playOrPause();
  }

  /** The content position the cast is at, seconds. */
  get positionSec(): number {
    return this.lastContentSec;
  }

  /** Ends this cast's server side: reports the stop (the resume point) and
   *  stops the HLS session. The receiver itself is the caller's to end.
   *  Resolves with the last content position. */
  async end(): Promise<number> {
    this.gen++; // drop any load in flight
    const pos = this.lastContentSec;
    const opts = this.opts;
    const plan = this.plan;
    this.teardown();
    if (opts && plan) {
      await this.deps
        .progress(opts.item.id, Math.round(pos * 1000), Math.round(opts.durationSec * 1000), 'stopped',
          castProgressDecision(plan), this.clientName)
        .catch(() => {});
    }
    await this.stopHls();
    this.opts = null;
    return pos;
  }

  /** Lets go of the cast without stopping it (the page is going away): the
   *  receiver keeps playing, its segment fetches keep the session alive. */
  detach(): void {
    this.gen++;
    this.reportNow(this.lastPaused ? 'paused' : 'playing');
    this.teardown();
    this.opts = null;
  }

  // ── internals ──────────────────────────────────────────────────────

  private teardown() {
    if (this.progressTimer) clearInterval(this.progressTimer);
    this.progressTimer = null;
    if (this.rpc) {
      try {
        this.rpc.removeEventListener(this.w.cast.framework.RemotePlayerEventType.ANY_CHANGE, this.onChange);
      } catch {
        /* SDK gone */
      }
    }
  }

  private async stopHls() {
    const hls = this.hls;
    this.hls = null;
    if (hls) await this.deps.stop(hls.sid, hls.token).catch(() => {});
  }

  private async loadAt(positionSec: number, profile: CastProfile): Promise<boolean> {
    const o = this.opts;
    const session = this.session();
    if (!o || !session) return false;
    const gen = ++this.gen;
    this.loading = true;
    this.playbackStarted = false;
    this.profile = profile;
    this.lastContentSec = positionSec;
    this.emit();
    const caps = CHROMECAST_CAPS[profile];
    const prev = this.hls;
    let started: TranscodeSession | null = null;
    const stopStarted = () => {
      if (started) void this.deps.stop(started.session_id, started.token).catch(() => {});
    };
    try {
      const verdict = await this.deps.decide(o.item.id, caps, o.file.id);
      if (gen !== this.gen) return false;
      const plan = planCast(o.file, verdict.decision, {
        audioTrackSelected: (o.audioStreamIndex ?? 0) > 0,
        burnIn: o.burnStreamIndex != null,
      });
      if (plan.mode === 'refuse') {
        this.onError(plan.message);
        return false;
      }

      let info: CastMediaInfo;
      let loadTime: number;
      let hls: { sid: string; token: string } | null = null;
      let offsetSec = 0;
      let fullTimeline = true;
      if (plan.mode === 'direct') {
        info = buildCastMediaInfo(o.item, o.file, this.origin, this.deps.assetToken?.() ?? null)!;
        const track = o.subtitle ? castSubtitleTrack(this.origin, o.subtitle, o.file.stream_token, 0) : null;
        if (track) info.tracks = [track];
        loadTime = positionSec;
      } else {
        // An explicit height (never Auto): a cast session is never an ABR
        // ladder. See buildCastStartBody.
        const body: TranscodeStartBody = buildCastStartBody({
          fileId: o.file.id,
          profile,
          file: o.file,
          positionSec,
          videoCopy: plan.videoCopy,
          audioStreamIndex: o.audioStreamIndex,
          burnStreamIndex: o.burnStreamIndex,
        });
        try {
          started = await this.deps.start(o.item.id, caps, body);
        } catch (e) {
          // A server whose TRANSCODE_MAX_HEIGHT is under the 4K profile's
          // 2160 refuses that height (400): ask again at 1080.
          if (!isBadRequest(e) || body.height <= 1080) throw e;
          started = await this.deps.start(o.item.id, caps, { ...body, height: 1080 });
        }
        if (gen !== this.gen) {
          stopStarted();
          return false;
        }
        hls = { sid: started.session_id, token: started.token };
        const playlistUrl = absoluteUrl(this.origin, started.playlist_url);
        const probe = await this.probePlaylist(playlistUrl, started.segment_format);
        if (gen !== this.gen) {
          stopStarted();
          return false;
        }
        offsetSec = started.start_offset_sec ?? body.position_ms / 1000;
        fullTimeline = probe.fullTimeline;
        loadTime = castLoadStartSec(positionSec, offsetSec, started.seg0_audio_gap_sec ?? 0);
        const track = o.subtitle
          ? castSubtitleTrack(this.origin, o.subtitle, o.file.stream_token, offsetSec * 1000)
          : null;
        info = buildCastHlsMediaInfo(o.item, o.file, {
          origin: this.origin,
          playlistUrl,
          segmentFormat: probe.format,
          subtitle: track,
          assetToken: this.deps.assetToken?.() ?? null,
        });
      }

      try {
        await session.loadMedia(this.buildRequest(info, loadTime));
      } catch (e) {
        throw new ReceiverLoadError(e);
      }
      if (gen !== this.gen) {
        stopStarted();
        return false;
      }
      this.plan = plan;
      this.hls = hls;
      this.offsetSec = offsetSec;
      this.fullTimeline = fullTimeline;
      this.media = session.getMediaSession?.() ?? null;
      this.loading = false;
      // A seek's restart replaced the previous session: transcode-start
      // already retired it server-side (one session per user and title);
      // this is the explicit stop.
      if (prev && prev.sid !== hls?.sid) void this.deps.stop(prev.sid, prev.token).catch(() => {});
      this.reportNow('playing');
      return true;
    } catch (e) {
      if (gen !== this.gen) return false;
      stopStarted();
      const fallback = fallbackCastProfile(profile);
      if (e instanceof ReceiverLoadError && fallback && !this.retried) {
        // The receiver couldn't play what the 4K profile produced (an older
        // device behind a 4K-sounding name, an HEVC it won't decode): once
        // more with the HD profile.
        this.retried = true;
        return this.loadAt(positionSec, fallback);
      }
      this.loading = false;
      this.emit();
      this.onError(
        e instanceof ReceiverLoadError
          ? "The Chromecast couldn't play this title."
          : e instanceof Error && e.message
            ? e.message
            : "Couldn't start casting.",
      );
      return false;
    }
  }

  /** Reads the session playlist: its segment container (the receiver must
   *  be told) and whether it's a whole-title ladder (a master playlist).
   *  The start response's segment_format is the fallback. */
  private async probePlaylist(
    url: string,
    hint: HlsSegmentFormat | undefined,
  ): Promise<{ format: HlsSegmentFormat; fullTimeline: boolean }> {
    const ctl = new AbortController();
    const timer = setTimeout(() => ctl.abort(), PLAYLIST_WAIT_MS);
    try {
      const sniff = sniffHlsPlaylist(await this.deps.fetchText(url, ctl.signal));
      if (sniff.kind === 'media') return { format: sniff.format ?? hint ?? 'ts', fullTimeline: false };
      let format: HlsSegmentFormat | null = null;
      if (sniff.variants[0]) {
        // A rung playlist is predicted; reading it starts no encode.
        const rung = sniffHlsPlaylist(await this.deps.fetchText(new URL(sniff.variants[0], url).toString(), ctl.signal));
        if (rung.kind === 'media') format = rung.format;
      }
      return { format: format ?? hint ?? 'ts', fullTimeline: true };
    } catch {
      // Unread: trust the start response, and restart on any seek outside
      // what the receiver reports as seekable.
      return { format: hint ?? 'ts', fullTimeline: false };
    } finally {
      clearTimeout(timer);
    }
  }

  private buildRequest(info: CastMediaInfo, currentTime: number): any {
    const m = this.w.chrome.cast.media;
    const mi = new m.MediaInfo(info.contentId, info.contentType);
    if (info.contentUrl) mi.contentUrl = info.contentUrl;
    mi.streamType = m.StreamType?.BUFFERED ?? 'BUFFERED';
    const meta = new m.GenericMediaMetadata();
    meta.title = info.metadata.title;
    if (info.metadata.subtitle) meta.subtitle = info.metadata.subtitle;
    if (info.metadata.images?.length) {
      meta.images = info.metadata.images.map((i) => new this.w.chrome.cast.Image(i.url));
    }
    mi.metadata = meta;
    if (info.duration) mi.duration = info.duration;
    mi.customData = info.customData;
    if (info.hlsSegmentFormat) mi.hlsSegmentFormat = info.hlsSegmentFormat;
    if (info.hlsVideoSegmentFormat) mi.hlsVideoSegmentFormat = info.hlsVideoSegmentFormat;
    if (info.tracks?.length) {
      mi.tracks = info.tracks.map((t) => {
        const tr = new m.Track(t.trackId, m.TrackType?.TEXT ?? 'TEXT');
        tr.trackContentId = t.trackContentId;
        tr.trackContentType = t.trackContentType;
        tr.subtype = m.TextTrackType?.SUBTITLES ?? 'SUBTITLES';
        tr.name = t.name;
        tr.language = t.language;
        return tr;
      });
    }
    const req = new m.LoadRequest(mi);
    req.currentTime = currentTime;
    req.autoplay = true;
    if (info.tracks?.length) req.activeTrackIds = [CAST_SUBTITLE_TRACK_ID];
    return req;
  }

  private handleChange() {
    const p = this.player;
    if (!p || !this.opts || this.loading) return;
    const state = p.playerState as string | undefined;
    if (state === 'PLAYING') this.playbackStarted = true;
    if (p.isMediaLoaded && state !== 'IDLE') {
      this.lastContentSec = castContentTime(this.offsetSec, p.currentTime);
    }
    if (p.isPaused !== this.lastPaused && p.isMediaLoaded) {
      this.lastPaused = !!p.isPaused;
      this.reportNow(this.lastPaused ? 'paused' : 'playing');
    }
    if (state === 'IDLE' && this.plan) this.handleIdle();
    this.emit();
  }

  private handleIdle() {
    const reason = this.media?.idleReason as string | undefined;
    if (reason === 'FINISHED' && !this.ended) {
      this.ended = true;
      if (this.opts) this.lastContentSec = this.opts.durationSec;
      this.reportNow('stopped');
      void this.stopHls();
      return;
    }
    if (reason === 'ERROR') {
      const fallback = fallbackCastProfile(this.profile);
      if (fallback && !this.retried && !this.playbackStarted) {
        // Loaded but never played: the receiver rejected the stream itself.
        this.retried = true;
        void this.loadAt(this.lastContentSec, fallback);
        return;
      }
      this.onError('The Chromecast stopped playing: it hit an error in the stream.');
    }
  }

  private heartbeat() {
    if (!this.player?.isMediaLoaded || this.loading || this.ended) return;
    this.reportNow(this.player.isPaused ? 'paused' : 'playing');
  }

  private reportNow(state: 'playing' | 'paused' | 'stopped') {
    const o = this.opts;
    if (!o || !this.plan) return;
    void this.deps
      .progress(o.item.id, Math.round(this.lastContentSec * 1000), Math.round(o.durationSec * 1000), state,
        castProgressDecision(this.plan), this.clientName)
      .catch(() => {});
  }

  private emit() {
    const p = this.player;
    this.onState({
      positionSec: this.lastContentSec,
      paused: p ? !!p.isPaused : true,
      buffering: this.loading || p?.playerState === 'BUFFERING',
      ended: this.ended,
    });
  }
}
