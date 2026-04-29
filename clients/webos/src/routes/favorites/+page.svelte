<script lang="ts">
  // Favorites — items the user has heart-clicked from any detail
  // screen. Read-only grid; clicking a card routes through openItem
  // so photos / collections still go to their right destinations.

  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { endpoints, Unauthorized, type FavoriteItem } from '$lib/api';
  import { focusManager } from '$lib/focus/manager';
  import PosterCard from '$lib/components/PosterCard.svelte';
  import Spinner from '$lib/components/Spinner.svelte';
  import { openItem } from '$lib/nav';

  let items = $state<FavoriteItem[] | null>(null);
  let error = $state('');

  onMount(() => {
    (async () => {
      try {
        items = await endpoints.favorites.list();
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
  <h1>Favorites</h1>

  {#if error}
    <p class="empty">{error}</p>
  {:else if !items}
    <Spinner />
  {:else if items.length === 0}
    <p class="empty">Heart-click an item from its detail page to pin it here.</p>
  {:else}
    <div class="grid">
      {#each items as it, i (it.id)}
        <PosterCard
          title={it.title}
          posterPath={it.poster_path ?? it.thumb_path}
          subtitle={it.year ? String(it.year) : undefined}
          autofocus={i === 0}
          onclick={() => openItem(it.id, it.type)}
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
