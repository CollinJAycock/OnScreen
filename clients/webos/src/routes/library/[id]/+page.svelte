<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/state';
  import { endpoints, Unauthorized, type MediaItem } from '$lib/api';
  import { focusManager } from '$lib/focus/manager';
  import PosterCard from '$lib/components/PosterCard.svelte';
  import Spinner from '$lib/components/Spinner.svelte';

  let items = $state<MediaItem[] | null>(null);
  let error = $state('');

  const libraryID = $derived(page.params.id!);

  onMount(() => {
    (async () => {
      try {
        items = await endpoints.libraries.listItems(libraryID);
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
  {#if error}
    <p class="error">{error}</p>
  {:else if !items}
    <Spinner />
  {:else}
    <div class="grid">
      {#each items as item, i (item.id)}
        <PosterCard
          title={item.title}
          posterPath={item.poster_path}
          subtitle={item.year ? String(item.year) : undefined}
          autofocus={i === 0}
          onclick={() => goto(`/item/${item.id}`)}
        />
      {/each}
    </div>
  {/if}
</div>

<style>
  .page {
    padding: var(--page-pad);
  }

  .grid {
    display: grid;
    grid-template-columns: repeat(6, 1fr);
    gap: var(--card-gap) var(--card-gap);
    row-gap: calc(var(--card-gap) + 24px);
  }

  .error {
    font-size: var(--font-md);
    color: #fca5a5;
  }
</style>
