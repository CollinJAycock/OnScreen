<script lang="ts">
  // Watchlist: the caller's watching statuses (Plan to Watch, Watching, On
  // Hold, Completed, Dropped) as a poster grid, one tab per status. The
  // server scopes the list to what the caller may see (libraries, rating
  // ceiling, labels); statuses are set from an item's page and changed or
  // removed here through the same per-item endpoints.
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import {
    watchlistApi,
    itemApi,
    assetUrl,
    type WatchlistCounts,
    type WatchlistItem,
    type WatchlistSort,
    type WatchStatusValue,
  } from '$lib/api';
  import { toast } from '$lib/stores/toast';
  import { itemHref } from '$lib/itemHref';
  import CardMenu from '$lib/components/CardMenu.svelte';
  import WatchBadge from '$lib/components/WatchBadge.svelte';
  import {
    STATUS_LABELS,
    WATCHLIST_STATUSES,
    applyStatusChange,
    cardSubtitle,
    parseStatus,
    tabCount,
    typeOptions,
  } from '$lib/watchlist';

  const PAGE_SIZE = 60;

  let ready = $state(false);
  let status = $state<WatchStatusValue>('plan_to_watch');
  let typeFilter = $state('');
  let sort = $state<WatchlistSort>('recent');

  let items = $state<WatchlistItem[]>([]);
  let total = $state(0);
  let counts = $state<WatchlistCounts | null>(null);
  let loading = $state(true);
  let loadingMore = $state(false);
  let error = $state('');
  let busyId = $state('');

  // Bumped on every fresh load so a slow response for a tab the user has
  // already left can't overwrite the current one.
  let loadSeq = 0;

  const hasMore = $derived(items.length < total);
  const types = $derived(typeOptions(counts, status));

  function errText(e: unknown, fallback: string): string {
    return e instanceof Error && e.message ? e.message : fallback;
  }

  async function loadCounts() {
    try {
      counts = await watchlistApi.counts();
    } catch {
      // Badges and the type filter are optional; the list still works.
      counts = null;
    }
  }

  async function load() {
    const seq = ++loadSeq;
    loading = true;
    error = '';
    try {
      const res = await watchlistApi.list({ status, type: typeFilter || undefined, sort, limit: PAGE_SIZE, offset: 0 });
      if (seq !== loadSeq) return;
      items = res.items;
      total = res.total;
    } catch (e) {
      if (seq !== loadSeq) return;
      items = [];
      total = 0;
      error = errText(e, 'Could not load your watchlist');
    } finally {
      if (seq === loadSeq) loading = false;
    }
  }

  async function loadMore() {
    if (loadingMore || loading || !hasMore) return;
    const seq = loadSeq;
    loadingMore = true;
    try {
      const res = await watchlistApi.list({
        status, type: typeFilter || undefined, sort, limit: PAGE_SIZE, offset: items.length,
      });
      if (seq !== loadSeq) return;
      const seen = new Set(items.map((it) => it.id));
      items = [...items, ...res.items.filter((it) => !seen.has(it.id))];
      total = res.total;
      // A short page means the list shrank under us; stop asking.
      if (res.items.length === 0) total = items.length;
    } catch (e) {
      if (seq === loadSeq) error = errText(e, 'Could not load more');
    } finally {
      loadingMore = false;
    }
  }

  function syncUrl() {
    const p = new URLSearchParams();
    if (status !== 'plan_to_watch') p.set('status', status);
    if (typeFilter) p.set('type', typeFilter);
    if (sort !== 'recent') p.set('sort', sort);
    const qs = p.toString();
    goto(`/watchlist${qs ? `?${qs}` : ''}`, { replaceState: true, keepFocus: true, noScroll: true });
  }

  function selectStatus(next: WatchStatusValue) {
    if (next === status) return;
    status = next;
    // A type the new tab doesn't have would show an empty grid.
    if (typeFilter && !(counts?.statuses?.[next]?.types?.[typeFilter])) typeFilter = '';
    syncUrl();
    load();
  }

  function selectType(next: string) {
    typeFilter = next;
    syncUrl();
    load();
  }

  function selectSort(next: WatchlistSort) {
    sort = next;
    syncUrl();
    load();
  }

  async function changeStatus(item: WatchlistItem, next: WatchStatusValue | null) {
    if (busyId) return;
    busyId = item.id;
    try {
      if (next) await itemApi.setWatchStatus(item.id, next);
      else await itemApi.clearWatchStatus(item.id);
      const r = applyStatusChange(items, counts, item, next);
      items = r.items;
      counts = r.counts;
      if (r.removed) total = Math.max(0, total - 1);
      toast.success(next ? `Moved "${item.title}" to ${STATUS_LABELS[next]}` : `Removed "${item.title}" from your list`);
      // Pull the next page forward if removing cards emptied the screen.
      if (items.length === 0 && hasMore) load();
    } catch (e) {
      toast.error(errText(e, 'Could not update the status'));
    } finally {
      busyId = '';
    }
  }

  function cardActions(item: WatchlistItem) {
    return [
      ...WATCHLIST_STATUSES.filter((s) => s !== item.status).map((s) => ({
        label: `Move to ${STATUS_LABELS[s]}`,
        onSelect: () => changeStatus(item, s),
      })),
      { label: 'Remove from list', onSelect: () => changeStatus(item, null) },
    ];
  }

  function posterSrc(path: string, w: number): string {
    return assetUrl(`/artwork/${encodeURI(path)}?w=${w}`);
  }

  // Infinite scroll: fetch the next page as the sentinel nears the bottom of
  // the scroll container (<main>, not the viewport — the shell doesn't scroll).
  function infiniteScroll(node: HTMLElement) {
    if (typeof IntersectionObserver === 'undefined') return;
    const obs = new IntersectionObserver(
      async (entries) => {
        if (!entries[0]?.isIntersecting) return;
        await loadMore();
        // Still in view after the page grew (a tall screen): re-observe so
        // the observer fires again instead of waiting for a scroll.
        if (hasMore) {
          obs.unobserve(node);
          obs.observe(node);
        }
      },
      { root: node.closest('main'), rootMargin: '600px' },
    );
    obs.observe(node);
    return { destroy() { obs.disconnect(); } };
  }

  onMount(() => {
    if (!localStorage.getItem('onscreen_user')) { goto('/login'); return; }
    const q = new URLSearchParams(window.location.search);
    status = parseStatus(q.get('status'));
    typeFilter = q.get('type') ?? '';
    sort = q.get('sort') === 'title' ? 'title' : 'recent';
    ready = true;
    loadCounts();
    load();
  });
