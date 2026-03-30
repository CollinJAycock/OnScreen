<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { historyApi, type WatchHistoryItem } from '$lib/api';

  let items: WatchHistoryItem[] = [];
  let loading = true;
  let loadingMore = false;
  let error = '';
  let hasMore = true;
  let ready = false;
  const PAGE_SIZE = 50;

  onMount(async () => {
    if (!localStorage.getItem('onscreen_user')) { goto('/login'); return; }
    ready = true;
    await load();
  });

  async function load() {
    loading = true; error = '';
    try {
      const res = await historyApi.list(PAGE_SIZE, 0);
      items = res.items;
      hasMore = res.items.length >= PAGE_SIZE;
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed to load history';
    } finally { loading = false; }
  }

  async function loadMore() {
    loadingMore = true;
    try {
      const res = await historyApi.list(PAGE_SIZE, items.length);
      items = [...items, ...res.items];
      hasMore = res.items.length >= PAGE_SIZE;
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed to load more';
    } finally { loadingMore = false; }
  }

  function relativeTime(iso: string): string {
    const now = Date.now();
    const then = new Date(iso).getTime();
    const diff = now - then;
    const mins = Math.floor(diff / 60000);
    if (mins < 1) return 'just now';
    if (mins < 60) return `${mins}m ago`;
    const hrs = Math.floor(mins / 60);
    if (hrs < 24) return `${hrs}h ago`;
    const days = Math.floor(hrs / 24);
    if (days < 30) return `${days}d ago`;
    const months = Math.floor(days / 30);
    if (months < 12) return `${months}mo ago`;
    return `${Math.floor(months / 12)}y ago`;
  }

  function formatDuration(ms: number | undefined): string {
    if (!ms) return '--';
    const totalMin = Math.floor(ms / 60000);
    const hrs = Math.floor(totalMin / 60);
    const mins = totalMin % 60;
    if (hrs > 0) return `${hrs}h ${mins}m`;
    return `${mins}m`;
  }

  const typeBadge: Record<string, { label: string; color: string }> = {
    movie: { label: 'Movie', color: '#60a5fa' },
    show:  { label: 'Show',  color: '#a78bfa' },
    episode: { label: 'Episode', color: '#a78bfa' },
    music: { label: 'Music', color: '#34d399' },
    photo: { label: 'Photo', color: '#fb923c' },
  };
</script>

<svelte:head><title>Watch History - OnScreen</title></svelte:head>

