<script lang="ts">
  import { focusable } from '$lib/focus/focusable';
  import { onDestroy } from 'svelte';
  import { api } from '$lib/api';

  interface Props {
    title: string;
    posterPath?: string;
    /** An API path for the image instead of an /artwork poster (a
     *  collection's uploaded cover); wins over posterPath. */
    posterSrc?: string;
    subtitle?: string;
    progressRatio?: number;
    /** Fully watched: a check mark in the corner. */
    watched?: boolean;
    /** Shows / seasons with episodes left: a count pill in the corner. */
    unwatchedCount?: number | null;
    onclick?: () => void;
    /** Holding OK opens this card's options (short press still clicks). */
    onlongpress?: () => void;
    /** The D-pad focus landed on this card (paged grids load the next page
     *  as focus nears the end). */
    onfocus?: () => void;
    autofocus?: boolean;
    /** Stable identity on the page (data-focus-key): Back to the page puts
     *  focus on this card again (lib/focus/memory). Unique per page. */
    focusKey?: string;
  }
  let {
    title,
    posterPath,
    posterSrc,
    subtitle,
    progressRatio,
    watched = false,
    unwatchedCount = null,
    onclick,
    onlongpress,
    onfocus,
    autofocus,
    focusKey,
  }: Props = $props();

  // api.assetUrl handles origin + `?token=<paseto>` for the
  // RequiredAllowQueryToken-protected /artwork/ route. The naive
  // `${origin}/artwork/...?w=400` URL omits auth and 401s — `<img>`
  // can't attach an Authorization header.
  // A poster that fails to load (a 404 for a missing file, or a transient
  // failure: a token renewed a moment late, a Wi-Fi blip) showed as an empty
  // tile. It gets the no-poster tile instead, and one more try after
  // RETRY_MS: the URL is built again then (assetUrl reads the token from
  // storage, which Svelte doesn't track, so `retry` is what rebuilds it),
  // picking up a renewed token. A second failure keeps the tile.
  const RETRY_MS = 15_000;
  let retry = $state(0);
  const posterUrl = $derived.by(() => {
    void retry;
    if (posterSrc) return api.assetUrl(posterSrc);
    return posterPath ? api.assetUrl(`/artwork/${posterPath}?w=400`) : '';
  });
  let failed = $state(false);
  let retryTimer: ReturnType<typeof setTimeout> | null = null;
  function onPosterError() {
    failed = true;
    if (retry > 0 || retryTimer) return;
    retryTimer = setTimeout(() => {
      retryTimer = null;
      failed = false;
      retry++;
    }, RETRY_MS);
  }
  onDestroy(() => {
    if (retryTimer) clearTimeout(retryTimer);
  });
</script>

<button
  use:focusable={{ autofocus, onLongPress: onlongpress, onFocus: onfocus }}
  class="card"
  data-focus-key={focusKey}
  {onclick}
>
  <div class="art">
    {#if posterUrl && !failed}
      <img src={posterUrl} alt="" loading="lazy" onerror={onPosterError} />
    {:else}
      <div class="no-poster">{title.slice(0, 2).toUpperCase()}</div>
    {/if}
    {#if unwatchedCount != null && unwatchedCount > 0}
      <span class="badge count" aria-label="{unwatchedCount} unwatched">{unwatchedCount}</span>
    {:else if watched}
      <span class="badge check" aria-label="Watched">
        <svg viewBox="0 0 24 24" width="26" height="26" aria-hidden="true">
          <polyline points="5 12.5 10 17.5 19 7" fill="none" stroke="currentColor" stroke-width="3.2" stroke-linecap="round" stroke-linejoin="round" />
        </svg>
      </span>
    {/if}
  </div>
  {#if progressRatio !== undefined && progressRatio > 0}
    <div class="progress" style="width: {Math.min(100, progressRatio * 100)}%"></div>
  {/if}
  <div class="label">
    <div class="title">{title}</div>
    {#if subtitle}<div class="subtitle">{subtitle}</div>{/if}
  </div>
</button>

<style>
  .card {
    display: block;
    width: 240px;
    background: transparent;
    border: none;
    padding: 0;
    text-align: left;
    font-family: inherit;
    color: var(--text-primary);
    cursor: pointer;
    flex: 0 0 auto;
  }

  .art {
    position: relative;
  }

  img, .no-poster {
    width: 240px;
    height: 360px;
    border-radius: 12px;
    background: var(--bg-elevated);
    object-fit: cover;
    display: block;
  }

  .no-poster {
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: var(--font-xl);
    color: var(--text-muted);
  }

  /* Watch-state corner badge: a check for watched, a count pill for
     unwatched episodes. Dark outline keeps it legible on bright art. */
  .badge {
    position: absolute;
    top: 10px;
    right: 10px;
    display: flex;
    align-items: center;
    justify-content: center;
    box-shadow: 0 0 0 3px rgba(7, 7, 13, 0.75);
    color: #fff;
    background: var(--accent);
  }
  .badge.check {
    width: 40px;
    height: 40px;
    border-radius: 50%;
  }
  .badge.count {
    min-width: 40px;
    height: 40px;
    padding: 0 10px;
    border-radius: 20px;
    font-size: var(--font-xs);
    font-weight: 700;
    font-variant-numeric: tabular-nums;
  }

  .progress {
    margin-top: -6px;
    height: 4px;
    background: var(--accent);
    border-radius: 2px;
    position: relative;
    z-index: 1;
  }

  .label {
    padding: 12px 4px 0;
    /* Reserve fixed height for label area so every card lands at the
       same total height regardless of title-line-count and presence
       of a subtitle. Cards in a row used to drift in height because
       the title clamp expanded for 2-line titles and the subtitle
       only rendered when item.year was set; rows now align cleanly
       on the bottom edge. */
    height: calc(var(--font-sm) * 1.3 * 2 + var(--font-xs) * 1.3 + 4px);
    box-sizing: content-box;
  }

  .title {
    font-size: var(--font-sm);
    line-height: 1.3;
    overflow: hidden;
    display: -webkit-box;
    -webkit-line-clamp: 2;
    line-clamp: 2;
    -webkit-box-orient: vertical;
    /* Fixed two-line block — 1-line titles render in the upper line
       and the second line stays reserved-but-empty. Prevents the
       card from shrinking when the title fits on one line. */
    min-height: calc(var(--font-sm) * 1.3 * 2);
  }

  .subtitle {
    font-size: var(--font-xs);
    color: var(--text-secondary);
    margin-top: 4px;
    line-height: 1.3;
    /* Reserve the line even when no subtitle is rendered, so cards
       with item.year align with cards that lack it. One line only —
       Next Up's "S2 · E5 — Episode title" can run long. */
    min-height: calc(var(--font-xs) * 1.3);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
</style>
