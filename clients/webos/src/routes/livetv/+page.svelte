<script lang="ts">
  // Live TV channel browser + in-page player. The grid lists every
  // enabled channel from /tv/channels, paired with the now/next
  // program from /tv/channels/now-next so the user can see what's
  // on before tuning. Clicking a row swaps the page into player
  // mode — same hls.js wrapper the /watch route uses, against the
  // server's `/api/v1/tv/channels/{id}/stream.m3u8?token=...` HLS
  // endpoint. The server multiplexes the tuner output for that
  // channel, so the URL is stable across viewers.
  //
  // No /watch indirection: live channels aren't media_items, so the
  // watch screen's item-fetch + transcode-session machinery doesn't
  // apply. Tuner output is HLS-already; hls.js opens it directly.
  //
  // Back from player returns to the grid; Back from grid returns to
  // /hub. Two-stack so the user can scrub channels without
  // re-fetching the list every time. The grid comes back with focus on
  // the channel that was playing (see returnChannelId).

  import { onMount, onDestroy, tick } from 'svelte';
  import { goto } from '$app/navigation';
  import { api, endpoints, Unauthorized, type Channel, type NowNext } from '$lib/api';
  import { focusable } from '$lib/focus/focusable';
  import { focusManager } from '$lib/focus/manager';
  import { findKeyed, focusFirstOf, restoreFocusTo } from '$lib/focus/memory';
  import type { RemoteKey } from '$lib/focus/keys';
  import { loadHls } from '$lib/player/hls-loader';
  import {
    TUNE_WATCHDOG_MS,
    describeTuneFailure,
    freshTuneBudget,
    planLiveFatal,
    retuneBudget,
    spendLivePlan,
    watchdogFails,
    zapStep,
    type LiveFatal,
    type TuneBudget,
    type TuneFailure,
    type TuneFailureCause,
  } from '$lib/liveTune';
  import Spinner from '$lib/components/Spinner.svelte';
  import TopNav from '$lib/components/TopNav.svelte';

  let channels = $state<Channel[]>([]);
  // Map of channel_id → [current, next] from the now-next response.
  // Channels missing from the response render "no guide data" — the
  // tuner still works fine without EPG metadata.
  let nowNextByChannel = $state<Record<string, [NowNext | null, NowNext | null]>>({});
  let error = $state('');
  let loading = $state(true);

  let mode = $state<'grid' | 'playing'>('grid');
  let activeChannel = $state<Channel | null>(null);
  // The row the grid gives focus back to when the player closes: the
  // channel playing then, zaps included. The grid is unmounted while a
  // channel plays and comes back scrolled to the top, so without it Back
  // put focus on the first channel; Android's grid keeps its place across
  // the player (GridScrollMemory). Null until a channel has played, when
  // the first row takes focus.
  let returnChannelId = $state<string | null>(null);

  let video: HTMLVideoElement | undefined = $state();
  let hls: { destroy: () => void } | null = null;
  // What the tune may still spend on recovering from fatal stream errors
  // (lib/liveTune). A tune by the user starts a fresh budget; the automatic
  // re-tune keeps the one it's spending, so a dead channel ends on the error
  // instead of re-tuning forever; the picture coming up refills it.
  let budget: TuneBudget = freshTuneBudget();
  // Bumped by every tune and teardown, so the error handler and the async
  // start of a tune that has been replaced (a zap, the re-tune, Back, the
  // error) do nothing.
  let tuneSeq = 0;
  // The picture has come up since the user tuned: a failure now is the
  // channel dropping, not the tune.
  let played = false;
  // Fires the error when there's no picture TUNE_WATCHDOG_MS into a tune or a
  // stall. hls.js alone can wait a minute on a playlist that never answers.
  let watchdog: ReturnType<typeof setTimeout> | null = null;
  // The player's error. Apart from `error` (the grid's), so it goes with the
  // player.
  let tuneError = $state<TuneFailure | null>(null);

  // Channel we were tuned to when the app went to the background, so we
  // can re-tune on resume. Live has no resume position — re-tuning rejoins
  // the live edge, which is the desired behaviour anyway.
  let suspendedChannel: Channel | null = null;

  // Release / re-acquire the hardware decoder around app suspend so it
  // isn't held while backgrounded. webOS fires visibilitychange and (on
  // some firmwares) a webOSRelaunch DOM event when the app is brought
  // back; handle both.
  function onVisibilityChange() {
    if (document.hidden) {
      if (mode === 'playing' && activeChannel) {
        suspendedChannel = activeChannel;
        stopPlayback();
      }
    } else if (suspendedChannel) {
      const ch = suspendedChannel;
      suspendedChannel = null;
      void play(ch);
    }
  }

  // In-player remote handling — mirrors the /watch key handler for the
  // keys that make sense on a live stream: OK / play-pause toggles (over a
  // failed tune's error, tries again), and ▲ / CH ▲ zap to the next channel
  // in the lineup, ▼ / CH ▼ to the previous (lib/liveTune zapStep). Back is
  // left to the pushBack handler below (returns to the grid). Without
  // this the player is inert — the grid is unmounted in playing mode,
  // so there's nothing focusable for OK to land on.
  function onKey(k: RemoteKey): boolean {
    if (mode !== 'playing') return false;
    switch (k) {
      case 'enter':
      case 'playpause':
      case 'play':
        // On the error, OK tunes the channel again (Android's Retry, the
        // watch screen's "OK to try again"), with a fresh budget.
        if (tuneError) {
          if (activeChannel) void play(activeChannel);
          return true;
        }
        if (!video) return true;
        if (video.paused) void video.play();
        else if (k !== 'play') video.pause();
        return true;
      case 'pause':
        if (!video?.paused) video?.pause();
        return true;
    }
    // ▲▼ and CH ▲▼ (the Magic Remote has those, but no ◀◀ ▶▶) agree: up
    // the lineup, as a TV's own do. Up used to go back up the list as the
    // grid lays it out while CH ▲ went forward. The list is in lineup order,
    // so "up" is the next row down.
    const step = zapStep(k);
    if (step === 0) return false;
    zapChannel(step);
    return true;
  }

  function zapChannel(dir: 1 | -1) {
    if (!activeChannel || channels.length < 2) return;
    const i = channels.findIndex((c) => c.id === activeChannel!.id);
    if (i < 0) return;
    void play(channels[(i + dir + channels.length) % channels.length]);
  }

  onMount(() => {
    void loadAll();
    const offBack = focusManager.pushBack(() => {
      if (mode === 'playing') {
        stopPlayback();
        return true;
      }
      goto('#/hub');
      return true;
    });
    const offKey = focusManager.pushKeyHandler(onKey);
    document.addEventListener('visibilitychange', onVisibilityChange);
    window.addEventListener('webOSRelaunch', onVisibilityChange);
    return () => {
      offBack();
      offKey();
      document.removeEventListener('visibilitychange', onVisibilityChange);
      window.removeEventListener('webOSRelaunch', onVisibilityChange);
    };
  });

  onDestroy(() => {
    tuneSeq++;
    clearWatchdog();
    hls?.destroy();
    hls = null;
  });

  async function loadAll() {
    loading = true;
    error = '';
    try {
      // Fan out — neither call depends on the other and both are
      // small; parallel cuts the cold-start latency in half.
      const [chans, nn] = await Promise.all([
        endpoints.livetv.channels(),
        endpoints.livetv.nowNext().catch(() => [] as NowNext[]),
      ]);
      channels = chans;
      const map: Record<string, [NowNext | null, NowNext | null]> = {};
      for (const row of nn) {
        const slot = map[row.channel_id] ?? [null, null];
        if (!slot[0]) slot[0] = row;
        else if (!slot[1]) slot[1] = row;
        map[row.channel_id] = slot;
      }
      nowNextByChannel = map;
    } catch (e) {
      if (e instanceof Unauthorized) {
        goto('#/login');
        return;
      }
      error = (e as Error).message ?? 'Could not load channels';
    } finally {
      loading = false;
    }
    // No channel row to take focus (none configured, or the list failed):
    // the focus manager places it, on the top nav, so it isn't left on
    // nothing with the first press spent finding it.
    if (channels.length === 0) {
      await tick();
      if (mode === 'grid') focusManager.refocus();
    }
  }

  /** Tunes `channel`. `retune`: the automatic second try after a fatal
   *  error, which keeps the budget it's spending. Anything else (a row, a
   *  zap, OK on the error, the app coming back) is the user's tune. */
  async function play(channel: Channel, retune = false) {
    const origin = api.getOrigin();
    const tok = api.getAssetToken();
    if (!origin || !tok) {
      // From the player (a zap, OK on the error) it's said there: the
      // grid's error isn't on screen.
      if (mode === 'playing') failTune({ message: 'Not signed in.' });
      else error = 'Not signed in';
      return;
    }
    const seq = ++tuneSeq;
    // The stream being replaced (a zap, the re-tune) stops now, before its
    // events can be taken for the new tune's.
    hls?.destroy();
    hls = null;
    budget = retune ? retuneBudget() : freshTuneBudget();
    if (!retune) {
      played = false;
      // The re-tune runs on the user's tune's clock, so one that fails
      // slowly still ends on the error in time.
      armWatchdog();
    }
    tuneError = null;
    activeChannel = channel;
    mode = 'playing';
    // A stale error from a previous tune shouldn't linger over the
    // freshly-tuned channel.
    error = '';
    // The HLS endpoint takes the purpose=asset token as `?token=` —
    // the <video> element + hls.js can't attach an Authorization
    // header, and the server rejects a general access token in a URL.
    const url = `${origin}/api/v1/tv/channels/${channel.id}/stream.m3u8?token=${encodeURIComponent(tok)}`;
    // Wait for Svelte to flush the DOM so bind:this={video} is applied
    // in the new mode — a single bare microtask isn't a guaranteed
    // flush on slow firmware, which left `video` undefined and the
    // channel silently un-tuned on first press.
    await tick();
    if (seq !== tuneSeq || !video) return;
    try {
      const Hls = await loadHls();
      if (seq !== tuneSeq) return;
      if (Hls.isSupported()) {
        const inst = new Hls({ lowLatencyMode: true });
        // Fatal errors climb lib/liveTune's ladder: resume a hiccuping
        // stream, recover the decoder, else re-tune once, then the error.
        inst.on(Hls.Events.ERROR, (_event, data) => {
          if (!data.fatal || seq !== tuneSeq) return;
          const fatal: LiveFatal = {
            kind:
              data.type === Hls.ErrorTypes.NETWORK_ERROR
                ? 'network'
                : data.type === Hls.ErrorTypes.MEDIA_ERROR
                  ? 'media'
                  : 'other',
            details: data.details ?? '',
            httpStatus: data.response?.code,
          };
          const plan = planLiveFatal(fatal, budget);
          spendLivePlan(plan, budget);
          if (plan === 'restartLoad') inst.startLoad();
          else if (plan === 'recoverMedia') inst.recoverMediaError();
          else if (plan === 'retune') void play(channel, true);
          else failTune(fatal);
        });
        inst.loadSource(url);
        inst.attachMedia(video);
        hls = inst;
      } else if (video.canPlayType('application/vnd.apple.mpegurl')) {
        // Native HLS path — older webOS Chromium may handle the
        // tuner output directly without MSE. There are no hls.js errors
        // to read here: a dead tune is the watchdog's to catch.
        video.src = url;
      } else {
        failTune({ message: 'This TV has no HLS playback.' });
        return;
      }
      video.muted = false;
      void video.play();
    } catch (e) {
      if (seq === tuneSeq) failTune({ message: (e as Error).message || 'The channel could not be started.' });
    }
  }

  // The tune is over: the stream stops (no hls.js retrying behind the
  // error) and the error shows over the black player, which stays up so OK
  // can try again and Up / Down / CH can move on to another channel.
  function failTune(cause: TuneFailureCause) {
    if (mode !== 'playing') return;
    tuneSeq++;
    clearWatchdog();
    hls?.destroy();
    hls = null;
    if (video) {
      video.pause();
      video.removeAttribute('src');
      video.load();
    }
    tuneError = describeTuneFailure(cause, played);
  }

  function armWatchdog() {
    clearWatchdog();
    watchdog = setTimeout(() => {
      watchdog = null;
      // Not over a pause with the stream in hand: a paused video never
      // fires the 'playing' that stands the watchdog down (watchdogFails).
      if (video && !watchdogFails(video)) return;
      failTune('timeout');
    }, TUNE_WATCHDOG_MS);
  }

  function clearWatchdog() {
    if (watchdog) clearTimeout(watchdog);
    watchdog = null;
  }

  // The picture is up (the first frame, or back after a stall): the tune
  // worked, so the watchdog stands down and the recovery budget is whole
  // again for the next drop.
  function onPlaying() {
    if (mode !== 'playing' || tuneError) return;
    played = true;
    budget = freshTuneBudget();
    clearWatchdog();
  }

  // Stalled for data: the watchdog runs from the start of the stall. A
  // tune's own is already running and isn't restarted.
  function onWaiting() {
    if (mode !== 'playing' || tuneError || watchdog) return;
    armWatchdog();
  }

  function stopPlayback() {
    // Noted first: the row to focus when the grid is back.
    if (activeChannel) returnChannelId = activeChannel.id;
    tuneSeq++;
    clearWatchdog();
    hls?.destroy();
    hls = null;
    if (video) {
      video.pause();
      video.removeAttribute('src');
      video.load();
    }
    tuneError = null;
    mode = 'grid';
    activeChannel = null;
    void focusReturnChannel();
  }

  // Once the grid has rendered again, focus goes to the channel that was
  // playing, scrolled to the middle of the screen at once (the first row
  // holds its autofocus back for it); the first row when it's gone. Nothing
  // when a tune has the player up again already.
  async function focusReturnChannel() {
    await tick();
    if (mode !== 'grid') return;
    const row = returnChannelId ? findKeyed(returnChannelId) : null;
    if (row) restoreFocusTo(row);
    else focusFirstOf('.grid [data-focusable]');
  }

  function timeRange(p: NowNext | null): string {
    if (!p) return '';
    try {
      const start = new Date(p.starts_at);
      const end = new Date(p.ends_at);
      const fmt = (d: Date) =>
        d.toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' });
      return `${fmt(start)} – ${fmt(end)}`;
    } catch {
      return '';
    }
  }
