<script lang="ts">
  // Watch-state overlay for a poster card: a checkmark when watched, an
  // unwatched-episode count on shows, or a progress bar along the bottom
  // edge when part-way through. Renders nothing when the item carries no
  // watch fields, so it's safe to drop into any card. The parent must be
  // position: relative (the poster box).
  import type { MediaItem } from '$lib/api';
  import { cardWatchBadge } from '$lib/watchState';

  export let item: Pick<MediaItem, 'watch_state' | 'view_offset_ms' | 'duration_ms' | 'leaf_count' | 'unwatched_count'>;

  $: badge = cardWatchBadge(item);
</script>

{#if badge}
  {#if badge.watched}
    <span class="wb-check" role="img" aria-label={badge.label} title={badge.label}>
      <svg viewBox="0 0 16 16" fill="currentColor" width="11" height="11" aria-hidden="true">
        <path d="M13.78 4.22a.75.75 0 010 1.06l-7.25 7.25a.75.75 0 01-1.06 0L2.22 9.28a.75.75 0 011.06-1.06L6 10.94l6.72-6.72a.75.75 0 011.06 0z"/>
      </svg>
    </span>
  {:else if badge.unwatchedCount != null}
    <span class="wb-count" role="img" aria-label={badge.label} title={badge.label}>{badge.unwatchedCount}</span>
  {:else if badge.progressPct != null}
    <div
      class="wb-progress"
      role="progressbar"
      aria-label={badge.label}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={Math.round(badge.progressPct)}
    >
      <div class="wb-progress-fill" style="width:{badge.progressPct}%"></div>
    </div>
  {/if}
{/if}

<style>
  .wb-check,
  .wb-count {
    position: absolute;
    top: 0.4rem;
    left: 0.4rem;
    z-index: 1;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    min-width: 20px;
    height: 20px;
    border-radius: 999px;
    background: var(--accent);
    color: #fff;
    box-shadow: 0 1px 4px rgba(0, 0, 0, 0.45);
    pointer-events: none;
  }
  .wb-count {
    padding: 0 0.35rem;
    font-size: 0.66rem;
    font-weight: 700;
    line-height: 1;
  }
  .wb-progress {
    position: absolute;
    left: 0;
    right: 0;
    bottom: 0;
    z-index: 1;
    height: 4px;
    background: rgba(0, 0, 0, 0.55);
    pointer-events: none;
  }
  .wb-progress-fill {
    height: 100%;
    background: var(--accent);
  }
</style>
