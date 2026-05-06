<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { endpoints, type HubData } from '$lib/api';
  import { Unauthorized } from '$lib/api';
  import HubRow from '$lib/components/HubRow.svelte';
  import PosterCard from '$lib/components/PosterCard.svelte';
  import Spinner from '$lib/components/Spinner.svelte';
  import { openItem } from '$lib/nav';

  let data = $state<HubData | null>(null);
  let error = $state('');

  onMount(() => {
    (async () => {
      try {
        data = await endpoints.hub.get();
      } catch (e) {
        if (e instanceof Unauthorized) goto('/login');
        else error = (e as Error).message;
      }
    })();
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
  <header>
    <h1>OnScreen</h1>
    <nav class="links">
      <a href="/search/" data-sveltekit-preload-data="false">search</a>
      <a href="/hub/" data-sveltekit-preload-data="false">home</a>
      <a href="/favorites/" data-sveltekit-preload-data="false">favorites</a>
      <a href="/history/" data-sveltekit-preload-data="false">history</a>
      <a href="/discover/" data-sveltekit-preload-data="false">discover</a>
      <a href="/livetv/" data-sveltekit-preload-data="false">live tv</a>
      <a href="/recordings/" data-sveltekit-preload-data="false">recordings</a>
      <a href="/settings/" data-sveltekit-preload-data="false">settings</a>
    </nav>
  </header>

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

    {#if data.recently_added.length > 0}
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

    {#if cwEmpty && data.recently_added.length === 0}
      <p class="empty">Your library is empty. Add a library and run a scan from the web UI.</p>
    {/if}
  {/if}
</div>

<style>
  .page {
    padding: 32px 0 0;
  }

  header {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    padding: 0 var(--page-pad);
    margin-bottom: 48px;
  }

  h1 {
    font-size: var(--font-xl);
    margin: 0;
    color: var(--accent);
  }

  .links {
    display: flex;
    gap: 32px;
    font-size: var(--font-md);
    color: var(--text-secondary);
  }

  .links a {
    color: inherit;
    text-decoration: none;
  }

  .error, .empty {
    padding: 0 var(--page-pad);
    font-size: var(--font-md);
  }

  .error { color: #fca5a5; }
  .empty { color: var(--text-secondary); }
</style>