</script>

<svelte:head><title>Watchlist - OnScreen</title></svelte:head>

{#if ready}
<div class="page">
  <div class="head">
    <h1 class="page-title">Watchlist</h1>
    <div class="controls">
      {#if types.length > 1 || typeFilter}
        <label class="control">
          <span class="sr-only">Type</span>
          <select value={typeFilter} onchange={(e) => selectType(e.currentTarget.value)} aria-label="Filter by type">
            <option value="">All types</option>
            {#each types as t (t.type)}
              <option value={t.type}>{t.label} ({t.count})</option>
            {/each}
            {#if typeFilter && !types.some((t) => t.type === typeFilter)}
              <option value={typeFilter}>{typeFilter}</option>
            {/if}
          </select>
        </label>
      {/if}
      <label class="control">
        <span class="sr-only">Sort</span>
        <select value={sort} onchange={(e) => selectSort(e.currentTarget.value as WatchlistSort)} aria-label="Sort">
          <option value="recent">Recently added</option>
          <option value="title">Title</option>
        </select>
      </label>
    </div>
  </div>

  <div class="tabs" role="tablist" aria-label="Status">
    {#each WATCHLIST_STATUSES as s (s)}
      {@const n = tabCount(counts, s)}
      <button type="button" role="tab" aria-selected={status === s} class:on={status === s} onclick={() => selectStatus(s)}>
        {STATUS_LABELS[s]}{#if n != null}<span class="tab-count">{n}</span>{/if}
      </button>
    {/each}
  </div>

  {#if error}
    <div class="banner-error">{error}</div>
  {/if}

  {#if loading}
    <div class="grid" aria-busy="true">
      {#each [1, 2, 3, 4, 5, 6, 7, 8, 9, 10] as _}
        <div class="skeleton-tile"></div>
      {/each}
    </div>
  {:else if items.length === 0 && !error}
    <div class="empty">
      <div class="empty-icon">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" width="48" height="48" aria-hidden="true">
          <path d="M17.593 3.322c1.1.128 1.907 1.077 1.907 2.185V21L12 17.25 4.5 21V5.507c0-1.108.806-2.057 1.907-2.185a48.507 48.507 0 0111.186 0z"/>
        </svg>
      </div>
      {#if typeFilter}
        <p class="empty-title">Nothing of this type in {STATUS_LABELS[status]}</p>
        <button type="button" class="btn-link" onclick={() => selectType('')}>Show all types</button>
      {:else}
        <p class="empty-title">Nothing in {STATUS_LABELS[status]} yet</p>
        <p class="empty-sub">
          Open a movie or show and pick a status from the <strong>Status</strong> menu on its page —
          "Plan to Watch" lands it here, and you can move it between lists from each card's ⋯ menu.
        </p>
      {/if}
    </div>
  {:else}
    <p class="summary">{total} {total === 1 ? 'item' : 'items'}</p>
    <div class="grid">
      {#each items as it (it.id)}
        {@const sub = cardSubtitle(it)}
        <div class="tile" class:busy={busyId === it.id}>
          <a class="tile-link" href={itemHref(it.type, it.id)}>
            <div class="poster">
              {#if it.poster_path}
                <img
                  src={posterSrc(it.poster_path, 300)}
                  srcset="{posterSrc(it.poster_path, 150)} 150w, {posterSrc(it.poster_path, 300)} 300w, {posterSrc(it.poster_path, 600)} 600w"
                  sizes="(max-width: 768px) 150px, 200px"
                  alt=""
                  loading="lazy"
                />
              {:else}
                <div class="poster-blank" aria-hidden="true">{it.title[0]?.toUpperCase() ?? '?'}</div>
              {/if}
              <WatchBadge item={it} />
            </div>
            <div class="tile-title">{it.title}</div>
            {#if sub}<div class="tile-sub">{sub}</div>{/if}
          </a>
          <div class="tile-menu">
            <CardMenu label="More actions for {it.title}" actions={cardActions(it)} />
          </div>
        </div>
      {/each}
    </div>

    {#if hasMore}
      <div class="load-more-wrap" use:infiniteScroll>
        <button class="btn-load-more" disabled={loadingMore} onclick={loadMore}>
          {loadingMore ? 'Loading...' : 'Load more'}
        </button>
      </div>
    {/if}
  {/if}
</div>
{/if}

<style>
  .page { padding: 2.5rem 2.5rem 4rem; max-width: 1400px; }
  .head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 1rem;
    flex-wrap: wrap;
    margin-bottom: 1rem;
  }
  .page-title {
    font-size: 1.1rem;
    font-weight: 700;
    color: var(--text-primary);
    letter-spacing: -0.02em;
  }
  .controls { display: flex; gap: 0.5rem; flex-wrap: wrap; }
  .control select {
    background: var(--input-bg);
    color: var(--text-primary);
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 0.35rem 0.55rem;
    font-size: 0.8rem;
  }
  .sr-only {
    position: absolute; width: 1px; height: 1px; padding: 0; margin: -1px;
    overflow: hidden; clip: rect(0, 0, 0, 0); white-space: nowrap; border: 0;
  }

  .tabs {
    display: flex;
    gap: 0.35rem;
    margin: 0 0 1.25rem;
    border-bottom: 1px solid var(--border);
    overflow-x: auto;
  }
  .tabs button {
    background: none;
    border: none;
    border-bottom: 2px solid transparent;
    margin-bottom: -1px;
    padding: 0.45rem 0.8rem;
    font-size: 0.82rem;
    font-weight: 600;
    color: var(--text-muted);
    cursor: pointer;
    white-space: nowrap;
  }
  .tabs button:hover { color: var(--text-secondary); }
  .tabs button.on { color: var(--text-primary); border-bottom-color: var(--accent); }
  .tab-count {
    margin-left: 0.4rem;
    font-size: 0.7rem;
    font-weight: 600;
    color: var(--text-muted);
    background: var(--bg-elevated);
    border-radius: 999px;
    padding: 0.05rem 0.4rem;
  }

  .banner-error {
    background: var(--error-bg);
    border: 1px solid var(--error);
    color: var(--error);
    padding: 0.65rem 1rem;
    border-radius: 8px;
    font-size: 0.8rem;
    margin-bottom: 1.5rem;
  }
  .summary { font-size: 0.75rem; color: var(--text-muted); margin-bottom: 0.75rem; }

  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(160px, 1fr));
    gap: 1rem;
  }
  .tile { position: relative; display: flex; flex-direction: column; }
  .tile.busy { opacity: 0.5; pointer-events: none; }
  .tile-link {
    text-decoration: none;
    color: inherit;
    display: flex;
    flex-direction: column;
    gap: 0.4rem;
  }
  .poster {
    position: relative;
    aspect-ratio: 2 / 3;
    border-radius: 8px;
    overflow: hidden;
    background: var(--bg-elevated);
    transition: transform 0.15s;
  }
  .tile-link:hover .poster { transform: translateY(-2px); }
  .poster img { width: 100%; height: 100%; object-fit: cover; display: block; }
  .poster-blank {
    width: 100%;
    height: 100%;
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 2rem;
    color: var(--text-muted);
  }
  .tile-title {
    font-size: 0.82rem;
    font-weight: 500;
    color: var(--text-primary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    padding-right: 1.6rem;
  }
  .tile-sub {
    font-size: 0.72rem;
    color: var(--text-muted);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    padding-right: 1.6rem;
  }
  .tile-menu { position: absolute; right: 0; bottom: 0; }

  .skeleton-tile {
    aspect-ratio: 2 / 3;
    background: linear-gradient(90deg, var(--bg-elevated) 25%, var(--bg-hover) 50%, var(--bg-elevated) 75%);
    background-size: 200% 100%;
    animation: shimmer 1.4s infinite;
    border-radius: 8px;
  }
  @keyframes shimmer { 0% { background-position: 200% 0; } 100% { background-position: -200% 0; } }

  .empty {
    display: flex;
    flex-direction: column;
    align-items: center;
    text-align: center;
    padding: 6rem 2rem;
    gap: 0.5rem;
  }
  .empty-icon { color: var(--text-muted); margin-bottom: 0.75rem; }
  .empty-title { font-size: 1rem; font-weight: 600; color: var(--text-muted); }
  .empty-sub { font-size: 0.82rem; color: var(--text-muted); max-width: 32rem; line-height: 1.5; }
  .empty-sub strong { color: var(--text-secondary); }
  .btn-link {
    background: none;
    border: none;
    color: var(--accent);
    font-size: 0.82rem;
    cursor: pointer;
    padding: 0.25rem;
  }

  .load-more-wrap { display: flex; justify-content: center; margin-top: 1.5rem; }
  .btn-load-more {
    padding: 0.6rem 1.2rem;
    background: var(--bg-elevated);
    color: var(--text-primary);
    border: 1px solid var(--border);
    border-radius: 8px;
    font-size: 0.85rem;
    cursor: pointer;
    transition: background 0.12s;
  }
  .btn-load-more:hover:not(:disabled) { background: var(--bg-hover); }
  .btn-load-more:disabled { opacity: 0.5; cursor: not-allowed; }

  @media (max-width: 600px) {
    .page { padding: 1.25rem 1rem 3rem; }
    .grid { grid-template-columns: repeat(auto-fill, minmax(120px, 1fr)); gap: 0.75rem; }
  }
</style>
