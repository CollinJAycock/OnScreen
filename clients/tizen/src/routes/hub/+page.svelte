<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { endpoints, type HubData } from '$lib/api';
  import { Unauthorized } from '$lib/api';
  import { focusManager } from '$lib/focus/manager';
  import HubRow from '$lib/components/HubRow.svelte';
  import PosterCard from '$lib/components/PosterCard.svelte';
  import Spinner from '$lib/components/Spinner.svelte';
  import TopNav from '$lib/components/TopNav.svelte';
  import { openItem } from '$lib/nav';

  let data = $state<HubData | null>(null);
  let error = $state('');

  // Minimal type for the one tizen.application call we make — same
  // local-cast pattern keys.ts uses, avoids the @types dependency.
  type TizenApplication = {
    application?: { getCurrentApplication(): { exit(): void } };
  };

  onMount(() => {
    (async () => {
      try {
        data = await endpoints.hub.get();
      } catch (e) {
        if (e instanceof Unauthorized) goto('#/login');
        else error = (e as Error).message;
      }
    })();

    // The hub is the app's home screen — Samsung certification requires
    // Back here to exit the app. The focus manager preventDefaults the
    // keypress when a back handler returns true, so it can't also fall
    // through to the webview default. window.tizen is absent in browser
    // dev (`vite dev`); returning true is then a handled no-op.
    return focusManager.pushBack(() => {
      (window as Window & { tizen?: TizenApplication })
        .tizen?.application?.getCurrentApplication()?.exit();
      return true;
    });
  });

  function open(id: string, type: string) {
    // Type-aware routing: photos go to a full-screen viewer,
    // collections drill into their item grid, everything else lands
    // on the standard /item detail page.
    openItem(id, type);
  }

  function progress(item: { view_offset_ms?: number; duration_ms?: number }): number | undefined {
    if (!item.view_offset_ms || !item.duration_ms) return undefined;
    return item.view_offset_ms / item.duration_ms;
  }

  // Resolve the three Continue Watching buckets. Newer servers
  // pre-split them; older servers send only the combined feed and
  // we filter client-side.
  const tv      = $derived(data?.continue_watching_tv ?? data?.continue_watching.filter(i => i.type === 'episode') ?? []);
  const movies  = $derived(data?.continue_watching_movies ?? data?.continue_watching.filter(i => i.type === 'movie') ?? []);
  const other   = $derived(data?.continue_watching_other ?? data?.continue_watching.filter(i => i.type !== 'episode' && i.type !== 'movie') ?? []);
  const cwEmpty = $derived(tv.length === 0 && movies.length === 0 && other.length === 0);
</script>

<div class="page">
  <TopNav />

  {#if error}
    <p class="error">{error}</p>
  {:else if !data}
    <Spinner />
  {:else}
    {#if tv.length > 0}
      <HubRow title="Continue Watching TV Shows">
        {#each tv as item, i (item.id)}
          <PosterCard
            title={item.title}
            posterPath={item.poster_path}
            subtitle={item.year ? String(item.year) : undefined}
            progressRatio={progress(item)}
            autofocus={i === 0}
            onclick={() => open(item.id, item.type)}
          />
        {/each}
      </HubRow>
    {/if}

    {#if movies.length > 0}
      <HubRow title="Continue Watching Movies">
        {#each movies as item, i (item.id)}
          <PosterCard
            title={item.title}
            posterPath={item.poster_path}
            subtitle={item.year ? String(item.year) : undefined}
            progressRatio={progress(item)}
            autofocus={tv.length === 0 && i === 0}
            onclick={() => open(item.id, item.type)}
          />
        {/each}
      </HubRow>
    {/if}

    {#if other.length > 0}
      <HubRow title="Continue Watching">
        {#each other as item, i (item.id)}
          <PosterCard
            title={item.title}
            posterPath={item.poster_path}
            subtitle={item.year ? String(item.year) : undefined}
            progressRatio={progress(item)}
            autofocus={tv.length === 0 && movies.length === 0 && i === 0}
            onclick={() => open(item.id, item.type)}
          />
        {/each}
      </HubRow>
    {/if}

    <!-- Per-library "Recently Added to <Library>" strips. Falls back
         to the flat aggregate on older servers that don't emit
         recently_added_by_library. -->
    {#if data.recently_added_by_library && data.recently_added_by_library.length > 0}
      {#each data.recently_added_by_library as row, ri (row.library_id)}
        {#if row.items.length > 0}
          <HubRow title="Recently Added to {row.library_name}">
            {#each row.items as item, i (item.id)}
              <PosterCard
                title={item.title}
                posterPath={item.poster_path}
                subtitle={item.year ? String(item.year) : undefined}
                autofocus={cwEmpty && ri === 0 && i === 0}
                onclick={() => open(item.id, item.type)}
              />
            {/each}
          </HubRow>
        {/if}
      {/each}
    {:else if data.recently_added.length > 0}
      <HubRow title="Recently Added">
        {#each data.recently_added as item, i (item.id)}
          <PosterCard
            title={item.title}
            posterPath={item.poster_path}
            subtitle={item.year ? String(item.year) : undefined}
            autofocus={cwEmpty && i === 0}
            onclick={() => open(item.id, item.type)}
          />
        {/each}
      </HubRow>
    {/if}

    {#if cwEmpty && (!data.recently_added_by_library || data.recently_added_by_library.every(r => r.items.length === 0)) && data.recently_added.length === 0}
      <p class="empty">Your library is empty. Add a library and run a scan from the web UI.</p>
    {/if}
  {/if}
</div>

<style>
  .page {
    padding: 0 0 32px;
  }

  .error, .empty {
    padding: 0 var(--page-pad);
    font-size: var(--font-md);
  }

  .error { color: #fca5a5; }
  .empty { color: var(--text-secondary); }
</style>