{#if ready}
<div class="page">
  <h1 class="page-title">Watch History</h1>

  {#if error}
    <div class="banner-error">{error}</div>
  {/if}

  {#if loading}
    <div class="skeleton-list">
      {#each [1,2,3,4,5,6] as _}
        <div class="skeleton-row"></div>
      {/each}
    </div>
  {:else if items.length === 0}
    <div class="empty">
      <div class="empty-icon">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" width="48" height="48">
          <circle cx="12" cy="12" r="10"/>
          <polyline points="12 6 12 12 16 14"/>
        </svg>
      </div>
      <p class="empty-title">No watch history</p>
      <p class="empty-sub">Your playback history will appear here once you start watching.</p>
    </div>
  {:else}
    <div class="history-list">
      {#each items as item (item.id)}
        {@const badge = typeBadge[item.type] ?? { label: item.type, color: '#888' }}
        <a class="history-row" href="/watch/{item.media_id}">
          <div class="thumb-cell">
            {#if item.thumb_path}
              <img src="/artwork/{item.thumb_path}" alt={item.title} loading="lazy" />
            {:else}
              <div class="thumb-blank">
                <span>{item.title[0]?.toUpperCase() ?? '?'}</span>
              </div>
            {/if}
          </div>

          <div class="info-cell">
            <div class="info-title">{item.title}</div>
            <div class="info-meta">
              <span class="type-badge" style="color:{badge.color}; border-color:{badge.color}">{badge.label}</span>
              {#if item.year}<span class="meta-year">{item.year}</span>{/if}
            </div>
          </div>

          <div class="detail-cell client">
            {#if item.client_name}
              <span class="detail-label">Client</span>
              <span class="detail-value">{item.client_name}</span>
            {/if}
          </div>

          <div class="detail-cell duration">
            <span class="detail-label">Duration</span>
            <span class="detail-value">{formatDuration(item.duration_ms)}</span>
          </div>

          <div class="detail-cell time">
            <span class="detail-label">Watched</span>
            <span class="detail-value">{relativeTime(item.occurred_at)}</span>
          </div>
        </a>
      {/each}
    </div>

    {#if hasMore}
      <div class="load-more-wrap">
        <button class="btn-load-more" disabled={loadingMore} on:click={loadMore}>
          {loadingMore ? 'Loading...' : 'Load more'}
        </button>
      </div>
    {/if}
  {/if}
</div>
{/if}

<style>
  .page { padding: 2.5rem 2.5rem 4rem; max-width: 1200px; }

  .page-title {
    font-size: 1.1rem;
    font-weight: 700;
    color: #eeeef8;
    letter-spacing: -0.02em;
    margin-bottom: 1.5rem;
  }

  .banner-error {
    background: rgba(248,113,113,0.1);
    border: 1px solid rgba(248,113,113,0.2);
    color: #fca5a5;
    padding: 0.65rem 1rem;
    border-radius: 8px;
    font-size: 0.8rem;
    margin-bottom: 1.5rem;
  }

  /* ── Skeleton ────────────────────────────────────────────────────────────── */
  .skeleton-list { display: flex; flex-direction: column; gap: 1px; }
  .skeleton-row {
    height: 64px;
    background: linear-gradient(90deg, #111118 25%, #16161f 50%, #111118 75%);
    background-size: 200% 100%;
    animation: shimmer 1.4s infinite;
    border-radius: 8px;
  }
  @keyframes shimmer { 0% { background-position: 200% 0; } 100% { background-position: -200% 0; } }

  /* ── Empty state ─────────────────────────────────────────────────────────── */
  .empty {
    display: flex;
    flex-direction: column;
    align-items: center;
    text-align: center;
    padding: 6rem 2rem;
    gap: 0.5rem;
  }
  .empty-icon { color: #2a2a40; margin-bottom: 0.75rem; }
  .empty-title { font-size: 1rem; font-weight: 600; color: #555577; }
  .empty-sub { font-size: 0.82rem; color: #33333d; }

  /* ── History list ────────────────────────────────────────────────────────── */
  .history-list {
    border: 1px solid rgba(255,255,255,0.055);
    border-radius: 12px;
    overflow: hidden;
    background: rgba(255,255,255,0.015);
  }

  .history-row {
    display: flex;
    align-items: center;
    gap: 1rem;
    padding: 0.7rem 1rem;
    text-decoration: none;
    color: inherit;
    transition: background 0.12s;
    border-bottom: 1px solid rgba(255,255,255,0.04);
  }
  .history-row:last-child { border-bottom: none; }
  .history-row:hover { background: rgba(255,255,255,0.035); }

  /* Thumbnail */
  .thumb-cell { flex: 0 0 48px; }
  .thumb-cell img {
    width: 48px;
    height: 32px;
    object-fit: cover;
    border-radius: 4px;
    display: block;
  }
  .thumb-blank {
    width: 48px;
    height: 32px;
    display: flex;
    align-items: center;
    justify-content: center;
    background: linear-gradient(135deg, #1a1a2e, #0f0f18);
    border-radius: 4px;
    font-size: 0.7rem;
    font-weight: 700;
    color: rgba(255,255,255,0.12);
  }

  /* Info */
  .info-cell { flex: 1; min-width: 0; }
  .info-title {
    font-size: 0.82rem;
    font-weight: 600;
    color: #cccce0;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .info-meta {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    margin-top: 0.15rem;
  }
  .type-badge {
    font-size: 0.6rem;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    padding: 0.1rem 0.35rem;
    border: 1px solid;
    border-radius: 3px;
    opacity: 0.8;
  }
  .meta-year {
    font-size: 0.7rem;
    color: rgba(255,255,255,0.3);
  }

  /* Detail columns */
  .detail-cell {
    display: flex;
    flex-direction: column;
    align-items: flex-end;
    flex: 0 0 auto;
    min-width: 70px;
  }
  .detail-label {
    font-size: 0.58rem;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: rgba(255,255,255,0.2);
  }
  .detail-value {
    font-size: 0.76rem;
    color: #8888aa;
    white-space: nowrap;
  }

  /* Hide client column on narrow screens */
  @media (max-width: 768px) {
    .page { padding: 1.25rem 1rem 5rem; }

    .client { display: none; }
    .duration { display: none; }

    .history-row { gap: 0.65rem; padding: 0.6rem 0.7rem; }

    .info-title { font-size: 0.78rem; }
    .detail-cell { min-width: 55px; }
    .detail-value { font-size: 0.7rem; }
  }

  /* ── Load more ───────────────────────────────────────────────────────────── */
  .load-more-wrap {
    display: flex;
    justify-content: center;
    padding: 1.5rem 0;
  }
  .btn-load-more {
    background: rgba(255,255,255,0.05);
    border: 1px solid rgba(255,255,255,0.09);
    border-radius: 7px;
    color: #8888aa;
    font-size: 0.8rem;
    font-weight: 600;
    padding: 0.5rem 1.5rem;
    cursor: pointer;
    transition: background 0.12s, color 0.12s;
  }
  .btn-load-more:hover { background: rgba(255,255,255,0.09); color: #aaaacc; }
  .btn-load-more:disabled { opacity: 0.5; cursor: not-allowed; }
</style>
