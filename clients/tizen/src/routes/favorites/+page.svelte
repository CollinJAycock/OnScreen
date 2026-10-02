<script lang="ts">
  // Favorites — items the user marked with ♥ Favorite on a detail
  // page. Read-only grid; clicking a card routes through openItem
  // so photos / collections still go to their right destinations.
  // Up to 200, like Android.

  import { onMount, tick } from 'svelte';
  import { goto } from '$app/navigation';
  import { endpoints, Unauthorized, type FavoriteItem } from '$lib/api';
  import { focusManager } from '$lib/focus/manager';
  import { focusFirstOf, restoreGuard, restoreKeyed, takeFocusMemo } from '$lib/focus/memory';
  import PosterCard from '$lib/components/PosterCard.svelte';
  import Spinner from '$lib/components/Spinner.svelte';
  import TopNav from '$lib/components/TopNav.svelte';
  import { openItem } from '$lib/nav';

  let items = $state<FavoriteItem[] | null>(null);
  let error = $state('');
  // Back from a title: the first card holds its autofocus and the one the
  // user opened takes focus again (lib/focus/memory).
  let restoring = $state(false);

  onMount(() => {
    const memo = takeFocusMemo();
    restoring = !!memo?.focusedId;
    // Ends the restore when the page goes away or the user moves focus
    // while the list is still loading (lib/focus/memory).
    const guard = restoreGuard();
    (async () => {
      try {
        items = await endpoints.favorites.list(200);
        if (restoring) {
          await tick();
          // Unfavorited meanwhile: the first card instead.
          if (!restoreKeyed(memo, guard)) focusFirstOf('.grid [data-focusable]', guard);
        }
      } catch (e) {
        if (e instanceof Unauthorized) goto('#/login');
        else error = (e as Error).message;
      }
    })();

    const offBack = focusManager.pushBack(() => {
      goto('#/hub');
      return true;
    });
    return () => {
      guard.end();
      offBack();
    };
  });
</script>

<div class="page">
  <TopNav />
  <h1>Favorites</h1>

  {#if error}
    <p class="empty">{error}</p>
  {:else if !items}
    <Spinner />
  {:else if items.length === 0}
    <p class="empty">Press ♥ Favorite on a title's page to pin it here.</p>
  {:else}
    <div class="grid">
      {#each items as it, i (it.id)}
        <PosterCard
          title={it.title}
          posterPath={it.poster_path ?? it.thumb_path}
          subtitle={it.year ? String(it.year) : undefined}
          focusKey={it.id}
          autofocus={i === 0 && !restoring}
          onclick={() => openItem(it.id, it.type)}
        />
      {/each}
    </div>
  {/if}
</div>

<style>
  /* A one-column grid rather than a flex column: grid `gap` works on
     webOS 6's Chromium 79, flexbox `gap` needs Chrome 84. */
  .page {
    padding: 0 var(--page-pad) var(--page-pad);
    display: grid;
    grid-template-columns: minmax(0, 1fr);
    align-content: start;
    row-gap: 24px;
  }
  h1 { font-size: var(--font-2xl); margin: 24px 0 0; }
  .empty { color: var(--text-secondary); font-size: var(--font-md); }
  .grid {
    display: grid;
    grid-template-columns: repeat(6, 1fr);
    gap: var(--card-gap);
  }
</style>
