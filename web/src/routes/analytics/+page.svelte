<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { api, analyticsApi, sessionsApi, assetUrl, type AnalyticsData, type DayCount, type DayBytes, type DayStreamTypes, type ActiveSession } from '$lib/api';

  let data: AnalyticsData | null = null;
  let loading = true;
  let error = '';        // shown full-screen only while no data has ever loaded
  let staleError = false; // a refresh failed but earlier data is still on screen
  let refreshing = false; // manual refresh in flight
  let rangeDays = 30;     // 7 | 30 | 90 — drives every windowed panel
  let sessions: ActiveSession[] = [];
  let alive = true;
  let ready = false;

  async function refresh(force = false) {
    if (!alive || (document.hidden && !force)) return;
    try {
      data = await analyticsApi.get({ days: rangeDays, ...(force ? { refresh: true } : {}) });
      error = '';
      staleError = false;
    } catch (e: unknown) {
      if (!alive) return;
      console.warn('analytics refresh failed', e);
      // Keep showing the last good data; only blank the page when there has
      // never been a successful load.
      if (data) staleError = true;
      else error = 'Couldn’t load analytics. Check that the server is reachable.';
    } finally {
      loading = false;
    }
  }

  async function manualRefresh() {
    refreshing = true;
    try { await refresh(true); } finally { refreshing = false; }
  }

  function setRange(days: number) {
    if (days === rangeDays) return;
    rangeDays = days;
    refresh(true); // force: the user explicitly asked for a different view
  }

  async function refreshSessions() {
    if (!alive || document.hidden) return;
    try {
      sessions = await sessionsApi.list() ?? [];
    } catch (e) { if (alive) console.warn(e); }
  }

  // Admin stream termination (server enforces owner-or-admin + audit-logs
  // the cross-user case). Optimistically drop the card; the next poll is
  // the source of truth if the stop failed.
  let stoppingId: string | null = null;
  async function stopSession(id: string) {
    stoppingId = id;
    try {
      await sessionsApi.stop(id);
      sessions = sessions.filter((s) => s.id !== id);
    } catch (e) {
      console.warn('stop session failed', e);
    } finally {
      stoppingId = null;
    }
  }

  function onVisibility() {
    // Catch up immediately when the tab returns to the foreground; the
    // interval ticks themselves no-op while hidden.
    if (!document.hidden) { refresh(); refreshSessions(); }
  }

  onMount(() => {
    const user = api.getUser();
    if (!user) { goto('/login'); return; }
    if (!user.is_admin) { goto('/'); return; }
    ready = true;
    refresh();
    refreshSessions();
    const slowInterval = setInterval(refresh, 30000);
    const fastInterval = setInterval(refreshSessions, 5000);
    document.addEventListener('visibilitychange', onVisibility);
    return () => {
      alive = false;
      clearInterval(slowInterval);
      clearInterval(fastInterval);
      document.removeEventListener('visibilitychange', onVisibility);
    };
  });

  // ── Formatting helpers ──────────────────────────────────────────────────────

  function fmtBytes(bytes: number): string {
    if (bytes === 0) return '0 B';
    const units = ['B', 'KB', 'MB', 'GB', 'TB'];
    const i = Math.floor(Math.log(bytes) / Math.log(1024));
    return (bytes / Math.pow(1024, i)).toFixed(i >= 3 ? 2 : 0) + ' ' + units[i];
  }

  function fmtDuration(ms: number): string {
    if (!ms) return '0 min';
    const d = Math.floor(ms / 86400000);
    const h = Math.floor((ms % 86400000) / 3600000);
    const m = Math.floor((ms % 3600000) / 60000);
    if (d > 0) return h > 0 ? `${d}d ${h}h` : `${d}d`;
    if (h === 0) return `${m} min`;
    if (m === 0) return `${h}h`;
    return `${h}h ${m}m`;
  }

  // Player-style clock for session positions (1:02:35 / 0:45).
  function fmtClock(ms: number): string {
    const s = Math.floor(ms / 1000);
    const h = Math.floor(s / 3600);
    const m = Math.floor((s % 3600) / 60);
    const sec = s % 60;
    const mm = h > 0 ? String(m).padStart(2, '0') : String(m);
    return `${h > 0 ? h + ':' : ''}${mm}:${String(sec).padStart(2, '0')}`;
  }

  // d is a date-only "YYYY-MM-DD" key in the viewer's timezone. Parse the
  // parts locally — new Date("2026-06-10") would read as UTC midnight and
  // render as the previous day for anyone west of Greenwich.
  function fmtDate(day: string): string {
    const [y, m, d] = day.split('-').map(Number);
    return new Date(y, m - 1, d).toLocaleDateString('en-US', { month: 'short', day: 'numeric' });
  }

  function fmtTime(iso: string): string {
    return new Date(iso).toLocaleString('en-US', {
      month: 'short', day: 'numeric',
      hour: 'numeric', minute: '2-digit'
    });
  }

  // Local-timezone "YYYY-MM-DD" — must match the server's tz-bucketed keys
  // (toISOString would build UTC keys and misalign evening plays).
  function localDayKey(d: Date): string {
    return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
  }

  // Artwork paths are filesystem-derived: encode each segment (handles # ? in
  // filenames) while keeping the / separators.
  function artworkSrc(path: string, w: number): string {
    const encoded = path.split('/').map(encodeURIComponent).join('/');
    return assetUrl(`/artwork/${encoded}?w=${w}`);
  }

  // The local-day keys for the selected window, oldest first. Use the
  // server-confirmed range (data.range_days) so the fill width can't drift
  // from what the response actually covers mid-range-switch.
  function windowKeys(n: number): string[] {
    const keys: string[] = [];
    for (let i = n - 1; i >= 0; i--) {
      const d = new Date();
      d.setDate(d.getDate() - i);
      keys.push(localDayKey(d));
    }
    return keys;
  }

  // Build a full window-sized array, filling missing dates with 0.
  function fillDays(raw: DayCount[], n: number): DayCount[] {
    const map = new Map(raw.map(d => [d.date, d.count]));
    return windowKeys(n).map(key => ({ date: key, count: map.get(key) ?? 0 }));
  }

  $: chartDays = data?.range_days ?? rangeDays;
  $: days = data ? fillDays(data.plays_by_day, chartDays) : [];
  $: maxDay = days.reduce((m, d) => Math.max(m, d.count), 0) || 1;

  function fillDayBytes(raw: DayBytes[], n: number): DayBytes[] {
    const map = new Map(raw.map(d => [d.date, d.bytes]));
    return windowKeys(n).map(key => ({ date: key, bytes: map.get(key) ?? 0 }));
  }

  $: bwDays = data ? fillDayBytes(data.bandwidth_by_day ?? [], chartDays) : [];
  $: maxBw  = bwDays.reduce((m, d) => Math.max(m, d.bytes), 0) || 1;

  // Stream-type stacked series (direct / transcode / unknown per local day).
  function fillStreamDays(raw: DayStreamTypes[], n: number): DayStreamTypes[] {
    const map = new Map(raw.map(d => [d.date, d]));
    return windowKeys(n).map(key =>
      map.get(key) ?? { date: key, direct: 0, transcode: 0, unknown: 0 });
  }

  $: streamDays = data ? fillStreamDays(data.stream_types_by_day ?? [], chartDays) : [];
  $: maxStream = streamDays.reduce((m, d) => Math.max(m, d.direct + d.transcode + d.unknown), 0) || 1;
  $: hasDecisionData = streamDays.some(d => d.direct + d.transcode > 0);

  // Hour-of-day chart: fill 0..23.
  $: hours = (() => {
    const map = new Map((data?.plays_by_hour ?? []).map(h => [h.hour, h.count]));
    return Array.from({ length: 24 }, (_, h) => ({ hour: h, count: map.get(h) ?? 0 }));
  })();
  $: maxHour = hours.reduce((m, h) => Math.max(m, h.count), 0) || 1;

  $: maxUserWatch = (data?.top_users ?? []).reduce((m, u) => Math.max(m, u.watch_time_ms), 0) || 1;
  $: maxClient = (data?.clients ?? []).reduce((m, c) => Math.max(m, c.count), 0) || 1;

  $: completionPct = data && data.completion.plays > 0
    ? Math.round((data.completion.completed / data.completion.plays) * 100)
    : null;

  function fmtHour(h: number): string {
    if (h === 0) return '12a';
    if (h === 12) return '12p';
    return h < 12 ? `${h}a` : `${h - 12}p`;
  }

  function pct(val: number, total: number): number {
    return total === 0 ? 0 : Math.round((val / total) * 100);
  }

  $: totalRes = (data?.libraries ?? []).reduce((s, l) =>
    s + l.res_4k + l.res_1080p + l.res_720p + l.res_sd, 0) || 1;
  $: res4k    = (data?.libraries ?? []).reduce((s, l) => s + l.res_4k, 0);
  $: res1080  = (data?.libraries ?? []).reduce((s, l) => s + l.res_1080p, 0);
  $: res720   = (data?.libraries ?? []).reduce((s, l) => s + l.res_720p, 0);
  $: resSd    = (data?.libraries ?? []).reduce((s, l) => s + l.res_sd, 0);

  $: maxCodec = (data?.video_codecs ?? []).reduce((m, c) => Math.max(m, c.count), 0) || 1;
  $: maxCont  = (data?.containers  ?? []).reduce((m, c) => Math.max(m, c.count), 0) || 1;
  $: maxPlayed = (data?.top_played ?? []).reduce((m, t) => Math.max(m, t.play_count), 0) || 1;
