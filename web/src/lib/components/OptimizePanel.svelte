<script lang="ts">
  // A title's "Optimize…" action (admin) and, for a playable item, its
  // optimized versions with progress and delete. A show or season queues
  // every episode under it; their versions are listed on each episode and on
  // Settings ▸ Library ▸ Optimized.
  import OptimizeDialog from './OptimizeDialog.svelte';
  import OptimizedVersionsList from './OptimizedVersionsList.svelte';

  interface Props {
    itemId: string;
    itemType: string;
    title?: string;
  }

  let { itemId, itemType, title = '' }: Props = $props();

  let open = $state(false);
  let refreshKey = $state(0);
  const playable = $derived(['movie', 'episode', 'home_video', 'music_video'].includes(itemType));
  const what = $derived(
    itemType === 'show' ? 'every episode of this show'
      : itemType === 'season' ? 'every episode of this season'
        : title || 'this title',
  );
</script>

<div class="optimize-panel">
  <button class="opt-btn" onclick={() => (open = true)}
          title="Make a smaller copy ahead of time that devices play without a live transcode">
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="14" height="14" aria-hidden="true">
      <polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2"/>
    </svg>
    Optimize…
  </button>
  {#if playable}
    <div class="versions">
      <OptimizedVersionsList {itemId} {refreshKey} hideWhenEmpty />
    </div>
  {/if}
</div>

<OptimizeDialog
  {open}
  selection={{ item_ids: [itemId] }}
  {what}
  onclose={() => (open = false)}
  ondone={() => (refreshKey += 1)}
/>

<style>
  .optimize-panel { display: flex; flex-direction: column; gap: 0.5rem; margin-bottom: 1.5rem; max-width: 560px; }
  .opt-btn {
    align-self: flex-start;
    display: inline-flex; align-items: center; gap: 0.35rem;
    background: var(--input-bg);
    border: 1px solid var(--border-strong);
    border-radius: 6px;
    color: var(--text-muted); font-size: 0.75rem; font-weight: 500;
    cursor: pointer; padding: 0.35rem 0.7rem;
    transition: all 0.12s;
  }
  .opt-btn:hover { color: var(--text-secondary); background: var(--bg-hover); }
</style>
