<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { audio, currentTrack, nextTrack, type AudioTrack } from '$lib/stores/audio';
  import { itemApi, getApiBase, getBearerToken, assetUrl } from '$lib/api';
  import { isTauri, audioPlayUrl, audioPreloadUrl, audioPause, audioResume, audioSeek, stopAudio, audioState, onMediaKey } from '$lib/native';
  import { nativeEngine } from '$lib/stores/nativeEngine';

  // Two audio elements rotated for gapless playback. `audioElA` and
  // `audioElB` swap roles every track: when one is "active" (playing
  // the current track), the other is "preload" (idle, with the next
  // track's bytes already in the browser cache + codec init done).
  // On `ended` we swap roles synchronously — sub-frame transition
  // instead of the ~250 ms a fresh src= + decode-init costs.
  let audioElA: HTMLAudioElement;
  let audioElB: HTMLAudioElement;
  let activeIsA = true;

  // Helper accessors instead of reactive `$:` aliases — Svelte's
  // dependency tracker would see `audioEl` mutations elsewhere in
  // the file (audioEl.src = ...) and flag a cycle. Plain functions
  // sidestep the reactivity entirely.
  function activeEl(): HTMLAudioElement { return activeIsA ? audioElA : audioElB; }
  function preloadEl(): HTMLAudioElement { return activeIsA ? audioElB : audioElA; }

  let loadedSrc = '';
  let preloadSrc = '';
  let durationMS = 0;
  let positionMS = 0;
  let scrubbing = false;
  let scrubMS = 0;
  let volume = 1;
  let muted = false;

  let track: AudioTrack | null = null;
  let upcoming: AudioTrack | null = null;
  let playing = false;
  let shuffle = false;
  let repeat: 'off' | 'one' | 'all' = 'off';

  // Native audio engine routing — when enabled in Tauri, FLAC tracks
  // bypass <audio> entirely and stream through the Rust cpal+claxon
  // pipeline. The two paths are mutually exclusive: when nativeActive
  // is true we never set src on the <audio> elements (they stay
  // silent), and when false the native engine is stopped so it
  // doesn't compete with the browser audio.
  let useNativeEngine = false;
  const unsubE = nativeEngine.subscribe((v) => { useNativeEngine = v; });
  // The "is native actually doing the playing right now" flag is
  // composed of three things: opt-in preference, in Tauri, and a
  // track is loaded. Cached as a function so the reactive blocks
  // below can branch on it without repeated lookups.
  function nativeActive(): boolean {
    return useNativeEngine && isTauri() && !!track;
  }
  // Tracks the URL the engine was last asked to play so we don't
  // re-call audio_play_url on irrelevant reactive triggers (volume
  // changes, position scrubs, etc.). Mirrors the loadedSrc field
  // used by the <audio> path.
  let nativeLoadedUrl = '';
  // Polling handle for the engine→UI sync (position display +
  // auto-advance on EOS). 250 ms is the same cadence the existing
  // `<audio>` `timeupdate` event fires at — keeps the seek bar
  // ticking without churning subscribers per frame.
  let nativePollHandle: ReturnType<typeof setInterval> | null = null;

  // Scrobble cadence — report `playing` every 10s so Continue Watching reflects
  // current position without flooding the API. Pause/stop are reported immediately.
  let lastReportedMS = 0;
  let lastReportedID = '';

  const unsubA = audio.subscribe((s) => {
    const wasPlaying = playing;
    playing = s.playing;
    shuffle = s.shuffle;
    repeat = s.repeat;
    // Edge: paused — report a discrete pause event for the active track.
    if (wasPlaying && !s.playing && track) {
      void report('paused');
    }
  });
  let prevTrack: AudioTrack | null = null;
  const unsubT = currentTrack.subscribe((t) => {
    // When the active track changes, mark the previous one stopped so it leaves
    // any "Now Playing" surfaces immediately.
    if (prevTrack && (!t || prevTrack.id !== t.id)) {
      void itemApi
        .progress(prevTrack.id, positionMS, durationMS || (prevTrack.durationMS ?? 0), 'stopped')
        .catch(() => {});
    }
    prevTrack = t;
    track = t;
    lastReportedMS = 0;
    lastReportedID = '';
  });
  const unsubN = nextTrack.subscribe((n) => {
    upcoming = n;
  });

  async function report(state: 'playing' | 'paused' | 'stopped') {
    if (!track) return;
    try {
      await itemApi.progress(track.id, positionMS, durationMS || (track.durationMS ?? 0), state);
      lastReportedMS = positionMS;
      lastReportedID = track.id;
    } catch { /* offline — drop the event */ }
  }

  // OS media-key listener handle. Registered on mount, torn down
  // on destroy. No-op in the browser bundle.
  let mediaKeyUnlisten: (() => void) | null = null;

  // Restore volume from localStorage so it persists across reloads.
  onMount(() => {
    const v = localStorage.getItem('onscreen_audio_volume');
    if (v !== null) {
      const n = parseFloat(v);
      if (!Number.isNaN(n)) volume = Math.max(0, Math.min(1, n));
    }
    const m = localStorage.getItem('onscreen_audio_muted');
    if (m === '1') muted = true;

    // Wire OS media keys → audio store. The Rust side registers the
    // shortcuts globally so they fire whether or not OnScreen is
    // focused. Guards against double-firing while no track is
    // loaded — pressing play-pause with an empty queue does nothing
    // rather than getting stuck in a paused-but-empty state.
    void onMediaKey((action) => {
      switch (action) {
        case 'play-pause':
          if (track) audio.togglePlay();
          break;
        case 'next':
          audio.next();
          break;
        case 'previous':
          audio.prev();
          break;
        case 'stop':
          audio.clear();
          break;
      }
    }).then((unlisten) => {
      mediaKeyUnlisten = unlisten;
    });
  });

  onDestroy(() => {
    unsubA(); unsubT(); unsubN(); unsubE();
    if (nativePollHandle) clearInterval(nativePollHandle);
    if (mediaKeyUnlisten) mediaKeyUnlisten();
    // Stop the native engine on player destroy so it doesn't keep
    // playing after navigation away from a route that owns the
    // AudioPlayer (currently only the root layout owns it, but keep
    // the cleanup safe for future component-scoped reuse).
    if (isTauri()) void stopAudio();
  });

  // Native engine → UI sync. Polls audio_state every 250 ms while
  // native playback is active so the seek bar ticks and auto-advance
  // fires on EOS. Skipped when nativeActive is false so we don't
  // burn an interval timer in browser builds. The poll cadence is
  // intentionally the same as `<audio>` timeupdate — we want the
  // two paths to feel identical to the user.
  function startNativePolling() {
    if (nativePollHandle) return;
    nativePollHandle = setInterval(async () => {
      try {
        const s = await audioState();
        if (!s.playing) {
          // Engine stopped on its own (most likely a play error
          // before any sample landed). Drop the poll loop and let
          // the next track-change reactive block start a fresh one.
          stopNativePolling();
          return;
        }
        // Push position into the store so the seek bar + scrobble
        // logic see the same number the <audio> path would emit.
        // Skip while scrubbing so we don't fight the user's drag.
        if (!scrubbing) {
          positionMS = s.position_ms;
          audio.setPosition(positionMS);
        }
        if (s.ended) {
          // Decoder hit EOS. Mark the finished track stopped at full
          // duration (matches the <audio> onEnded path) and advance.
          // The advance triggers a track change, which kicks off a
          // new audioPlayUrl call — by the time the engine sees the
          // new source, ended is back to false.
          if (track) {
            const d = durationMS || (track.durationMS ?? 0);
            void itemApi.progress(track.id, d, d, 'stopped').catch(() => {});
          }
          stopNativePolling();
          audio.next();
        }
      } catch {
        // IPC failure — most likely the engine is between tracks.
        // Polling resumes on the next tick.
      }
    }, 250);
  }

  function stopNativePolling() {
    if (nativePollHandle) {
      clearInterval(nativePollHandle);
      nativePollHandle = null;
    }
  }

  // Native engine routing. Mirrors the <audio> reactive blocks but
  // calls the Rust IPC instead of touching DOM elements. Runs first
  // so on a track change we kick off the engine before the <audio>
  // block decides whether to attach src — and the <audio> block
  // skips its work entirely when nativeActive() is true.
  $: if (track && nativeActive()) {
    // Resolve the absolute URL the Rust ureq client can fetch. The
    // api.ts apiBase is either same-origin "/api/v1" (browser) or
    // "<server>/api/v1" (Tauri). For Tauri it's always absolute so
    // dropping /api/v1 → /media/stream/<id> gives us the right URL.
    const base = getApiBase().replace(/\/api\/v1\/?$/, '');
    const desired = `${base}/media/stream/${track.fileId}`;
    if (nativeLoadedUrl !== desired) {
      nativeLoadedUrl = desired;
      positionMS = 0;
      durationMS = (track.durationMS ?? 0);
      // Pass the bearer so the engine's HTTP fetch can authenticate
      // — same auth as the api.ts wrapper uses for everything else.
      // The play call is fire-and-forget; engine errors are logged
      // to the console, the user sees the play button stuck on (no
      // position update yet — Phase 2 polling fixes that).
      void audioPlayUrl(desired, getBearerToken(), null).then(() => {
        startNativePolling();
      }).catch((err) => {
        console.warn('native engine play failed:', err);
        stopNativePolling();
        // On engine failure (most likely: non-FLAC source),
        // disable native for this session so the <audio> fallback
        // takes over on the next track change. User can re-enable
        // via /native/server.
        nativeEngine.set(false);
        nativeLoadedUrl = '';
      });
    }
  } else if (!track && nativeLoadedUrl !== '') {
    nativeLoadedUrl = '';
    stopNativePolling();
    if (isTauri()) void stopAudio();
  }

  // Pause/resume sync for native playback. The <audio> block below
  // only fires when loadedSrc is set; its no-op path during native
  // playback is correct. This block handles the native equivalent.
  $: if (nativeActive() && nativeLoadedUrl) {
    if (playing) {
      void audioResume();
    } else {
      void audioPause();
    }
  }

  // Optimistically preload the next track on the native engine when
  // upcoming changes — same trigger as the existing <audio> preload
  // block but going through the engine's audio_preload_url IPC. The
  // engine spawns a decoder thread + ringbuf so the matching
  // audio_play_url call (when we advance to this track) skips the
  // HTTP + claxon round-trip and the gap between tracks shrinks
  // from ~200-500 ms to whatever cpal's device-activation cost is
  // (~10-20 ms on every host we care about).
  let nativePreloadedUrl = '';
  $: if (nativeActive() && upcoming && track && upcoming.id !== track.id) {
    const base = getApiBase().replace(/\/api\/v1\/?$/, '');
    const desired = `${base}/media/stream/${upcoming.fileId}`;
    if (nativePreloadedUrl !== desired) {
      nativePreloadedUrl = desired;
      void audioPreloadUrl(desired, getBearerToken());
    }
  } else if (nativeActive() && !upcoming) {
    nativePreloadedUrl = '';
  }

  // Swap source when track changes; set src='' when track cleared.
  // Two paths: (1) the new track is what the preload element already
  // has buffered → flip activeIsA, no fresh src= load; (2) it's not
  // (user picked a different track manually, no preload happened in
  // time, etc.) → fall back to loading on the active element.
  // Skipped entirely when the native engine owns playback — the
  // <audio> elements stay silent so they don't double-play.
  $: if (audioElA && audioElB && track && !nativeActive()) {
    // Wrap with assetUrl so cross-origin native builds (when the
    // user has the engine OFF and falls back to <audio>) hit the
    // configured server, not the Tauri webview origin. Browser
    // same-origin builds get the path back unchanged.
    const desired = assetUrl(`/media/stream/${track.fileId}`);
    if (loadedSrc !== desired) {
      if (preloadSrc === desired) {
        // Gapless path: the next-track element is already primed —
        // flip roles and play. The browser's already done codec init
        // and (usually) buffered the head of the file, so the swap
        // is sub-frame audible-wise.
        activeIsA = !activeIsA;
        loadedSrc = desired;
        preloadSrc = '';
        // The newly-active element keeps whatever currentTime it had
        // (typically 0 since it was idle). Reset position display.
        positionMS = 0;
        durationMS = (track.durationMS ?? 0);
      } else {
        // Cold path: src= load on the active element. Same code as
        // before this change — gapless only kicks in for natural
        // queue advancement.
        loadedSrc = desired;
        const el = activeEl();
        el.src = desired;
        el.currentTime = 0;
        positionMS = 0;
        durationMS = (track.durationMS ?? 0);
      }
    }
  } else if (audioElA && audioElB && !track && loadedSrc !== '') {
    loadedSrc = '';
    const el = activeEl();
    el.removeAttribute('src');
    el.load();
  }

  // Keep the preload element pointed at the next track. Skipped when
  // upcoming === current (repeat=one) — would just hammer the same
  // file pointlessly. Re-runs whenever the queue changes shape so
  // toggling shuffle / appending / reordering gets reflected.
  // Skipped entirely under native engine — gapless preload there
  // is the engine's responsibility (next commit on the audio track).
  $: if (audioElA && audioElB && upcoming && track && upcoming.id !== track.id && !nativeActive()) {
    const desired = assetUrl(`/media/stream/${upcoming.fileId}`);
    if (preloadSrc !== desired) {
      preloadSrc = desired;
      const el = preloadEl();
      el.src = desired;
      el.currentTime = 0;
      // load() forces the browser to start fetching + codec init now
      // rather than waiting for the first play() call. preload="auto"
      // would do this implicitly but isn't reliable across browsers
      // when the element is currently muted/silent.
      el.load();
    }
  } else if (audioElA && audioElB && !upcoming && preloadSrc !== '') {
    preloadSrc = '';
    const el = preloadEl();
    el.removeAttribute('src');
    el.load();
  }

  // Mirror playing flag to the element. Browser autoplay policies may reject;
  // catch and pause the store so the UI matches reality. Skipped when
  // native engine is active — its own pause/resume sync block above
  // handles the same flag flip via IPC.
  $: if (audioElA && audioElB && loadedSrc && !nativeActive()) {
    const el = activeEl();
    if (playing && el.paused) {
      el.play().catch(() => audio.pause());
    } else if (!playing && !el.paused) {
      el.pause();
    }
  }

  // activeIsA referenced directly (not via activeEl() helper) so Svelte's
  // dep tracker re-runs this block on gapless swap. Without that, the
  // element that was the silent preload buffer becomes active and plays
  // the next track at volume=0.
  $: if (audioElA && audioElB) {
    const active = activeIsA ? audioElA : audioElB;
    const preload = activeIsA ? audioElB : audioElA;
    active.volume = muted ? 0 : volume;
    preload.volume = 0;
  }

  function persistVolume() {
    try {
      localStorage.setItem('onscreen_audio_volume', String(volume));
      localStorage.setItem('onscreen_audio_muted', muted ? '1' : '0');
    } catch { /* private mode */ }
  }

  function onTimeUpdate() {
    if (scrubbing) return;
    const el = activeEl();
    if (!el) return;
    positionMS = Math.round(el.currentTime * 1000);
    audio.setPosition(positionMS);
    // Periodic playing-state scrobble so resume position survives reload.
    if (playing && track && (track.id !== lastReportedID || Math.abs(positionMS - lastReportedMS) >= 10000)) {
      void report('playing');
    }
  }

  function onLoadedMeta() {
    const el = activeEl();
    if (!el) return;
    if (Number.isFinite(el.duration) && el.duration > 0) {
      durationMS = Math.round(el.duration * 1000);
    }
  }

  function onEnded() {
    // Mark the finished track as stopped at full duration so it doesn't linger
    // as "in progress" — then advance.
    if (track) {
      const d = durationMS || (track.durationMS ?? 0);
      void itemApi.progress(track.id, d, d, 'stopped').catch(() => {});
    }
    audio.next();
  }

  function onAudioError() {
    // Skip past unplayable files instead of getting stuck.
    audio.next();
  }

  function startScrub(e: Event) {
    scrubbing = true;
    updateScrub(e);
  }
  function updateScrub(e: Event) {
    const input = e.target as HTMLInputElement;
    scrubMS = parseInt(input.value, 10);
    positionMS = scrubMS;
  }
  function commitScrub() {
    if (nativeActive() && track && Number.isFinite(scrubMS)) {
      // Native engine path. Optimistic store update so the seek bar
      // snaps to the dropped position immediately rather than
      // rubber-banding back to the old position for a frame.
      //
      // Polling is suspended around the IPC because audio_seek
      // tears down the current pipeline before building the new one;
      // a poll tick landing in the in-between (engine.current = None)
      // window would see playing=false and exit the loop. Restart
      // after the seek settles — at that point the new pipeline is
      // already producing samples so the next tick gets a clean
      // playing=true reading.
      const target = scrubMS;
      audio.setPosition(target);
      positionMS = target;
      stopNativePolling();
      audioSeek(target, getBearerToken(), null)
        .catch((err) => console.warn('native engine seek failed:', err))
        .finally(() => startNativePolling());
      void report(playing ? 'playing' : 'paused');
    } else {
      const el = activeEl();
      if (el && Number.isFinite(scrubMS)) {
        el.currentTime = scrubMS / 1000;
        audio.setPosition(scrubMS);
        if (track) void report(playing ? 'playing' : 'paused');
      }
    }
    scrubbing = false;
  }

  function fmt(ms: number): string {
    if (!Number.isFinite(ms) || ms < 0) ms = 0;
    const s = Math.floor(ms / 1000);
    const m = Math.floor(s / 60);
    const sec = s % 60;
    return `${m}:${String(sec).padStart(2, '0')}`;
  }

  function toggleMute() {
    muted = !muted;
    persistVolume();
  }

  function onVolumeChange(e: Event) {
    const v = parseFloat((e.target as HTMLInputElement).value);
    volume = v;
    if (volume > 0) muted = false;
    persistVolume();
  }
