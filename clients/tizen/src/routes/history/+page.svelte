<script lang="ts">
  // Watch history — chronological list of recent watch_events for
  // the signed-in user. Same grid layout as Favorites but cards
  // include a relative timestamp so the user can tell "yesterday"
  // from "last month" at a glance.

  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { endpoints, Unauthorized, type HistoryItem } from '$lib/api';
  import { focusManager } from '$lib/focus/manager';
  import PosterCard from '$lib/components/PosterCard.svelte';
  import Spinner from '$lib/components/Spinner.svelte';
  import { openItem } from '$lib/nav';

  let items = $state<HistoryItem[] | null>(null);
  let error = $state('');

  // Coarse-grained "5m ago" / "yesterday" formatter — TV cards have
  // limited room and the user mostly cares about ordering, not exact
  // timestamps. Same buckets the web client uses.
  function relTime(iso: string): string {
    const t = Date.parse(iso);
    if (Number.isNaN(t)) return '';
    const diffSec = Math.max(0, Math.floor((Date.now() - t) / 1000));
    if (diffSec < 60) return 'just now';
    if (diffSec < 3600) return `${Math.floor(diffSec / 60)}m ago`;
    if (diffSec < 86400) return `${Math.floor(diffSec / 3600)}h ago`;
    if (diffSec < 86400 * 7) return `${Math.floor(diffSec / 86400)}d ago`;
    return new Date(t).toLocaleDateString();
  }

  onMount(() => {
    (async () => {
      try {
        items = await endpoints.history.list();
      } catch (e) {
        if (e instanceof Unauthorized) goto('/login');
        else error = (e as Error).message;
      }
    })();

    return focusManager.pushBack(() => {
      goto('/hub');
      return true;
    });
  });
</script>

<div class="page">
  <h1>History</h1>

  {#if error}
    <p class="empty">{error}</p>
  {:else if !items}
    <Spinner />
  {:else if items.length === 0}
    <p class="empty">Nothing watched yet.</p>
  {:else}
    <div class="grid">
      {#each items as it, i (it.id)}
        <PosterCard
          title={it.title}
          posterPath={it.thumb_path}
          subtitle={relTime(it.occurred_at)}
          autofocus={i === 0}
          onclick={() => openItem(it.media_id, it.type)}
        />
      {/each}
    </div>
  {/if}
</div>

<style>
  .page {
    padding: var(--page-pad);
    display: flex;
    flex-direction: column;
    gap: 24px;
  }
  h1 { font-size: var(--font-2xl); margin: 0; }
  .empty { color: var(--text-secondary); font-size: var(--font-md); }
  .grid {
    display: grid;
    grid-template-columns: repeat(6, 1fr);
    gap: var(--card-gap);
  }
</style>
