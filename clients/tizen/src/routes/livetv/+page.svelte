<script lang="ts">
  // Live TV channel browser + in-page player. The grid lists every
  // enabled channel from /tv/channels, paired with the now/next
  // program from /tv/channels/now-next so the user can see what's
  // on before tuning. Clicking a row swaps the page into player
  // mode — same AVPlay wrapper the /watch route uses, against the
  // server's `/api/v1/tv/channels/{id}/stream.m3u8?token=...` HLS
  // endpoint. The server multiplexes the tuner output for that
  // channel, so the URL is stable across viewers.
  //
  // No /watch indirection: live channels aren't media_items, so the
  // watch screen's item-fetch + transcode-session machinery doesn't
  // apply. Tuner output is HLS-already; AVPlay opens it directly.
  //
  // Back from player returns to the grid; Back from grid returns
  // to /hub. Two-stack so the user can scrub channels without
  // re-fetching the list every time.

  import { onMount, onDestroy } from 'svelte';
  import { goto } from '$app/navigation';
  import { api, endpoints, Unauthorized, type Channel, type NowNext } from '$lib/api';
  import { focusable } from '$lib/focus/focusable';
  import { focusManager } from '$lib/focus/manager';
  import { avplay } from '$lib/player/avplay';
  import Spinner from '$lib/components/Spinner.svelte';

  let channels = $state<Channel[]>([]);
  // Map of channel_id → [current, next] from the now-next response.
  // Channels missing from the response render "no guide data" rather
  // than dropping out of the list — the tuner still works fine
  // without EPG metadata.
  let nowNextByChannel = $state<Record<string, [NowNext | null, NowNext | null]>>({});
  let error = $state('');
  let loading = $state(true);

  let mode = $state<'grid' | 'playing'>('grid');
  let activeChannel = $state<Channel | null>(null);

  // HTML5 fallback for `vite dev` against a desktop browser, same
  // pattern as the /watch route. AVPlay renders to a hardware
  // overlay behind the webview on real Tizen.
  let video: HTMLVideoElement | undefined = $state();
  const usingAvPlay = $derived(avplay.available());

  onMount(() => {
    void loadAll();
    return focusManager.pushBack(() => {
      if (mode === 'playing') {
        stopPlayback();
        return true;
      }
      goto('/hub');
      return true;
    });
  });

  onDestroy(() => {
    if (usingAvPlay) avplay.close();
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
      if (e instanceof Unauthorized) goto('/login');
      else error = (e as Error).message ?? 'Could not load channels';
    } finally {
      loading = false;
    }
  }

  function play(channel: Channel) {
    const origin = api.getOrigin();
    const tok = api.getToken();
    if (!origin || !tok) {
      error = 'Not signed in';
      return;
    }
    activeChannel = channel;
    mode = 'playing';
    // The HLS endpoint takes the bearer as `?token=` because AVPlay
    // can't attach an Authorization header. Same convention the
    // /watch route uses for transcode sessions.
    const url = `${origin}/api/v1/tv/channels/${channel.id}/stream.m3u8`;
    if (usingAvPlay) {
      avplay.open(
        { url, streamingMode: 'HLS', bearer: tok },
        {
          onError: (msg) => { error = msg; },
        },
      );
    } else if (video) {
      // HTML5 fallback for dev — server returns plain HLS, browser's
      // native MSE handles it on Safari and via the page CSP on Tizen
      // simulator. Production runs through AVPlay above.
      video.src = `${url}?token=${encodeURIComponent(tok)}`;
      void video.play();
    }
  }

  function stopPlayback() {
    if (usingAvPlay) avplay.close();
    if (video) {
      video.pause();
      video.removeAttribute('src');
      video.load();
    }
    mode = 'grid';
    activeChannel = null;
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
    <header>
      <h1>Live TV</h1>
      <nav class="links">
        <a href="/hub/" data-sveltekit-preload-data="false">home</a>
        <a href="/recordings/" data-sveltekit-preload-data="false">recordings</a>
      </nav>
    </header>

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
            use:focusable={{ autofocus: i === 0 }}
            class="channel-row"
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
    {#if usingAvPlay}
      <div class="avplay-host"></div>
    {:else}
      <!-- svelte-ignore a11y_media_has_caption -->
      <video bind:this={video} class="video" autoplay></video>
    {/if}
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
        <div class="overlay-hint">Back to return to channels</div>
      </div>
    {/if}
    {#if error}
      <p class="error player-error">{error}</p>
    {/if}
  </div>
{/if}

<style>
  .page {
    padding: 32px var(--page-pad) 0;
  }
  header {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    margin-bottom: 32px;
  }
  h1 {
    font-size: var(--font-xl);
    margin: 0;
    color: var(--accent);
  }
  .links {
    display: flex;
    gap: 32px;
    font-size: var(--font-md);
    color: var(--text-secondary);
  }
  .links a { color: inherit; text-decoration: none; }

  .error { color: #fca5a5; padding: 16px 0; }
  .empty { color: var(--text-secondary); }

  .grid {
    display: flex;
    flex-direction: column;
    gap: 12px;
    max-width: 1600px;
  }
  .channel-row {
    display: grid;
    grid-template-columns: 240px 1fr;
    gap: 32px;
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
    display: flex;
    gap: 16px;
    align-items: center;
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
    display: flex;
    flex-direction: column;
    gap: 4px;
    justify-content: center;
  }
  .program-now {
    display: flex;
    gap: 12px;
    align-items: baseline;
    flex-wrap: wrap;
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
  .video, .avplay-host {
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
  .player-error {
    position: absolute;
    top: 60px;
    right: 60px;
    background: rgba(0, 0, 0, 0.7);
    padding: 12px 18px;
    border-radius: 6px;
  }
</style>
