<script lang="ts">
  // Collection detail — grid of items in a curated list (auto-genre,
  // smart playlist, manual playlist all render the same way; collection
  // type is informational). Mirrors the Android CollectionFragment.

  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/state';
  import {
    endpoints,
    Unauthorized,
    type CollectionItem,
    type MediaCollection
  } from '$lib/api';
  import { focusManager } from '$lib/focus/manager';
  import PosterCard from '$lib/components/PosterCard.svelte';
  import Spinner from '$lib/components/Spinner.svelte';
  import { openItem } from '$lib/nav';

  const collectionId = $derived(page.params.id!);

  let collection = $state<MediaCollection | null>(null);
  let items = $state<CollectionItem[] | null>(null);
  let error = $state('');

  onMount(() => {
    (async () => {
      try {
        // Fetch collection metadata + items in parallel — the title
        // header should appear at the same time as the grid.
        const [meta, list] = await Promise.all([
          endpoints.collections.get(collectionId),
          endpoints.collections.items(collectionId)
        ]);
        collection = meta;
        items = list;
      } catch (e) {
        if (e instanceof Unauthorized) goto('/login');
        else error = (e as Error).message;
      }
    })();

    return focusManager.pushBack(() => {
      history.back();
      return true;
    });
  });
</script>

<div class="page">
  {#if error}
    <p class="error">{error}</p>
  {:else if !collection || !items}
    <Spinner />
  {:else}
    <h1>{collection.name}</h1>
    {#if collection.description}<p class="desc">{collection.description}</p>{/if}

    {#if items.length === 0}
      <p class="empty">This collection is empty.</p>
    {:else}
      <div class="grid">
        {#each items as it, i (it.id)}
          <PosterCard
            title={it.title}
            posterPath={it.poster_path}
            subtitle={it.year ? String(it.year) : undefined}
            autofocus={i === 0}
            onclick={() => openItem(it.id, it.type)}
          />
        {/each}
      </div>
    {/if}
  {/if}
</div>

<style>
  .page {
    padding: var(--page-pad);
    display: flex;
    flex-direction: column;
    gap: 24px;
  }
  h1 {
    font-size: var(--font-2xl);
    margin: 0;
  }
  .desc {
    color: var(--text-secondary);
    font-size: var(--font-md);
    margin: 0;
  }
  .empty, .error {
    color: var(--text-secondary);
    font-size: var(--font-md);
  }
  .grid {
    display: grid;
    grid-template-columns: repeat(6, 1fr);
    gap: var(--card-gap);
  }
</style>
