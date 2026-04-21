<script lang="ts">
  import { onMount, onDestroy, tick } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { itemApi, mediaApi, libraryApi, transcodeApi, userApi, subtitleApi, type ItemDetail, type ChildItem, type ItemFile, type MediaItem, type MatchCandidate, type AudioStream, type SubtitleStream, type ExternalSubtitle, type SubtitleSearchResult } from '$lib/api';
  import Hls from 'hls.js';
  import PlaylistPicker from '$lib/components/PlaylistPicker.svelte';

  let showPlaylistPicker = false;

  $: id = $page.params.id!;

  let mounted = false;
  let prevId = '';

  let item: ItemDetail | null = null;
  let siblings: ChildItem[] = []; // other episodes in the same season
  let loading = true;
  let error = '';
  let tonemapWarning = '';

  // Video element reference
  let videoEl: HTMLVideoElement;
  let containerEl: HTMLDivElement;

  // Playback state
  let paused = true;
  let currentTime = 0;
  let duration = 0;
  let volume = 1;
  let muted = false;
  let buffered = 0; // 0-1
  let fullscreen = false;
  let ended = false;

  // Controls visibility
  let showControls = true;
  let hideTimer: ReturnType<typeof setTimeout> | null = null;

  // Seeking via drag
  let seeking = false;
  let seekBarEl: HTMLDivElement;

  // Trickplay (seekbar thumbnail previews). Cues come from a WebVTT index
  // served at /trickplay/{id}/index.vtt; each cue points at a region of a
  // sprite sheet via #xywh=x,y,w,h. When unavailable, hover preview falls
  // back to a time label only.
  type TrickplayCue = {
    start: number; // seconds
    end: number;
    url: string; // resolved sprite URL
    x: number;
    y: number;
    w: number;
    h: number;
  };
  let trickplayCues: TrickplayCue[] = [];
  let trickplayBaseURL = ''; // e.g. /trickplay/<itemID>/
  let hoverVisible = false;
  let hoverX = 0; // px from left of seek bar
  let hoverTime = 0; // seconds in content time

  // Progress save timer
  let progressTimer: ReturnType<typeof setInterval> | null = null;

  // Quality picker — width is used for filtering (stable across aspect ratios),
  // height is sent to the backend for the actual transcode resolution.
  type QualityOption = { label: string; height: number; width: number };
  const qualityOptions: QualityOption[] = [
    { label: 'Auto (Direct Play)', height: 0,    width: 0 },
    { label: '4K (2160p)',         height: 2160, width: 3840 },
    { label: '1080p',              height: 1080, width: 1920 },
    { label: '720p',               height: 720,  width: 1280 },
    { label: '480p',               height: 480,  width: 854 },
    { label: '360p',               height: 360,  width: 640 },
  ];
  let selectedQuality: QualityOption = qualityOptions[0];
  let showQualityMenu = false;

  // Subtitle picker
  let showSubtitleMenu = false;
  // A picked subtitle is either an embedded stream (served via
  // /media/subtitles/{fileId}/{streamIndex}) or an external one (served via
  // /media/external-subtitles/{subId}). The unified shape lets the renderer
  // load cues from one URL without caring where it came from.
  type PickedSubtitle = {
    key: string;        // stable id for active highlight
    label: string;
    language: string;
    forced: boolean;
    url: string;
    origin: 'embedded' | 'external';
  };
  let selectedSubtitle: PickedSubtitle | null = null;
  // Subtitle font size: 'small' | 'medium' | 'large'
  const subtitleSizes = ['small', 'medium', 'large'] as const;
  type SubtitleSize = typeof subtitleSizes[number];
  let subtitleSize: SubtitleSize = (typeof localStorage !== 'undefined' && localStorage.getItem('subtitle_size') as SubtitleSize) || 'medium';
  // Subtitle delay in seconds (positive = subs appear later, negative = earlier).
  let subtitleDelay = 0;

  // Online subtitle search modal state. openSubtitleSearch triggers a search
  // using the item title (and S/E for episodes). Users can refine the query,
  // pick a result, and download — the result is appended to external_subtitles
  // on the file and auto-selected.
  let showSubtitleSearch = false;
  let subtitleSearchQuery = '';
  let subtitleSearchLang = 'en';
  let subtitleSearchResults: SubtitleSearchResult[] = [];
  let subtitleSearchLoading = false;
  let subtitleSearchError = '';
  let subtitleDownloadingId: number | null = null;

  // Audio track picker
  let showAudioMenu = false;
  let selectedAudioIndex = -1; // -1 = default (first track)

  // Chapter menu
  let showChapterMenu = false;

  // Text-based subtitle codecs that ffmpeg can convert to WebVTT.
  const textSubCodecs = new Set(['srt', 'subrip', 'ass', 'ssa', 'mov_text', 'webvtt', 'text']);

  // ── JavaScript subtitle renderer ─────────────────────────────────────────
  // Native <track> elements are unreliable with HLS.js/MSE. Instead, we fetch
  // the WebVTT, parse cues, and render them in a <div> overlay synced to
  // currentTime via onTimeUpdate.
  interface SubCue { start: number; end: number; text: string; }
  let activeCues: SubCue[] = [];
  let allCues: SubCue[] = [];
  let subtitleFetchId = 0; // dedup concurrent fetches

  // Load cues when subtitle selection changes.
  $: if (selectedSubtitle) {
    loadSubtitleCues(selectedSubtitle.url);
  } else {
    allCues = [];
    activeCues = [];
  }

  async function loadSubtitleCues(url: string) {
    const fetchId = ++subtitleFetchId;
    try {
      const resp = await fetch(url);
      if (!resp.ok || fetchId !== subtitleFetchId) return;
      const vtt = await resp.text();
      allCues = parseWebVTT(vtt);
    } catch {
      allCues = [];
    }
  }

  function openSubtitleSearch() {
    if (!item) return;
    subtitleSearchQuery = item.title;
    subtitleSearchResults = [];
    subtitleSearchError = '';
    showSubtitleSearch = true;
    runSubtitleSearch();
  }

  async function runSubtitleSearch() {
    if (!item) return;
    subtitleSearchLoading = true;
    subtitleSearchError = '';
    try {
      const { items } = await subtitleApi.search(item.id, {
        lang: subtitleSearchLang || undefined,
        query: subtitleSearchQuery || undefined,
      });
      subtitleSearchResults = items;
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : 'Search failed';
      subtitleSearchError = msg.toLowerCase().includes('not configured')
        ? 'OpenSubtitles is not configured. Ask an admin to set the API key.'
        : msg;
    } finally {
      subtitleSearchLoading = false;
    }
  }

  async function downloadSubtitle(res: SubtitleSearchResult) {
    if (!item?.files?.[0]) return;
    subtitleDownloadingId = res.provider_file_id;
    try {
      const ext = await subtitleApi.download(item.id, {
        file_id: item.files[0].id,
        provider_file_id: res.provider_file_id,
        language: res.language || subtitleSearchLang,
        title: res.release || res.file_name,
        hearing_impaired: res.hearing_impaired,
        rating: res.rating,
        download_count: res.download_count,
      });
      // Refresh the item so the new external sub appears in the picker.
      const fresh = await itemApi.get(item.id);
      item = fresh;
      // Auto-select the newly downloaded subtitle.
      const picked = (fresh.files?.[0]?.external_subtitles ?? [])
        .find(e => e.id === ext.id);
      if (picked) selectedSubtitle = externalToPicked(picked);
      showSubtitleSearch = false;
    } catch (e: unknown) {
      subtitleSearchError = e instanceof Error ? e.message : 'Download failed';
    } finally {
      subtitleDownloadingId = null;
    }
  }

  // escapeCueText escapes HTML special characters in a raw WebVTT cue and then
  // converts newlines to <br>. Cue text originates from third-party subtitle
  // files — it is not trusted, so we escape before any {@html} rendering to
  // prevent subtitle-driven XSS.
  function escapeCueText(raw: string): string {
    const escaped = raw
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#39;');
    return escaped.replace(/\n/g, '<br>');
  }

  function parseWebVTT(vtt: string): SubCue[] {
    const cues: SubCue[] = [];
    const blocks = vtt.split(/\n\n+/);
    for (const block of blocks) {
      const lines = block.trim().split('\n');
      for (let i = 0; i < lines.length; i++) {
        const match = lines[i].match(/^(\d{2}):(\d{2}):(\d{2})\.(\d{3})\s*-->\s*(\d{2}):(\d{2}):(\d{2})\.(\d{3})/);
        if (!match) {
          // Try MM:SS.mmm format (no hours)
          const m2 = lines[i].match(/^(\d{2}):(\d{2})\.(\d{3})\s*-->\s*(\d{2}):(\d{2})\.(\d{3})/);
          if (m2) {
            const start = parseInt(m2[1]) * 60 + parseInt(m2[2]) + parseInt(m2[3]) / 1000;
            const end = parseInt(m2[4]) * 60 + parseInt(m2[5]) + parseInt(m2[6]) / 1000;
            const text = lines.slice(i + 1).join('\n').trim();
            if (text) cues.push({ start, end, text });
          }
          continue;
        }
        const start = parseInt(match[1]) * 3600 + parseInt(match[2]) * 60 + parseInt(match[3]) + parseInt(match[4]) / 1000;
        const end = parseInt(match[5]) * 3600 + parseInt(match[6]) * 60 + parseInt(match[7]) + parseInt(match[8]) / 1000;
        const text = lines.slice(i + 1).join('\n').trim();
        if (text) cues.push({ start, end, text });
      }
    }
    return cues;
  }

  // Disable native text tracks to avoid double rendering.
  $: if (videoEl?.textTracks?.length) {
    for (let i = 0; i < videoEl.textTracks.length; i++) {
      videoEl.textTracks[i].mode = 'hidden';
    }
  }

  function embeddedToPicked(file: ItemFile, s: SubtitleStream): PickedSubtitle {
    return {
      key: `e:${s.index}`,
      label: s.title || s.language || `Track ${s.index}`,
      language: s.language || '',
      forced: s.forced,
      url: `/media/subtitles/${file.id}/${s.index}`,
      origin: 'embedded',
    };
  }
  function externalToPicked(ext: ExternalSubtitle): PickedSubtitle {
    return {
      key: `x:${ext.id}`,
      label: ext.title || ext.language || 'External',
      language: ext.language || '',
      forced: ext.forced,
      url: ext.url,
      origin: 'external',
    };
  }

  $: textSubtitles = (() => {
    const file = item?.files?.[0];
    if (!file) return [] as PickedSubtitle[];
    const embedded = (file.subtitle_streams ?? [])
      .filter(s => textSubCodecs.has(s.codec.toLowerCase()))
      .map(s => embeddedToPicked(file, s));
    const external = (file.external_subtitles ?? []).map(externalToPicked);
    return [...embedded, ...external];
  })();

  $: audioStreams = item?.files?.[0]?.audio_streams ?? [];

  // Skip the auto-seek in onVideoLoaded during quality switches
  let skipAutoSeek = false;

  // Audio codecs that browsers can decode natively.
  // Audio codecs browsers can decode natively in MP4/WebM containers.
  // AC-3, E-AC-3, DTS, TrueHD, ALAC, MP2, raw PCM are not reliably supported
  // in any browser — they must be transcoded to AAC.
  const browserAudioCodecs = new Set([
    'aac', 'mp3', 'opus', 'flac', 'vorbis',
  ]);
  // Containers browsers handle reliably for direct play (faststart MP4/MOV/WebM).
  const browserContainers = new Set(['mp4', 'webm', 'mov']);
  // Detect HEVC (H.265) playback support via MediaSource Extensions.
  // HLS.js transmuxes MPEG-TS → fMP4 before feeding MSE, so check mp4 not mp2t.
  let clientSupportsHEVC = false;
  try {
    clientSupportsHEVC = typeof MediaSource !== 'undefined' &&
      MediaSource.isTypeSupported('video/mp4; codecs="hvc1.1.6.L150.B0"');
  } catch { /* MSE unavailable */ }

  // Video codecs browsers can decode natively.
  const browserVideoCodecs = new Set(['h264', 'vp8', 'vp9', 'av1']);
  // Video codecs that can be stream-copied (remuxed) into MPEG-TS for HLS.js.
  // AV1 and VP8/VP9 are NOT here: MPEG-TS cannot carry AV1, and VP8/VP9 are
  // WebM-only — putting them in MPEG-TS produces an unplayable stream.
  const remuxableVideoCodecs = new Set(['h264']);

  // HEVC direct play / remux: if the browser can hardware-decode H.265,
  // treat it like H.264 — direct play from MP4, remux from MKV.
  if (clientSupportsHEVC) {
    browserVideoCodecs.add('hevc');
    browserVideoCodecs.add('h265');
    remuxableVideoCodecs.add('hevc');
    remuxableVideoCodecs.add('h265');
  }

  // HDR display detection — avoid direct play/remux of HDR content on SDR screens
  // (video plays but looks washed out without tonemapping).
  const clientSupportsHDR = typeof window !== 'undefined' &&
    window.matchMedia('(dynamic-range: high)').matches;

  /** True when the browser can play this file directly — compatible container + codecs + faststart. */
  function canDirectPlay(file: ItemFile | undefined): boolean {
    if (!file) return false;
    // HDR content on an SDR display needs tonemapping — can't direct play.
    if (file.hdr_type && !clientSupportsHDR) return false;
    const container = (file.container ?? '').toLowerCase();
    const videoCodec = (file.video_codec ?? '').toLowerCase();
    const audioCodec = (file.audio_codec ?? '').toLowerCase();
    if (!browserContainers.has(container)) return false;
    if (videoCodec && !browserVideoCodecs.has(videoCodec)) return false;
    if (audioCodec && !browserAudioCodecs.has(audioCodec)) return false;
    // Non-faststart MP4/MOV files have moov at the end — the browser must
    // fetch the tail of the file before playback can begin, causing silence
    // and buffering. Route these through the remux path instead.
    if (!file.faststart) return false;
    return true;
  }

  /** True when the video can be stream-copied (remuxed) into MPEG-TS HLS instead of re-encoded. */
  function canRemuxVideo(file: ItemFile | undefined): boolean {
    if (!file) return false;
    // HDR content on an SDR display needs tonemapping — can't remux.
    if (file.hdr_type && !clientSupportsHDR) return false;
    const videoCodec = (file.video_codec ?? '').toLowerCase();
    return remuxableVideoCodecs.has(videoCodec);
  }

  // HLS transcode state
  let hlsInstance: Hls | null = null;
  let transcodeSessionId: string | null = null;
  let transcodeToken: string | null = null;
  // Offset (seconds) of where the current HLS stream starts within the content.
  // videoEl.currentTime + hlsOffsetSec = real content position.
  let hlsOffsetSec = 0;
  // True while an HLS session is active. Needed to distinguish HLS-at-offset-0
  // from direct play (both have hlsOffsetSec === 0).
  let hlsActive = false;
  // PTS offset: some source files shift MPEG-TS timestamps (edit lists, chapter
  // tracks). videoEl.currentTime includes this offset, but subtitle timestamps
  // from the source file don't. Captured on first play to correct subtitle sync.
  let hlsPtsOffset = 0;
  // True when the current HLS session is a remux (video copy). Used to detect
  // when the browser can't decode the remuxed video and escalate to full transcode.
  let hlsIsRemux = false;

  // Reactive: available quality options filtered to source resolution.
  // Use width for filtering — it's a stable indicator of quality tier regardless
  // of aspect ratio (a 1920x800 scope movie is still "1080p" quality).
  // Allow 5% tolerance so files like 1918x796 still qualify as "1080p tier".
  // Hide "Auto (Direct Play)" when the file requires transcoding.
  $: sourceWidth = item?.files?.[0]?.resolution_w ?? 0;
  $: sourceFile = item?.files?.[0];
  $: canAuto = canDirectPlay(sourceFile) || canRemuxVideo(sourceFile);
  $: availableQualities = qualityOptions.filter(
    q => {
      if (q.height === 0) return canAuto;
      return sourceWidth === 0 || q.width <= sourceWidth * 1.05;
    }
  );

  $: nextEpisode = (() => {
    if (!item || item.type !== 'episode' || item.index == null) return null;
    return siblings.find(s => s.index != null && s.index === (item!.index! + 1)) ?? null;
  })();

  // Chapters from the source file (already parsed by ffprobe at scan time).
  $: chapters = item?.files?.[0]?.chapters ?? [];
  // The current chapter — based on content position (currentTime + hlsOffsetSec).
  $: contentTimeSec = currentTime + hlsOffsetSec;
  $: currentChapter = (() => {
    if (chapters.length === 0) return null;
    const tMs = contentTimeSec * 1000;
    return chapters.find(c => tMs >= c.start_ms && tMs < c.end_ms) ?? null;
  })();

  // Admin gate for the manual marker editor. Non-admin users see only the
  // Skip Intro/Skip Credits buttons; the editor panel is hidden entirely.
  let isAdmin = false;
  onMount(() => {
    try {
      const raw = typeof localStorage !== 'undefined' ? localStorage.getItem('onscreen_user') : null;
      if (raw) isAdmin = !!JSON.parse(raw)?.is_admin;
    } catch {}
  });

  // Intro/credits markers — episodes only; movies omit this field.
  $: markers = item?.markers ?? [];
  // The marker we're currently inside, if any. Intro wins over credits when
  // they overlap (shouldn't happen, but be deterministic).
  $: currentMarker = (() => {
    if (markers.length === 0) return null;
    const tMs = contentTimeSec * 1000;
    return (
      markers.find(m => m.kind === 'intro' && tMs >= m.start_ms && tMs < m.end_ms) ??
      markers.find(m => m.kind === 'credits' && tMs >= m.start_ms && tMs < m.end_ms) ??
      null
    );
  })();
  function skipMarker() {
    if (!currentMarker) return;
    seekToContentTime(currentMarker.end_ms / 1000);
  }

  // ── Admin manual marker editor ─────────────────────────────────────────────
  // Popover UI that lets an admin set/clear intro and credits markers on the
  // current episode. Partial edits (start set, end not yet set) are kept in
  // local state and only PUT to the API once both ends are valid.
  let showMarkerEditor = false;
  let markerError = '';
  let markerBusy = false;
  // Pending edits keyed by kind, keeping start and end in ms. Both must be set
  // (and end > start) before we hit the server.
  type PendingMarker = { startMs?: number; endMs?: number };
  let pending: { intro: PendingMarker; credits: PendingMarker } = { intro: {}, credits: {} };

  function findMarker(kind: 'intro' | 'credits') {
    return markers.find(m => m.kind === kind) ?? null;
  }

  function fmtMs(ms?: number): string {
    if (ms == null || !isFinite(ms)) return '—';
    const s = Math.max(0, Math.round(ms / 1000));
    const m = Math.floor(s / 60);
    return `${m.toString().padStart(2, '0')}:${(s % 60).toString().padStart(2, '0')}`;
  }

  function captureNow(kind: 'intro' | 'credits', edge: 'start' | 'end') {
    const ms = Math.max(0, Math.round(contentTimeSec * 1000));
    pending = {
      ...pending,
      [kind]: { ...pending[kind], [edge === 'start' ? 'startMs' : 'endMs']: ms },
    };
  }

  function currentStart(kind: 'intro' | 'credits'): number | undefined {
    return pending[kind].startMs ?? findMarker(kind)?.start_ms;
  }
  function currentEnd(kind: 'intro' | 'credits'): number | undefined {
    return pending[kind].endMs ?? findMarker(kind)?.end_ms;
  }

  async function saveMarker(kind: 'intro' | 'credits') {
    if (!item) return;
    const start = currentStart(kind);
    const end = currentEnd(kind);
    markerError = '';
    if (start == null || end == null) {
      markerError = 'Set both start and end before saving.';
      return;
    }
    if (end <= start) {
      markerError = 'End must be after start.';
      return;
    }
    markerBusy = true;
    try {
      const saved = await itemApi.upsertMarker(item.id, kind, start, end);
      const rest = (item.markers ?? []).filter(m => m.kind !== kind);
      item = { ...item, markers: [...rest, saved].sort((a, b) => a.start_ms - b.start_ms) };
      pending = { ...pending, [kind]: {} };
    } catch (e: unknown) {
      markerError = e instanceof Error ? e.message : 'Save failed';
    } finally {
      markerBusy = false;
    }
  }

  async function clearMarker(kind: 'intro' | 'credits') {
    if (!item) return;
    markerError = '';
    markerBusy = true;
    try {
      await itemApi.deleteMarker(item.id, kind);
      item = { ...item, markers: (item.markers ?? []).filter(m => m.kind !== kind) };
      pending = { ...pending, [kind]: {} };
    } catch (e: unknown) {
      markerError = e instanceof Error ? e.message : 'Delete failed';
    } finally {
      markerBusy = false;
    }
  }

  // Favorites: optimistically toggle, server is source of truth on next load.
  let favoriteBusy = false;
  async function toggleFavorite() {
    if (!item || favoriteBusy) return;
    favoriteBusy = true;
    const wasFavorite = item.is_favorite;
    item = { ...item, is_favorite: !wasFavorite };
    try {
      if (wasFavorite) await itemApi.removeFavorite(item.id);
      else await itemApi.addFavorite(item.id);
    } catch {
      if (item) item = { ...item, is_favorite: wasFavorite };
    } finally {
      favoriteBusy = false;
    }
  }

  function jumpToChapter(startMs: number) {
    seekToContentTime(startMs / 1000);
  }

  // Next-episode autoplay countdown.
  let autoplayCountdown = 0;
  let autoplayTimer: ReturnType<typeof setInterval> | null = null;
  let autoplayCancelled = false;

  function cancelAutoplay(permanent = false) {
    if (permanent) autoplayCancelled = true;
    if (autoplayTimer) { clearInterval(autoplayTimer); autoplayTimer = null; }
    autoplayCountdown = 0;
  }

  function startAutoplayCountdown() {
    if (autoplayTimer || autoplayCancelled || !nextEpisode) return;
    autoplayCountdown = 10;
    autoplayTimer = setInterval(() => {
      autoplayCountdown -= 1;
      if (autoplayCountdown <= 0) {
        if (autoplayTimer) { clearInterval(autoplayTimer); autoplayTimer = null; }
        if (nextEpisode && !autoplayCancelled) goto(`/watch/${nextEpisode.id}`);
      }
    }, 1000);
  }

  onMount(async () => {
    if (!localStorage.getItem('onscreen_user')) { goto('/login'); return; }
    // Safari uses the prefixed event name for fullscreen changes.
    document.addEventListener('webkitfullscreenchange', onFullscreenChange);
    prevId = id;
    await load();
    mounted = true;
  });

  $: if (mounted && id && id !== prevId) {
    prevId = id;
    clearTimers();
    if (videoEl && !videoEl.paused) saveProgress('stopped');
    stopTranscodeSession();
    destroyHls();
    item = null;
    siblings = [];
    musicChildren = [];
    hlsOffsetSec = 0;
    hlsActive = false;
    selectedQuality = qualityOptions[0];
    skipAutoSeek = false;
    ended = false;
    error = '';
    loading = true;
    transcodeSessionId = null;
    transcodeToken = null;
    autoplayCancelled = false;
    cancelAutoplay();
    load();
  }

  onDestroy(() => {
    clearTimers();
    document.removeEventListener('webkitfullscreenchange', onFullscreenChange);
    window.removeEventListener('mousemove', onSeekMouseMove);
    window.removeEventListener('mouseup', onSeekMouseUp);
    if (videoEl && !videoEl.paused && item) {
      const body = JSON.stringify({
        view_offset_ms: Math.round((videoEl.currentTime + hlsOffsetSec) * 1000),
        duration_ms: item.duration_ms ?? 0,
        state: 'stopped'
      });
      fetch(`/api/v1/items/${item.id}/progress`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body,
        keepalive: true,
        credentials: 'same-origin'
      }).catch(() => {});
    }
    stopTranscodeSession();
    destroyHls();
  });

  // ── Detail view state (shows / seasons) ──────────────────────────────────
  let seasons: ChildItem[] = [];
  let seasonEpisodes: Map<string, ChildItem[]> = new Map();
  let selectedSeasonId: string | null = null;
  $: isDetailView = item != null && (item.type === 'show' || item.type === 'season' || item.type === 'artist' || item.type === 'album');

  // Music detail state
  let musicChildren: ChildItem[] = []; // albums (for artist) or tracks (for album)
  let musicScanning = false;

  async function scanMusicLibrary() {
    if (!item || musicScanning) return;
    musicScanning = true;
    try {
      await libraryApi.scan(item.library_id);
      // Poll for updated children a few times.
      for (let i = 0; i < 10; i++) {
        await new Promise(r => setTimeout(r, 3000));
        await loadMusicDetail();
        if (musicChildren.length > 0) break;
      }
    } catch { /* ignore */ } finally {
      musicScanning = false;
    }
  }
  $: isPhoto = item != null && item.type === 'photo';

  // Photo viewer state
  let photoSiblings: MediaItem[] = [];
  $: photoIndex = photoSiblings.findIndex(p => p.id === id);
  $: prevPhoto = photoIndex > 0 ? photoSiblings[photoIndex - 1] : null;
  $: nextPhoto = photoIndex >= 0 && photoIndex < photoSiblings.length - 1 ? photoSiblings[photoIndex + 1] : null;

  let photoZoom = 1;
  let photoPanning = false;
  let photoPanX = 0;
  let photoPanY = 0;
  let photoPanStartX = 0;
  let photoPanStartY = 0;

  function onPhotoWheel(e: WheelEvent) {
    e.preventDefault();
    const delta = e.deltaY > 0 ? -0.1 : 0.1;
    photoZoom = Math.max(0.5, Math.min(5, photoZoom + delta));
    if (photoZoom <= 1) { photoPanX = 0; photoPanY = 0; }
  }
  function onPhotoPanStart(e: MouseEvent) {
    if (photoZoom <= 1) return;
    photoPanning = true;
    photoPanStartX = e.clientX - photoPanX;
    photoPanStartY = e.clientY - photoPanY;
  }
  function onPhotoPanMove(e: MouseEvent) {
    if (!photoPanning) return;
    photoPanX = e.clientX - photoPanStartX;
    photoPanY = e.clientY - photoPanStartY;
  }
  function onPhotoPanEnd() { photoPanning = false; }
  function resetPhotoZoom() { photoZoom = 1; photoPanX = 0; photoPanY = 0; }
  $: selectedEpisodes = selectedSeasonId ? (seasonEpisodes.get(selectedSeasonId) ?? []) : [];

  // ── Fix Match modal state ──────────────────────────────────────────────────
  let showMatchModal = false;
  let matchQuery = '';
  let matchCandidates: MatchCandidate[] = [];
  let matchSearching = false;
  let matchApplying = false;
  let matchError = '';

  async function openMatchModal() {
    showMatchModal = true;
    matchQuery = item?.title ?? '';
    matchCandidates = [];
    matchError = '';
    matchSearching = false;
    matchApplying = false;
    // Auto-search with current title.
    if (matchQuery && item) {
      await searchMatch();
    }
  }

  async function searchMatch() {
    if (!item || !matchQuery.trim()) return;
    matchSearching = true;
    matchError = '';
    try {
      matchCandidates = await itemApi.searchMatch(item.id, matchQuery.trim());
    } catch (e: unknown) {
      matchError = e instanceof Error ? e.message : 'Search failed';
    } finally {
      matchSearching = false;
    }
  }

  async function applyMatch(tmdbId: number) {
    if (!item) return;
    matchApplying = true;
    matchError = '';
    try {
      await itemApi.applyMatch(item.id, tmdbId);
      showMatchModal = false;
      // Reload the page after a short delay to let enrichment finish.
      setTimeout(() => { load(); }, 2000);
    } catch (e: unknown) {
      matchError = e instanceof Error ? e.message : 'Failed to apply match';
    } finally {
      matchApplying = false;
    }
  }

  async function loadShowDetail() {
    if (!item) return;

    if (item.type === 'show') {
      const r = await itemApi.children(item.id);
      seasons = r.items
        .filter(c => c.type === 'season')
        .sort((a, b) => (a.index ?? 0) - (b.index ?? 0));

      // Auto-select the first season and load its episodes.
      if (seasons.length > 0) {
        await selectSeason(seasons[0].id);
      }
    } else if (item.type === 'season') {
      // Landed directly on a season — load its episodes.
      const r = await itemApi.children(item.id);
      const eps = r.items
        .filter(c => c.type === 'episode')
        .sort((a, b) => (a.index ?? 0) - (b.index ?? 0));
      seasons = [{ ...item as unknown as ChildItem }]; // single-season view
      seasonEpisodes.set(item.id, eps);
      selectedSeasonId = item.id;
    }
  }

  async function loadMusicDetail() {
    if (!item) return;
    const r = await itemApi.children(item.id);
    if (item.type === 'artist') {
      musicChildren = r.items.filter(c => c.type === 'album').sort((a, b) => (a.year ?? 0) - (b.year ?? 0));
    } else if (item.type === 'album') {
      musicChildren = r.items.filter(c => c.type === 'track').sort((a, b) => (a.index ?? 0) - (b.index ?? 0));
    }
  }

  async function selectSeason(seasonId: string) {
    selectedSeasonId = seasonId;
    if (!seasonEpisodes.has(seasonId)) {
      const r = await itemApi.children(seasonId);
      const eps = r.items
        .filter(c => c.type === 'episode')
        .sort((a, b) => (a.index ?? 0) - (b.index ?? 0));
      seasonEpisodes.set(seasonId, eps);
      seasonEpisodes = seasonEpisodes; // trigger reactivity
    }
  }

  async function load() {
    loading = true;
    error = '';
    try {
      item = await itemApi.get(id);

      // Shows and seasons: load detail view instead of trying to play.
      if (item.type === 'show' || item.type === 'season') {
        await loadShowDetail();
        return;
      }

      // Artists and albums: load music detail view.
      if (item.type === 'artist' || item.type === 'album') {
        await loadMusicDetail();
        return;
      }

      // Photos: no video to set up, just display the image.
      if (item.type === 'photo') {
        photoZoom = 1;
        photoPanX = 0;
        photoPanY = 0;
        // Load siblings for arrow navigation (only once per library).
        if (!photoSiblings.length || !photoSiblings.some(p => p.id === item!.id)) {
          const r = await mediaApi.listItems(item.library_id, 500, 0, { sort: 'title', sort_dir: 'asc' });
          photoSiblings = r.items;
        }
        return;
      }

      if (item.type === 'episode' && item.parent_id) {
        const r = await itemApi.children(item.parent_id);
        siblings = r.items.sort((a, b) => (a.index ?? 0) - (b.index ?? 0));
      }

      // Seekbar thumbnail previews (best-effort — silent when not generated).
      if (item.type === 'movie' || item.type === 'episode') {
        loadTrickplay(item.id);
      }
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : 'Failed to load';
      error = msg.toLowerCase().includes('forbidden') || msg.toLowerCase().includes('insufficient permissions')
        ? 'This content is restricted by your profile\'s content rating.'
        : msg;
    } finally {
      loading = false;
    }
    // Auto-select preferred audio/subtitle tracks based on user preferences.
    try {
      const prefs = await userApi.getPreferences();
      if (prefs.preferred_subtitle_lang && !selectedSubtitle) {
        const match = textSubtitles.find((s: PickedSubtitle) => s.language === prefs.preferred_subtitle_lang);
        if (match) selectedSubtitle = match;
      }
      if (prefs.preferred_audio_lang && item?.files?.[0]?.audio_streams?.length) {
        const streams = item.files[0].audio_streams;
        const idx = streams.findIndex(s => s.language === prefs.preferred_audio_lang);
        if (idx >= 0 && idx !== 0) selectedAudioIndex = idx;
      }
    } catch { /* preferences unavailable — use defaults */ }

    // Wait for Svelte to render the <video> element (gated on item && streamURL).
    await tick();
    if (!item?.files?.[0]?.stream_url || !videoEl) return;

    const file = item.files[0];
    // Signal intent to auto-play so controls don't flash a paused state.
    paused = false;
    // Non-default audio track selected — must go through transcode even for direct-playable files.
    const needsAudioSwitch = selectedAudioIndex > 0;

    if (!file.video_codec) {
      // Audio-only file (FLAC, MP3, AAC, Opus) — browser <video> element can play
      // these natively from the raw stream without any transcoding.
      videoEl.src = file.stream_url;
      videoEl.load();
    } else if (canDirectPlay(file) && !needsAudioSwitch) {
      // Direct play — browser handles container + codecs natively.
      videoEl.src = file.stream_url;
      videoEl.load();
    } else if (canRemuxVideo(file) || (canDirectPlay(file) && needsAudioSwitch)) {
      // Video is browser-compatible (H.264) but audio or container is not,
      // or a non-default audio track was selected.
      // Stream-copy the video and only transcode audio → fast, lossless video.
      const posMs = item.view_offset_ms > 0 ? item.view_offset_ms : 0;
      await switchToTranscode(0, posMs, true);
    } else {
      // Full transcode needed (non-browser video codec like HEVC/VC-1/MPEG-2).
      // Match source resolution: 4K sources default to 4K, everything else to 1080p.
      const sourceH = file.resolution_h ?? 0;
      const defaultHeight = sourceH >= 2160 ? 2160 : 1080;
      const match = availableQualities.find(q => q.height === defaultHeight)
                 ?? availableQualities.find(q => q.height > 0)
                 ?? qualityOptions[2]; // 1080p fallback
      selectedQuality = match;
      const posMs = item.view_offset_ms > 0 ? item.view_offset_ms : 0;
      await switchToTranscode(match.height, posMs);
    }
  }

  // ── Quality switching ────────────────────────────────────────────────────────

  async function selectQuality(q: QualityOption) {
    if (q === selectedQuality) { showQualityMenu = false; return; }
    selectedQuality = q;
    showQualityMenu = false;

    if (!item || !item.files?.length) return;

    const posMs = Math.floor(currentTime * 1000); // currentTime is content-relative

    skipAutoSeek = true;
    if (q.height === 0) {
      const file = item.files[0];
      if (canDirectPlay(file)) {
        await switchToDirectPlay(posMs);
      } else if (canRemuxVideo(file)) {
        // "Auto" but can't direct-play → remux (video copy + audio transcode).
        await switchToTranscode(0, posMs, true);
      } else {
        // "Auto" but nothing is browser-compatible → full transcode at 1080p.
        await switchToTranscode(1080, posMs);
      }
    } else {
      // Explicit resolution selected — full transcode.
      await switchToTranscode(q.height, posMs);
    }
  }

  async function switchToDirectPlay(posMs: number) {
    const wasPlaying = videoEl && !videoEl.paused;
    destroyHls();
    await stopTranscodeSession();
    hlsOffsetSec = 0;
    hlsActive = false;

    const file = item!.files[0];
    videoEl.src = file.stream_url;
    videoEl.load();

    // Restore position once metadata loads.
    const restorePos = () => {
      videoEl.currentTime = posMs / 1000;
      if (wasPlaying) videoEl.play().catch(() => {});
      videoEl.removeEventListener('loadedmetadata', restorePos);
    };
    videoEl.addEventListener('loadedmetadata', restorePos);
  }

  async function switchToTranscode(height: number, posMs: number, videoCopy: boolean = false) {
    if (!item) return;
    const wasPlaying = videoEl && !videoEl.paused;
    await stopTranscodeSession();
    destroyHls();

    // Warn admin when HDR content on an SDR screen requires tonemapping transcode.
    const file = item.files?.[0];
    if (!videoCopy && file?.hdr_type && !clientSupportsHDR) {
      tonemapWarning = `This ${file.resolution_h}p HDR file requires real-time tonemapping. ` +
        `Enable HDR on your display for best performance.`;
    } else {
      tonemapWarning = '';
    }

    try {
      const audioIdx = selectedAudioIndex >= 0 ? selectedAudioIndex : undefined;
      const sess = await transcodeApi.start(item.id, height, posMs, item.files[0]?.id, videoCopy, audioIdx, clientSupportsHEVC);
      transcodeSessionId = sess.session_id;
      transcodeToken = sess.token;
      attachHls(sess.playlist_url, posMs / 1000, wasPlaying, videoCopy);
    } catch (e) {
      error = e instanceof Error ? e.message : 'Transcode failed';
    }
  }

  async function selectAudioTrack(index: number) {
    if (index === selectedAudioIndex) { showAudioMenu = false; return; }
    selectedAudioIndex = index;
    showAudioMenu = false;

    if (!item || !item.files?.length) return;
    const posMs = Math.floor(currentTime * 1000);
    skipAutoSeek = true;

    if (index <= 0 && canDirectPlay(item.files[0])) {
      // Switching back to default track on a direct-playable file — go back to direct play.
      await switchToDirectPlay(posMs);
    } else {
      // Non-default audio → transcode. Use remux if video is browser-compatible.
      const file = item.files[0];
      if (canRemuxVideo(file) || canDirectPlay(file)) {
        await switchToTranscode(0, posMs, true);
      } else {
        const h = selectedQuality.height > 0 ? selectedQuality.height : (file.resolution_h ?? 1080);
        await switchToTranscode(h, posMs);
      }
    }
  }

  function attachHls(playlistUrl: string, startSec: number, autoPlay: boolean, isRemux: boolean = false) {
    // The HLS stream begins at t=0 representing content position startSec.
    // We track the offset ourselves; do NOT seek inside the stream.
    hlsOffsetSec = startSec;
    hlsActive = true;
    hlsIsRemux = isRemux;

    // Use file-level duration (from ffprobe) first, then fall back to item-level.
    const file = item?.files?.[0];
    const fileDurMs = file?.duration_ms ?? item?.duration_ms;
    if (fileDurMs) duration = fileDurMs / 1000;

    if (Hls.isSupported()) {
      hlsInstance = new Hls({
        // Cap the forward buffer to 30 s and lock maxMaxBufferLength to match.
        // If maxMaxBufferLength > maxBufferLength, HLS.js can expand its target
        // up to that ceiling, race ahead to FFmpeg's live edge, and stall when
        // there are no more segments to fetch. Jellyfin uses this same pattern.
        maxBufferLength: 30,
        maxMaxBufferLength: 30,
        startFragPrefetch: true,
        lowLatencyMode: false,
        backBufferLength: Infinity,
        // Force playback to start at position 0 (not the live edge).
        startPosition: 0,
        // Disable live-edge sync until ENDLIST appears. Without this, HLS.js
        // seeks the player forward toward the live edge and stalls.
        liveSyncDurationCount: 999,
        liveMaxLatencyDurationCount: 1002,
        // Skip the 3×1s nudge loop for PTS-offset sources — seek immediately.
        nudgeMaxRetry: 0,
      });
      hlsInstance.loadSource(playlistUrl);
      hlsInstance.attachMedia(videoEl);
      hlsInstance.on(Hls.Events.MANIFEST_PARSED, () => {
        if (autoPlay) videoEl.play().catch(() => {});
      });
      hlsInstance.on(Hls.Events.ERROR, (_event: any, data: any) => {
        console.warn('[HLS] error', data.type, data.details, data.fatal, data);
        if (!data.fatal) return;
        if (data.type === Hls.ErrorTypes.MEDIA_ERROR) {
          // Media decode error — attempt HLS.js recovery once.
          console.warn('[HLS] attempting media error recovery');
          hlsInstance?.recoverMediaError();
        } else {
          // Network or other fatal error — surface to user.
          error = `Playback error: ${data.details ?? 'unknown'}`;
        }
      });
    } else if (videoEl.canPlayType('application/vnd.apple.mpegURL')) {
      // Safari native HLS
      videoEl.src = playlistUrl;
      videoEl.load();
      const onMeta = () => {
        if (autoPlay) videoEl.play().catch(() => {});
        videoEl.removeEventListener('loadedmetadata', onMeta);
      };
      videoEl.addEventListener('loadedmetadata', onMeta);
    } else {
      error = 'HLS playback is not supported in this browser.';
    }
  }

  function destroyHls() {
    if (hlsInstance) {
      hlsInstance.destroy();
      hlsInstance = null;
    }
    hlsActive = false;
    hlsOffsetSec = 0;
    hlsPtsOffset = 0;
    hlsIsRemux = false;
  }

  async function stopTranscodeSession() {
    if (transcodeSessionId && transcodeToken) {
      try {
        await transcodeApi.stop(transcodeSessionId, transcodeToken);
      } catch (e) { console.warn(e); }
      transcodeSessionId = null;
      transcodeToken = null;
    }
  }

  // ── Video events ──────────────────────────────────────────────────────────────

  // Buffering indicator — shown while the browser is waiting for data.
  let buffering = false;

  function onVideoLoaded() {
    // If the browser received video segments but videoWidth is 0, it can't decode
    // the video codec (e.g. Hi10P H.264, HEVC in remux). Escalate to full transcode.
    // Skip this check for audio-only files — they legitimately have no video track.
    if (videoEl.videoWidth === 0 && item?.files?.[0] && item.files[0].video_codec) {
      const file = item.files[0];
      const posMs = hlsActive
        ? Math.round((videoEl.currentTime + hlsOffsetSec) * 1000)
        : (item.view_offset_ms > 0 ? item.view_offset_ms : 0);
      if (!hlsActive && canRemuxVideo(file)) {
        // Direct play failed — try remux first.
        switchToTranscode(0, posMs, true);
      } else {
        // Remux also failed (hlsIsRemux) or no remux option — full transcode.
        const h = file.resolution_h ?? 1080;
        switchToTranscode(h, posMs);
      }
      return;
    }

    if (!hlsActive) {
      if (isFinite(videoEl.duration) && videoEl.duration > 0) {
        duration = videoEl.duration;
      }
      // Resume from last saved position.
      if (!skipAutoSeek && item && item.view_offset_ms > 0) {
        const offsetSec = item.view_offset_ms / 1000;
        if (duration - offsetSec > 30) {
          videoEl.currentTime = offsetSec;
        }
      }
      skipAutoSeek = false;
    }
    videoEl.play().catch(() => {});
  }

  function onWaiting()  { buffering = true; }
  function onPlaying()  {
    buffering = false;
    // Capture PTS offset on first play of an HLS session. If videoEl.currentTime
    // is well above 0 but hlsOffsetSec is 0 (started from beginning), the source
    // file has shifted MPEG-TS timestamps. Subtract this from subtitle matching.
    if (hlsActive && hlsPtsOffset === 0 && hlsOffsetSec === 0 && videoEl.currentTime > 0.5) {
      hlsPtsOffset = videoEl.currentTime;
    }
  }

  function onTimeUpdate() {
    if (!seeking) currentTime = videoEl.currentTime + hlsOffsetSec;
    if (videoEl.buffered.length > 0 && duration > 0) {
      buffered = Math.min(1, (videoEl.buffered.end(videoEl.buffered.length - 1) + hlsOffsetSec) / duration);
    }
    // Direct play only: keep duration in sync with the video element.
    // In HLS mode, duration is fixed from item.duration_ms (set in attachHls).
    if (!hlsActive && isFinite(videoEl.duration) && videoEl.duration > 0) {
      duration = videoEl.duration;
    }
    // Update active subtitle cues. Subtract PTS offset (container timestamp shift)
    // and add subtitle delay (user adjustment for poorly synced source subs).
    if (allCues.length > 0) {
      const t = currentTime - hlsPtsOffset - subtitleDelay;
      activeCues = allCues.filter(c => t >= c.start && t <= c.end);
    } else if (activeCues.length > 0) {
      activeCues = [];
    }
  }

  function onPlay()  { paused = false; ended = false; startProgressTimer(); }
  function onPause() { paused = true; stopProgressTimer(); saveProgress('paused'); }
  function onEnded() { ended = true; paused = true; stopProgressTimer(); saveProgress('stopped'); stopTranscodeSession(); }

  function startProgressTimer() {
    stopProgressTimer();
    progressTimer = setInterval(() => saveProgress('playing'), 5000);
  }
  function stopProgressTimer() {
    if (progressTimer) { clearInterval(progressTimer); progressTimer = null; }
  }
  function clearTimers() {
    stopProgressTimer();
    if (hideTimer) { clearTimeout(hideTimer); hideTimer = null; }
  }

  async function saveProgress(state: 'playing' | 'paused' | 'stopped') {
    if (!item || !videoEl || duration === 0) return;
    try {
      await itemApi.progress(item.id, Math.floor(currentTime * 1000), Math.floor(duration * 1000), state);
    } catch (e) { console.warn(e); }
  }

  function togglePlay() {
    if (!videoEl) return;
    if (videoEl.paused) videoEl.play().catch(() => {});
    else videoEl.pause();
  }

  function onKeyDown(e: KeyboardEvent) {
    // Photo viewer keys
    if (isPhoto) {
      if (e.key === 'ArrowRight' && nextPhoto) { e.preventDefault(); goto(`/watch/${nextPhoto.id}`); }
      if (e.key === 'ArrowLeft' && prevPhoto) { e.preventDefault(); goto(`/watch/${prevPhoto.id}`); }
      if (e.key === 'Escape') { e.preventDefault(); goBackToLibrary(); }
      return;
    }
    if (!videoEl) return;
    // Close menus on Escape
    if (e.key === 'Escape') { showQualityMenu = false; showSubtitleMenu = false; showAudioMenu = false; return; }
    switch (e.key) {
      case ' ':
      case 'k':
        e.preventDefault();
        togglePlay();
        break;
      case 'ArrowRight':
        e.preventDefault();
        seekToContentTime(currentTime + 10);
        break;
      case 'ArrowLeft':
        e.preventDefault();
        seekToContentTime(currentTime - 10);
        break;
      case 'ArrowUp':
        e.preventDefault();
        videoEl.volume = Math.min(videoEl.volume + 0.1, 1);
        volume = videoEl.volume;
        break;
      case 'ArrowDown':
        e.preventDefault();
        videoEl.volume = Math.max(videoEl.volume - 0.1, 0);
        volume = videoEl.volume;
        break;
      case 'f':
        e.preventDefault();
        toggleFullscreen();
        break;
      case 'm':
        e.preventDefault();
        videoEl.muted = !videoEl.muted;
        muted = videoEl.muted;
        break;
    }
  }

  function getSeekFraction(e: MouseEvent | TouchEvent): number {
    const rect = seekBarEl.getBoundingClientRect();
    const clientX = 'touches' in e ? e.touches[0].clientX : e.clientX;
    return Math.max(0, Math.min(1, (clientX - rect.left) / rect.width));
  }

  // ── Trickplay hover preview ───────────────────────────────────────────────
  // parseTrickplayVTT turns a WebVTT trickplay index into cue objects. Payload
  // lines look like "sprite_000.jpg#xywh=0,0,320,180". We resolve filenames
  // against baseURL so the <img> tag can load them directly.
  function parseTrickplayVTT(text: string, baseURL: string): TrickplayCue[] {
    const cues: TrickplayCue[] = [];
    const lines = text.split(/\r?\n/);
    for (let i = 0; i < lines.length; i++) {
      const arrow = lines[i].indexOf('-->');
      if (arrow < 0) continue;
      const start = parseVTTTime(lines[i].slice(0, arrow).trim());
      const end = parseVTTTime(lines[i].slice(arrow + 3).trim());
      const payload = (lines[i + 1] ?? '').trim();
      const hash = payload.indexOf('#xywh=');
      if (start < 0 || end < 0 || hash < 0) continue;
      const file = payload.slice(0, hash);
      const coords = payload.slice(hash + 6).split(',').map(n => parseInt(n, 10));
      if (coords.length < 4 || coords.some(Number.isNaN)) continue;
      cues.push({
        start,
        end,
        url: baseURL + file,
        x: coords[0],
        y: coords[1],
        w: coords[2],
        h: coords[3],
      });
    }
    return cues;
  }

  // parseVTTTime accepts HH:MM:SS.mmm or MM:SS.mmm. Returns -1 on bad input.
  function parseVTTTime(s: string): number {
    const m = s.match(/^(?:(\d+):)?(\d+):(\d+)(?:\.(\d+))?$/);
    if (!m) return -1;
    const h = m[1] ? parseInt(m[1], 10) : 0;
    const mi = parseInt(m[2], 10);
    const se = parseInt(m[3], 10);
    const ms = m[4] ? parseInt(m[4].padEnd(3, '0').slice(0, 3), 10) : 0;
    return h * 3600 + mi * 60 + se + ms / 1000;
  }

  async function loadTrickplay(itemId: string) {
    trickplayCues = [];
    trickplayBaseURL = `/trickplay/${itemId}/`;
    try {
      const r = await fetch(trickplayBaseURL + 'index.vtt');
      if (!r.ok) return;
      const text = await r.text();
      trickplayCues = parseTrickplayVTT(text, trickplayBaseURL);
    } catch {
      // Trickplay is optional — silent failure keeps the player working.
    }
  }

  // findTrickplayCue does a linear scan. Cue lists are short (100s of entries
  // for a 2h movie at 10s intervals); a binary search isn't worth the code.
  function findTrickplayCue(t: number): TrickplayCue | null {
    for (const c of trickplayCues) {
      if (t >= c.start && t < c.end) return c;
    }
    return null;
  }

  function onSeekHoverMove(e: MouseEvent) {
    if (!seekBarEl || !duration) return;
    const rect = seekBarEl.getBoundingClientRect();
    hoverX = Math.max(0, Math.min(rect.width, e.clientX - rect.left));
    hoverTime = (hoverX / rect.width) * duration;
    hoverVisible = true;
  }

  function onSeekHoverLeave() {
    hoverVisible = false;
  }

  function formatHoverTime(t: number): string {
    if (!isFinite(t) || t < 0) return '0:00';
    const h = Math.floor(t / 3600);
    const m = Math.floor((t % 3600) / 60);
    const s = Math.floor(t % 60);
    return h > 0
      ? `${h}:${m.toString().padStart(2, '0')}:${s.toString().padStart(2, '0')}`
      : `${m}:${s.toString().padStart(2, '0')}`;
  }

  $: hoverCue = hoverVisible ? findTrickplayCue(hoverTime) : null;

  function onSeekMouseDown(e: MouseEvent) {
    seeking = true;
    currentTime = getSeekFraction(e) * duration; // preview in content time
    window.addEventListener('mousemove', onSeekMouseMove);
    window.addEventListener('mouseup', onSeekMouseUp);
  }
  function onSeekMouseMove(e: MouseEvent) {
    if (!seeking) return;
    currentTime = getSeekFraction(e) * duration;
  }
  function onSeekMouseUp(e: MouseEvent) {
    if (!seeking) return;
    seeking = false;
    const targetSec = getSeekFraction(e) * duration;
    window.removeEventListener('mousemove', onSeekMouseMove);
    window.removeEventListener('mouseup', onSeekMouseUp);
    seekToContentTime(targetSec);
    saveProgress(videoEl.paused ? 'paused' : 'playing');
  }

  // Touch-specific seek bar handlers — prevent the overlay's swipe-gesture
  // system from hijacking drags that start on the seek bar.
  function onSeekTouchStart(e: TouchEvent) {
    e.stopPropagation();
    seeking = true;
    currentTime = getSeekFraction(e) * duration;
  }
  function onSeekTouchMove(e: TouchEvent) {
    if (!seeking) return;
    e.stopPropagation();
    e.preventDefault();
    currentTime = getSeekFraction(e) * duration;
  }
  function onSeekTouchEnd(e: TouchEvent) {
    if (!seeking) return;
    e.stopPropagation();
    seeking = false;
    const rect = seekBarEl.getBoundingClientRect();
    const clientX = e.changedTouches[0].clientX;
    const frac = Math.max(0, Math.min(1, (clientX - rect.left) / rect.width));
    seekToContentTime(frac * duration);
    saveProgress(videoEl.paused ? 'paused' : 'playing');
  }

  // Seek to an absolute content position (seconds).
  // In HLS mode, restarts the transcode session if the target is outside
  // the current stream window; otherwise seeks within the stream directly.
  function seekToContentTime(targetSec: number) {
    targetSec = Math.max(0, Math.min(targetSec, duration));
    if (!hlsActive) {
      videoEl.currentTime = targetSec;
      return;
    }
    // Determine the seekable range within the current HLS stream.
    // videoEl.duration can be Infinity for live-ish streams (before ENDLIST),
    // so fall back to the seekable/buffered end reported by the browser.
    let streamDur = 0;
    if (isFinite(videoEl.duration) && videoEl.duration > 0) {
      streamDur = videoEl.duration;
    } else if (videoEl.seekable?.length) {
      streamDur = videoEl.seekable.end(videoEl.seekable.length - 1);
    } else if (videoEl.buffered?.length) {
      streamDur = videoEl.buffered.end(videoEl.buffered.length - 1);
    }
    const streamEnd = hlsOffsetSec + streamDur;
    const streamTime = targetSec - hlsOffsetSec;
    if (targetSec >= hlsOffsetSec && targetSec <= streamEnd && streamTime >= 0) {
      videoEl.currentTime = streamTime;
    } else {
      // Outside the current HLS window — restart transcode at new position.
      // Preserve remux mode if we were in it (videoCopy).
      switchToTranscode(selectedQuality.height, Math.floor(targetSec * 1000), hlsIsRemux);
    }
  }

  function onVolumeChange(e: Event) {
    volume = (e.target as HTMLInputElement).valueAsNumber;
    videoEl.volume = volume;
    if (volume > 0) videoEl.muted = false;
    muted = videoEl.muted;
  }

  async function toggleFullscreen() {
    if (!document.fullscreenElement) {
      await (containerEl.requestFullscreen || (containerEl as any).webkitRequestFullscreen)?.call(containerEl).catch(() => {});
      fullscreen = true;
      // Lock to landscape on mobile.
      try { await (screen.orientation as any)?.lock?.('landscape'); } catch {}
    } else {
      await (document.exitFullscreen || (document as any).webkitExitFullscreen)?.call(document).catch(() => {});
      fullscreen = false;
      try { screen.orientation?.unlock?.(); } catch {}
    }
  }

  function onFullscreenChange() { fullscreen = !!(document.fullscreenElement || (document as any).webkitFullscreenElement); }

  function resetHideTimer() {
    showControls = true;
    if (hideTimer) clearTimeout(hideTimer);
    if (!paused) {
      hideTimer = setTimeout(() => {
        if (!showQualityMenu) showControls = false;
      }, 3000);
    }
  }

  function onMouseMove() { resetHideTimer(); }
  function onMouseLeave() {
    if (!paused && hideTimer) clearTimeout(hideTimer);
    if (!paused && !showQualityMenu && !showSubtitleMenu && !showAudioMenu) showControls = false;
  }

  function fmtTime(sec: number): string {
    if (!isFinite(sec)) return '0:00';
    const h = Math.floor(sec / 3600);
    const m = Math.floor((sec % 3600) / 60);
    const s = Math.floor(sec % 60);
    if (h > 0) return `${h}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`;
    return `${m}:${String(s).padStart(2, '0')}`;
  }

  function goBack() {
    saveProgress('stopped');
    history.back();
  }

  function goBackToLibrary() {
    if (item?.library_id) {
      goto(`/libraries/${item.library_id}`);
    } else {
      history.back();
    }
  }

  // ── Mobile touch gestures ──────────────────────────────────────────────────
  let isMobile = false;
  let touchStartX = 0;
  let touchStartY = 0;
  let touchStartTime = 0;
  let touchGesture: 'none' | 'seek' | 'volume' | 'brightness' | 'swipe-down' = 'none';
  let gestureLabel = '';
  let showGestureLabel = false;
  // Double-tap state
  let lastTapTime = 0;
  let lastTapX = 0;
  let rippleX = 0;
  let rippleY = 0;
  let rippleSide: 'left' | 'right' | '' = '';
  let showRipple = false;
  let rippleTimeout: ReturnType<typeof setTimeout> | null = null;
  // Bottom sheet state (mobile menus)
  let showBottomSheet: 'quality' | 'subtitle' | 'audio' | '' = '';
  // Brightness overlay
  let brightnessLevel = 1;

  $: if (typeof window !== 'undefined') {
    isMobile = window.matchMedia('(max-width: 768px), (pointer: coarse)').matches;
  }

  function onTouchStart(e: TouchEvent) {
    if (!videoEl || isDetailView || isPhoto) return;
    const t = e.touches[0];
    touchStartX = t.clientX;
    touchStartY = t.clientY;
    touchStartTime = Date.now();
    touchGesture = 'none';
  }

  function onTouchMove(e: TouchEvent) {
    if (!videoEl || touchGesture === 'none' && !e.touches.length) return;
    const t = e.touches[0];
    const dx = t.clientX - touchStartX;
    const dy = t.clientY - touchStartY;
    const absDx = Math.abs(dx);
    const absDy = Math.abs(dy);

    // Determine gesture direction if not yet locked in.
    if (touchGesture === 'none') {
      if (absDx < 10 && absDy < 10) return; // dead zone
      if (absDx > absDy) {
        touchGesture = 'seek';
      } else if (dy > 0 && absDy > absDx * 2) {
        touchGesture = 'swipe-down';
      } else {
        // Vertical gesture: left half = brightness, right half = volume
        const w = containerEl?.clientWidth ?? window.innerWidth;
        touchGesture = touchStartX < w / 2 ? 'brightness' : 'volume';
      }
    }

    e.preventDefault();

    if (touchGesture === 'seek') {
      const seekDelta = dx / 3; // ~3px per second
      const target = Math.max(0, Math.min(duration, currentTime + seekDelta));
      gestureLabel = `${seekDelta >= 0 ? '+' : ''}${Math.round(seekDelta)}s`;
      showGestureLabel = true;
    } else if (touchGesture === 'volume') {
      const volDelta = -dy / 200;
      const newVol = Math.max(0, Math.min(1, volume + volDelta));
      videoEl.volume = newVol;
      volume = newVol;
      if (volume > 0) { videoEl.muted = false; muted = false; }
      gestureLabel = `Volume ${Math.round(volume * 100)}%`;
      showGestureLabel = true;
      // Reset touch origin for continuous adjustment.
      touchStartY = t.clientY;
    } else if (touchGesture === 'brightness') {
      const brDelta = -dy / 200;
      brightnessLevel = Math.max(0.2, Math.min(1, brightnessLevel + brDelta));
      gestureLabel = `Brightness ${Math.round(brightnessLevel * 100)}%`;
      showGestureLabel = true;
      touchStartY = t.clientY;
    }
  }

  function onTouchEnd(e: TouchEvent) {
    if (!videoEl || isDetailView || isPhoto) return;
    const now = Date.now();
    const elapsed = now - touchStartTime;

    if (touchGesture === 'seek') {
      // Apply final seek position.
      const t = e.changedTouches[0];
      const dx = t.clientX - touchStartX;
      const seekDelta = dx / 3;
      seekToContentTime(currentTime + seekDelta);
    } else if (touchGesture === 'swipe-down') {
      const t = e.changedTouches[0];
      const dy = t.clientY - touchStartY;
      if (dy > 100) {
        // Swipe down = dismiss player
        goBack();
        showGestureLabel = false;
        touchGesture = 'none';
        return;
      }
    } else if (touchGesture === 'none' && elapsed < 300) {
      // Tap — check for double-tap.
      const t = e.changedTouches[0];
      const timeSinceLastTap = now - lastTapTime;
      const distFromLastTap = Math.abs(t.clientX - lastTapX);

      if (timeSinceLastTap < 350 && distFromLastTap < 80) {
        // Double-tap detected
        const w = containerEl?.clientWidth ?? window.innerWidth;
        const third = w / 3;
        rippleX = t.clientX;
        rippleY = t.clientY;

        if (t.clientX < third) {
          seekToContentTime(currentTime - 10);
          rippleSide = 'left';
          triggerRipple();
        } else if (t.clientX > w - third) {
          seekToContentTime(currentTime + 10);
          rippleSide = 'right';
          triggerRipple();
        } else {
          // Center double-tap = toggle play.
          togglePlay();
        }
        lastTapTime = 0;
      } else {
        lastTapTime = now;
        lastTapX = t.clientX;
        // Single tap — toggle controls after a short delay.
        setTimeout(() => {
          if (Date.now() - lastTapTime >= 300 && lastTapTime === now) {
            resetHideTimer();
          }
        }, 310);
      }
    }

    showGestureLabel = false;
    touchGesture = 'none';
  }

  function triggerRipple() {
    showRipple = true;
    if (rippleTimeout) clearTimeout(rippleTimeout);
    rippleTimeout = setTimeout(() => { showRipple = false; rippleSide = ''; }, 500);
  }

  // Open bottom sheet on mobile instead of popup menus.
  function openMobileMenu(menu: 'quality' | 'subtitle' | 'audio') {
    showBottomSheet = menu;
    showQualityMenu = false;
    showSubtitleMenu = false;
    showAudioMenu = false;
  }

  $: progress = duration > 0 ? currentTime / duration : 0;
  $: showNextEpisodePrompt = nextEpisode != null && duration > 0 && (ended || duration - currentTime < 60);
  $: if (showNextEpisodePrompt && !autoplayTimer && !autoplayCancelled) startAutoplayCountdown();
  $: if (!showNextEpisodePrompt) cancelAutoplay();
  $: streamURL = item?.files?.[0]?.stream_url ?? '';
