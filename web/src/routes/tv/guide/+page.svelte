<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { liveTvApi, type LiveTVChannel, type LiveTVProgram } from '$lib/api';
  import { toast } from '$lib/stores/toast';

  let channels: LiveTVChannel[] = [];
  let programs: LiveTVProgram[] = [];
  let loading = true;
  let error = '';
  let ready = false;

  // Window state. Snap `from` to the current half-hour so the time labels
  // along the top look clean (3:00, 3:30, 4:00...) instead of "3:17".
  let windowStart = snapToHalfHour(new Date());
  // Default window: 4 hours visible. Server caps the request at 24h, but
  // realistically users never want to scroll more than a few hours ahead.
  const windowHours = 4;
  // Each half-hour cell is this many CSS pixels wide. 180px gives enough
  // room for a typical title without truncation but stays compact.
  const halfHourPx = 180;
  // Channel sidebar (logo + name + number) width.
  const channelColPx = 200;

  // Selected program for the detail popover.
  let selected: LiveTVProgram | null = null;
  let recording = false;

  async function recordSelected() {
    if (!selected) return;
    recording = true;
    try {
      await liveTvApi.createSchedule({
        type: 'once',
        program_id: selected.id,
        channel_id: selected.channel_id,
      });
      toast.success(`Recording scheduled: ${selected.title}`);
      selected = null;
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Failed to schedule recording');
    } finally { recording = false; }
  }

  $: windowEnd = new Date(windowStart.getTime() + windowHours * 3600_000);

  // Group programs by channel for fast lookup at render time.
  $: byChannel = (() => {
    const out: Record<string, LiveTVProgram[]> = {};
    for (const p of programs) {
      if (!out[p.channel_id]) out[p.channel_id] = [];
      out[p.channel_id].push(p);
    }
    return out;
  })();

  // Half-hour tick marks for the time-axis header.
  $: halfHourTicks = (() => {
    const ticks: Date[] = [];
    const totalSlots = windowHours * 2;
    for (let i = 0; i < totalSlots; i++) {
      ticks.push(new Date(windowStart.getTime() + i * 30 * 60_000));
    }
    return ticks;
  })();

  // CSS pixel position of "now" along the time axis. Drives the red
  // current-time line. Returns -1 when "now" is outside the visible
  // window so the caller can hide the marker.
  $: nowMarkerPx = (() => {
    const now = Date.now();
    const start = windowStart.getTime();
    const end = windowEnd.getTime();
    if (now < start || now > end) return -1;
    return ((now - start) / (end - start)) * (windowHours * 2 * halfHourPx);
  })();

  // Re-render the now marker every minute so it tracks live time.
  let _tick = 0;
  onMount(() => {
    if (!localStorage.getItem('onscreen_user')) { goto('/login'); return; }
    ready = true;
    void load();
    const id = setInterval(() => { _tick++; }, 60_000);
    return () => clearInterval(id);
  });

  $: if (ready) void reloadOnWindowChange(windowStart);

  async function reloadOnWindowChange(_: Date) {
    if (!ready) return;
    await load();
  }

  async function load() {
    loading = true; error = '';
    try {
      const [chRes, pgRes] = await Promise.all([
        liveTvApi.channels(),
        liveTvApi.guide(windowStart.toISOString(), windowEnd.toISOString()),
      ]);
      channels = chRes.items;
      programs = pgRes.items;
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed to load guide';
    } finally { loading = false; }
  }

  function snapToHalfHour(d: Date): Date {
    const out = new Date(d);
    out.setMinutes(out.getMinutes() < 30 ? 0 : 30, 0, 0);
    return out;
  }

  function shiftWindow(hours: number) {
    windowStart = new Date(windowStart.getTime() + hours * 3600_000);
  }
  function jumpToNow() {
    windowStart = snapToHalfHour(new Date());
  }

  // CSS pixel position + width for a program tile inside the time axis.
  // Programs that start before `windowStart` get clipped at the left edge;
  // those that end after `windowEnd` get clipped at the right.
  function tilePosition(p: LiveTVProgram) {
    const start = windowStart.getTime();
    const end = windowEnd.getTime();
    const pStart = Math.max(new Date(p.starts_at).getTime(), start);
    const pEnd = Math.min(new Date(p.ends_at).getTime(), end);
    const totalPx = windowHours * 2 * halfHourPx;
    const left = ((pStart - start) / (end - start)) * totalPx;
    const width = ((pEnd - pStart) / (end - start)) * totalPx;
    return { left, width };
  }

  function fmtTickLabel(d: Date): string {
    return d.toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' });
  }
  function fmtSelectedTime(p: LiveTVProgram): string {
    const s = new Date(p.starts_at);
    const e = new Date(p.ends_at);
    return `${s.toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' })} – ${e.toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' })}`;
  }
  function fmtSelectedDate(p: LiveTVProgram): string {
    return new Date(p.starts_at).toLocaleDateString([], { weekday: 'short', month: 'short', day: 'numeric' });
  }
</script>

<svelte:head><title>Guide - OnScreen</title></svelte:head>

