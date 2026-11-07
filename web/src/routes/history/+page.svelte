<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { historyApi, assetUrl, type WatchHistoryItem } from '$lib/api';
  import { t, i18n, type MessageKey, type Translator } from '$lib/i18n/index.svelte';

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
      error = e instanceof Error ? e.message : t('history.loadFailed');
    } finally { loading = false; }
  }

  async function loadMore() {
    loadingMore = true;
    try {
      const res = await historyApi.list(PAGE_SIZE, items.length);
      items = [...items, ...res.items];
      hasMore = res.items.length >= PAGE_SIZE;
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : t('history.loadMoreFailed');
    } finally { loadingMore = false; }
  }

  // The translator is passed in so the template re-renders on a language change.
  function relativeTime(iso: string, tx: Translator): string {
    const now = Date.now();
    const then = new Date(iso).getTime();
    const diff = now - then;
    const mins = Math.floor(diff / 60000);
    if (mins < 1) return tx('browse.justNow');
    if (mins < 60) return tx('browse.minutesAgo', { count: mins });
    const hrs = Math.floor(mins / 60);
    if (hrs < 24) return tx('browse.hoursAgo', { count: hrs });
    const days = Math.floor(hrs / 24);
    if (days < 30) return tx('browse.daysAgo', { count: days });
    const months = Math.floor(days / 30);
    if (months < 12) return tx('browse.monthsAgo', { count: months });
    return tx('browse.yearsAgo', { count: Math.floor(months / 12) });
  }

  function formatDuration(ms: number | undefined, tx: Translator): string {
    if (!ms) return '--';
    const totalMin = Math.floor(ms / 60000);
    const hrs = Math.floor(totalMin / 60);
    const mins = totalMin % 60;
    if (hrs > 0) return tx('browse.durationHm', { hours: hrs, minutes: mins });
    return tx('browse.durationM', { minutes: mins });
  }

  const typeBadge: Record<string, { labelKey: MessageKey; color: string }> = {
    movie: { labelKey: 'history.typeMovie', color: '#60a5fa' },
    show:  { labelKey: 'history.typeShow',  color: '#a78bfa' },
    episode: { labelKey: 'history.typeEpisode', color: '#a78bfa' },
    music: { labelKey: 'history.typeMusic', color: '#34d399' },
    photo: { labelKey: 'history.typePhoto', color: '#fb923c' },
  };
</script>

<svelte:head><title>{$i18n('history.pageTitle')}</title></svelte:head>

{#if ready}
<div class="page">
  <h1 class="page-title">{$i18n('history.title')}</h1>

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
      <p class="empty-title">{$i18n('history.empty')}</p>
      <p class="empty-sub">{$i18n('history.emptyHint')}</p>
    </div>
  {:else}
    <div class="history-list">
      {#each items as item (item.id)}
        {@const badge = typeBadge[item.type]}
        {@const badgeColor = badge?.color ?? '#888'}
        <a class="history-row" href="/watch/{item.media_id}">
          <div class="thumb-cell">
            {#if item.thumb_path}
              <img src={assetUrl(`/artwork/${encodeURI(item.thumb_path)}?w=150`)} alt={item.title} loading="lazy" />
            {:else}
              <div class="thumb-blank">
                <span>{item.title[0]?.toUpperCase() ?? '?'}</span>
              </div>
            {/if}
          </div>

          <div class="info-cell">
            <div class="info-title">{item.title}</div>
            <div class="info-meta">
              <span class="type-badge" style="color:{badgeColor}; border-color:{badgeColor}">{badge ? $i18n(badge.labelKey) : item.type}</span>
              {#if item.year}<span class="meta-year">{item.year}</span>{/if}
            </div>
          </div>

          <div class="detail-cell client">
            {#if item.client_name}
              <span class="detail-label">{$i18n('history.client')}</span>
              <span class="detail-value">{item.client_name}</span>
            {/if}
          </div>

          <div class="detail-cell duration">
            <span class="detail-label">{$i18n('history.duration')}</span>
            <span class="detail-value">{formatDuration(item.duration_ms, $i18n)}</span>
          </div>

          <div class="detail-cell time">
            <span class="detail-label">{$i18n('history.watched')}</span>
            <span class="detail-value">{relativeTime(item.occurred_at, $i18n)}</span>
          </div>
        </a>
      {/each}
    </div>

    {#if hasMore}
      <div class="load-more-wrap">
        <button class="btn-load-more" disabled={loadingMore} on:click={loadMore}>
          {loadingMore ? $i18n('browse.loading') : $i18n('browse.loadMore')}
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
    color: var(--text-primary);
    letter-spacing: -0.02em;
    margin-bottom: 1.5rem;
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

  /* ── Skeleton ────────────────────────────────────────────────────────────── */
  .skeleton-list { display: flex; flex-direction: column; gap: 1px; }
  .skeleton-row {
    height: 64px;
    background: linear-gradient(90deg, var(--bg-elevated) 25%, #16161f 50%, var(--bg-elevated) 75%);
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
  .empty-icon { color: var(--text-muted); margin-bottom: 0.75rem; }
  .empty-title { font-size: 1rem; font-weight: 600; color: var(--text-muted); }
  .empty-sub { font-size: 0.82rem; color: var(--text-muted); }

  /* ── History list ────────────────────────────────────────────────────────── */
  .history-list {
    border: 1px solid var(--border);
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
    border-bottom: 1px solid var(--bg-hover);
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
    background: linear-gradient(135deg, var(--bg-secondary), var(--bg-primary));
    border-radius: 4px;
    font-size: 0.7rem;
    font-weight: 700;
    color: var(--border-strong);
  }

  /* Info */
  .info-cell { flex: 1; min-width: 0; }
  .info-title {
    font-size: 0.82rem;
    font-weight: 600;
    color: var(--text-primary);
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
    color: var(--text-muted);
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
    color: var(--text-muted);
  }
  .detail-value {
    font-size: 0.76rem;
    color: var(--text-secondary);
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
    background: var(--bg-hover);
    border: 1px solid var(--border-strong);
    border-radius: 7px;
    color: var(--text-secondary);
    font-size: 0.8rem;
    font-weight: 600;
    padding: 0.5rem 1.5rem;
    cursor: pointer;
    transition: background 0.12s, color 0.12s;
  }
  .btn-load-more:hover { background: var(--border-strong); color: var(--text-secondary); }
  .btn-load-more:disabled { opacity: 0.5; cursor: not-allowed; }
</style>