</script>

<svelte:head><title>{item?.title ?? 'Watch'} — OnScreen</title></svelte:head>

<!-- svelte-ignore avoid-is -->
<svelte:window on:keydown={onKeyDown} on:fullscreenchange={onFullscreenChange} on:click={() => { showQualityMenu = false; showSubtitleMenu = false; showAudioMenu = false; showChapterMenu = false; }} />

{#if isPhoto && item}
<!-- Photo viewer -->
<!-- svelte-ignore a11y-no-static-element-interactions -->
<div class="photo-viewer" on:wheel={onPhotoWheel} on:mousedown={onPhotoPanStart} on:mousemove={onPhotoPanMove} on:mouseup={onPhotoPanEnd} on:mouseleave={onPhotoPanEnd}>
  {#if loading}
    <div class="center-msg"><div class="spinner"></div></div>
  {:else if error}
    <div class="center-msg">
      <p class="err-text">{error}</p>
      <button class="back-btn" on:click={goBackToLibrary}>← Back</button>
    </div>
  {:else}
    <img
      class="photo-image"
      src="/artwork/{encodeURI(item.poster_path ?? '')}?v={item.updated_at}"
      alt={item.title}
      draggable="false"
      style="transform: scale({photoZoom}) translate({photoPanX / photoZoom}px, {photoPanY / photoZoom}px);"
    />
    <div class="photo-toolbar">
      <button class="photo-btn" on:click={goBackToLibrary} title="Close (Esc)">
        <svg viewBox="0 0 24 24" fill="currentColor" width="24" height="24"><path d="M19 6.41L17.59 5 12 10.59 6.41 5 5 6.41 10.59 12 5 17.59 6.41 19 12 13.41 17.59 19 19 17.59 13.41 12z"/></svg>
      </button>
      <span class="photo-title">{item.title}</span>
      <button class="photo-btn" on:click={resetPhotoZoom} title="Reset zoom">
        <svg viewBox="0 0 24 24" fill="currentColor" width="20" height="20"><path d="M15.5 14h-.79l-.28-.27a6.5 6.5 0 0 0 1.48-5.34c-.47-2.78-2.79-5-5.59-5.34a6.505 6.505 0 0 0-7.27 7.27c.34 2.8 2.56 5.12 5.34 5.59a6.5 6.5 0 0 0 5.34-1.48l.27.28v.79l4.26 4.25a1 1 0 0 0 1.41-1.41L15.5 14zm-6 0C7.01 14 5 11.99 5 9.5S7.01 5 9.5 5 14 7.01 14 9.5 11.99 14 9.5 14z"/></svg>
        {Math.round(photoZoom * 100)}%
      </button>
      <button class="photo-btn" on:click={() => { photoZoom = Math.min(5, photoZoom + 0.25); }} title="Zoom in">+</button>
      <button class="photo-btn" on:click={() => { photoZoom = Math.max(0.5, photoZoom - 0.25); if (photoZoom <= 1) { photoPanX = 0; photoPanY = 0; } }} title="Zoom out">−</button>
      {#if photoSiblings.length > 1}
        <span class="photo-counter">{photoIndex + 1} / {photoSiblings.length}</span>
      {/if}
    </div>
    {#if prevPhoto}
      <button class="photo-nav photo-nav-left" on:click={() => goto(`/watch/${prevPhoto.id}`)} title="Previous (←)">
        <svg viewBox="0 0 24 24" fill="currentColor" width="32" height="32"><path d="M15.41 7.41L14 6l-6 6 6 6 1.41-1.41L10.83 12z"/></svg>
      </button>
    {/if}
    {#if nextPhoto}
      <button class="photo-nav photo-nav-right" on:click={() => goto(`/watch/${nextPhoto.id}`)} title="Next (→)">
        <svg viewBox="0 0 24 24" fill="currentColor" width="32" height="32"><path d="M10 6L8.59 7.41 13.17 12l-4.58 4.59L10 18l6-6z"/></svg>
      </button>
    {/if}
  {/if}
</div>
{:else if !isDetailView}
<!-- svelte-ignore a11y-no-static-element-interactions -->
<div
  class="player-container"
  class:hide-cursor={!showControls && !paused}
  bind:this={containerEl}
  on:mousemove={onMouseMove}
  on:mouseleave={onMouseLeave}
  on:touchstart={onTouchStart}
  on:touchmove={onTouchMove}
  on:touchend={onTouchEnd}
  style={brightnessLevel < 1 ? `filter: brightness(${brightnessLevel})` : ''}
  role="region"
  aria-label="Video player"
>
  {#if loading}
    <div class="center-msg">
      <div class="spinner"></div>
    </div>
  {:else if error}
    <div class="center-msg">
      <p class="err-text">{error}</p>
      <button class="back-btn" on:click={goBack}>← Back</button>
    </div>
  {:else if item && streamURL}
    <!-- Video -->
    <video
      bind:this={videoEl}
      class="video"
      on:loadedmetadata={onVideoLoaded}
      on:timeupdate={onTimeUpdate}
      on:play={onPlay}
      on:pause={onPause}
      on:ended={onEnded}
      on:waiting={onWaiting}
      on:playing={onPlaying}
      preload="auto"
    >
      <track kind="captions" />
    </video>

    <!-- JS-rendered subtitle overlay (native <track> is unreliable with HLS.js/MSE) -->
    {#if activeCues.length > 0}
      <div class="subtitle-overlay subs-{subtitleSize}">
        {#each activeCues as cue}
          <span class="subtitle-cue">{@html escapeCueText(cue.text)}</span>
        {/each}
      </div>
    {/if}

    {#if tonemapWarning}
      <div class="tonemap-banner">{tonemapWarning}</div>
    {/if}

    {#if buffering}
      <div class="buffer-overlay">
        <div class="spinner"></div>
      </div>
    {/if}

    <!-- Fanart background (blurred, behind controls) -->
    {#if item.fanart_path}
      <div class="fanart-bg" style="background-image:url('/artwork/{item.fanart_path}?v={item.updated_at}&w=640')"></div>
    {/if}

    <!-- Controls overlay -->
    <!-- svelte-ignore a11y-click-events-have-key-events -->
    <!-- svelte-ignore a11y-no-static-element-interactions -->
    <div class="controls-overlay" class:visible={showControls || paused} on:click={togglePlay}>

      <!-- Top bar -->
      <div class="top-bar" on:click|stopPropagation>
        <button class="back-btn icon-btn" on:click={goBack} title="Back">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" width="20" height="20">
            <polyline points="15 18 9 12 15 6"/>
          </svg>
        </button>
        <div class="top-title">
          <span class="top-title-main">{item.title}</span>
          {#if item.type === 'episode' && item.index != null}
            <span class="top-title-sub">Episode {item.index}</span>
          {/if}
          {#if currentChapter}
            <span class="top-title-sub">· {currentChapter.title}</span>
          {/if}
        </div>
        <button
          class="icon-btn small favorite-btn"
          class:is-favorite={item.is_favorite}
          on:click={toggleFavorite}
          disabled={favoriteBusy}
          title={item.is_favorite ? 'Remove from favorites' : 'Add to favorites'}
          aria-label={item.is_favorite ? 'Remove from favorites' : 'Add to favorites'}
        >
          {#if item.is_favorite}
            <svg viewBox="0 0 24 24" fill="currentColor" width="20" height="20"><path d="M12 21.35l-1.45-1.32C5.4 15.36 2 12.28 2 8.5 2 5.42 4.42 3 7.5 3c1.74 0 3.41.81 4.5 2.09C13.09 3.81 14.76 3 16.5 3 19.58 3 22 5.42 22 8.5c0 3.78-3.4 6.86-8.55 11.54L12 21.35z"/></svg>
          {:else}
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="20" height="20"><path d="M12 21.35l-1.45-1.32C5.4 15.36 2 12.28 2 8.5 2 5.42 4.42 3 7.5 3c1.74 0 3.41.81 4.5 2.09C13.09 3.81 14.76 3 16.5 3 19.58 3 22 5.42 22 8.5c0 3.78-3.4 6.86-8.55 11.54L12 21.35z"/></svg>
          {/if}
        </button>
      </div>

      <!-- Bottom bar -->
      <div class="bottom-bar" on:click|stopPropagation>
        <!-- Seek bar -->
        <div
          class="seek-bar"
          bind:this={seekBarEl}
          on:mousedown={onSeekMouseDown}
          on:mousemove={onSeekHoverMove}
          on:mouseleave={onSeekHoverLeave}
          on:touchstart={onSeekTouchStart}
          on:touchmove={onSeekTouchMove}
          on:touchend={onSeekTouchEnd}
          role="slider"
          aria-label="Seek"
          aria-valuemin={0}
          aria-valuemax={duration}
          aria-valuenow={currentTime}
          tabindex="0"
        >
          <div class="seek-track">
            <div class="seek-buffered" style="width:{buffered * 100}%"></div>
            <div class="seek-progress" style="width:{progress * 100}%"></div>
            {#if duration > 0 && chapters.length > 0}
              {#each chapters.slice(1) as ch}
                <div class="seek-chapter" style="left:{Math.min(100, (ch.start_ms / 1000 / duration) * 100)}%"></div>
              {/each}
            {/if}
            <div class="seek-thumb" style="left:{progress * 100}%"></div>
          </div>
          {#if hoverVisible && duration > 0}
            <div class="seek-preview" style="left:{hoverX}px">
              {#if hoverCue}
                <div
                  class="seek-preview-thumb"
                  style="width:{hoverCue.w}px;height:{hoverCue.h}px;background-image:url('{hoverCue.url}');background-position:-{hoverCue.x}px -{hoverCue.y}px"
                ></div>
              {/if}
              <div class="seek-preview-time">{formatHoverTime(hoverTime)}</div>
            </div>
          {/if}
        </div>

        <!-- Controls row -->
        <div class="controls-row">
          <div class="controls-left">
            <!-- Play/pause -->
            <button class="icon-btn" on:click={togglePlay} title={paused ? 'Play (k)' : 'Pause (k)'}>
              {#if paused}
                <svg viewBox="0 0 24 24" fill="currentColor" width="22" height="22">
                  <polygon points="5,3 19,12 5,21"/>
                </svg>
              {:else}
                <svg viewBox="0 0 24 24" fill="currentColor" width="22" height="22">
                  <rect x="6" y="4" width="4" height="16"/><rect x="14" y="4" width="4" height="16"/>
                </svg>
              {/if}
            </button>

            <!-- Skip back 10s -->
            <button class="icon-btn small" on:click={() => seekToContentTime(currentTime - 10)} title="−10s (←)">
              <svg viewBox="0 0 24 24" fill="currentColor" width="18" height="18">
                <path d="M12.5 3a9 9 0 1 0 9 9h-2a7 7 0 1 1-7-7V3z"/>
                <path d="M12.5 1 8 5l4.5 4V1z"/>
                <text x="12" y="14.5" text-anchor="middle" font-size="5" font-family="sans-serif" fill="currentColor">10</text>
              </svg>
            </button>

            <!-- Skip forward 10s -->
            <button class="icon-btn small" on:click={() => seekToContentTime(currentTime + 10)} title="+10s (→)">
              <svg viewBox="0 0 24 24" fill="currentColor" width="18" height="18">
                <path d="M11.5 3a9 9 0 1 1-9 9h2a7 7 0 1 0 7-7V3z"/>
                <path d="M11.5 1l4.5 4-4.5 4V1z"/>
                <text x="12" y="14.5" text-anchor="middle" font-size="5" font-family="sans-serif" fill="currentColor">10</text>
              </svg>
            </button>

            <!-- Volume -->
            <button class="icon-btn small" on:click={() => { videoEl.muted = !videoEl.muted; muted = videoEl.muted; }} title="Mute (m)">
              {#if muted || volume === 0}
                <svg viewBox="0 0 24 24" fill="currentColor" width="18" height="18">
                  <path d="M11 5L6 9H2v6h4l5 4V5zM23 9l-6 6M17 9l6 6" stroke="currentColor" stroke-width="2" fill="none"/>
                </svg>
              {:else}
                <svg viewBox="0 0 24 24" fill="currentColor" width="18" height="18">
                  <path d="M11 5L6 9H2v6h4l5 4V5z"/>
                  <path d="M15.54 8.46a5 5 0 0 1 0 7.07M19.07 4.93a10 10 0 0 1 0 14.14" stroke="currentColor" stroke-width="2" fill="none"/>
                </svg>
              {/if}
            </button>
            <input
              class="volume-slider"
              type="range"
              min="0"
              max="1"
              step="0.05"
              value={muted ? 0 : volume}
              on:input={onVolumeChange}
              aria-label="Volume"
            />

            <!-- Time -->
            <span class="time">{fmtTime(currentTime)} / {fmtTime(duration)}</span>
          </div>

          <div class="controls-right">
            <!-- Admin: manual intro/credits markers (episodes only) -->
            {#if isAdmin && item?.type === 'episode'}
              <div class="quality-picker" on:click|stopPropagation>
                <button
                  class="icon-btn small quality-btn"
                  on:click|stopPropagation={() => { showMarkerEditor = !showMarkerEditor; showChapterMenu = false; showQualityMenu = false; showSubtitleMenu = false; showAudioMenu = false; }}
                  title="Edit intro/credits markers"
                >
                  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="16" height="16">
                    <path d="M12 20l9-9-5-5L3 14v6h6z"/>
                    <path d="M14 7l3 3"/>
                  </svg>
                  <span class="quality-label">Markers</span>
                </button>
                {#if showMarkerEditor}
                  <!-- svelte-ignore a11y-no-static-element-interactions -->
                  <div class="quality-menu marker-menu" on:click|stopPropagation role="menu" aria-label="Edit markers">
                    {#each ['intro', 'credits'] as kind}
                      {@const existing = findMarker(kind as 'intro' | 'credits')}
                      {@const startMs = currentStart(kind as 'intro' | 'credits')}
                      {@const endMs = currentEnd(kind as 'intro' | 'credits')}
                      <div class="marker-row">
                        <div class="marker-head">
                          <span class="marker-kind">{kind === 'intro' ? 'Intro' : 'Credits'}</span>
                          {#if existing}
                            <span class="marker-src">{existing.source}</span>
                          {:else}
                            <span class="marker-src none">none</span>
                          {/if}
                        </div>
                        <div class="marker-times">
                          <button type="button" class="marker-set" on:click={() => captureNow(kind as 'intro' | 'credits', 'start')}>
                            Start: {fmtMs(startMs)}
                          </button>
                          <button type="button" class="marker-set" on:click={() => captureNow(kind as 'intro' | 'credits', 'end')}>
                            End: {fmtMs(endMs)}
                          </button>
                        </div>
                        <div class="marker-actions">
                          <button type="button" class="marker-save" disabled={markerBusy} on:click={() => saveMarker(kind as 'intro' | 'credits')}>
                            Save
                          </button>
                          {#if existing}
                            <button type="button" class="marker-clear" disabled={markerBusy} on:click={() => clearMarker(kind as 'intro' | 'credits')}>
                              Clear
                            </button>
                          {/if}
                        </div>
                      </div>
                    {/each}
                    {#if markerError}
                      <div class="marker-error">{markerError}</div>
                    {/if}
                    <div class="marker-hint">Tip: pause at the right frame, then click Start or End to capture the current time.</div>
                  </div>
                {/if}
              </div>
            {/if}

            <!-- Chapter picker -->
            {#if chapters.length > 0}
              <div class="quality-picker" on:click|stopPropagation>
                <button
                  class="icon-btn small quality-btn"
                  on:click|stopPropagation={() => { showChapterMenu = !showChapterMenu; showQualityMenu = false; showSubtitleMenu = false; showAudioMenu = false; }}
                  title="Chapters"
                >
                  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="16" height="16">
                    <path d="M4 6h16M4 12h16M4 18h10"/>
                  </svg>
                  <span class="quality-label">Ch {chapters.findIndex(c => c === currentChapter) + 1 || 1}/{chapters.length}</span>
                </button>
                {#if showChapterMenu}
                  <!-- svelte-ignore a11y-no-static-element-interactions -->
                  <div class="quality-menu chapter-menu" on:click|stopPropagation role="menu" aria-label="Chapters">
                    {#each chapters as ch, i}
                      <button
                        class="quality-option"
                        class:active={ch === currentChapter}
                        on:click={() => { jumpToChapter(ch.start_ms); showChapterMenu = false; }}
                        role="menuitem"
                      >
                        <span class="chapter-num">{i + 1}.</span>
                        <span class="chapter-title">{ch.title || `Chapter ${i + 1}`}</span>
                        <span class="chapter-time">{fmtTime(ch.start_ms / 1000)}</span>
                      </button>
                    {/each}
                  </div>
                {/if}
              </div>
            {/if}

            <!-- Add to playlist -->
            <button class="icon-btn small" on:click|stopPropagation={() => showPlaylistPicker = true} title="Add to playlist">
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="16" height="16">
                <path d="M12 5v14M5 12h14"/>
              </svg>
            </button>

            <!-- Subtitle picker -->
            <div class="quality-picker" on:click|stopPropagation>
                <button
                  class="icon-btn small quality-btn"
                  on:click|stopPropagation={() => { if (isMobile) { openMobileMenu('subtitle'); } else { showSubtitleMenu = !showSubtitleMenu; showQualityMenu = false; showAudioMenu = false; } }}
                  title="Subtitles"
                >
                  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="16" height="16">
                    <rect x="2" y="4" width="20" height="16" rx="2"/>
                    <path d="M7 15h4M13 15h4M7 11h10"/>
                  </svg>
                  <span class="quality-label">{selectedSubtitle ? (selectedSubtitle.language || 'Sub') : 'Off'}</span>
                </button>

                {#if showSubtitleMenu}
                  <!-- svelte-ignore a11y-no-static-element-interactions -->
                  <div class="quality-menu" on:click|stopPropagation role="menu" aria-label="Subtitle options">
                    <button
                      class="quality-option"
                      class:active={selectedSubtitle === null}
                      on:click={() => { selectedSubtitle = null; showSubtitleMenu = false; }}
                      role="menuitem"
                    >Off</button>
                    {#each textSubtitles as sub (sub.key)}
                      <button
                        class="quality-option"
                        class:active={selectedSubtitle?.key === sub.key}
                        on:click={() => { selectedSubtitle = sub; showSubtitleMenu = false; }}
                        role="menuitem"
                      >
                        {sub.label}
                        {#if sub.forced} (forced){/if}
                        {#if sub.origin === 'external'} · online{/if}
                      </button>
                    {/each}
                    <button
                      class="quality-option search-online-option"
                      on:click={() => { showSubtitleMenu = false; openSubtitleSearch(); }}
                      role="menuitem"
                    >Search online…</button>
                    {#if selectedSubtitle}
                      <div class="subtitle-size-row">
                        <span class="subtitle-size-label">Size</span>
                        {#each subtitleSizes as sz}
                          <button
                            class="subtitle-size-btn"
                            class:active={subtitleSize === sz}
                            on:click|stopPropagation={() => { subtitleSize = sz; localStorage.setItem('subtitle_size', sz); }}
                          >{sz[0].toUpperCase() + sz.slice(1)}</button>
                        {/each}
                      </div>
                      <div class="subtitle-size-row">
                        <span class="subtitle-size-label">Sync</span>
                        <button class="subtitle-size-btn" on:click|stopPropagation={() => subtitleDelay -= 0.5}>-0.5s</button>
                        <span class="subtitle-delay-value">{subtitleDelay > 0 ? '+' : ''}{subtitleDelay.toFixed(1)}s</span>
                        <button class="subtitle-size-btn" on:click|stopPropagation={() => subtitleDelay += 0.5}>+0.5s</button>
                        {#if subtitleDelay !== 0}
                          <button class="subtitle-size-btn" on:click|stopPropagation={() => subtitleDelay = 0}>Reset</button>
                        {/if}
                      </div>
                    {/if}
                  </div>
                {/if}
              </div>

            <!-- Audio track picker -->
            {#if audioStreams.length > 1}
              <div class="quality-picker" on:click|stopPropagation>
                <button
                  class="icon-btn small quality-btn"
                  on:click|stopPropagation={() => { if (isMobile) { openMobileMenu('audio'); } else { showAudioMenu = !showAudioMenu; showQualityMenu = false; showSubtitleMenu = false; } }}
                  title="Audio track"
                >
                  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="16" height="16">
                    <path d="M9 18V5l12-2v13"/>
                    <circle cx="6" cy="18" r="3"/><circle cx="18" cy="16" r="3"/>
                  </svg>
                  <span class="quality-label">{selectedAudioIndex >= 0 ? (audioStreams[selectedAudioIndex]?.language || `Track ${selectedAudioIndex}`) : (audioStreams[0]?.language || 'Default')}</span>
                </button>

                {#if showAudioMenu}
                  <!-- svelte-ignore a11y-no-static-element-interactions -->
                  <div class="quality-menu" on:click|stopPropagation role="menu" aria-label="Audio track options">
                    {#each audioStreams as stream, i}
                      <button
                        class="quality-option"
                        class:active={(selectedAudioIndex < 0 && i === 0) || selectedAudioIndex === i}
                        on:click={() => selectAudioTrack(i)}
                        role="menuitem"
                      >
                        {stream.title || stream.language || `Track ${stream.index}`}
                        {#if stream.language && stream.title} — {stream.language}{/if}
                        {#if stream.channels} ({stream.channels}ch){/if}
                      </button>
                    {/each}
                  </div>
                {/if}
              </div>
            {/if}

            <!-- Quality picker -->
            <div class="quality-picker" on:click|stopPropagation>
              <button
                class="icon-btn small quality-btn"
                on:click|stopPropagation={() => { if (isMobile) { openMobileMenu('quality'); } else { showQualityMenu = !showQualityMenu; showSubtitleMenu = false; showAudioMenu = false; } }}
                title="Quality"
              >
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="16" height="16">
                  <path d="M12 20h9M16.5 3.5a2.121 2.121 0 0 1 3 3L7 19l-4 1 1-4L16.5 3.5z"/>
                </svg>
                <span class="quality-label">{selectedQuality.label === 'Auto (Direct Play)' ? 'Auto' : selectedQuality.label}</span>
              </button>

              {#if showQualityMenu}
                <!-- svelte-ignore a11y-no-static-element-interactions -->
                <div
                  class="quality-menu"
                  on:click|stopPropagation
                  role="menu"
                  aria-label="Quality options"
                >
                  {#each availableQualities as q}
                    <button
                      class="quality-option"
                      class:active={q === selectedQuality}
                      on:click={() => selectQuality(q)}
                      role="menuitem"
                    >
                      {q.label}
                    </button>
                  {/each}
                </div>
              {/if}
            </div>

            <!-- Fullscreen -->
            <button class="icon-btn small" on:click={toggleFullscreen} title="Fullscreen (f)">
              {#if fullscreen}
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="18" height="18">
                  <path d="M8 3v3a2 2 0 0 1-2 2H3m18 0h-3a2 2 0 0 1-2-2V3m0 18v-3a2 2 0 0 1 2-2h3M3 16h3a2 2 0 0 1 2 2v3"/>
                </svg>
              {:else}
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="18" height="18">
                  <path d="M8 3H5a2 2 0 0 0-2 2v3m18 0V5a2 2 0 0 0-2-2h-3m0 18h3a2 2 0 0 0 2-2v-3M3 16v3a2 2 0 0 0 2 2h3"/>
                </svg>
              {/if}
            </button>
          </div>
        </div>
      </div>
    </div>

    <!-- Skip Intro / Skip Credits -->
    {#if currentMarker}
      <button class="skip-marker" on:click={skipMarker}>
        {currentMarker.kind === 'intro' ? 'Skip Intro' : 'Skip Credits'} →
      </button>
    {/if}

    <!-- Next episode prompt -->
    {#if showNextEpisodePrompt && nextEpisode}
      <div class="next-episode">
        <span class="next-label">Up Next{autoplayCountdown > 0 ? ` · ${autoplayCountdown}s` : ''}</span>
        <span class="next-title">{nextEpisode.title}</span>
        <a href="/watch/{nextEpisode.id}" class="next-btn">
          Play →
        </a>
        {#if autoplayCountdown > 0}
          <button class="next-cancel" on:click={() => cancelAutoplay(true)}>Cancel</button>
        {/if}
      </div>
    {/if}

    <!-- Double-tap ripple animation -->
    {#if showRipple}
      <div class="ripple-overlay {rippleSide}">
        <div class="ripple-circle" style="left:{rippleX}px;top:{rippleY}px"></div>
        <span class="ripple-label">{rippleSide === 'left' ? '−10s' : '+10s'}</span>
      </div>
    {/if}

    <!-- Touch gesture label -->
    {#if showGestureLabel}
      <div class="gesture-label">{gestureLabel}</div>
    {/if}

    <!-- Mobile bottom sheet menus -->
    {#if showBottomSheet}
      <!-- svelte-ignore a11y-click-events-have-key-events -->
      <!-- svelte-ignore a11y-no-static-element-interactions -->
      <div class="bottom-sheet-backdrop" on:click={() => { showBottomSheet = ''; }}>
        <!-- svelte-ignore a11y-click-events-have-key-events -->
        <!-- svelte-ignore a11y-no-static-element-interactions -->
        <div class="bottom-sheet" on:click|stopPropagation>
          <div class="bottom-sheet-handle"></div>
          {#if showBottomSheet === 'quality'}
            <div class="bottom-sheet-title">Quality</div>
            {#each availableQualities as q}
              <button
                class="bottom-sheet-option"
                class:active={q === selectedQuality}
                on:click={() => { selectQuality(q); showBottomSheet = ''; }}
              >{q.label}</button>
            {/each}
          {:else if showBottomSheet === 'subtitle'}
            <div class="bottom-sheet-title">Subtitles</div>
            <button
              class="bottom-sheet-option"
              class:active={selectedSubtitle === null}
              on:click={() => { selectedSubtitle = null; showBottomSheet = ''; }}
            >Off</button>
            {#each textSubtitles as sub (sub.key)}
              <button
                class="bottom-sheet-option"
                class:active={selectedSubtitle?.key === sub.key}
                on:click={() => { selectedSubtitle = sub; showBottomSheet = ''; }}
              >
                {sub.label}
                {#if sub.forced} (forced){/if}
                {#if sub.origin === 'external'} · online{/if}
              </button>
            {/each}
            <button
              class="bottom-sheet-option search-online-option"
              on:click={() => { showBottomSheet = ''; openSubtitleSearch(); }}
            >Search online…</button>
            {#if selectedSubtitle}
              <div class="subtitle-size-row" style="padding: 0.5rem 1.25rem;">
                <span class="subtitle-size-label">Size</span>
                {#each subtitleSizes as sz}
                  <button
                    class="subtitle-size-btn"
                    class:active={subtitleSize === sz}
                    on:click|stopPropagation={() => { subtitleSize = sz; localStorage.setItem('subtitle_size', sz); }}
                  >{sz[0].toUpperCase() + sz.slice(1)}</button>
                {/each}
              </div>
              <div class="subtitle-size-row" style="padding: 0.5rem 1.25rem;">
                <span class="subtitle-size-label">Sync</span>
                <button class="subtitle-size-btn" on:click|stopPropagation={() => subtitleDelay -= 0.5}>-0.5s</button>
                <span class="subtitle-delay-value">{subtitleDelay > 0 ? '+' : ''}{subtitleDelay.toFixed(1)}s</span>
                <button class="subtitle-size-btn" on:click|stopPropagation={() => subtitleDelay += 0.5}>+0.5s</button>
                {#if subtitleDelay !== 0}
                  <button class="subtitle-size-btn" on:click|stopPropagation={() => subtitleDelay = 0}>Reset</button>
                {/if}
              </div>
            {/if}
          {:else if showBottomSheet === 'audio'}
            <div class="bottom-sheet-title">Audio Track</div>
            {#each audioStreams as stream, i}
              <button
                class="bottom-sheet-option"
                class:active={(selectedAudioIndex < 0 && i === 0) || selectedAudioIndex === i}
                on:click={() => { selectAudioTrack(i); showBottomSheet = ''; }}
              >
                {stream.title || stream.language || `Track ${stream.index}`}
                {#if stream.language && stream.title} — {stream.language}{/if}
                {#if stream.channels} ({stream.channels}ch){/if}
              </button>
            {/each}
          {/if}
        </div>
      </div>
    {/if}

  {:else if item}
    <div class="center-msg">
      <p class="err-text">No playable file found for this item.</p>
      <button class="back-btn" on:click={goBack}>← Back</button>
    </div>
  {/if}
</div>
{/if}

{#if isDetailView && item}
<!-- Show / Season detail view -->
<div class="detail-page">
  <!-- Fanart hero -->
  {#if item.fanart_path}
    <div class="detail-hero" style="background-image:url('/artwork/{item.fanart_path}?v={item.updated_at}&w=1280')">
      <div class="detail-hero-fade"></div>
    </div>
  {/if}

  <div class="detail-content" class:no-hero={!item.fanart_path}>
    <button class="detail-back" on:click={() => history.back()}>
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" width="18" height="18"><polyline points="15 18 9 12 15 6"/></svg>
      Back
    </button>

    <div class="detail-header">
      {#if item.poster_path}
        <img class="detail-poster" src="/artwork/{encodeURI(item.poster_path)}?v={item.updated_at}&w=300"
             srcset="/artwork/{encodeURI(item.poster_path)}?v={item.updated_at}&w=150 150w, /artwork/{encodeURI(item.poster_path)}?v={item.updated_at}&w=300 300w, /artwork/{encodeURI(item.poster_path)}?v={item.updated_at}&w=600 600w"
             sizes="(max-width: 768px) 120px, 220px"
             alt="{item.title}" />
      {/if}
      <div class="detail-meta">
        <h1 class="detail-title">{item.title}</h1>
        <div class="detail-tags">
          {#if item.year}<span>{item.year}</span>{/if}
          {#if item.content_rating}<span>{item.content_rating}</span>{/if}
          {#if item.rating}<span>&#9733; {item.rating.toFixed(1)}</span>{/if}
        </div>
        {#if item.genres?.length}
          <div class="detail-genres">{item.genres.join(', ')}</div>
        {/if}
        {#if item.summary}
          <p class="detail-summary">{item.summary}</p>
        {/if}
      </div>
    </div>

    <!-- Fix Match button (shows and movies) -->
    {#if item.type === 'show' || item.type === 'movie'}
      <button class="fix-match-btn" on:click={openMatchModal}>
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="14" height="14"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg>
        Fix Match
      </button>
    {/if}

    {#if item.files?.length}
      <a
        class="download-btn"
        href="/media/stream/{item.files[0].id}"
        download={`${item.title}.${item.files[0].container ?? 'mkv'}`}
        title="Download the original file"
      >
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="14" height="14"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/></svg>
        Download
      </a>
    {/if}

    <!-- Season selector (shows only) -->
    {#if item.type === 'show' && seasons.length > 1}
      {#if seasons.length <= 9}
        <div class="season-tabs">
          {#each seasons as season}
            <button
              class="season-tab"
              class:active={selectedSeasonId === season.id}
              on:click={() => selectSeason(season.id)}
            >
              {season.title}
            </button>
          {/each}
        </div>
      {:else}
        <div class="season-dropdown">
          <select
            value={selectedSeasonId}
            on:change={(e) => selectSeason(e.currentTarget.value)}
          >
            {#each seasons as season}
              <option value={season.id}>{season.title}</option>
            {/each}
          </select>
        </div>
      {/if}
    {/if}

    <!-- Episode list (shows/seasons) -->
    {#if item.type === 'show' || item.type === 'season'}
      <div class="episode-list">
        {#each selectedEpisodes as ep}
          <a href="/watch/{ep.id}" class="episode-row">
            <div class="ep-number">{ep.index ?? '—'}</div>
            <div class="ep-info">
              <div class="ep-title">{ep.title}</div>
              {#if ep.summary}
                <div class="ep-summary">{ep.summary}</div>
              {/if}
            </div>
            {#if ep.duration_ms}
              <div class="ep-duration">{Math.round(ep.duration_ms / 60000)}m</div>
            {/if}
          </a>
        {/each}
        {#if selectedEpisodes.length === 0}
          <div class="ep-empty">No episodes found.</div>
        {/if}
      </div>
    {/if}

    <!-- Music scan button -->
    {#if item.type === 'artist' || item.type === 'album'}
      <button class="music-scan-btn" class:running={musicScanning} disabled={musicScanning} on:click={scanMusicLibrary}>
        {#if musicScanning}
          <span class="spin">⟳</span> Scanning…
        {:else}
          <svg viewBox="0 0 16 16" fill="currentColor" width="13" height="13"><path fill-rule="evenodd" d="M8 2.5A5.5 5.5 0 1013.5 8a.75.75 0 011.5 0 7 7 0 11-3.5-6.062V.75a.75.75 0 011.5 0v3a.75.75 0 01-.75.75h-3a.75.75 0 010-1.5h1.335A5.472 5.472 0 008 2.5z" clip-rule="evenodd"/></svg>
          Scan Library
        {/if}
      </button>
    {/if}

    <!-- Music: album grid (artist) or track list (album) -->
    {#if item.type === 'artist'}
      <div class="music-album-grid">
        {#each musicChildren as album}
          <a class="music-album-card" href="/watch/{album.id}">
            {#if album.poster_path}
              <img src="/artwork/{encodeURI(album.poster_path)}?v={album.updated_at}&w=300" alt={album.title} loading="lazy" />
            {:else}
              <div class="music-album-blank">♪</div>
            {/if}
            <div class="music-album-title">{album.title}</div>
            {#if album.year}<div class="music-album-year">{album.year}</div>{/if}
          </a>
        {/each}
        {#if musicChildren.length === 0}
          <div class="ep-empty">No albums found.</div>
        {/if}
      </div>
    {:else if item.type === 'album'}
      <div class="episode-list">
        {#each musicChildren as track}
          <a href="/watch/{track.id}" class="episode-row">
            <div class="ep-number">{track.index ?? '—'}</div>
            <div class="ep-info">
              <div class="ep-title">{track.title}</div>
            </div>
            {#if track.duration_ms}
              <div class="ep-duration">{Math.floor(track.duration_ms / 60000)}:{String(Math.floor((track.duration_ms % 60000) / 1000)).padStart(2, '0')}</div>
            {/if}
          </a>
        {/each}
        {#if musicChildren.length === 0}
          <div class="ep-empty">No tracks found.</div>
        {/if}
      </div>
    {/if}
  </div>
</div>
{/if}

<!-- Fix Match modal -->
{#if showMatchModal}
<!-- svelte-ignore a11y-click-events-have-key-events -->
<!-- svelte-ignore a11y-no-static-element-interactions -->
<div class="match-overlay" on:click={() => showMatchModal = false}>
  <!-- svelte-ignore a11y-click-events-have-key-events -->
  <!-- svelte-ignore a11y-no-static-element-interactions -->
  <div class="match-modal" on:click|stopPropagation>
    <h2>Fix Match</h2>
    <p class="match-hint">Search TMDB for the correct title and select it.</p>

    <form class="match-search-form" on:submit|preventDefault={searchMatch}>
      <input
        type="text"
        bind:value={matchQuery}
        placeholder="Search TMDB..."
        class="match-input"
        autofocus
      />
      <button type="submit" class="match-search-btn" disabled={matchSearching}>
        {matchSearching ? 'Searching...' : 'Search'}
      </button>
    </form>

    {#if matchError}
      <div class="match-error">{matchError}</div>
    {/if}

    <div class="match-results">
      {#each matchCandidates as c}
        <button
          class="match-result"
          disabled={matchApplying}
          on:click={() => applyMatch(c.tmdb_id)}
        >
          {#if c.poster_url}
            <img class="match-poster" src={c.poster_url} alt="" />
          {:else}
            <div class="match-poster-blank"></div>
          {/if}
          <div class="match-result-info">
            <div class="match-result-title">{c.title}</div>
            <div class="match-result-meta">
              {#if c.year}<span>{c.year}</span>{/if}
              {#if c.rating}<span>&#9733; {c.rating.toFixed(1)}</span>{/if}
            </div>
            {#if c.summary}
              <div class="match-result-summary">{c.summary}</div>
            {/if}
          </div>
        </button>
      {/each}
      {#if !matchSearching && matchCandidates.length === 0 && matchQuery}
        <div class="match-empty">No results found.</div>
      {/if}
    </div>

    <button class="match-cancel" on:click={() => showMatchModal = false}>Cancel</button>
  </div>
</div>
{/if}

{#if item}
  <PlaylistPicker
    mediaItemId={item.id}
    open={showPlaylistPicker}
    on:close={() => showPlaylistPicker = false}
  />
{/if}

{#if showSubtitleSearch}
  <!-- svelte-ignore a11y-click-events-have-key-events -->
  <!-- svelte-ignore a11y-no-static-element-interactions -->
  <div class="subsearch-backdrop" on:click={() => { showSubtitleSearch = false; }}>
    <div class="subsearch-modal" on:click|stopPropagation>
      <div class="subsearch-header">
        <h2>Search subtitles</h2>
        <button class="subsearch-close" on:click={() => { showSubtitleSearch = false; }} aria-label="Close">×</button>
      </div>
      <form class="subsearch-form" on:submit|preventDefault={runSubtitleSearch}>
        <input
          class="subsearch-input"
          type="text"
          placeholder="Title"
          bind:value={subtitleSearchQuery}
        />
        <select class="subsearch-lang" bind:value={subtitleSearchLang}>
          <option value="en">English</option>
          <option value="es">Spanish</option>
          <option value="fr">French</option>
          <option value="de">German</option>
          <option value="it">Italian</option>
          <option value="pt">Portuguese</option>
          <option value="ja">Japanese</option>
          <option value="ko">Korean</option>
          <option value="zh">Chinese</option>
          <option value="ru">Russian</option>
          <option value="ar">Arabic</option>
          <option value="hi">Hindi</option>
          <option value="nl">Dutch</option>
          <option value="sv">Swedish</option>
          <option value="pl">Polish</option>
        </select>
        <button type="submit" class="subsearch-submit" disabled={subtitleSearchLoading}>
          {subtitleSearchLoading ? 'Searching…' : 'Search'}
        </button>
      </form>
      {#if subtitleSearchError}
        <div class="subsearch-error">{subtitleSearchError}</div>
      {/if}
      <div class="subsearch-results">
        {#if subtitleSearchLoading && subtitleSearchResults.length === 0}
          <div class="subsearch-empty">Loading…</div>
        {:else if subtitleSearchResults.length === 0 && !subtitleSearchError}
          <div class="subsearch-empty">No results yet.</div>
        {:else}
          {#each subtitleSearchResults as res (res.provider_file_id)}
            <button
              class="subsearch-result"
              on:click={() => downloadSubtitle(res)}
              disabled={subtitleDownloadingId !== null}
            >
              <div class="subsearch-result-top">
                <span class="subsearch-result-title">{res.release || res.file_name}</span>
                <span class="subsearch-result-lang">{res.language}</span>
              </div>
              <div class="subsearch-result-meta">
                {#if res.hd}<span>HD</span>{/if}
                {#if res.from_trusted}<span>Trusted</span>{/if}
                {#if res.hearing_impaired}<span>SDH</span>{/if}
                {#if res.download_count > 0}<span>↓ {res.download_count.toLocaleString()}</span>{/if}
                {#if res.rating > 0}<span>★ {res.rating.toFixed(1)}</span>{/if}
                {#if res.uploader_name}<span>@{res.uploader_name}</span>{/if}
              </div>
              {#if subtitleDownloadingId === res.provider_file_id}
                <div class="subsearch-result-downloading">Downloading…</div>
              {/if}
            </button>
          {/each}
        {/if}
      </div>
    </div>
  </div>
{/if}

<style>
  .player-container {
    position: fixed;
    inset: 0;
    background: #000;
    display: flex;
    align-items: center;
    justify-content: center;
    overflow: hidden;
  }
  .player-container.hide-cursor { cursor: none; }

  .video {
    width: 100%;
    height: 100%;
    object-fit: contain;
    display: block;
    position: relative;
    z-index: 1;
  }
  /* ── JS subtitle overlay ────────────────────────────── */
  .subtitle-overlay {
    position: absolute;
    bottom: 10%;
    left: 5%;
    right: 5%;
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 0.2em;
    z-index: 4;
    pointer-events: none;
    text-align: center;
  }
  .subtitle-cue {
    background: rgba(0, 0, 0, 0.75);
    color: #fff;
    padding: 0.15em 0.5em;
    border-radius: 3px;
    line-height: 1.4;
    text-shadow: 1px 1px 2px rgba(0,0,0,0.8);
  }
  .subtitle-overlay.subs-small .subtitle-cue { font-size: 1rem; }
  .subtitle-overlay.subs-medium .subtitle-cue { font-size: 1.4rem; }
  .subtitle-overlay.subs-large .subtitle-cue { font-size: 2rem; }

  .fanart-bg {
    position: absolute;
    inset: 0;
    background-size: cover;
    background-position: center;
    filter: blur(40px) brightness(0.2);
    transform: scale(1.1);
    z-index: 0;
    pointer-events: none;
  }

  /* ── Controls overlay ─────────────────────────────────── */
  .controls-overlay {
    position: absolute;
    inset: 0;
    display: flex;
    flex-direction: column;
    justify-content: space-between;
    opacity: 0;
    transition: opacity 0.25s;
    z-index: 10;
  }
  .controls-overlay.visible { opacity: 1; }

  .top-bar {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    padding: 1.25rem 1.5rem;
    padding-top: max(1.25rem, env(safe-area-inset-top, 0px));
    padding-left: max(1.5rem, env(safe-area-inset-left, 0px));
    padding-right: max(1.5rem, env(safe-area-inset-right, 0px));
    background: linear-gradient(to bottom, rgba(0,0,0,0.7) 0%, transparent 100%);
  }

  .top-title {
    display: flex;
    flex-direction: column;
    flex: 1;
    min-width: 0;
  }
  .favorite-btn { color: rgba(255,255,255,0.85); }
  .favorite-btn.is-favorite { color: #f87171; }
  .favorite-btn:disabled { opacity: 0.6; cursor: progress; }
  .top-title-main {
    font-size: 0.95rem;
    font-weight: 600;
    color: #fff;
    line-height: 1.2;
  }
  .top-title-sub {
    font-size: 0.75rem;
    color: rgba(255,255,255,0.55);
  }

  .bottom-bar {
    padding: 0 1.5rem 1.25rem;
    padding-bottom: max(1.25rem, env(safe-area-inset-bottom, 0px));
    padding-left: max(1.5rem, env(safe-area-inset-left, 0px));
    padding-right: max(1.5rem, env(safe-area-inset-right, 0px));
    background: linear-gradient(to top, rgba(0,0,0,0.8) 0%, transparent 100%);
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }

  /* ── Seek bar ─────────────────────────────────────────── */
  .seek-bar {
    width: 100%;
    height: 18px;
    cursor: pointer;
    display: flex;
    align-items: center;
    padding: 6px 0;
    box-sizing: border-box;
    position: relative;
  }

  /* Hover preview: sprite crop + time label, anchored above the cursor. */
  .seek-preview {
    position: absolute;
    bottom: 22px;
    transform: translateX(-50%);
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 4px;
    pointer-events: none;
    z-index: 2;
  }
  .seek-preview-thumb {
    background-repeat: no-repeat;
    border: 1px solid rgba(255,255,255,0.25);
    border-radius: 4px;
    box-shadow: 0 4px 12px rgba(0,0,0,0.6);
  }
  .seek-preview-time {
    background: rgba(0,0,0,0.8);
    color: #fff;
    font-size: 0.75rem;
    font-variant-numeric: tabular-nums;
    padding: 2px 6px;
    border-radius: 3px;
  }
  .seek-track {
    position: relative;
    width: 100%;
    height: 4px;
    background: rgba(255,255,255,0.2);
    border-radius: 2px;
    overflow: visible;
  }
  .seek-buffered {
    position: absolute;
    top: 0; left: 0;
    height: 100%;
    background: rgba(255,255,255,0.3);
    border-radius: 2px;
    pointer-events: none;
  }
  .seek-progress {
    position: absolute;
    top: 0; left: 0;
    height: 100%;
    background: #7c6af7;
    border-radius: 2px;
    pointer-events: none;
  }
  .seek-chapter {
    position: absolute;
    top: -1px;
    width: 2px;
    height: calc(100% + 2px);
    background: rgba(255,255,255,0.85);
    transform: translateX(-1px);
    pointer-events: none;
    border-radius: 1px;
  }
  .seek-thumb {
    position: absolute;
    top: 50%;
    transform: translate(-50%, -50%);
    width: 14px;
    height: 14px;
    background: #fff;
    border-radius: 50%;
    pointer-events: none;
    box-shadow: 0 1px 4px rgba(0,0,0,0.5);
    transition: transform 0.1s;
  }
  .seek-bar:hover .seek-thumb { transform: translate(-50%, -50%) scale(1.3); }
  .seek-bar:hover .seek-track { height: 5px; }

  /* ── Controls row ─────────────────────────────────────── */
  .controls-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
  }
  .controls-left, .controls-right {
    display: flex;
    align-items: center;
    gap: 0.25rem;
  }

  .icon-btn {
    background: none;
    border: none;
    color: rgba(255,255,255,0.9);
    cursor: pointer;
    padding: 0.4rem;
    border-radius: 6px;
    display: flex;
    align-items: center;
    justify-content: center;
    transition: background 0.12s, color 0.12s;
  }
  .icon-btn:hover { background: rgba(255,255,255,0.1); color: #fff; }
  .icon-btn.small { padding: 0.3rem; }

  .back-btn {
    background: none;
    border: none;
    color: rgba(255,255,255,0.85);
    cursor: pointer;
    padding: 0.35rem 0.5rem;
    border-radius: 6px;
    display: flex;
    align-items: center;
    gap: 0.25rem;
    transition: background 0.12s;
  }
  .back-btn:hover { background: rgba(255,255,255,0.1); color: #fff; }

  .volume-slider {
    width: 70px;
    height: 4px;
    -webkit-appearance: none;
    appearance: none;
    background: rgba(255,255,255,0.3);
    border-radius: 2px;
    cursor: pointer;
    outline: none;
  }
  .volume-slider::-webkit-slider-thumb {
    -webkit-appearance: none;
    width: 12px; height: 12px;
    border-radius: 50%;
    background: #fff;
    cursor: pointer;
  }

  .time {
    font-size: 0.78rem;
    color: rgba(255,255,255,0.75);
    font-variant-numeric: tabular-nums;
    white-space: nowrap;
    padding-left: 0.25rem;
  }

  /* ── Quality picker ───────────────────────────────────── */
  .quality-picker {
    position: relative;
  }

  .quality-btn {
    gap: 0.3rem;
    font-size: 0.72rem;
    font-weight: 600;
    color: rgba(255,255,255,0.85);
  }

  .quality-label {
    font-size: 0.72rem;
    font-weight: 600;
    letter-spacing: 0.01em;
  }

  .quality-menu {
    position: absolute;
    bottom: calc(100% + 8px);
    right: 0;
    background: rgba(15,15,25,0.95);
    border: 1px solid rgba(255,255,255,0.12);
    border-radius: 8px;
    padding: 0.35rem;
    display: flex;
    flex-direction: column;
    min-width: 160px;
    z-index: 30;
    backdrop-filter: blur(12px);
    box-shadow: 0 4px 24px rgba(0,0,0,0.5);
  }

  .quality-option {
    background: none;
    border: none;
    color: rgba(255,255,255,0.8);
    cursor: pointer;
    padding: 0.45rem 0.75rem;
    border-radius: 5px;
    text-align: left;
    font-size: 0.8rem;
    transition: background 0.1s, color 0.1s;
    white-space: nowrap;
  }
  .quality-option:hover { background: rgba(255,255,255,0.1); color: #fff; }
  .quality-option.active { color: #7c6af7; font-weight: 600; }

  .chapter-menu { min-width: 240px; max-height: 280px; overflow-y: auto; }
  .chapter-menu .quality-option {
    display: grid;
    grid-template-columns: auto 1fr auto;
    gap: 0.5rem;
    align-items: baseline;
  }
  .chapter-num { color: rgba(255,255,255,0.45); font-variant-numeric: tabular-nums; }
  .chapter-title { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .chapter-time { color: rgba(255,255,255,0.45); font-size: 0.7rem; font-variant-numeric: tabular-nums; }

  /* ── Marker editor (admin) ───────────────────────────── */
  .marker-menu { min-width: 280px; padding: 0.5rem; gap: 0.4rem; }
  .marker-row {
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
    padding: 0.5rem;
    border-radius: 6px;
    background: rgba(255,255,255,0.03);
    border: 1px solid rgba(255,255,255,0.06);
  }
  .marker-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.5rem;
  }
  .marker-kind {
    color: #fff;
    font-size: 0.8rem;
    font-weight: 600;
    letter-spacing: 0.02em;
  }
  .marker-src {
    color: rgba(255,255,255,0.55);
    font-size: 0.65rem;
    text-transform: uppercase;
    letter-spacing: 0.08em;
  }
  .marker-src.none { color: rgba(255,255,255,0.3); }
  .marker-times { display: flex; gap: 0.35rem; }
  .marker-set {
    flex: 1;
    background: rgba(255,255,255,0.06);
    border: 1px solid rgba(255,255,255,0.1);
    color: rgba(255,255,255,0.85);
    cursor: pointer;
    padding: 0.35rem 0.5rem;
    border-radius: 5px;
    font-size: 0.72rem;
    font-variant-numeric: tabular-nums;
    transition: background 0.1s, border-color 0.1s;
  }
  .marker-set:hover { background: rgba(124,106,247,0.25); border-color: rgba(124,106,247,0.5); }
  .marker-actions { display: flex; gap: 0.35rem; }
  .marker-save,
  .marker-clear {
    flex: 1;
    border: none;
    padding: 0.4rem 0.5rem;
    border-radius: 5px;
    font-size: 0.75rem;
    font-weight: 600;
    cursor: pointer;
    transition: background 0.1s, opacity 0.1s;
  }
  .marker-save { background: #7c6af7; color: #fff; }
  .marker-save:hover:not(:disabled) { background: #8c7bff; }
  .marker-clear {
    background: rgba(255,90,90,0.15);
    color: #ff8a8a;
    border: 1px solid rgba(255,90,90,0.25);
  }
  .marker-clear:hover:not(:disabled) { background: rgba(255,90,90,0.25); }
  .marker-save:disabled,
  .marker-clear:disabled { opacity: 0.5; cursor: not-allowed; }
  .marker-error {
    color: #ff8a8a;
    font-size: 0.72rem;
    padding: 0.25rem 0.35rem;
  }
  .marker-hint {
    color: rgba(255,255,255,0.45);
    font-size: 0.68rem;
    line-height: 1.35;
    padding: 0.25rem 0.35rem;
  }

  /* ── Next episode ─────────────────────────────────────── */
  .skip-marker {
    position: absolute;
    bottom: 5rem;
    right: 1.5rem;
    padding: 0.6rem 1.1rem;
    background: rgba(15,15,25,0.85);
    border: 1px solid rgba(255,255,255,0.18);
    border-radius: 8px;
    color: #fff;
    font-size: 0.85rem;
    font-weight: 600;
    cursor: pointer;
    z-index: 21;
    backdrop-filter: blur(8px);
    transition: background 0.12s, border-color 0.12s;
  }
  .skip-marker:hover {
    background: rgba(124,106,247,0.9);
    border-color: rgba(124,106,247,0.9);
  }

  .next-episode {
    position: absolute;
    bottom: 5rem;
    right: 1.5rem;
    background: rgba(15,15,25,0.9);
    border: 1px solid rgba(255,255,255,0.1);
    border-radius: 10px;
    padding: 0.75rem 1rem;
    display: flex;
    flex-direction: column;
    gap: 0.35rem;
    z-index: 20;
    backdrop-filter: blur(8px);
  }
  .next-label {
    font-size: 0.65rem;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: #7c6af7;
    font-weight: 700;
  }
  .next-title {
    font-size: 0.8rem;
    color: #eeeef8;
    font-weight: 500;
    max-width: 200px;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .next-btn {
    align-self: flex-end;
    padding: 0.3rem 0.7rem;
    background: #7c6af7;
    border-radius: 6px;
    color: #fff;
    font-size: 0.75rem;
    font-weight: 600;
    text-decoration: none;
    transition: background 0.12s;
  }
  .next-btn:hover { background: #8f7ef9; }
  .next-cancel {
    align-self: flex-end;
    padding: 0.25rem 0.6rem;
    background: transparent;
    border: 1px solid rgba(255,255,255,0.18);
    border-radius: 6px;
    color: rgba(255,255,255,0.7);
    font-size: 0.7rem;
    cursor: pointer;
    transition: background 0.12s, color 0.12s;
  }
  .next-cancel:hover { background: rgba(255,255,255,0.08); color: #fff; }

  /* ── Loading / error ─────────────────────────────────── */
  .center-msg {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 1rem;
    color: #eeeef8;
  }
  .err-text { font-size: 0.9rem; color: #fca5a5; margin: 0; }

  .spinner {
    width: 36px; height: 36px;
    border: 3px solid rgba(255,255,255,0.15);
    border-top-color: #7c6af7;
    border-radius: 50%;
    animation: spin 0.8s linear infinite;
  }
  @keyframes spin { to { transform: rotate(360deg); } }

  .tonemap-banner {
    position: absolute;
    top: 12px;
    left: 50%;
    transform: translateX(-50%);
    background: rgba(0, 0, 0, 0.8);
    color: #fbbf24;
    font-size: 0.8rem;
    padding: 8px 16px;
    border-radius: 6px;
    border: 1px solid rgba(251, 191, 36, 0.3);
    z-index: 20;
    max-width: 90%;
    text-align: center;
    pointer-events: none;
    animation: fadeout 0s 10s forwards;
  }
  @keyframes fadeout { to { opacity: 0; } }

  .buffer-overlay {
    position: absolute;
    inset: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    pointer-events: none;
    z-index: 5;
  }

  /* ── Subtitle size control ─────────────────────────── */
  .subtitle-size-row {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    padding: 0.5rem 0.75rem;
    border-top: 1px solid rgba(255,255,255,0.08);
    margin-top: 0.25rem;
  }
  .subtitle-size-label {
    font-size: 0.72rem;
    color: rgba(255,255,255,0.5);
    margin-right: 0.25rem;
  }
  .subtitle-size-btn {
    background: rgba(255,255,255,0.08);
    border: 1px solid rgba(255,255,255,0.12);
    color: rgba(255,255,255,0.7);
    border-radius: 4px;
    padding: 0.2rem 0.5rem;
    font-size: 0.7rem;
    cursor: pointer;
  }
  .subtitle-size-btn.active {
    background: #7c6af7;
    border-color: #7c6af7;
    color: #fff;
  }
  .subtitle-delay-value {
    font-size: 0.72rem;
    color: rgba(255,255,255,0.7);
    min-width: 2.5rem;
    text-align: center;
  }

  /* ── Detail view (shows / seasons) ───────────────── */
  .detail-page {
    position: fixed; inset: 0;
    background: var(--bg-primary);
    overflow-y: auto;
    color: var(--text-primary);
  }
  .detail-hero {
    position: relative;
    width: 100%; height: 340px;
    background-size: cover;
    background-position: center top;
  }
  .detail-hero-fade {
    position: absolute; inset: 0;
    background: linear-gradient(to bottom, transparent 30%, var(--bg-primary) 100%);
  }
  .detail-content {
    position: relative;
    max-width: 900px;
    margin: -100px auto 0;
    padding: 0 2rem 3rem;
    z-index: 1;
  }
  .detail-content.no-hero {
    margin-top: 2rem;
  }
  .detail-back {
    display: inline-flex; align-items: center; gap: 0.35rem;
    background: none; border: none;
    color: var(--text-secondary); font-size: 0.8rem;
    cursor: pointer; margin-bottom: 1.25rem;
    padding: 0;
  }
  .detail-back:hover { color: var(--text-primary); }

  .detail-header {
    display: flex; gap: 1.5rem;
    margin-bottom: 2rem;
  }
  .detail-poster {
    width: 160px; height: auto;
    border-radius: 8px;
    object-fit: cover;
    flex-shrink: 0;
    box-shadow: 0 4px 24px var(--shadow);
  }
  .detail-meta { flex: 1; min-width: 0; }
  .detail-title {
    font-size: 1.6rem; font-weight: 700;
    letter-spacing: -0.02em;
    margin: 0 0 0.5rem;
  }
  .detail-tags {
    display: flex; gap: 0.75rem;
    font-size: 0.8rem; color: var(--text-muted);
    margin-bottom: 0.4rem;
  }
  .detail-genres { font-size: 0.78rem; color: var(--text-muted); margin-bottom: 0.75rem; }
  .detail-summary {
    font-size: 0.82rem; color: var(--text-secondary);
    line-height: 1.6; margin: 0;
    display: -webkit-box;
    -webkit-line-clamp: 4;
    -webkit-box-orient: vertical;
    overflow: hidden;
  }

  /* Season tabs */
  .season-tabs {
    display: flex; gap: 0.25rem;
    margin-bottom: 1.25rem;
    overflow-x: auto;
    scrollbar-width: none;
  }
  .season-tabs::-webkit-scrollbar { display: none; }
  .season-tab {
    padding: 0.4rem 0.9rem;
    background: var(--input-bg);
    border: 1px solid var(--border);
    border-radius: 6px;
    color: var(--text-muted); font-size: 0.78rem; font-weight: 500;
    cursor: pointer; white-space: nowrap;
    transition: all 0.12s;
  }
  .season-tab:hover { color: var(--text-secondary); border-color: var(--border-strong); }
  .season-tab.active {
    background: var(--accent-bg);
    border-color: var(--accent);
    color: var(--accent-text);
  }
  .season-dropdown {
    margin-bottom: 1.25rem;
  }
  .season-dropdown select {
    background: var(--input-bg);
    border: 1px solid var(--border-strong);
    border-radius: 6px;
    color: var(--text-primary);
    font-size: 0.82rem;
    font-weight: 500;
    padding: 0.45rem 2rem 0.45rem 0.75rem;
    cursor: pointer;
    appearance: none;
    background-image: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='12' height='12' fill='%23777' viewBox='0 0 16 16'%3E%3Cpath d='M8 11L3 6h10z'/%3E%3C/svg%3E");
    background-repeat: no-repeat;
    background-position: right 0.6rem center;
  }
  .season-dropdown select:focus {
    outline: none;
    border-color: var(--accent);
    box-shadow: 0 0 0 3px var(--accent-bg);
  }
  .season-dropdown select option {
    background: var(--bg-elevated);
    color: var(--text-primary);
  }

  /* Episode list */
  .episode-list {
    display: flex; flex-direction: column;
  }
  .episode-row {
    display: flex; align-items: flex-start; gap: 1rem;
    padding: 0.85rem 0.5rem;
    border-bottom: 1px solid var(--border);
    text-decoration: none; color: inherit;
    transition: background 0.1s;
  }
  .episode-row:hover { background: var(--bg-hover); }
  .ep-number {
    width: 2rem; flex-shrink: 0;
    font-size: 0.85rem; font-weight: 600;
    color: var(--text-muted); text-align: center;
    padding-top: 0.1rem;
  }
  .ep-info { flex: 1; min-width: 0; }
  .ep-title {
    font-size: 0.88rem; font-weight: 500;
    color: var(--text-primary); margin-bottom: 0.2rem;
  }
  .ep-summary {
    font-size: 0.75rem; color: var(--text-muted);
    line-height: 1.5;
    display: -webkit-box;
    -webkit-line-clamp: 2;
    -webkit-box-orient: vertical;
    overflow: hidden;
  }
  .ep-duration {
    font-size: 0.75rem; color: var(--text-muted);
    flex-shrink: 0; padding-top: 0.15rem;
  }
  .ep-empty {
    padding: 2rem; text-align: center;
    font-size: 0.85rem; color: var(--text-muted);
  }

  /* Fix Match button */
  .fix-match-btn {
    display: inline-flex; align-items: center; gap: 0.35rem;
    background: var(--input-bg);
    border: 1px solid var(--border-strong);
    border-radius: 6px;
    color: var(--text-muted); font-size: 0.75rem; font-weight: 500;
    cursor: pointer; padding: 0.35rem 0.7rem;
    margin-bottom: 1.5rem;
    transition: all 0.12s;
  }
  .fix-match-btn:hover { color: var(--text-secondary); border-color: var(--border-strong); background: var(--bg-hover); }

  .download-btn {
    display: inline-flex; align-items: center; gap: 0.35rem;
    background: var(--input-bg);
    border: 1px solid var(--border-strong);
    border-radius: 6px;
    color: var(--text-muted); font-size: 0.75rem; font-weight: 500;
    cursor: pointer; padding: 0.35rem 0.7rem;
    margin-bottom: 1.5rem; margin-left: 0.5rem;
    text-decoration: none;
    transition: all 0.12s;
  }
  .download-btn:hover { color: var(--text-secondary); border-color: var(--border-strong); background: var(--bg-hover); }

  /* Match modal overlay */
  .match-overlay {
    position: fixed; inset: 0;
    background: rgba(0,0,0,0.7);
    z-index: 100;
    display: flex; align-items: center; justify-content: center;
    padding: 1rem;
  }
  .match-modal {
    background: var(--bg-elevated);
    border: 1px solid var(--border-strong);
    border-radius: 12px;
    width: 100%; max-width: 520px; max-height: 80vh;
    display: flex; flex-direction: column;
    padding: 1.5rem;
    overflow: hidden;
  }
  .match-modal h2 {
    font-size: 1.1rem; font-weight: 700; color: var(--text-primary);
    margin: 0 0 0.25rem;
  }
  .match-hint {
    font-size: 0.75rem; color: var(--text-muted); margin: 0 0 1rem;
  }
  .match-search-form {
    display: flex; gap: 0.5rem; margin-bottom: 0.75rem;
  }
  .match-input {
    flex: 1;
    background: var(--input-bg);
    border: 1px solid var(--border);
    border-radius: 7px;
    padding: 0.45rem 0.7rem;
    font-size: 0.85rem; color: var(--text-primary);
    outline: none;
  }
  .match-input:focus { border-color: var(--accent); box-shadow: 0 0 0 3px var(--accent-bg); }
  .match-search-btn {
    padding: 0.45rem 0.8rem;
    background: var(--accent); border: none; border-radius: 7px;
    color: #fff; font-size: 0.8rem; font-weight: 600;
    cursor: pointer; white-space: nowrap;
  }
  .match-search-btn:hover { background: var(--accent-hover); }
  .match-search-btn:disabled { opacity: 0.5; cursor: not-allowed; }
  .match-error {
    font-size: 0.78rem; color: var(--error);
    padding: 0.4rem 0; margin-bottom: 0.5rem;
  }
  .match-results {
    flex: 1; overflow-y: auto; min-height: 0;
    display: flex; flex-direction: column; gap: 0.25rem;
    margin-bottom: 1rem;
  }
  .match-result {
    display: flex; align-items: flex-start; gap: 0.75rem;
    padding: 0.65rem 0.5rem;
    background: none; border: 1px solid transparent;
    border-radius: 8px; cursor: pointer;
    text-align: left; color: inherit;
    transition: all 0.1s;
  }
  .match-result:hover { background: var(--bg-hover); border-color: var(--border); }
  .match-result:disabled { opacity: 0.5; cursor: wait; }
  .match-poster {
    width: 48px; height: 72px; object-fit: cover;
    border-radius: 4px; flex-shrink: 0;
    background: var(--bg-elevated);
  }
  .match-poster-blank {
    width: 48px; height: 72px;
    border-radius: 4px; flex-shrink: 0;
    background: var(--bg-elevated);
  }
  .match-result-info { flex: 1; min-width: 0; }
  .match-result-title { font-size: 0.85rem; font-weight: 500; color: #ddd; }
  .match-result-meta {
    display: flex; gap: 0.5rem;
    font-size: 0.75rem; color: #666;
    margin-top: 0.15rem;
  }
  .match-result-summary {
    font-size: 0.72rem; color: #555; line-height: 1.5;
    margin-top: 0.3rem;
    display: -webkit-box;
    -webkit-line-clamp: 2;
    -webkit-box-orient: vertical;
    overflow: hidden;
  }
  .match-empty {
    padding: 1.5rem; text-align: center;
    font-size: 0.8rem; color: #444;
  }
  .match-cancel {
    align-self: flex-end;
    background: none; border: 1px solid var(--border);
    border-radius: 6px; padding: 0.35rem 0.8rem;
    color: #777; font-size: 0.78rem; cursor: pointer;
  }
  .match-cancel:hover { color: #bbb; border-color: rgba(255,255,255,0.15); }

  /* ── Photo viewer ───────────────────────────────────── */
  .photo-viewer {
    position: fixed;
    inset: 0;
    background: #000;
    display: flex;
    align-items: center;
    justify-content: center;
    overflow: hidden;
    cursor: grab;
  }
  .photo-viewer:active { cursor: grabbing; }
  .photo-image {
    max-width: 100%;
    max-height: 100%;
    object-fit: contain;
    user-select: none;
    transition: transform 0.1s ease-out;
  }
  .photo-toolbar {
    position: absolute;
    top: 0;
    left: 0;
    right: 0;
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.75rem 1rem;
    background: linear-gradient(to bottom, rgba(0,0,0,0.7), transparent);
    z-index: 10;
    opacity: 0;
    transition: opacity 0.3s;
  }
  .photo-viewer:hover .photo-toolbar { opacity: 1; }
  .photo-title {
    flex: 1;
    color: #fff;
    font-size: 0.95rem;
    font-weight: 500;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .photo-btn {
    display: flex;
    align-items: center;
    gap: 0.25rem;
    background: rgba(255,255,255,0.1);
    border: none;
    border-radius: 6px;
    color: #fff;
    padding: 0.4rem 0.6rem;
    cursor: pointer;
    font-size: 0.85rem;
  }
  .photo-btn:hover { background: rgba(255,255,255,0.2); }
  .photo-btn svg { width: 20px; height: 20px; }
  .photo-counter {
    color: rgba(255,255,255,0.6);
    font-size: 0.8rem;
    white-space: nowrap;
  }
  .photo-nav {
    position: absolute;
    top: 50%;
    transform: translateY(-50%);
    background: rgba(0,0,0,0.4);
    border: none;
    border-radius: 50%;
    color: #fff;
    width: 48px;
    height: 48px;
    display: flex;
    align-items: center;
    justify-content: center;
    cursor: pointer;
    z-index: 10;
    opacity: 0;
    transition: opacity 0.3s, background 0.2s;
  }
  .photo-viewer:hover .photo-nav { opacity: 1; }
  .photo-nav:hover { background: rgba(0,0,0,0.7); }
  .photo-nav-left { left: 1rem; }
  .photo-nav-right { right: 1rem; }

  /* ── Music ──────────────────────────────────────────── */
  .music-scan-btn {
    display: inline-flex; align-items: center; gap: 6px;
    margin: 0 1.5rem 1rem;
    padding: 6px 14px;
    background: transparent;
    border: 1px solid rgba(255,255,255,0.15);
    border-radius: 6px;
    color: rgba(255,255,255,0.7);
    font-size: 0.78rem;
    cursor: pointer;
    transition: border-color 0.15s, color 0.15s;
  }
  .music-scan-btn:hover { border-color: rgba(124,106,247,0.5); color: #fff; }
  .music-scan-btn.running { opacity: 0.6; cursor: not-allowed; }
  .music-scan-btn:disabled { cursor: not-allowed; }
  .music-album-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(140px, 1fr));
    gap: 1rem;
    padding: 0 1.5rem 2rem;
  }
  .music-album-card {
    text-decoration: none;
    color: inherit;
    display: flex;
    flex-direction: column;
    gap: 0.4rem;
  }
  .music-album-card img {
    width: 100%;
    aspect-ratio: 1;
    object-fit: cover;
    border-radius: 8px;
    background: var(--bg-elevated);
  }
  .music-album-blank {
    width: 100%;
    aspect-ratio: 1;
    border-radius: 8px;
    background: var(--bg-elevated);
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 2.5rem;
    color: #333;
  }
  .music-album-title {
    font-size: 0.85rem;
    font-weight: 500;
    color: #e0e0e0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .music-album-year {
    font-size: 0.75rem;
    color: #666;
  }
  .music-album-card:hover .music-album-title { color: #fff; }

  /* ── Double-tap ripple ───────────────────────────────── */
  .ripple-overlay {
    position: absolute;
    inset: 0;
    pointer-events: none;
    z-index: 15;
    display: flex;
    align-items: center;
  }
  .ripple-overlay.left { justify-content: flex-start; padding-left: 3rem; }
  .ripple-overlay.right { justify-content: flex-end; padding-right: 3rem; }
  .ripple-circle {
    position: absolute;
    width: 80px;
    height: 80px;
    border-radius: 50%;
    background: rgba(255,255,255,0.15);
    transform: translate(-50%, -50%) scale(0);
    animation: rippleExpand 0.5s ease-out forwards;
  }
  @keyframes rippleExpand {
    0% { transform: translate(-50%, -50%) scale(0); opacity: 1; }
    100% { transform: translate(-50%, -50%) scale(3); opacity: 0; }
  }
  .ripple-label {
    color: #fff;
    font-size: 1.1rem;
    font-weight: 700;
    text-shadow: 0 1px 4px rgba(0,0,0,0.6);
    animation: rippleFade 0.5s ease-out forwards;
  }
  @keyframes rippleFade {
    0% { opacity: 1; transform: scale(1); }
    100% { opacity: 0; transform: scale(1.2); }
  }

  /* ── Touch gesture label ─────────────────────────────── */
  .gesture-label {
    position: absolute;
    top: 50%;
    left: 50%;
    transform: translate(-50%, -50%);
    background: rgba(0,0,0,0.7);
    color: #fff;
    font-size: 1rem;
    font-weight: 600;
    padding: 0.5rem 1rem;
    border-radius: 8px;
    pointer-events: none;
    z-index: 16;
    backdrop-filter: blur(4px);
  }

  /* ── Online subtitle search modal ─────────────────────── */
  .search-online-option {
    border-top: 1px solid rgba(255,255,255,0.08);
    font-weight: 500;
    color: #8ab4ff;
  }
  .subsearch-backdrop {
    position: fixed;
    inset: 0;
    background: rgba(0,0,0,0.72);
    z-index: 90;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 1rem;
    backdrop-filter: blur(4px);
  }
  .subsearch-modal {
    background: rgba(18,18,28,0.98);
    border: 1px solid rgba(255,255,255,0.08);
    border-radius: 12px;
    width: 100%;
    max-width: 640px;
    max-height: 80vh;
    display: flex;
    flex-direction: column;
    color: #fff;
    overflow: hidden;
  }
  .subsearch-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 1rem 1.25rem 0.5rem;
  }
  .subsearch-header h2 {
    margin: 0;
    font-size: 1.05rem;
    font-weight: 600;
  }
  .subsearch-close {
    background: none;
    border: none;
    color: rgba(255,255,255,0.6);
    font-size: 1.6rem;
    line-height: 1;
    cursor: pointer;
    padding: 0 0.25rem;
  }
  .subsearch-close:hover { color: #fff; }
  .subsearch-form {
    display: flex;
    gap: 0.5rem;
    padding: 0.5rem 1.25rem 0.75rem;
  }
  .subsearch-input {
    flex: 1;
    background: rgba(255,255,255,0.06);
    border: 1px solid rgba(255,255,255,0.1);
    color: #fff;
    padding: 0.5rem 0.75rem;
    border-radius: 6px;
    font-size: 0.9rem;
  }
  .subsearch-lang {
    background: rgba(255,255,255,0.06);
    border: 1px solid rgba(255,255,255,0.1);
    color: #fff;
    padding: 0.5rem;
    border-radius: 6px;
    font-size: 0.9rem;
  }
  .subsearch-submit {
    background: #3b82f6;
    border: none;
    color: #fff;
    padding: 0.5rem 1rem;
    border-radius: 6px;
    cursor: pointer;
    font-size: 0.9rem;
    font-weight: 500;
  }
  .subsearch-submit:disabled { opacity: 0.5; cursor: default; }
  .subsearch-error {
    margin: 0 1.25rem 0.75rem;
    padding: 0.5rem 0.75rem;
    background: rgba(239,68,68,0.15);
    border: 1px solid rgba(239,68,68,0.3);
    border-radius: 6px;
    color: #fca5a5;
    font-size: 0.85rem;
  }
  .subsearch-results {
    overflow-y: auto;
    flex: 1;
    padding: 0 0.5rem 0.75rem;
  }
  .subsearch-empty {
    text-align: center;
    color: rgba(255,255,255,0.5);
    padding: 2rem 0;
    font-size: 0.9rem;
  }
  .subsearch-result {
    display: block;
    width: 100%;
    text-align: left;
    background: transparent;
    border: 1px solid transparent;
    border-radius: 8px;
    color: #fff;
    padding: 0.65rem 0.75rem;
    cursor: pointer;
    margin-bottom: 0.25rem;
  }
  .subsearch-result:hover {
    background: rgba(255,255,255,0.05);
    border-color: rgba(255,255,255,0.1);
  }
  .subsearch-result:disabled { opacity: 0.6; cursor: default; }
  .subsearch-result-top {
    display: flex;
    justify-content: space-between;
    gap: 0.5rem;
    align-items: baseline;
    margin-bottom: 0.25rem;
  }
  .subsearch-result-title {
    font-size: 0.9rem;
    font-weight: 500;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    min-width: 0;
    flex: 1;
  }
  .subsearch-result-lang {
    font-size: 0.75rem;
    text-transform: uppercase;
    color: rgba(255,255,255,0.6);
    letter-spacing: 0.05em;
    flex: none;
  }
  .subsearch-result-meta {
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem;
    font-size: 0.75rem;
    color: rgba(255,255,255,0.55);
  }
  .subsearch-result-downloading {
    font-size: 0.75rem;
    color: #8ab4ff;
    margin-top: 0.25rem;
  }

  /* ── Bottom sheet (mobile menus) ─────────────────────── */
  .bottom-sheet-backdrop {
    position: absolute;
    inset: 0;
    background: rgba(0,0,0,0.5);
    z-index: 40;
    display: flex;
    align-items: flex-end;
    justify-content: center;
  }
  .bottom-sheet {
    background: rgba(15,15,25,0.97);
    border-top-left-radius: 16px;
    border-top-right-radius: 16px;
    width: 100%;
    max-width: 420px;
    max-height: 60vh;
    overflow-y: auto;
    padding: 0.5rem 0 1rem;
    backdrop-filter: blur(16px);
    animation: sheetUp 0.2s ease-out;
  }
  @keyframes sheetUp {
    from { transform: translateY(100%); }
    to { transform: translateY(0); }
  }
  .bottom-sheet-handle {
    width: 36px;
    height: 4px;
    border-radius: 2px;
    background: rgba(255,255,255,0.25);
    margin: 0.5rem auto 0.75rem;
  }
  .bottom-sheet-title {
    font-size: 0.82rem;
    font-weight: 600;
    color: rgba(255,255,255,0.6);
    text-transform: uppercase;
    letter-spacing: 0.06em;
    padding: 0 1.25rem 0.5rem;
  }
  .bottom-sheet-option {
    display: block;
    width: 100%;
    background: none;
    border: none;
    color: rgba(255,255,255,0.85);
    font-size: 0.92rem;
    padding: 0.75rem 1.25rem;
    text-align: left;
    cursor: pointer;
    transition: background 0.12s;
  }
  .bottom-sheet-option:hover { background: rgba(255,255,255,0.06); }
  .bottom-sheet-option.active {
    color: #7c6af7;
    font-weight: 600;
  }

  /* ── Mobile responsive ───────────────────────────────── */
  @media (max-width: 768px), (pointer: coarse) {
    /* Larger touch targets */
    .icon-btn {
      padding: 0.6rem;
      min-width: 44px;
      min-height: 44px;
    }
    .icon-btn.small {
      padding: 0.5rem;
      min-width: 44px;
      min-height: 44px;
    }

    /* Wider seek bar touch target */
    .seek-bar {
      height: 28px;
      padding: 8px 0;
    }
    .seek-track {
      height: 8px;
      border-radius: 4px;
    }
    .seek-thumb {
      width: 18px;
      height: 18px;
    }

    /* Hide volume slider on mobile (replaced by gesture) */
    .volume-slider { display: none; }

    /* Compact controls for smaller screens */
    .top-bar { padding: 0.75rem 1rem; }
    .bottom-bar { padding: 0 1rem 0.75rem; }

    .controls-left, .controls-right { gap: 0; }

    .time { font-size: 0.72rem; }

    .quality-label { display: none; }

    /* Next episode card */
    .next-episode {
      right: 0.75rem;
      bottom: 4.5rem;
    }

    /* Detail page mobile adjustments */
    .detail-content { padding: 0 1rem 2rem; }
    .detail-header { flex-direction: column; gap: 1rem; }
    .detail-poster { width: 120px; }
    .detail-title { font-size: 1.3rem; }
  }
</style>
