<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { api, analyticsApi, sessionsApi, type AnalyticsData, type DayCount, type DayBytes, type ActiveSession } from '$lib/api';

  let data: AnalyticsData | null = null;
  let loading = true;
  let error = '';
  let sessions: ActiveSession[] = [];
  let alive = true;
  let ready = false;

  async function refresh() {
    if (!alive) return;
    try {
      data = await analyticsApi.get();
      error = '';
    } catch (e: unknown) {
      if (!alive) return;
      error = e instanceof Error ? e.message : 'Failed to load analytics';
    } finally {
      loading = false;
    }
  }

  async function refreshSessions() {
    if (!alive) return;
    try {
      sessions = await sessionsApi.list() ?? [];
    } catch (e) { if (alive) console.warn(e); }
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
    return () => {
      alive = false;
      clearInterval(slowInterval);
      clearInterval(fastInterval);
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
    const h = Math.floor(ms / 3600000);
    const m = Math.floor((ms % 3600000) / 60000);
    if (h === 0) return `${m} min`;
    if (m === 0) return `${h}h`;
    return `${h}h ${m}m`;
  }

  function fmtDate(iso: string): string {
    return new Date(iso).toLocaleDateString('en-US', { month: 'short', day: 'numeric' });
  }

  function fmtTime(iso: string): string {
    return new Date(iso).toLocaleString('en-US', {
      month: 'short', day: 'numeric',
      hour: 'numeric', minute: '2-digit'
    });
  }

  // Build a full 30-day array, filling missing dates with 0.
  function fillDays(raw: DayCount[]): DayCount[] {
    const map = new Map(raw.map(d => [d.date, d.count]));
    const days: DayCount[] = [];
    for (let i = 29; i >= 0; i--) {
      const d = new Date();
      d.setDate(d.getDate() - i);
      const key = d.toISOString().slice(0, 10);
      days.push({ date: key, count: map.get(key) ?? 0 });
    }
    return days;
  }

  $: days = data ? fillDays(data.plays_by_day) : [];
  $: maxDay = days.reduce((m, d) => Math.max(m, d.count), 0) || 1;

  function fillDayBytes(raw: DayBytes[]): DayBytes[] {
    const map = new Map(raw.map(d => [d.date, d.bytes]));
    const days: DayBytes[] = [];
    for (let i = 29; i >= 0; i--) {
      const d = new Date();
      d.setDate(d.getDate() - i);
      const key = d.toISOString().slice(0, 10);
      days.push({ date: key, bytes: map.get(key) ?? 0 });
    }
    return days;
  }

  $: bwDays = data ? fillDayBytes(data.bandwidth_by_day ?? []) : [];
  $: maxBw  = bwDays.reduce((m, d) => Math.max(m, d.bytes), 0) || 1;

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
  </header>

  {#if loading}
    <div class="empty">Loading…</div>
  {:else if error}
    <div class="empty error">{error}</div>
  {:else if data}

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
                <img class="stream-poster" src="/artwork/{encodeURI(s.poster_path)}?w=150"
                     srcset="/artwork/{encodeURI(s.poster_path)}?w=75 75w, /artwork/{encodeURI(s.poster_path)}?w=150 150w, /artwork/{encodeURI(s.poster_path)}?w=300 300w"
                     sizes="80px"
                     alt={s.title} />
              {:else}
                <div class="stream-poster placeholder"></div>
              {/if}
              <div class="stream-info">
                <div class="stream-title">{s.title}{#if s.year} <span class="muted">({s.year})</span>{/if}</div>
                <div class="stream-meta">
                  <span class="stream-decision" class:transcode={s.decision === 'transcode'}>{s.decision === 'directPlay' ? 'Direct Play' : s.decision === 'directStream' ? 'Direct Stream' : s.decision === 'remux' ? 'Remux' : 'Transcoding'}</span>
                  {#if s.bitrate_kbps}<span class="muted">· {(s.bitrate_kbps / 1000).toFixed(1)} Mbps</span>{/if}
                  {#if s.client_name}<span class="muted">· {s.client_name}</span>{/if}
                </div>
                <div class="stream-progress-track">
                  <div class="stream-progress-fill" style="width:{pct}%"></div>
                </div>
                <div class="stream-times muted">
                  {fmtDuration(s.position_ms)}{#if s.duration_ms} / {fmtDuration(s.duration_ms)}{/if}
                </div>
              </div>
            </div>
          {/each}
        </div>
      </section>
    {/if}

    <div class="grid">

      <!-- ── Play activity ─────────────────────────────────────────────── -->
      <section class="panel wide">
        <h2>Play activity <span class="muted">— last 30 days</span></h2>
        <div class="bar-chart">
          {#each days as d}
            <div class="bar-col" title="{fmtDate(d.date)}: {d.count} play{d.count === 1 ? '' : 's'}">
              <div class="bar-fill" style="height:{(d.count / maxDay) * 100}%"></div>
              {#if d.count > 0}
                <div class="bar-tip">{d.count}</div>
              {/if}
            </div>
          {/each}
        </div>
        <div class="bar-x-labels">
          {#each days as d, i}
            {#if i === 0 || i === 14 || i === 29}
              <span style="grid-column:{i + 1}">{fmtDate(d.date)}</span>
            {/if}
          {/each}
        </div>
      </section>

      <!-- ── Bandwidth ─────────────────────────────────────────────────── -->
      <section class="panel wide">
        <h2>Bandwidth <span class="muted">— last 30 days</span></h2>
        <div class="bar-chart">
          {#each bwDays as d}
            <div class="bar-col" title="{fmtDate(d.date)}: {fmtBytes(d.bytes)}">
              <div class="bar-fill bw" style="height:{(d.bytes / maxBw) * 100}%"></div>
              {#if d.bytes > 0}
                <div class="bar-tip">{fmtBytes(d.bytes)}</div>
              {/if}
            </div>
          {/each}
        </div>
        <div class="bar-x-labels">
          {#each bwDays as d, i}
            {#if i === 0 || i === 14 || i === 29}
              <span style="grid-column:{i + 1}">{fmtDate(d.date)}</span>
            {/if}
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
        <h2>Most played</h2>
        {#if data.top_played.length === 0}
          <p class="muted small">No plays recorded yet</p>
        {:else}
          <div class="top-list">
            {#each data.top_played as item, i}
              <div class="top-row">
                <span class="top-rank">{i + 1}</span>
                {#if item.poster_path}
                  <img class="top-thumb" src="/artwork/{encodeURI(item.poster_path)}?w=150"
                       srcset="/artwork/{encodeURI(item.poster_path)}?w=75 75w, /artwork/{encodeURI(item.poster_path)}?w=150 150w, /artwork/{encodeURI(item.poster_path)}?w=300 300w"
                       sizes="48px"
                       alt={item.title} loading="lazy" />
                {:else}
                  <div class="top-thumb placeholder"></div>
                {/if}
                <div class="top-info">
                  <div class="top-title">{item.title}</div>
                  {#if item.year}<div class="top-year">{item.year}</div>{/if}
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
                <th>Client</th>
                <th>Duration</th>
                <th>Played</th>
              </tr>
            </thead>
            <tbody>
              {#each data.recent_plays as p}
                <tr>
                  <td class="col-title">{p.title}{#if p.year} <span class="muted">({p.year})</span>{/if}</td>
                  <td><span class="badge">{p.type}</span></td>
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

  .page-header { margin-bottom: 1.8rem; }
  h1 { font-size: 1.35rem; font-weight: 700; letter-spacing: -0.02em; }
  h2 { font-size: 0.78rem; font-weight: 600; color: var(--text-muted); text-transform: uppercase;
       letter-spacing: 0.07em; margin-bottom: 1rem; }

  .empty { color: var(--text-muted); padding: 4rem; text-align: center; }
  .error { color: #f76a6a; }
  .muted { color: var(--text-muted); }
  .small { font-size: 0.82rem; }

  /* ── Stat cards ──────────────────────────────────────────────────────── */
  .cards {
    display: grid;
    grid-template-columns: repeat(5, 1fr);
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
    grid-template-columns: repeat(30, 1fr);
    font-size: 0.65rem;
    color: var(--text-muted);
  }
  .bar-x-labels span { grid-row: 1; white-space: nowrap; }

  /* ── Horizontal bar rows ─────────────────────────────────────────────── */
  .hbars { display: flex; flex-direction: column; gap: 0.55rem; }
  .hbar-row { display: flex; align-items: center; gap: 0.6rem; }
  .hbar-label { width: 52px; font-size: 0.78rem; color: var(--text-secondary); flex-shrink: 0; }
  .hbar-track { flex: 1; height: 8px; background: var(--border); border-radius: 4px; overflow: hidden; }
  .hbar-fill  { height: 100%; border-radius: 4px; transition: width 0.3s ease; }
  .hbar-count { width: 32px; text-align: right; font-size: 0.75rem; color: var(--text-muted); flex-shrink: 0; }

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