{#if ready}
<div class="page">
  <div class="header">
    <h1>Guide</h1>
    <div class="controls">
      <button class="btn" on:click={() => shiftWindow(-windowHours)}>← Earlier</button>
      <button class="btn" on:click={jumpToNow}>Now</button>
      <button class="btn" on:click={() => shiftWindow(windowHours)}>Later →</button>
      <a class="btn" href="/tv/recordings">Recordings</a>
      <a class="btn" href="/tv">Channels</a>
    </div>
  </div>

  {#if error}
    <div class="banner-error">{error}</div>
  {/if}

  <div class="grid-wrap">
    <!-- Time axis header. Sticky at the top so it stays visible while
         scrolling the channel column vertically. -->
    <div class="header-row" style:--col-w="{channelColPx}px">
      <div class="header-channel-cell">
        {#if loading}<span class="muted">Loading…</span>{/if}
      </div>
      <div class="header-time-cells" style:width="{windowHours * 2 * halfHourPx}px">
        {#each halfHourTicks as tick}
          <div class="header-tick" style:width="{halfHourPx}px">{fmtTickLabel(tick)}</div>
        {/each}
      </div>
    </div>

    <!-- Body: each row is one channel; programs are absolutely positioned
         tiles whose left/width come from time-to-pixel mapping. -->
    <div class="rows">
      {#each channels as ch (ch.id)}
        <div class="row">
          <a class="channel-cell" href="/tv/{ch.id}" style:width="{channelColPx}px">
            {#if ch.logo_url}
              <img class="logo" src={ch.logo_url} alt="" loading="lazy" />
            {:else}
              <div class="logo-blank">{ch.number}</div>
            {/if}
            <div class="channel-text">
              <div class="channel-number">{ch.number}</div>
              <div class="channel-name">{ch.name}</div>
            </div>
          </a>
          <div class="programs-track" style:width="{windowHours * 2 * halfHourPx}px">
            {#each (byChannel[ch.id] ?? []) as p (p.id)}
              {@const pos = tilePosition(p)}
              <button
                class="program-tile"
                style:left="{pos.left}px"
                style:width="{pos.width}px"
                on:click={() => selected = p}
                title={p.title}
              >
                <div class="tile-title">{p.title}</div>
                {#if p.subtitle}
                  <div class="tile-subtitle">{p.subtitle}</div>
                {:else if p.season_num && p.episode_num}
                  <div class="tile-subtitle">S{p.season_num} · E{p.episode_num}</div>
                {/if}
              </button>
            {/each}
          </div>
        </div>
      {/each}

      {#if !loading && channels.length === 0}
        <div class="empty">No channels — add a tuner in <a href="/settings/tv">Settings → Live TV</a>.</div>
      {/if}
    </div>

    <!-- Now marker — vertical red line spanning all rows. Hidden when
         "now" falls outside the visible window. -->
    {#if nowMarkerPx >= 0}
      <div class="now-line" style:left="calc({channelColPx}px + {nowMarkerPx}px)"></div>
    {/if}
  </div>

  <p class="legend">
    Tip: click a program for details. The grid auto-refreshes when you
    change the window.
    {#if !loading && channels.length > 0 && programs.length === 0}
      <br /><strong>No EPG data</strong> — add an XMLTV or Schedules Direct
      source in Settings → Live TV (coming next).
    {/if}
  </p>
</div>
{/if}

{#if selected}
  <div class="modal-backdrop" on:click={() => selected = null} role="presentation">
    <div class="modal" on:click|stopPropagation role="dialog">
      <div class="modal-meta">{fmtSelectedDate(selected)} · {fmtSelectedTime(selected)}</div>
      <h2 class="modal-title">{selected.title}</h2>
      {#if selected.subtitle}
        <div class="modal-subtitle">{selected.subtitle}</div>
      {/if}
      {#if selected.season_num && selected.episode_num}
        <div class="modal-ep">Season {selected.season_num}, Episode {selected.episode_num}</div>
      {/if}
      {#if selected.description}
        <p class="modal-desc">{selected.description}</p>
      {/if}
      {#if selected.category && selected.category.length > 0}
        <div class="modal-cats">
          {#each selected.category as cat}
            <span class="badge">{cat}</span>
          {/each}
        </div>
      {/if}
      <div class="modal-actions">
        <a class="btn" href="/tv/{selected.channel_id}">Watch channel</a>
        <button class="btn btn-primary" disabled={recording} on:click={recordSelected}>
          {recording ? 'Scheduling…' : 'Record'}
        </button>
        <button class="btn" on:click={() => selected = null}>Close</button>
      </div>
    </div>
  </div>
{/if}

<style>
  .page { padding: 1.5rem 2rem; max-width: 100%; }
  .header { display: flex; align-items: center; justify-content: space-between; margin-bottom: 1rem; }
  h1 { font-size: 1.1rem; font-weight: 700; color: var(--text-primary); margin: 0; letter-spacing: -0.02em; }
  .controls { display: flex; gap: 0.4rem; }
  .btn {
    padding: 0.4rem 0.8rem; background: var(--bg-elevated);
    color: var(--text-primary); border: 1px solid var(--border);
    border-radius: 4px; font-size: 0.78rem; cursor: pointer; text-decoration: none;
  }
  .btn:hover { background: var(--bg-hover); }
  .btn-primary { background: var(--accent); color: white; border-color: var(--accent); }
  .btn-primary:hover:not(:disabled) { filter: brightness(1.1); }
  .btn:disabled { opacity: 0.5; cursor: not-allowed; }

  .banner-error { background: var(--error-bg); border: 1px solid var(--error); color: var(--error); padding: 0.65rem 1rem; border-radius: 6px; margin-bottom: 1rem; }

  .grid-wrap { position: relative; overflow: auto; border: 1px solid var(--border); border-radius: 8px; background: var(--bg-elevated); }

  .header-row { display: flex; position: sticky; top: 0; z-index: 2; background: var(--bg-elevated); border-bottom: 1px solid var(--border); }
  .header-channel-cell { width: var(--col-w); flex-shrink: 0; padding: 0.5rem 0.75rem; border-right: 1px solid var(--border); display: flex; align-items: center; }
  .muted { color: var(--text-muted); font-size: 0.78rem; }
  .header-time-cells { display: flex; flex-shrink: 0; }
  .header-tick { flex-shrink: 0; padding: 0.5rem 0.6rem; font-size: 0.72rem; color: var(--text-muted); border-right: 1px solid var(--border); }

  .rows { display: flex; flex-direction: column; }
  .row { display: flex; border-bottom: 1px solid var(--border); height: 70px; }
  .row:last-child { border-bottom: none; }

  .channel-cell {
    flex-shrink: 0; padding: 0.5rem 0.75rem;
    border-right: 1px solid var(--border);
    display: flex; align-items: center; gap: 0.5rem;
    text-decoration: none; color: inherit;
    background: var(--bg-elevated);
    position: sticky; left: 0; z-index: 1;
  }
  .channel-cell:hover { background: var(--bg-hover); }
  .logo { width: 40px; height: 28px; object-fit: contain; background: #000; border-radius: 3px; flex-shrink: 0; }
  .logo-blank { width: 40px; height: 28px; display: flex; align-items: center; justify-content: center; font-weight: 700; font-size: 0.72rem; background: var(--bg); border-radius: 3px; flex-shrink: 0; }
  .channel-text { display: flex; flex-direction: column; min-width: 0; }
  .channel-number { font-size: 0.65rem; color: var(--text-muted); }
  .channel-name { font-size: 0.78rem; font-weight: 600; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

  .programs-track { position: relative; height: 100%; flex-shrink: 0; }
  .program-tile {
    position: absolute; top: 4px; bottom: 4px;
    background: var(--bg); border: 1px solid var(--border); border-radius: 4px;
    padding: 0.3rem 0.55rem; cursor: pointer; overflow: hidden;
    text-align: left; color: inherit; font: inherit;
    display: flex; flex-direction: column; gap: 0.15rem;
    transition: background 0.12s, border-color 0.12s;
  }
  .program-tile:hover { background: var(--bg-hover); border-color: var(--accent); }
  .tile-title { font-size: 0.78rem; font-weight: 600; color: var(--text-primary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .tile-subtitle { font-size: 0.7rem; color: var(--text-muted); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

  .now-line {
    position: absolute; top: 0; bottom: 0; width: 2px;
    background: var(--error, red); opacity: 0.85;
    pointer-events: none; z-index: 1;
  }

  .empty { padding: 2rem; color: var(--text-muted); text-align: center; }
  .empty a { color: var(--accent); }
  .legend { color: var(--text-muted); font-size: 0.75rem; margin-top: 0.75rem; text-align: center; }
  .legend a { color: var(--accent); }

  .modal-backdrop { position: fixed; inset: 0; background: rgba(0,0,0,0.6); display: flex; align-items: center; justify-content: center; z-index: 100; }
  .modal { background: var(--bg-elevated); border: 1px solid var(--border); border-radius: 10px; padding: 1.5rem; max-width: 520px; width: 90%; max-height: 80vh; overflow: auto; }
  .modal-meta { color: var(--text-muted); font-size: 0.78rem; }
  .modal-title { font-size: 1.1rem; margin: 0.3rem 0 0.5rem; color: var(--text-primary); }
  .modal-subtitle { color: var(--text-secondary); font-size: 0.9rem; }
  .modal-ep { color: var(--text-muted); font-size: 0.78rem; margin-top: 0.3rem; }
  .modal-desc { font-size: 0.85rem; color: var(--text-primary); line-height: 1.5; margin: 1rem 0; }
  .modal-cats { display: flex; flex-wrap: wrap; gap: 0.3rem; margin-bottom: 1rem; }
  .badge { background: var(--bg); color: var(--text-secondary); border: 1px solid var(--border); border-radius: 3px; padding: 0.1rem 0.4rem; font-size: 0.7rem; }
  .modal-actions { display: flex; gap: 0.5rem; justify-content: flex-end; }
</style>