</script>

{#if mode === 'grid'}
  <div class="page">
    <TopNav />
    <h1>Live TV</h1>

    {#if error}
      <p class="error">{error}</p>
    {/if}

    {#if loading}
      <Spinner />
    {:else if channels.length === 0}
      <p class="empty">
        No channels configured. Add a tuner from the web settings UI.
      </p>
    {:else}
      <div class="grid">
        {#each channels as ch, i (ch.id)}
          {@const slot = nowNextByChannel[ch.id]}
          {@const now = slot?.[0] ?? null}
          {@const next = slot?.[1] ?? null}
          <button
            use:focusable={{ autofocus: i === 0 && !returnChannelId }}
            class="channel-row"
            data-focus-key={ch.id}
            onclick={() => play(ch)}
          >
            <div class="channel-id">
              {#if ch.logo_url}
                <img src={ch.logo_url} alt="" class="channel-logo" />
              {:else}
                <div class="channel-logo placeholder"></div>
              {/if}
              <div class="channel-name">
                <div class="channel-num">{ch.number}</div>
                <div class="channel-call">{ch.callsign ?? ch.name}</div>
              </div>
            </div>
            <div class="program">
              {#if now}
                <div class="program-now">
                  <span class="program-time">{timeRange(now)}</span>
                  <span class="program-title">{now.title}</span>
                  {#if now.subtitle}<span class="program-sub">{now.subtitle}</span>{/if}
                </div>
                {#if next}
                  <div class="program-next">
                    Next · {timeRange(next)} · {next.title}
                  </div>
                {/if}
              {:else}
                <div class="program-empty">No guide data</div>
              {/if}
            </div>
          </button>
        {/each}
      </div>
    {/if}
  </div>
{:else}
  <div class="player">
    <!-- svelte-ignore a11y_media_has_caption -->
    <video
      bind:this={video}
      class="video"
      autoplay
      onplaying={onPlaying}
      onwaiting={onWaiting}
    ></video>
    {#if activeChannel}
      <div class="channel-overlay">
        <div class="overlay-num">{activeChannel.number}</div>
        <div class="overlay-name">{activeChannel.callsign ?? activeChannel.name}</div>
        {#if nowNextByChannel[activeChannel.id]?.[0]}
          {@const now = nowNextByChannel[activeChannel.id][0]!}
          <div class="overlay-program">
            {timeRange(now)} · {now.title}
          </div>
        {/if}
        <div class="overlay-hint">
          {#if channels.length > 1}▲▼ or CH ▲▼ to change channel · {/if}Back to return to channels
        </div>
      </div>
    {/if}
    {#if tuneError}
      <div class="player-error-wrap">
        <div class="player-error" role="alert">
          <div class="player-error-title">{tuneError.title}</div>
          {#if tuneError.detail}<div class="player-error-detail">{tuneError.detail}</div>{/if}
          <div class="player-error-hint">OK to try again · Back to return to channels</div>
        </div>
      </div>
    {/if}
  </div>
{/if}

<style>
  .page {
    padding: 0 var(--page-pad);
  }
  h1 {
    font-size: var(--font-2xl);
    margin: 24px 0 32px;
  }

  .error { color: #fca5a5; padding: 16px 0; }
  .empty { color: var(--text-secondary); }

  /* Spacing in the channel list is margins, not flexbox `gap` (Chrome 84):
     webOS 6 runs Chromium 79. Flex items' margins don't collapse, so the
     spacing is the same. A row is flex too, not the 240px | 1fr grid it
     reads as: Chromium 79 can't make a <button> a grid container (it
     computes display: grid, then stacks the children as full-width blocks,
     as the C1 showed), so the channel column is a fixed 240px item, the
     program takes the rest, and a margin stands in for the 32px gap. */
  .grid {
    display: flex;
    flex-direction: column;
    max-width: 1600px;
  }
  .channel-row + .channel-row {
    margin-top: 12px;
  }
  .channel-row {
    display: flex;
    align-items: stretch;
    background: rgba(255, 255, 255, 0.03);
    padding: 16px 20px;
    border-radius: 8px;
    border: 2px solid transparent;
    color: inherit;
    text-align: left;
    cursor: pointer;
    font-family: inherit;
  }
  .channel-row:focus,
  .channel-row:focus-visible {
    border-color: var(--accent);
    outline: none;
    background: rgba(124, 106, 247, 0.12);
  }
  .channel-id {
    /* min-width 0 holds it at 240px as the grid track did: a long callsign
       wraps rather than pushing the program over. */
    flex: 0 0 240px;
    min-width: 0;
    margin-right: 32px;
    display: flex;
    align-items: center;
  }
  .channel-name {
    margin-left: 16px;
  }
  .channel-logo {
    width: 80px;
    height: 60px;
    object-fit: contain;
    background: rgba(255, 255, 255, 0.05);
    border-radius: 4px;
  }
  .channel-logo.placeholder {
    display: block;
  }
  .channel-num {
    font-size: var(--font-md);
    font-weight: 600;
    color: var(--accent);
  }
  .channel-call {
    font-size: var(--font-sm);
    color: var(--text-secondary);
  }
  .program {
    flex: 1 1 0%;
    min-width: 0;
    display: flex;
    flex-direction: column;
    justify-content: center;
  }
  /* Wraps: each part carries a top + right margin and the line pulls itself
     up and right by the same, spacing them like `gap: 12px` across and
     between wrapped lines. */
  .program-now {
    display: flex;
    align-items: baseline;
    flex-wrap: wrap;
    margin: -12px -12px 0 0;
  }
  .program-now > span {
    margin: 12px 12px 0 0;
  }
  .program-time {
    font-family: monospace;
    color: var(--text-secondary);
    font-size: var(--font-sm);
  }
  .program-title {
    font-size: var(--font-md);
    font-weight: 600;
  }
  .program-sub {
    font-size: var(--font-sm);
    color: var(--text-secondary);
    font-style: italic;
  }
  .program-next {
    margin-top: 4px;
    font-size: var(--font-sm);
    color: var(--text-secondary);
  }
  .program-empty {
    font-size: var(--font-sm);
    color: var(--text-secondary);
    font-style: italic;
  }

  .player {
    position: fixed;
    inset: 0;
    background: #000;
  }
  .video {
    width: 100%;
    height: 100%;
    object-fit: contain;
  }
  .channel-overlay {
    position: absolute;
    top: 60px;
    left: 60px;
    background: rgba(0, 0, 0, 0.7);
    padding: 16px 24px;
    border-radius: 8px;
    color: white;
  }
  .overlay-num {
    font-size: var(--font-xl);
    font-weight: 700;
    color: var(--accent);
  }
  .overlay-name {
    font-size: var(--font-md);
    margin-top: 4px;
  }
  .overlay-program {
    font-size: var(--font-sm);
    color: rgba(255, 255, 255, 0.85);
    margin-top: 8px;
  }
  .overlay-hint {
    font-size: var(--font-sm);
    color: var(--text-secondary);
    margin-top: 12px;
  }
  /* Centred over the black player (a failed tune has nothing else on
     screen), clear of the channel overlay; the wrapper only centres, so the
     box shrinks to its text. */
  .player-error-wrap {
    position: absolute;
    top: 0;
    right: 0;
    bottom: 0;
    left: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    pointer-events: none;
  }
  .player-error {
    max-width: 60%;
    background: rgba(0, 0, 0, 0.8);
    padding: 28px 40px;
    border-radius: 8px;
    text-align: center;
    color: white;
  }
  .player-error-title {
    font-size: var(--font-xl);
    color: #fca5a5;
  }
  .player-error-detail {
    margin-top: 12px;
    font-size: var(--font-md);
    color: rgba(255, 255, 255, 0.85);
  }
  .player-error-hint {
    margin-top: 20px;
    font-size: var(--font-sm);
    color: var(--text-secondary);
  }
</style>
