<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { upcomingApi, type UpcomingItem, type UpcomingResponse } from '$lib/api';
  import { itemHref } from '$lib/itemHref';
  import {
    FILTERS, STATUSES, WEEKDAYS, addDays, agendaDays, buildMonthGrid, entryText,
    fetchRange, filterItems, formatDayKey, formatTime, formatWhen, groupByDay,
    initialView, localDateKey, monthTitle, relativeDay, releaseTypeLabel,
    safePosterUrl, shiftMonth, statusLabel, visibleRange,
    type UpcomingFilter, type UpcomingView,
  } from './upcoming';

  // What Radarr / Sonarr expect to arrive, as a month grid or a 30-day agenda.
  // Everyone can see it; the server already drops what a restricted profile
  // isn't allowed to know about, so nothing here filters for permissions.

  // Admins get a link to the arr settings from the "nothing connected" state.
  export let isAdmin = false;

  const start = new Date();
  let today = localDateKey(start);
  let cursor = { year: start.getFullYear(), month: start.getMonth() };
  let view: UpcomingView = initialView(typeof window === 'undefined' ? Infinity : window.innerWidth);
  let filter: UpcomingFilter = 'all';

  let loading = true;
  let error = '';
  let data: UpcomingResponse | null = null;
  // Month / view clicks can outrun the network; only the latest load may land.
  let loadSeq = 0;

  let selected: UpcomingItem | null = null;
  let returnFocus: HTMLElement | null = null;
  let modalEl: HTMLDivElement | null = null;
  let closeBtn: HTMLButtonElement | null = null;

  $: grid = buildMonthGrid(cursor.year, cursor.month);
  $: range = visibleRange(view, cursor, today);
  $: byDay = groupByDay(filterItems(data?.items ?? [], filter), range);
  $: agenda = agendaDays(byDay);
  $: services = data?.services ?? [];
  $: failing = services.filter(s => !s.ok);
  $: allFailed = services.length > 0 && failing.length === services.length;
  $: selectedPoster = selected ? safePosterUrl(selected.poster_url) : null;

  onMount(load);

  async function load() {
    const seq = ++loadSeq;
    today = localDateKey(new Date());
    const { from, to } = fetchRange(visibleRange(view, cursor, today));
    loading = true; error = '';
    try {
      const res = await upcomingApi.get(from, to);
      if (seq !== loadSeq) return;
      data = res;
    } catch {
      if (seq !== loadSeq) return;
      error = 'Couldn’t load upcoming releases.';
    } finally {
      if (seq === loadSeq) loading = false;
    }
  }

  function shift(delta: number) {
    cursor = shiftMonth(cursor, delta);
    load();
  }

  function goToday() {
    const now = new Date();
    cursor = { year: now.getFullYear(), month: now.getMonth() };
    load();
  }

  function setView(v: UpcomingView) {
    if (v === view) return;
    view = v;
    load();
  }

  async function openEntry(item: UpcomingItem, e: MouseEvent) {
    returnFocus = e.currentTarget as HTMLElement;
    selected = item;
    await tick();
    closeBtn?.focus();
  }

  function closeEntry() {
    selected = null;
    returnFocus?.focus();
    returnFocus = null;
  }

  function onOverlayClick(e: MouseEvent) {
    if (e.target === e.currentTarget) closeEntry();
  }

  // Escape closes the detail panel; Tab stays inside it while it's open.
  function onKeydown(e: KeyboardEvent) {
    if (!selected) return;
    if (e.key === 'Escape') {
      e.preventDefault();
      closeEntry();
      return;
    }
    if (e.key !== 'Tab' || !modalEl) return;
    const focusable = modalEl.querySelectorAll<HTMLElement>('a[href], button:not([disabled])');
    if (focusable.length === 0) return;
    const first = focusable[0];
    const last = focusable[focusable.length - 1];
    if (e.shiftKey && document.activeElement === first) {
      e.preventDefault(); last.focus();
    } else if (!e.shiftKey && document.activeElement === last) {
      e.preventDefault(); first.focus();
    }
  }
</script>

<svelte:window on:keydown={onKeydown} />

