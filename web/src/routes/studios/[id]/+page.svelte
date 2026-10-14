<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { studiosApi, assetUrl, type Studio, type MediaItem, type StudioItemsParams } from '$lib/api';
  import { itemHref } from '$lib/itemHref';
  import { studioKindLabel } from '$lib/studios';

  const PAGE = 60;

  let studio: Studio | null = null;
  let items: MediaItem[] = [];
  let total = 0;
  let loading = true;
  let loadingMore = false;
  let error = '';
  let sort: NonNullable<StudioItemsParams['sort']> = 'title';

  $: id = $page.params.id!;
  // Opened from a library's studio list: show that library's titles only.
  $: libraryId = $page.url.searchParams.get('library_id') ?? undefined;

  function params(): StudioItemsParams {
    return { sort, library_id: libraryId };
  }

  async function load() {
    loading = true;
    error = '';
    try {
      const [s, page1] = await Promise.all([studiosApi.get(id), studiosApi.items(id, PAGE, 0, params())]);
      studio = s;
      items = page1.items;
      total = page1.total;
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed to load';
    } finally {
      loading = false;
    }
  }

  async function loadMore() {
    loadingMore = true;
    try {
      const next = await studiosApi.items(id, PAGE, items.length, params());
      items = [...items, ...next.items];
      total = next.total;
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed to load';
    } finally {
      loadingMore = false;
    }
  }

  async function changeSort() {
    try {
      const page1 = await studiosApi.items(id, PAGE, 0, params());
      items = page1.items;
      total = page1.total;
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed to load';
    }
  }

  onMount(() => {
    if (!localStorage.getItem('onscreen_user')) { goto('/login'); return; }
    load();
  });
</script>

<svelte:head><title>{studio ? `${studio.name} — OnScreen` : 'OnScreen'}</title></svelte:head>

<div class="page">
  {#if loading}
    <p class="loading">Loading…</p>
  {:else if error && !studio}
    <p class="err">{error}</p>
  {:else if studio}
    <header>
      {#if libraryId}<a class="back" href="/libraries/{libraryId}/studios">← {studioKindLabel(studio.kind, true)}</a>{/if}
      <h1>{studio.name}</h1>
      <span class="subtle">{studioKindLabel(studio.kind)} · {total} {total === 1 ? 'title' : 'titles'}</span>
      <label class="sort">
        Sort
        <select value={sort}
                on:change={(e) => { sort = e.currentTarget.value as typeof sort; changeSort(); }}>
          <option value="title">Title</option>
          <option value="year">Year</option>
          <option value="rating">Rating</option>
          <option value="created_at">Date added</option>
        </select>
      </label>
    </header>

    {#if error}<p class="err">{error}</p>{/if}

    {#if items.length === 0}
      <p class="empty">No titles here.</p>
    {:else}
      <div class="grid">
        {#each items as item (item.id)}
          <a class="card" href={itemHref(item.type, item.id)} title={item.title}>
            {#if item.poster_path}
              <img class="poster" src={assetUrl(`/artwork/${encodeURI(item.poster_path)}?w=300`)}
                   srcset="{assetUrl(`/artwork/${encodeURI(item.poster_path)}?w=150`)} 150w, {assetUrl(`/artwork/${encodeURI(item.poster_path)}?w=300`)} 300w, {assetUrl(`/artwork/${encodeURI(item.poster_path)}?w=450`)} 450w"
                   sizes="(max-width: 768px) 100px, 180px"
                   alt={item.title} loading="lazy" />
            {:else}
              <div class="poster placeholder"><span>{item.title.charAt(0)}</span></div>
            {/if}
            <div class="meta">
              <div class="title">{item.title}</div>
              <div class="sub">
                {#if item.year}{item.year}{/if}
                {#if item.rating}<span class="dot">·</span>{item.rating.toFixed(1)}{/if}
              </div>
            </div>
          </a>
        {/each}
      </div>
      {#if items.length < total}
        <div class="more">
          <button on:click={loadMore} disabled={loadingMore}>{loadingMore ? 'Loading…' : 'Load more'}</button>
        </div>
      {/if}
    {/if}
  {/if}
</div>

<style>
  .page { padding: 1.5rem 2rem; max-width: 1200px; margin: 0 auto; }
  header { display: flex; align-items: baseline; flex-wrap: wrap; gap: 1rem; margin-bottom: 1.5rem; }
  .back { color: var(--text-secondary); text-decoration: none; font-size: 0.85rem; }
  .back:hover { color: var(--text-primary); }
  h1 { margin: 0; font-size: 1.5rem; }
  .subtle { color: var(--text-secondary); font-size: 0.85rem; }
  .sort { margin-left: auto; color: var(--text-secondary); font-size: 0.8rem; display: flex; gap: 0.4rem; align-items: center; }
  .sort select {
    background: var(--bg-hover); border: 1px solid var(--border-strong); border-radius: 7px;
    color: var(--text-primary); padding: 0.3rem 0.5rem; font-size: 0.8rem;
  }
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(150px, 1fr));
    gap: 1rem;
  }
  .card {
    display: flex; flex-direction: column; text-decoration: none; color: inherit;
    border-radius: 8px; overflow: hidden;
    background: var(--input-bg); border: 1px solid var(--border);
    transition: border-color 0.15s, transform 0.15s;
  }
  .card:hover { border-color: rgba(124,106,247,0.3); transform: translateY(-2px); }
  .poster { width: 100%; aspect-ratio: 2/3; object-fit: cover; display: block; }
  .poster.placeholder {
    display: flex; align-items: center; justify-content: center;
    background: rgba(255,255,255,0.02); font-size: 2rem; color: var(--text-muted);
  }
  .meta { padding: 0.6rem 0.5rem; }
  .title { font-size: 0.78rem; font-weight: 600; color: var(--text-primary); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .sub { font-size: 0.68rem; color: var(--text-muted); margin-top: 0.2rem; }
  .dot { margin: 0 0.25rem; }
  .more { display: flex; justify-content: center; margin: 1.5rem 0; }
  .more button {
    background: var(--surface); border: 1px solid var(--border); border-radius: 8px;
    color: var(--text-primary); padding: 0.5rem 1.2rem; cursor: pointer;
  }
  .loading, .err, .empty { color: var(--text-secondary); padding: 2rem 0; text-align: center; }
  .err { color: var(--error); }
  @media (max-width: 768px) {
    .page { padding: 1rem; }
    .grid { grid-template-columns: repeat(auto-fill, minmax(100px, 1fr)); gap: 0.6rem; }
  }
</style>