</script>

{#if ready}
<div class="page">
  <header class="page-header">
    <h1>Analytics</h1>
    {#if data}
      <div class="header-controls">
        <div class="range-picker" role="group" aria-label="Date range">
          {#each [7, 30, 90] as d}
            <button class="range-btn" class:active={rangeDays === d} on:click={() => setRange(d)}>
              {d}d
            </button>
          {/each}
        </div>
        <button class="refresh-btn" on:click={manualRefresh} disabled={refreshing}>
          {refreshing ? 'Refreshing…' : 'Refresh'}
        </button>
      </div>
    {/if}
  </header>

  {#if loading}
    <div class="empty">Loading…</div>
  {:else if error && !data}
    <div class="empty error">
      <p>{error}</p>
      <button class="refresh-btn" on:click={() => { loading = true; refresh(true); }}>Try again</button>
    </div>
  {:else if data}

    {#if staleError}
      <div class="stale-banner" role="status">Couldn’t refresh — showing the last loaded data.</div>
    {/if}

    <!-- ── Overview stat cards ───────────────────────────────────────────── -->
    <section class="cards">
      <div class="card">
        <div class="card-value">{data.overview.total_items.toLocaleString()}</div>
        <div class="card-label">Items</div>
      </div>
      <div class="card">
        <div class="card-value">{fmtBytes(data.overview.total_size_bytes)}</div>
        <div class="card-label">Storage</div>
      </div>
      <div class="card">
        <div class="card-value">{data.overview.total_plays.toLocaleString()}</div>
        <div class="card-label">Plays</div>
      </div>
      <div class="card">
        <div class="card-value">{fmtDuration(data.overview.total_watch_time_ms)}</div>
        <div class="card-label">Watch time</div>
      </div>
      <div class="card">
        <div class="card-value">{data.overview.total_files.toLocaleString()}</div>
        <div class="card-label">Files</div>
      </div>
      <div class="card" title="Plays whose final position reached 90% of the runtime, last {chartDays} days">
        <div class="card-value">{completionPct === null ? '—' : completionPct + '%'}</div>
        <div class="card-label">Completion</div>
      </div>
    </section>

    <!-- ── Now Playing ────────────────────────────────────────────────────── -->
    {#if sessions.length > 0}
      <section class="now-playing">
        <h2>Now playing <span class="live-dot"></span></h2>
        <div class="stream-list">
          {#each sessions as s}
            {@const pct = s.duration_ms && s.duration_ms > 0 ? Math.min(100, (s.position_ms / s.duration_ms) * 100) : 0}
            <div class="stream-card">
              {#if s.poster_path}
                <img class="stream-poster" src={artworkSrc(s.poster_path, 150)}
                     srcset="{artworkSrc(s.poster_path, 75)} 75w, {artworkSrc(s.poster_path, 150)} 150w, {artworkSrc(s.poster_path, 300)} 300w"
                     sizes="80px"
                     alt={s.title} />
              {:else}
                <div class="stream-poster placeholder"></div>
              {/if}
              <div class="stream-info">
                <div class="stream-title">
                  {#if s.parent_title}<span class="muted">{s.parent_title} · </span>{/if}{s.title}{#if s.year} <span class="muted">({s.year})</span>{/if}
                </div>
                <div class="stream-meta">
                  <span class="stream-decision" class:transcode={s.decision === 'transcode'}>{s.decision === 'directPlay' ? 'Direct Play' : s.decision === 'directStream' ? 'Direct Stream' : s.decision === 'remux' ? 'Remux' : 'Transcoding'}</span>
                  {#if s.bitrate_kbps}<span class="muted">· {(s.bitrate_kbps / 1000).toFixed(1)} Mbps</span>{/if}
                  {#if s.client_name}<span class="muted">· {s.client_name}</span>{/if}
                </div>
                <div class="stream-progress-track">
                  <div class="stream-progress-fill" style="width:{pct}%"></div>
                </div>
                <div class="stream-times muted">
                  {fmtClock(s.position_ms)}{#if s.duration_ms} / {fmtClock(s.duration_ms)}{/if}
                </div>
              </div>
              <!-- Owners can stop their own stream; admins can stop anyone's
                   (the server enforces + audit-logs; non-admins only ever see
                   their own sessions in this list anyway). -->
              <button class="stream-stop" title="Stop this stream"
                      disabled={stoppingId === s.id}
                      on:click={() => stopSession(s.id)}>
                {stoppingId === s.id ? 'Stopping…' : 'Stop'}
              </button>
            </div>
          {/each}
        </div>
      </section>
    {/if}

    <div class="grid">

      <!-- ── Play activity ─────────────────────────────────────────────── -->
      <section class="panel wide">
        <h2>Play activity <span class="muted">— last {chartDays} days</span></h2>
        <div class="bar-chart" role="img"
             aria-label="Daily play counts for the last {chartDays} days. Peak day: {maxDay} plays.">
          {#each days as d}
            <div class="bar-col" title="{fmtDate(d.date)}: {d.count} play{d.count === 1 ? '' : 's'}">
              <div class="bar-fill" style="height:{(d.count / maxDay) * 100}%"></div>
              {#if d.count > 0}
                <div class="bar-tip">{d.count}</div>
              {/if}
            </div>
          {/each}
        </div>
        <div class="bar-x-labels" style="grid-template-columns:repeat({days.length}, 1fr)">
          {#each days as d, i}
            {#if i === 0 || i === Math.floor((days.length - 1) / 2) || i === days.length - 1}
              <span style="grid-column:{i + 1}">{fmtDate(d.date)}</span>
            {/if}
          {/each}
        </div>
      </section>

      <!-- ── Bandwidth ─────────────────────────────────────────────────── -->
      <section class="panel wide">
        <h2>Bandwidth <span class="muted">— last {chartDays} days · estimated from source bitrate</span></h2>
        <div class="bar-chart" role="img"
             aria-label="Estimated daily streaming bandwidth for the last {chartDays} days. Peak day: {fmtBytes(maxBw)}.">
          {#each bwDays as d}
            <div class="bar-col" title="{fmtDate(d.date)}: {fmtBytes(d.bytes)}">
              <div class="bar-fill bw" style="height:{(d.bytes / maxBw) * 100}%"></div>
              {#if d.bytes > 0}
                <div class="bar-tip">{fmtBytes(d.bytes)}</div>
              {/if}
            </div>
          {/each}
        </div>
        <div class="bar-x-labels" style="grid-template-columns:repeat({bwDays.length}, 1fr)">
          {#each bwDays as d, i}
            {#if i === 0 || i === Math.floor((bwDays.length - 1) / 2) || i === bwDays.length - 1}
              <span style="grid-column:{i + 1}">{fmtDate(d.date)}</span>
            {/if}
          {/each}
        </div>
      </section>

      <!-- ── Stream types (direct vs transcode) ────────────────────────── -->
      <section class="panel wide">
        <h2>
          Stream types <span class="muted">— last {chartDays} days</span>
          <span class="legend">
            <span class="legend-item"><span class="legend-dot direct"></span>Direct</span>
            <span class="legend-item"><span class="legend-dot transcode"></span>Transcode</span>
            <span class="legend-item"><span class="legend-dot unknown"></span>Unknown</span>
          </span>
        </h2>
        {#if !hasDecisionData}
          <p class="muted small">No stream-type data yet — plays report their decision going forward;
            history from before this feature shows as unknown.</p>
        {/if}
        <div class="bar-chart" role="img"
             aria-label="Daily plays split by stream type (direct vs transcode) for the last {chartDays} days.">
          {#each streamDays as d}
            {@const total = d.direct + d.transcode + d.unknown}
            <div class="bar-col"
                 title="{fmtDate(d.date)}: {d.direct} direct, {d.transcode} transcode{d.unknown ? `, ${d.unknown} unknown` : ''}">
              {#if d.unknown > 0}<div class="bar-seg unknown" style="height:{(d.unknown / maxStream) * 100}%"></div>{/if}
              {#if d.transcode > 0}<div class="bar-seg transcode" style="height:{(d.transcode / maxStream) * 100}%"></div>{/if}
              {#if d.direct > 0}<div class="bar-seg direct" style="height:{(d.direct / maxStream) * 100}%"></div>{/if}
              {#if total === 0}<div class="bar-fill" style="height:0"></div>{/if}
              {#if total > 0}
                <div class="bar-tip">{total}</div>
              {/if}
            </div>
          {/each}
        </div>
        <div class="bar-x-labels" style="grid-template-columns:repeat({streamDays.length}, 1fr)">
          {#each streamDays as d, i}
            {#if i === 0 || i === Math.floor((streamDays.length - 1) / 2) || i === streamDays.length - 1}
              <span style="grid-column:{i + 1}">{fmtDate(d.date)}</span>
            {/if}
          {/each}
        </div>
      </section>

      <!-- ── Top users ─────────────────────────────────────────────────── -->
      <section class="panel">
        <h2>Top users <span class="muted">— last {chartDays} days</span></h2>
        {#if data.top_users.length === 0}
          <p class="muted small">No plays recorded yet</p>
        {:else}
          <div class="hbars">
            {#each data.top_users as u}
              <div class="hbar-row">
                <span class="hbar-label user" title={u.username}>{u.username}</span>
                <div class="hbar-track">
                  <div class="hbar-fill" style="width:{pct(u.watch_time_ms, maxUserWatch)}%; background:#7c6af7"></div>
                </div>
                <span class="hbar-count wide" title="{u.play_count} plays">{fmtDuration(u.watch_time_ms)}</span>
              </div>
            {/each}
          </div>
        {/if}
      </section>

      <!-- ── Clients ───────────────────────────────────────────────────── -->
      <section class="panel">
        <h2>Clients <span class="muted">— last {chartDays} days</span></h2>
        {#if data.clients.length === 0}
          <p class="muted small">No plays recorded yet</p>
        {:else}
          <div class="hbars">
            {#each data.clients as c}
              <div class="hbar-row">
                <span class="hbar-label user" title={c.client}>{c.client}</span>
                <div class="hbar-track">
                  <div class="hbar-fill" style="width:{pct(c.count, maxClient)}%; background:#3ab8f7"></div>
                </div>
                <span class="hbar-count">{c.count}</span>
              </div>
            {/each}
          </div>
        {/if}
      </section>

      <!-- ── Plays by hour ─────────────────────────────────────────────── -->
      <section class="panel wide">
        <h2>Plays by hour of day <span class="muted">— last {chartDays} days</span></h2>
        <div class="bar-chart" role="img"
             aria-label="Plays by local hour of day for the last {chartDays} days.">
          {#each hours as h}
            <div class="bar-col" title="{fmtHour(h.hour)}: {h.count} play{h.count === 1 ? '' : 's'}">
              <div class="bar-fill" style="height:{(h.count / maxHour) * 100}%"></div>
              {#if h.count > 0}
                <div class="bar-tip">{h.count}</div>
              {/if}
            </div>
          {/each}
        </div>
        <div class="bar-x-labels" style="grid-template-columns:repeat(24, 1fr)">
          {#each [0, 6, 12, 18, 23] as h}
            <span style="grid-column:{h + 1}">{fmtHour(h)}</span>
          {/each}
        </div>
      </section>

      <!-- ── Resolution breakdown ──────────────────────────────────────── -->
      <section class="panel">
        <h2>Resolution</h2>
        <div class="hbars">
          {#each [['4K', res4k, '#7c6af7'], ['1080p', res1080, '#5b8cf7'], ['720p', res720, '#3ab8f7'], ['SD', resSd, '#3af7a0']] as [label, val, color]}
            <div class="hbar-row">
              <span class="hbar-label">{label}</span>
              <div class="hbar-track">
                <div class="hbar-fill" style="width:{pct(Number(val), totalRes)}%; background:{color}"></div>
              </div>
              <span class="hbar-count">{val}</span>
            </div>
          {/each}
        </div>
      </section>

      <!-- ── Video codecs ──────────────────────────────────────────────── -->
      <section class="panel">
        <h2>Video codec</h2>
        {#if data.video_codecs.length === 0}
          <p class="muted small">No data yet</p>
        {:else}
          <div class="hbars">
            {#each data.video_codecs as c}
              <div class="hbar-row">
                <span class="hbar-label">{c.codec}</span>
                <div class="hbar-track">
                  <div class="hbar-fill" style="width:{pct(c.count, maxCodec)}%; background:#7c6af7"></div>
                </div>
                <span class="hbar-count">{c.count}</span>
              </div>
            {/each}
          </div>
        {/if}
      </section>

      <!-- ── Containers ────────────────────────────────────────────────── -->
      <section class="panel">
        <h2>Container</h2>
        {#if data.containers.length === 0}
          <p class="muted small">No data yet</p>
        {:else}
          <div class="hbars">
            {#each data.containers as c}
              <div class="hbar-row">
                <span class="hbar-label">{c.container}</span>
                <div class="hbar-track">
                  <div class="hbar-fill" style="width:{pct(c.count, maxCont)}%; background:#5b8cf7"></div>
                </div>
                <span class="hbar-count">{c.count}</span>
              </div>
            {/each}
          </div>
        {/if}
      </section>

      <!-- ── Libraries ────────────────────────────────────────────────── -->
      {#if data.libraries.length > 0}
        <section class="panel">
          <h2>Libraries</h2>
          <div class="lib-list">
            {#each data.libraries as lib}
              <div class="lib-row">
                <div class="lib-name">{lib.name}</div>
                <div class="lib-meta">
                  <span>{lib.item_count} items</span>
                  <span>{fmtBytes(lib.total_size_bytes)}</span>
                </div>
              </div>
            {/each}
          </div>
        </section>
      {/if}

      <!-- ── Most played ────────────────────────────────────────────────── -->
      <section class="panel">
        <h2>Most played <span class="muted">— last {chartDays} days</span></h2>
        {#if data.top_played.length === 0}
          <p class="muted small">No plays recorded yet</p>
        {:else}
          <div class="top-list">
            {#each data.top_played as item, i}
              <div class="top-row">
                <span class="top-rank">{i + 1}</span>
                {#if item.poster_path}
                  <img class="top-thumb" src={artworkSrc(item.poster_path, 150)}
                       srcset="{artworkSrc(item.poster_path, 75)} 75w, {artworkSrc(item.poster_path, 150)} 150w, {artworkSrc(item.poster_path, 300)} 300w"
                       sizes="48px"
                       alt={item.title} loading="lazy" />
                {:else}
                  <div class="top-thumb placeholder"></div>
                {/if}
                <div class="top-info">
                  <div class="top-title" title="{item.parent_title ? item.parent_title + ' — ' : ''}{item.title}">{item.title}</div>
                  {#if item.parent_title}
                    <div class="top-year">{item.parent_title}</div>
                  {:else if item.year}
                    <div class="top-year">{item.year}</div>
                  {/if}
                </div>
                <div class="top-bar-wrap">
                  <div class="top-bar" style="width:{pct(item.play_count, maxPlayed)}%"></div>
                </div>
                <span class="top-count">{item.play_count}</span>
              </div>
            {/each}
          </div>
        {/if}
      </section>

      <!-- ── Recent plays ────────────────────────────────────────────────── -->
      <section class="panel wide">
        <h2>Recent plays</h2>
        {#if data.recent_plays.length === 0}
          <p class="muted small">No plays recorded yet</p>
        {:else}
          <table class="recent-table">
            <thead>
              <tr>
                <th>Title</th>
                <th>Type</th>
                <th>User</th>
                <th>Client</th>
                <th>Duration</th>
                <th>Played</th>
              </tr>
            </thead>
            <tbody>
              {#each data.recent_plays as p}
                <tr>
                  <td class="col-title">
                    {#if p.parent_title}<span class="muted">{p.parent_title} · </span>{/if}{p.title}{#if p.year} <span class="muted">({p.year})</span>{/if}
                  </td>
                  <td><span class="badge">{p.type}</span></td>
                  <td class="muted">{p.user_name ?? '—'}</td>
                  <td class="muted">{p.client_name ?? '—'}</td>
                  <td class="muted">{p.duration_ms ? fmtDuration(p.duration_ms) : '—'}</td>
                  <td class="muted">{fmtTime(p.occurred_at)}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        {/if}
      </section>

    </div>
  {/if}
</div>
{/if}

<style>
  .page { padding: 2rem 2.5rem; max-width: 1400px; }

  .page-header { margin-bottom: 1.8rem; display: flex; align-items: center; justify-content: space-between; }
  h1 { font-size: 1.35rem; font-weight: 700; letter-spacing: -0.02em; }

  .header-controls { display: flex; align-items: center; gap: 0.7rem; }

  .range-picker {
    display: flex; border: 1px solid var(--border); border-radius: 6px; overflow: hidden;
  }
  .range-btn {
    font-size: 0.75rem; padding: 0.4rem 0.7rem; border: none; cursor: pointer;
    background: var(--bg-secondary); color: var(--text-muted);
  }
  .range-btn + .range-btn { border-left: 1px solid var(--border); }
  .range-btn:hover { color: var(--text-primary); }
  .range-btn.active { background: var(--accent-bg); color: var(--accent-text); }

  .refresh-btn {
    font-size: 0.78rem; padding: 0.4rem 0.9rem; border-radius: 6px;
    background: var(--bg-secondary); border: 1px solid var(--border);
    color: var(--text-secondary); cursor: pointer;
  }
  .refresh-btn:hover:not(:disabled) { border-color: var(--accent); color: var(--text-primary); }
  .refresh-btn:disabled { opacity: 0.6; cursor: default; }

  .stale-banner {
    margin-bottom: 1.2rem; padding: 0.55rem 0.9rem; border-radius: 8px;
    font-size: 0.8rem; color: #f7c46a;
    background: rgba(247, 196, 106, 0.08); border: 1px solid rgba(247, 196, 106, 0.25);
  }
  h2 { font-size: 0.78rem; font-weight: 600; color: var(--text-muted); text-transform: uppercase;
       letter-spacing: 0.07em; margin-bottom: 1rem; }

  .empty { color: var(--text-muted); padding: 4rem; text-align: center; }
  .error { color: #f76a6a; }
  .muted { color: var(--text-muted); }
  .small { font-size: 0.82rem; }

  /* ── Stat cards ──────────────────────────────────────────────────────── */
  .cards {
    display: grid;
    grid-template-columns: repeat(6, 1fr);
    gap: 1rem;
    margin-bottom: 1.8rem;
  }
  .card {
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: 10px;
    padding: 1.1rem 1.2rem;
  }
  .card-value { font-size: 1.5rem; font-weight: 700; letter-spacing: -0.03em; }
  .card-label { font-size: 0.75rem; color: var(--text-muted); margin-top: 0.2rem; text-transform: uppercase; letter-spacing: 0.06em; }

  /* ── Grid layout ─────────────────────────────────────────────────────── */
  .grid {
    display: grid;
    grid-template-columns: repeat(2, 1fr);
    gap: 1.2rem;
  }

  .panel {
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: 10px;
    padding: 1.2rem 1.4rem;
  }
  .panel.wide { grid-column: 1 / -1; }

  /* ── Activity bar chart ──────────────────────────────────────────────── */
  .bar-chart {
    display: flex;
    align-items: flex-end;
    gap: 3px;
    height: 80px;
    margin-bottom: 0.3rem;
  }
  .bar-col {
    flex: 1;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: flex-end;
    height: 100%;
    position: relative;
    cursor: default;
  }
  .bar-col:hover .bar-fill { background: var(--accent-text); }
  .bar-col:hover .bar-fill.bw { background: #3ab8f7; }
  .bar-fill.bw { background: #5b8cf7; }
  .bar-col:hover .bar-tip { opacity: 1; }
  .bar-fill {
    width: 100%;
    min-height: 2px;
    background: var(--accent);
    border-radius: 2px 2px 0 0;
    transition: background 0.1s;
  }
  .bar-tip {
    position: absolute;
    top: -22px;
    font-size: 0.65rem;
    color: var(--text-primary);
    background: var(--bg-secondary);
    border-radius: 3px;
    padding: 1px 4px;
    white-space: nowrap;
    opacity: 0;
    pointer-events: none;
    transition: opacity 0.1s;
  }
  .bar-x-labels {
    display: grid;
    grid-template-columns: repeat(30, 1fr); /* overridden inline per chart width */
    font-size: 0.65rem;
    color: var(--text-muted);
  }
  .bar-x-labels span { grid-row: 1; white-space: nowrap; }

  /* Stacked stream-type segments (bottom-up: direct, transcode, unknown). */
  .bar-seg { width: 100%; }
  .bar-seg.direct    { background: #3ab8f7; border-radius: 2px 2px 0 0; }
  .bar-seg.transcode { background: #f7a03a; }
  .bar-seg.unknown   { background: var(--border-strong); }

  .legend { float: right; display: inline-flex; gap: 0.8rem; text-transform: none; letter-spacing: 0; }
  .legend-item { display: inline-flex; align-items: center; gap: 0.3rem; font-size: 0.7rem; color: var(--text-muted); }
  .legend-dot { width: 8px; height: 8px; border-radius: 2px; display: inline-block; }
  .legend-dot.direct    { background: #3ab8f7; }
  .legend-dot.transcode { background: #f7a03a; }
  .legend-dot.unknown   { background: var(--border-strong); }

  /* ── Horizontal bar rows ─────────────────────────────────────────────── */
  .hbars { display: flex; flex-direction: column; gap: 0.55rem; }
  .hbar-row { display: flex; align-items: center; gap: 0.6rem; }
  .hbar-label { width: 52px; font-size: 0.78rem; color: var(--text-secondary); flex-shrink: 0; }
  .hbar-label.user {
    width: 110px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
  }
  .hbar-track { flex: 1; height: 8px; background: var(--border); border-radius: 4px; overflow: hidden; }
  .hbar-fill  { height: 100%; border-radius: 4px; transition: width 0.3s ease; }
  .hbar-count { width: 32px; text-align: right; font-size: 0.75rem; color: var(--text-muted); flex-shrink: 0; }
  .hbar-count.wide { width: 64px; }

  /* ── Libraries ───────────────────────────────────────────────────────── */
  .lib-list { display: flex; flex-direction: column; gap: 0.5rem; }
  .lib-row  { display: flex; justify-content: space-between; align-items: center; }
  .lib-name { font-size: 0.85rem; }
  .lib-meta { display: flex; gap: 1rem; font-size: 0.75rem; color: var(--text-muted); }

  /* ── Most played ─────────────────────────────────────────────────────── */
  .top-list { display: flex; flex-direction: column; gap: 0.55rem; }
  .top-row  { display: flex; align-items: center; gap: 0.65rem; }
  .top-rank { width: 16px; font-size: 0.72rem; color: var(--text-muted); text-align: right; flex-shrink: 0; }
  .top-thumb {
    width: 28px; height: 42px; border-radius: 3px; object-fit: cover; flex-shrink: 0;
  }
  .top-thumb.placeholder {
    background: var(--border); border-radius: 3px;
  }
  .top-info { width: 130px; flex-shrink: 0; }
  .top-title { font-size: 0.82rem; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .top-year  { font-size: 0.72rem; color: var(--text-muted); }
  .top-bar-wrap { flex: 1; height: 6px; background: var(--border); border-radius: 3px; overflow: hidden; }
  .top-bar  { height: 100%; background: var(--accent); border-radius: 3px; }
  .top-count { width: 28px; text-align: right; font-size: 0.75rem; color: var(--text-muted); flex-shrink: 0; }

  /* ── Recent plays table ──────────────────────────────────────────────── */
  .recent-table { width: 100%; border-collapse: collapse; font-size: 0.82rem; }
  .recent-table th {
    text-align: left; font-size: 0.7rem; font-weight: 600; color: var(--text-muted);
    text-transform: uppercase; letter-spacing: 0.06em;
    padding: 0 0.75rem 0.6rem;
    border-bottom: 1px solid var(--border);
  }
  .recent-table td { padding: 0.5rem 0.75rem; border-bottom: 1px solid var(--bg-hover); }
  .recent-table tr:last-child td { border-bottom: none; }
  .col-title { color: var(--text-primary); }
  .badge {
    font-size: 0.68rem; padding: 2px 6px; border-radius: 4px;
    background: var(--accent-bg); color: var(--accent-text);
    text-transform: capitalize;
  }

  /* ── Now Playing ─────────────────────────────────────────────────────────── */
  .now-playing {
    margin: 0 0 1.5rem;
    padding: 1rem 1.25rem;
    background: rgba(124,106,247,0.07);
    border: 1px solid var(--accent-bg);
    border-radius: 10px;
  }
  .now-playing h2 {
    font-size: 0.78rem; font-weight: 600; color: var(--accent-text);
    text-transform: uppercase; letter-spacing: 0.06em;
    margin-bottom: 0.9rem;
    display: flex; align-items: center; gap: 0.5rem;
  }
  .live-dot {
    display: inline-block; width: 7px; height: 7px;
    border-radius: 50%; background: #3af7a0;
    animation: pulse 1.4s ease-in-out infinite;
  }
  @media (prefers-reduced-motion: reduce) {
    .live-dot { animation: none; }
  }
  .stream-list { display: flex; flex-direction: column; gap: 0.75rem; }
  .stream-card {
    display: flex; align-items: center; gap: 0.9rem;
  }
  .stream-poster {
    width: 44px; height: 64px; border-radius: 4px;
    object-fit: cover; flex-shrink: 0; background: var(--bg-secondary);
  }
  .stream-poster.placeholder { background: var(--bg-secondary); }
  .stream-info { flex: 1; min-width: 0; }
  .stream-stop {
    flex-shrink: 0; padding: 0.3rem 0.7rem;
    background: transparent; color: var(--text-muted);
    border: 1px solid var(--border); border-radius: 6px;
    font-size: 0.75rem; cursor: pointer;
    transition: color 0.15s, border-color 0.15s;
  }
  .stream-stop:hover:not(:disabled) { color: #f87171; border-color: #f87171; }
  .stream-stop:disabled { opacity: 0.6; cursor: default; }
  .stream-title {
    font-size: 0.85rem; font-weight: 600; color: var(--text-primary);
    white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
    margin-bottom: 0.2rem;
  }
  .stream-meta {
    font-size: 0.72rem; color: var(--text-muted); margin-bottom: 0.4rem;
    display: flex; align-items: center; gap: 0.4rem;
  }
  .stream-decision { color: #3ab8f7; }
  .stream-decision.transcode { color: #f7a03a; }
  .stream-progress-track {
    height: 3px; background: var(--border-strong);
    border-radius: 2px; overflow: hidden; margin-bottom: 0.25rem;
  }
  .stream-progress-fill {
    height: 100%; background: var(--accent);
    border-radius: 2px; transition: width 1s linear;
  }
  .stream-times { font-size: 0.68rem; }

  /* ── Mobile ────────────────────────────────────────────────────────────── */
  @media (max-width: 768px) {
    .page { padding: 1.25rem 1rem 5rem; }

    .cards {
      grid-template-columns: repeat(2, 1fr);
    }
    .card { padding: 0.85rem 0.9rem; }
    .card-value { font-size: 1.2rem; }

    .grid {
      grid-template-columns: 1fr;
    }
    .panel.wide { grid-column: 1; }
    .panel { padding: 1rem; }

    .bar-chart { gap: 2px; height: 60px; }
    .bar-tip { font-size: 0.55rem; }

    .top-info { width: 90px; }

    .recent-table { display: block; overflow-x: auto; white-space: nowrap; }
  }
</style>
