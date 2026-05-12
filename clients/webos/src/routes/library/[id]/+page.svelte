<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/state';
  import { endpoints, Unauthorized, type MediaItem, type Library } from '$lib/api';
  import { focusManager } from '$lib/focus/manager';
  import PosterCard from '$lib/components/PosterCard.svelte';
  import Spinner from '$lib/components/Spinner.svelte';
  import TopNav from '$lib/components/TopNav.svelte';
  import { openItem, goBack } from '$lib/nav';

  let items = $state<MediaItem[] | null>(null);
  let library = $state<Library | null>(null);
  let error = $state('');

  const libraryID = $derived(page.params.id!);

  onMount(() => {
    (async () => {
      try {
        // Fire item-list + library-list in parallel — list-by-id
        // isn't exposed yet, so we filter the small library list
        // client-side for the header label.
        const [libs, libItems] = await Promise.all([
          endpoints.libraries.list(),
          endpoints.libraries.listItems(libraryID),
        ]);
        library = libs.find((l) => l.id === libraryID) ?? null;
        items = libItems;
      } catch (e) {
        if (e instanceof Unauthorized) goto('#/login');
        else error = (e as Error).message;
      }
    })();

    return focusManager.pushBack(() => {
      goBack();
      return true;
    });
  });
</script>

<div class="page">
  <TopNav />
  <h1>{library?.name ?? 'Library'}</h1>

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
          onclick={() => openItem(item.id, item.type)}
        />
      {/each}
    </div>
  {/if}
</div>

<style>
  .page {
    padding: 0 var(--page-pad) var(--page-pad);
  }
  h1 {
    font-size: var(--font-2xl);
    margin: 24px 0 32px;
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