<div class="upcoming">
  <div class="toolbar">
    <div class="nav">
      {#if view === 'month'}
        <button type="button" class="btn ghost sm nav-btn" aria-label="Previous month" on:click={() => shift(-1)}>‹</button>
        <button type="button" class="btn ghost sm" on:click={goToday}>Today</button>
        <button type="button" class="btn ghost sm nav-btn" aria-label="Next month" on:click={() => shift(1)}>›</button>
      {/if}
    </div>
    <h2 class="range-title" aria-live="polite">
      {view === 'month' ? monthTitle(cursor.year, cursor.month) : 'Next 30 days'}
    </h2>
    <div class="segmented" role="group" aria-label="Calendar view">
      <button type="button" class:active={view === 'month'} aria-pressed={view === 'month'} on:click={() => setView('month')}>Month</button>
      <button type="button" class:active={view === 'agenda'} aria-pressed={view === 'agenda'} on:click={() => setView('agenda')}>Agenda</button>
    </div>
  </div>

  <div class="subbar">
    <div class="chips" role="group" aria-label="Show">
      {#each FILTERS as f (f.key)}
        <button type="button" class="chip" class:active={filter === f.key} aria-pressed={filter === f.key} on:click={() => (filter = f.key)}>
          {f.label}
        </button>
      {/each}
    </div>
    <ul class="legend" aria-label="Status colours">
      {#each STATUSES as s (s.key)}
        <li title={s.hint}><span class="swatch st-{s.key}" aria-hidden="true"></span>{s.label}</li>
      {/each}
    </ul>
  </div>

  {#if failing.length > 0}
    <p class="warn" role="status" title={failing.map(s => `${s.name}: ${s.error}`).join('\n')}>
      Couldn’t reach {failing.map(s => s.name).join(', ')} — entries from {failing.length === 1 ? 'it' : 'them'} may be missing.
    </p>
  {/if}

  {#if error}
    <div class="banner error">
      {error}
      <button type="button" class="btn ghost sm" on:click={load}>Try again</button>
    </div>
  {/if}

  {#if loading && !data}
    {#if !error}<div class="skeleton-block" aria-label="Loading upcoming releases"></div>{/if}
  {:else if data && services.length === 0}
    <div class="empty">
      <p>No Radarr or Sonarr connected, so there’s nothing to show yet.</p>
      {#if isAdmin}
        <p><a href="/settings/arr-services">Connect one in Settings</a></p>
      {:else}
        <p>Ask an admin to connect one.</p>
      {/if}
    </div>
  {:else if data}
    {#if !loading && !allFailed && byDay.size === 0}
      <p class="empty-note">Nothing expected in this range</p>
    {/if}

    {#if view === 'month'}
      <div class="grid-scroll" class:busy={loading} aria-busy={loading}>
        <div class="weekdays" aria-hidden="true">
          {#each WEEKDAYS as w}<span>{w}</span>{/each}
        </div>
        <ol class="month-grid">
          {#each grid as cell (cell.key)}
            <li class="cell" class:outside={!cell.inMonth} class:today={cell.key === today} data-day={cell.key}>
              <time class="daynum" datetime={cell.key}>{cell.day}</time>
              {#each byDay.get(cell.key) ?? [] as item (item.id)}
                <button type="button" class="entry st-{item.status}" title={entryText(item)} on:click={e => openEntry(item, e)}>
                  <span class="entry-title">{item.title}</span>
                  {#if item.kind === 'movie'}
                    <span class="tag">{releaseTypeLabel(item.release_type)}</span>
                  {:else if item.subtitle}
                    <span class="entry-sub">{item.subtitle}</span>
                  {/if}
                  <span class="sr-only">, {statusLabel(item.status)}</span>
                </button>
              {/each}
            </li>
          {/each}
        </ol>
      </div>
    {:else if agenda.length > 0}
      <div class="agenda" class:busy={loading} aria-busy={loading}>
        {#each agenda as day (day.key)}
          <section class="agenda-day">
            <h3 class="agenda-date" class:today={day.key === today}>
              {formatDayKey(day.key)}
              {#if relativeDay(day.key, today)}<span class="rel">{relativeDay(day.key, today)}</span>{/if}
            </h3>
            <ul>
              {#each day.items as item (item.id)}
                <li>
                  <button type="button" class="agenda-entry st-{item.status}" title={entryText(item)} on:click={e => openEntry(item, e)}>
                    <span class="a-when">
                      {#if item.kind === 'movie'}
                        <span class="tag">{releaseTypeLabel(item.release_type)}</span>
                      {:else}
                        {formatTime(item)}
                      {/if}
                    </span>
                    <span class="a-main">
                      <span class="a-title">{item.title}</span>
                      {#if item.subtitle}<span class="a-sub">{item.subtitle}</span>{/if}
                    </span>
                    <span class="a-meta">
                      {#if item.network}<span class="a-network">{item.network}</span>{/if}
                      <span class="a-status st-{item.status}">{statusLabel(item.status)}</span>
                    </span>
                  </button>
                </li>
              {/each}
            </ul>
          </section>
        {/each}
      </div>
    {/if}
  {/if}
</div>

{#if selected}
  <div class="modal-overlay" role="presentation" on:click={onOverlayClick}>
    <div class="modal detail st-{selected.status}" role="dialog" aria-modal="true" aria-labelledby="upcoming-detail-title" bind:this={modalEl}>
      <button type="button" class="close-btn" aria-label="Close" bind:this={closeBtn} on:click={closeEntry}>×</button>
      <div class="detail-body">
        {#if selectedPoster}
          <img class="detail-poster" src={selectedPoster} alt="" loading="lazy" />
        {/if}
        <div class="detail-info">
          <h3 id="upcoming-detail-title" class="detail-title">
            {selected.title}
            {#if selected.year}<span class="detail-year">({selected.year})</span>{/if}
          </h3>
          {#if selected.subtitle}<p class="detail-sub">{selected.subtitle}</p>{/if}
          <div class="detail-pills">
            <span class="status-pill st-{selected.status}">{statusLabel(selected.status)}</span>
            <span class="tag">{releaseTypeLabel(selected.release_type)}</span>
            {#if selected.certification}<span class="tag">{selected.certification}</span>{/if}
          </div>
          <dl class="facts">
            <dt>{selected.kind === 'episode' ? 'Airs' : 'Release'}</dt>
            <dd>{formatWhen(selected)}</dd>
            {#if selected.network}<dt>Network</dt><dd>{selected.network}</dd>{/if}
            <dt>From</dt>
            <dd>{selected.service}</dd>
          </dl>
          {#if selected.overview}<p class="overview">{selected.overview}</p>{/if}
          {#if selected.item_id}
            <a class="btn primary sm" href={itemHref(selected.kind === 'movie' ? 'movie' : 'show', selected.item_id)}>Open in OnScreen</a>
          {/if}
        </div>
      </div>
    </div>
  </div>
{/if}

<style>
  /* Status colours reuse the theme's semantic tokens so light mode follows. */
  .st-downloaded { --st: var(--success); --st-bg: var(--success-bg); }
  .st-missing    { --st: var(--error);   --st-bg: var(--error-bg); }
  .st-upcoming   { --st: var(--info);    --st-bg: var(--info-bg); }

  .sr-only {
    position: absolute; width: 1px; height: 1px; padding: 0; margin: -1px;
    overflow: hidden; clip: rect(0, 0, 0, 0); white-space: nowrap; border: 0;
  }

  .toolbar {
    display: grid;
    grid-template-columns: 1fr auto 1fr;
    align-items: center;
    gap: 0.75rem;
    margin-bottom: 0.9rem;
  }
  .nav { display: flex; gap: 0.35rem; }
  .range-title {
    font-size: 1rem;
    font-weight: 700;
    color: var(--text-primary);
    letter-spacing: -0.01em;
    text-align: center;
    margin: 0;
  }

  .btn {
    display: inline-flex; align-items: center; justify-content: center;
    padding: 0.35rem 0.65rem;
    font-size: 0.74rem;
    font-weight: 600;
    border-radius: 7px;
    cursor: pointer;
    border: 1px solid transparent;
    text-decoration: none;
    line-height: 1.1;
    transition: background 0.12s, color 0.12s, border-color 0.12s;
  }
  .btn.primary { background: var(--accent); color: #fff; }
  .btn.primary:hover { background: var(--accent-hover); }
  .btn.ghost { background: transparent; border-color: var(--border-strong); color: var(--text-secondary); }
  .btn.ghost:hover { background: var(--bg-hover); color: var(--text-primary); }
  .nav-btn { min-width: 2rem; font-size: 1rem; padding: 0.2rem 0.5rem; }

  .segmented {
    justify-self: end;
    display: inline-flex;
    border: 1px solid var(--border-strong);
    border-radius: 7px;
    overflow: hidden;
  }
  .segmented button {
    background: transparent;
    border: none;
    color: var(--text-muted);
    font-size: 0.74rem;
    font-weight: 600;
    padding: 0.38rem 0.75rem;
    cursor: pointer;
  }
  .segmented button + button { border-left: 1px solid var(--border-strong); }
  .segmented button:hover { color: var(--text-secondary); background: var(--bg-hover); }
  .segmented button.active { background: var(--accent-bg); color: var(--accent-text); }

  .subbar {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 0.6rem 1rem;
    margin-bottom: 1rem;
  }
  .chips { display: flex; gap: 0.35rem; }
  .chip {
    background: transparent;
    border: 1px solid var(--border-strong);
    border-radius: 999px;
    color: var(--text-secondary);
    font-size: 0.74rem;
    padding: 0.28rem 0.8rem;
    cursor: pointer;
  }
  .chip:hover { background: var(--bg-hover); }
  .chip.active { background: var(--accent-bg); border-color: var(--accent); color: var(--accent-text); }

  .legend { display: flex; gap: 0.9rem; list-style: none; margin: 0; padding: 0; font-size: 0.72rem; color: var(--text-muted); }
  .legend li { display: inline-flex; align-items: center; gap: 0.35rem; }
  .swatch { width: 0.7rem; height: 0.7rem; border-radius: 3px; background: var(--st); }

  button:focus-visible, a:focus-visible { outline: 2px solid var(--accent); outline-offset: 1px; }

  .warn {
    font-size: 0.76rem;
    color: #fcd34d;
    background: rgba(251,191,36,0.1);
    border: 1px solid rgba(251,191,36,0.2);
    border-radius: 8px;
    padding: 0.45rem 0.8rem;
    margin: 0 0 1rem;
  }
  .banner { padding: 0.6rem 0.9rem; border-radius: 8px; font-size: 0.8rem; margin-bottom: 1.25rem; display: flex; align-items: center; justify-content: space-between; gap: 0.75rem; }
  .banner.error { background: var(--error-bg); border: 1px solid rgba(248,113,113,0.2); color: var(--error); }

  .skeleton-block {
    background: linear-gradient(90deg, var(--bg-elevated) 25%, var(--bg-hover) 50%, var(--bg-elevated) 75%);
    background-size: 200% 100%;
    animation: shimmer 1.4s infinite;
    border-radius: 8px;
    height: 320px;
  }
  @keyframes shimmer { 0% { background-position: 200% 0; } 100% { background-position: -200% 0; } }

  .empty { text-align: center; padding: 3rem 1rem; color: var(--text-muted); font-size: 0.85rem; }
  .empty p + p { margin-top: 0.5rem; }
  .empty a { color: var(--accent-text); text-decoration: none; font-weight: 600; }
  .empty a:hover { text-decoration: underline; }
  .empty-note { font-size: 0.8rem; color: var(--text-muted); margin: 0 0 0.75rem; text-align: center; }

  .busy { opacity: 0.6; transition: opacity 0.15s; }

  /* ── Month grid ── */
  .grid-scroll { overflow-x: auto; }
  .weekdays, .month-grid {
    display: grid;
    grid-template-columns: repeat(7, minmax(0, 1fr));
    min-width: 560px;
  }
  .weekdays span {
    font-size: 0.68rem;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    color: var(--text-muted);
    padding: 0 0.4rem 0.4rem;
  }
  .month-grid {
    list-style: none;
    margin: 0;
    padding: 0;
    gap: 1px;
    background: var(--border);
    border: 1px solid var(--border);
    border-radius: 10px;
    overflow: hidden;
  }
  .cell {
    background: var(--bg-primary);
    min-height: 108px;
    padding: 0.3rem;
    display: flex;
    flex-direction: column;
    gap: 3px;
    min-width: 0;
  }
  .cell.outside { background: var(--bg-secondary); }
  .cell.outside .daynum, .cell.outside .entry { opacity: 0.45; }
  .daynum {
    align-self: flex-end;
    font-size: 0.72rem;
    font-weight: 600;
    color: var(--text-secondary);
    min-width: 1.5rem;
    height: 1.5rem;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    border-radius: 999px;
  }
  .cell.today { background: var(--accent-bg); }
  .cell.today .daynum { background: var(--accent); color: #fff; }

  .entry {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 1px;
    width: 100%;
    min-width: 0;
    text-align: left;
    background: var(--st-bg);
    border: none;
    border-left: 3px solid var(--st);
    border-radius: 4px;
    padding: 0.22rem 0.35rem;
    cursor: pointer;
    color: var(--text-primary);
    font: inherit;
  }
  .entry:hover { filter: brightness(1.25); }
  .entry-title, .entry-sub {
    display: block;
    max-width: 100%;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .entry-title { font-size: 0.72rem; font-weight: 600; }
  .entry-sub { font-size: 0.66rem; color: var(--text-secondary); }

  .tag {
    font-size: 0.6rem;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--text-secondary);
    border: 1px solid var(--border-strong);
    border-radius: 4px;
    padding: 0.05rem 0.3rem;
    white-space: nowrap;
  }

  /* ── Agenda ── */
  .agenda-day + .agenda-day { margin-top: 1.1rem; }
  .agenda-date {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    font-size: 0.78rem;
    font-weight: 700;
    color: var(--text-secondary);
    margin: 0 0 0.45rem;
    padding-bottom: 0.3rem;
    border-bottom: 1px solid var(--border);
  }
  .agenda-date.today { color: var(--accent-text); }
  .rel {
    font-size: 0.62rem;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--accent-text);
    background: var(--accent-bg);
    border-radius: 10px;
    padding: 0.1rem 0.45rem;
  }
  .agenda ul { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 0.35rem; }
  .agenda-entry {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    width: 100%;
    text-align: left;
    background: rgba(255,255,255,0.025);
    border: 1px solid var(--border);
    border-left: 3px solid var(--st);
    border-radius: 8px;
    padding: 0.55rem 0.75rem;
    cursor: pointer;
    color: var(--text-primary);
    font: inherit;
  }
  .agenda-entry:hover { background: var(--bg-hover); }
  .a-when { flex: 0 0 4.5rem; font-size: 0.74rem; color: var(--text-secondary); font-variant-numeric: tabular-nums; }
  .a-main { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 1px; }
  .a-title, .a-sub { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .a-title { font-size: 0.86rem; font-weight: 600; }
  .a-sub { font-size: 0.75rem; color: var(--text-secondary); }
  .a-meta { display: flex; align-items: center; gap: 0.6rem; flex-shrink: 0; font-size: 0.72rem; color: var(--text-muted); }
  .a-status { color: var(--st); font-weight: 600; }

  /* ── Detail panel ── */
  .modal-overlay {
    position: fixed; inset: 0; background: var(--shadow);
    display: flex; align-items: center; justify-content: center; z-index: 1000;
    padding: 1rem;
  }
  .modal {
    position: relative;
    background: var(--bg-elevated);
    border: 1px solid var(--border);
    border-top: 3px solid var(--st);
    border-radius: 12px;
    padding: 1.4rem;
    max-width: 560px; width: 100%;
    max-height: 85vh;
    overflow-y: auto;
    box-shadow: 0 20px 60px var(--shadow);
  }
  .close-btn {
    position: absolute; top: 0.6rem; right: 0.7rem;
    background: none; border: none; color: var(--text-muted);
    font-size: 1.3rem; line-height: 1; cursor: pointer; padding: 0.1rem 0.35rem; border-radius: 6px;
  }
  .close-btn:hover { color: var(--text-primary); background: var(--bg-hover); }
  .detail-body { display: flex; gap: 1.1rem; }
  .detail-poster { width: 120px; flex-shrink: 0; aspect-ratio: 2/3; object-fit: cover; border-radius: 8px; background: var(--bg-hover); align-self: flex-start; }
  .detail-info { flex: 1; min-width: 0; }
  .detail-title { font-size: 1.05rem; font-weight: 700; color: var(--text-primary); margin: 0 1.5rem 0.2rem 0; }
  .detail-year { color: var(--text-muted); font-weight: 400; }
  .detail-sub { font-size: 0.85rem; color: var(--text-secondary); margin: 0 0 0.5rem; }
  .detail-pills { display: flex; flex-wrap: wrap; gap: 0.4rem; align-items: center; margin: 0.4rem 0 0.8rem; }
  .status-pill {
    font-size: 0.65rem; font-weight: 600; padding: 0.18rem 0.55rem; border-radius: 10px;
    text-transform: uppercase; letter-spacing: 0.05em;
    background: var(--st-bg); color: var(--st);
  }
  .facts { display: grid; grid-template-columns: auto 1fr; gap: 0.3rem 0.9rem; font-size: 0.8rem; margin: 0 0 0.8rem; }
  .facts dt { color: var(--text-muted); }
  .facts dd { color: var(--text-primary); margin: 0; }
  .overview { font-size: 0.8rem; line-height: 1.5; color: var(--text-secondary); margin: 0 0 1rem; }

  @media (max-width: 700px) {
    .toolbar { grid-template-columns: 1fr auto; }
    .range-title { grid-column: 1 / -1; grid-row: 1; }
    .segmented { grid-column: 2; }
    .a-when { flex-basis: 3.8rem; }
    .a-network { display: none; }
  }
  @media (max-width: 480px) {
    .detail-body { flex-direction: column; }
    .detail-poster { width: 96px; }
  }
</style>