</script>

<!-- Two audio elements rotated for gapless transitions. The "active"
     one (whichever activeIsA points at) carries the timeupdate /
     ended / error handlers; the "preload" one just buffers the next
     track. Roles flip on track change; we wire handlers to BOTH
     elements so they always fire on whichever is currently active. -->
<audio
  bind:this={audioElA}
  on:timeupdate={() => activeIsA && onTimeUpdate()}
  on:loadedmetadata={() => activeIsA && onLoadedMeta()}
  on:ended={() => activeIsA && onEnded()}
  on:error={() => activeIsA && onAudioError()}
  preload="auto"
></audio>
<audio
  bind:this={audioElB}
  on:timeupdate={() => !activeIsA && onTimeUpdate()}
  on:loadedmetadata={() => !activeIsA && onLoadedMeta()}
  on:ended={() => !activeIsA && onEnded()}
  on:error={() => !activeIsA && onAudioError()}
  preload="auto"
></audio>

{#if track}
  <aside class="player" aria-label="Audio player">
    <div class="left">
      <button class="close-btn" on:click={() => audio.clear()}
              title="Close player" aria-label="Close player">
        <svg viewBox="0 0 16 16" width="14" height="14" fill="currentColor">
          <path d="M2.146 2.854a.5.5 0 1 1 .708-.708L8 7.293l5.146-5.147a.5.5 0 0 1 .708.708L8.707 8l5.147 5.146a.5.5 0 0 1-.708.708L8 8.707l-5.146 5.147a.5.5 0 0 1-.708-.708L7.293 8 2.146 2.854z"/>
        </svg>
      </button>
      {#if track.posterPath}
        <img class="art"
             src={assetUrl(`/artwork/${encodeURI(track.posterPath)}?w=120`)}
             alt={track.album ?? ''} />
      {:else}
        <div class="art placeholder">♪</div>
      {/if}
      <div class="meta">
        <div class="title">{track.title}</div>
        <div class="sub">
          {#if track.artist}
            {#if track.artistId}
              <a href="/artists/{track.artistId}">{track.artist}</a>
            {:else}{track.artist}{/if}
          {/if}
          {#if track.artist && track.album} · {/if}
          {#if track.album}
            {#if track.albumId}
              <a href="/albums/{track.albumId}">{track.album}</a>
            {:else}{track.album}{/if}
          {/if}
        </div>
      </div>
    </div>

    <div class="center">
      <div class="transport">
        <button class="t-btn" class:on={shuffle} on:click={() => audio.toggleShuffle()}
                title="Shuffle" aria-label="Shuffle" aria-pressed={shuffle}>
          <svg viewBox="0 0 16 16" width="14" height="14" fill="currentColor">
            <path d="M0 3.5A.5.5 0 0 1 .5 3H1c2.202 0 3.827 1.24 4.874 2.418.49.552.865 1.102 1.126 1.532a1.5 1.5 0 0 1-.001 1.65l-.121.193A12.6 12.6 0 0 0 5.874 9.582C4.827 10.76 3.202 12 1 12H.5a.5.5 0 0 1 0-1H1c1.798 0 3.173-1.01 4.126-2.082.473-.532.806-1.06 1.014-1.396a.5.5 0 0 0 0-.514c-.208-.336-.541-.864-1.014-1.396C4.173 4.51 2.798 3.5 1 3.5H.5a.5.5 0 0 1-.5-.5z"/>
            <path d="M13 5.466V4.5a.25.25 0 0 1 .41-.192l2.36 1.966c.12.1.12.284 0 .384l-2.36 1.966A.25.25 0 0 1 13 8.434V7.5c-1.473 0-2.42 1.05-3.084 2.05L9.36 9.7l.39-.6.234-.413c.65-1.124 1.652-2.221 3.016-2.221zM13 10.466V11.5a.25.25 0 0 0 .41.192l2.36-1.966a.25.25 0 0 0 0-.384l-2.36-1.966A.25.25 0 0 0 13 7.566v.934c-1.473 0-2.42-1.05-3.084-2.05l-.234-.413a14.6 14.6 0 0 1-.39-.6l-.391.625C8.231 6.954 7.225 7.5 5.5 7.5H5a.5.5 0 0 0 0 1h.5c1.725 0 2.731.546 3.401 1.438.144.193.273.39.391.575l.39.6.234.413C10.58 11.95 11.527 13 13 13z"/>
          </svg>
        </button>
        <button class="t-btn" on:click={() => audio.prev()} title="Previous" aria-label="Previous">
          <svg viewBox="0 0 16 16" width="16" height="16" fill="currentColor">
            <path d="M.5 3.5A.5.5 0 0 1 1 4v3.248l6.267-3.636c.52-.302 1.233.043 1.233.696v2.94l6.267-3.636c.52-.302 1.233.043 1.233.696v7.384c0 .653-.713.998-1.233.696L8.5 8.752v2.94c0 .653-.713.998-1.233.696L1 8.752V12a.5.5 0 0 1-1 0V4a.5.5 0 0 1 .5-.5z"/>
          </svg>
        </button>
        <button class="t-btn play" on:click={() => audio.togglePlay()}
                title={playing ? 'Pause' : 'Play'} aria-label={playing ? 'Pause' : 'Play'}>
          {#if playing}
            <svg viewBox="0 0 16 16" width="18" height="18" fill="currentColor">
              <path d="M5.5 3.5A1.5 1.5 0 0 1 7 5v6a1.5 1.5 0 0 1-3 0V5a1.5 1.5 0 0 1 1.5-1.5zm5 0A1.5 1.5 0 0 1 12 5v6a1.5 1.5 0 0 1-3 0V5a1.5 1.5 0 0 1 1.5-1.5z"/>
            </svg>
          {:else}
            <svg viewBox="0 0 16 16" width="18" height="18" fill="currentColor">
              <path d="m11.596 8.697-6.363 3.692c-.54.313-1.233-.066-1.233-.697V4.308c0-.63.692-1.01 1.233-.696l6.363 3.692a.802.802 0 0 1 0 1.393z"/>
            </svg>
          {/if}
        </button>
        <button class="t-btn" on:click={() => audio.next()} title="Next" aria-label="Next">
          <svg viewBox="0 0 16 16" width="16" height="16" fill="currentColor">
            <path d="M15.5 3.5A.5.5 0 0 0 15 4v3.248L8.733 3.612C8.213 3.31 7.5 3.655 7.5 4.308v2.94L1.233 3.612C.713 3.31 0 3.655 0 4.308v7.384c0 .653.713.998 1.233.696L7.5 8.752v2.94c0 .653.713.998 1.233.696L15 8.752V12a.5.5 0 0 0 1 0V4a.5.5 0 0 0-.5-.5z"/>
          </svg>
        </button>
        <button class="t-btn" class:on={repeat !== 'off'} on:click={() => audio.cycleRepeat()}
                title="Repeat: {repeat}" aria-label="Repeat: {repeat}">
          {#if repeat === 'one'}
            <svg viewBox="0 0 16 16" width="14" height="14" fill="currentColor">
              <path d="M11 5.466V4H5a4 4 0 0 0-3.584 5.777.5.5 0 1 1-.896.446A5 5 0 0 1 5 3h6V1.534a.25.25 0 0 1 .41-.192l2.36 1.966c.12.1.12.284 0 .384l-2.36 1.966a.25.25 0 0 1-.41-.192zm3.81.086a.5.5 0 0 1 .67.225A5 5 0 0 1 11 13H5v1.466a.25.25 0 0 1-.41.192l-2.36-1.966a.25.25 0 0 1 0-.384l2.36-1.966a.25.25 0 0 1 .41.192V11h6a4 4 0 0 0 3.585-5.777.5.5 0 0 1 .225-.67z"/>
              <path d="M9 5.5a.5.5 0 0 0-.854-.354l-1.5 1.5a.5.5 0 1 0 .708.708L8 6.707V10.5a.5.5 0 0 0 1 0z"/>
            </svg>
          {:else}
            <svg viewBox="0 0 16 16" width="14" height="14" fill="currentColor">
              <path d="M11 5.466V4H5a4 4 0 0 0-3.584 5.777.5.5 0 1 1-.896.446A5 5 0 0 1 5 3h6V1.534a.25.25 0 0 1 .41-.192l2.36 1.966c.12.1.12.284 0 .384l-2.36 1.966a.25.25 0 0 1-.41-.192zm3.81.086a.5.5 0 0 1 .67.225A5 5 0 0 1 11 13H5v1.466a.25.25 0 0 1-.41.192l-2.36-1.966a.25.25 0 0 1 0-.384l2.36-1.966a.25.25 0 0 1 .41.192V11h6a4 4 0 0 0 3.585-5.777.5.5 0 0 1 .225-.67z"/>
            </svg>
          {/if}
        </button>
      </div>
      <div class="seek-row">
        <span class="t">{fmt(positionMS)}</span>
        <input
          type="range"
          class="seek"
          min="0"
          max={Math.max(durationMS, 1)}
          step="1000"
          value={positionMS}
          on:input={updateScrub}
          on:mousedown={startScrub}
          on:touchstart={startScrub}
          on:change={commitScrub}
          aria-label="Seek"
        />
        <span class="t">{fmt(durationMS)}</span>
      </div>
    </div>

    <div class="right">
      <button class="vol-btn" on:click={toggleMute} aria-label={muted ? 'Unmute' : 'Mute'}>
        {#if muted || volume === 0}
          <svg viewBox="0 0 16 16" width="14" height="14" fill="currentColor"><path d="M6.717 3.55A.5.5 0 0 1 7 4v8a.5.5 0 0 1-.812.39L3.825 10.5H1.5A.5.5 0 0 1 1 10V6a.5.5 0 0 1 .5-.5h2.325l2.363-1.89a.5.5 0 0 1 .529-.06zm5.927.346a.5.5 0 0 1 .708 0L15 5.293l1.646-1.647a.5.5 0 0 1 .708.708L15.707 6l1.647 1.646a.5.5 0 0 1-.707.708L15 6.707l-1.646 1.647a.5.5 0 0 1-.708-.708L14.293 6l-1.647-1.646a.5.5 0 0 1 0-.708z"/></svg>
        {:else}
          <svg viewBox="0 0 16 16" width="14" height="14" fill="currentColor"><path d="M11.536 14.01A8.473 8.473 0 0 0 14.026 8a8.473 8.473 0 0 0-2.49-6.01l-.708.707A7.476 7.476 0 0 1 13.025 8c0 2.071-.84 3.946-2.197 5.303l.708.707z"/><path d="M10.121 12.596A6.48 6.48 0 0 0 12.025 8a6.48 6.48 0 0 0-1.904-4.596l-.707.707A5.483 5.483 0 0 1 11.025 8a5.483 5.483 0 0 1-1.61 3.89l.706.706z"/><path d="M8.707 11.182A4.486 4.486 0 0 0 10.025 8a4.486 4.486 0 0 0-1.318-3.182L8 5.525A3.489 3.489 0 0 1 9.025 8 3.49 3.49 0 0 1 8 10.475l.707.707z"/><path d="M6.717 3.55A.5.5 0 0 1 7 4v8a.5.5 0 0 1-.812.39L3.825 10.5H1.5A.5.5 0 0 1 1 10V6a.5.5 0 0 1 .5-.5h2.325l2.363-1.89a.5.5 0 0 1 .529-.06z"/></svg>
        {/if}
      </button>
      <input
        type="range"
        class="vol"
        min="0"
        max="1"
        step="0.01"
        value={muted ? 0 : volume}
        on:input={onVolumeChange}
        aria-label="Volume"
      />
    </div>
  </aside>
{/if}

<style>
  .player {
    position: fixed;
    left: 216px; /* sidebar width */
    right: 0;
    bottom: 0;
    height: 76px;
    background: var(--bg-secondary);
    border-top: 1px solid var(--border);
    display: grid;
    grid-template-columns: minmax(220px, 1fr) minmax(280px, 2fr) minmax(180px, 1fr);
    align-items: center;
    padding: 0 1.25rem;
    gap: 1rem;
    z-index: 50;
    box-shadow: 0 -4px 20px rgba(0,0,0,0.25);
  }

  .left { display: flex; align-items: center; gap: 0.75rem; min-width: 0; }
  .art {
    width: 52px; height: 52px; border-radius: 4px; object-fit: cover;
    background: var(--bg-elevated); flex-shrink: 0;
  }
  .art.placeholder {
    display: flex; align-items: center; justify-content: center;
    color: var(--text-muted); font-size: 1.5rem;
  }
  .meta { min-width: 0; }
  .title { font-size: 0.85rem; font-weight: 500; overflow: hidden;
           text-overflow: ellipsis; white-space: nowrap; }
  .sub { font-size: 0.72rem; color: var(--text-muted); overflow: hidden;
         text-overflow: ellipsis; white-space: nowrap; }
  .sub a { color: inherit; text-decoration: none; }
  .sub a:hover { color: var(--text-secondary); }

  .center { display: flex; flex-direction: column; align-items: center; gap: 0.3rem; min-width: 0; }
  .transport { display: flex; align-items: center; gap: 0.6rem; }
  .t-btn {
    background: none; border: 0; cursor: pointer;
    padding: 0.4rem; border-radius: 4px;
    color: var(--text-muted); display: inline-flex;
    transition: color 0.12s;
  }
  .t-btn:hover { color: var(--text-primary); }
  .t-btn.on { color: var(--accent); }
  .t-btn.play {
    background: var(--text-primary); color: var(--bg-primary);
    border-radius: 999px; padding: 0.5rem;
  }
  .t-btn.play:hover { background: var(--accent); color: white; }

  .seek-row { display: flex; align-items: center; gap: 0.6rem; width: 100%; max-width: 540px; }
  .t { font-size: 0.7rem; color: var(--text-muted); font-variant-numeric: tabular-nums; min-width: 2.6rem; text-align: center; }
  .seek {
    flex: 1; -webkit-appearance: none; appearance: none; height: 4px;
    background: var(--border-strong); border-radius: 2px; cursor: pointer; outline: none;
  }
  .seek::-webkit-slider-thumb {
    -webkit-appearance: none; appearance: none; width: 12px; height: 12px;
    border-radius: 50%; background: var(--text-primary); cursor: pointer;
    border: 0;
  }
  .seek::-moz-range-thumb {
    width: 12px; height: 12px; border-radius: 50%;
    background: var(--text-primary); cursor: pointer; border: 0;
  }

  .right { display: flex; align-items: center; gap: 0.5rem; justify-content: flex-end; }
  .vol-btn {
    background: none; border: 0; cursor: pointer; color: var(--text-muted);
    padding: 0.3rem; border-radius: 4px;
  }
  .vol-btn:hover { color: var(--text-primary); }
  .vol {
    width: 90px; -webkit-appearance: none; appearance: none; height: 4px;
    background: var(--border-strong); border-radius: 2px; cursor: pointer; outline: none;
  }
  .vol::-webkit-slider-thumb {
    -webkit-appearance: none; appearance: none; width: 10px; height: 10px;
    border-radius: 50%; background: var(--text-primary); cursor: pointer; border: 0;
  }
  .vol::-moz-range-thumb {
    width: 10px; height: 10px; border-radius: 50%;
    background: var(--text-primary); cursor: pointer; border: 0;
  }

  .close-btn {
    background: none; border: 0; cursor: pointer;
    color: var(--text-muted); padding: 0.3rem; border-radius: 4px;
    display: inline-flex; align-items: center; justify-content: center;
  }
  .close-btn:hover { color: var(--text-primary); }

  /* Mobile: stack and shorten — sit above the bottom nav (60px). */
  @media (max-width: 768px) {
    .player {
      left: 0;
      bottom: 60px;
      height: auto;
      padding: 0.5rem 0.75rem;
      grid-template-columns: 1fr auto;
      grid-template-areas:
        "left right"
        "center center";
      gap: 0.5rem;
    }
    .left { grid-area: left; gap: 0.5rem; }
    .right { grid-area: right; }
    .center { grid-area: center; gap: 0.2rem; }
    .art { width: 40px; height: 40px; }
    .vol { display: none; }
    .seek-row { max-width: none; }
  }
</style>
