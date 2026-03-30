<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/stores';
  import { goto } from '$app/navigation';
  import { collectionApi, type Collection, type CollectionItem } from '$lib/api';

  let collection: Collection | null = null;
  let items: CollectionItem[] = [];
  let total = 0;
  let loading = true;
  let error = '';

  // Edit state
  let editing = false;
  let editName = '';

  $: id = $page.params.id!;

  onMount(async () => {
    if (!localStorage.getItem('onscreen_user')) { goto('/login'); return; }
    await load();
  });

  async function load() {
    loading = true;
    try {
      collection = await collectionApi.get(id);
      const res = await collectionApi.items(id, 200, 0);
      items = res.items;
      total = res.total;
    } catch (e: unknown) { error = e instanceof Error ? e.message : 'Failed'; }
    finally { loading = false; }
  }

  async function saveEdit() {
    if (!collection || !editName.trim()) return;
    try {
      collection = await collectionApi.update(id, editName.trim());
      editing = false;
    } catch (e: unknown) { error = e instanceof Error ? e.message : 'Failed'; }
  }

  async function removeItem(itemId: string) {
    try {
      await collectionApi.removeItem(id, itemId);
      items = items.filter(i => i.id !== itemId);
      total--;
    } catch (e: unknown) { error = e instanceof Error ? e.message : 'Failed'; }
  }

  function startEdit() {
    if (!collection) return;
    editName = collection.name;
    editing = true;
  }

  function fmt(ms: number): string {
    const m = Math.floor(ms / 60000);
    if (m < 60) return `${m}m`;
    const h = Math.floor(m / 60);
    return `${h}h ${m % 60}m`;
  }
</script>

<svelte:head>
  <title>{collection?.name ?? 'Collection'} — OnScreen</title>
</svelte:head>

<div class="page">
  {#if loading}
    <div class="loading">Loading...</div>
  {:else if collection}
    <div class="header">
      <button class="back" on:click={() => goto('/collections')}>&larr;</button>
      {#if editing}
        <form class="edit-form" on:submit|preventDefault={saveEdit}>
          <input bind:value={editName} autofocus />
          <button type="submit" class="btn-save">Save</button>
          <button type="button" class="btn-cancel" on:click={() => editing = false}>Cancel</button>
        </form>
      {:else}
        <h1>{collection.name}</h1>
        {#if collection.type === 'playlist'}
          <button class="btn-edit" on:click={startEdit}>Edit</button>
        {/if}
      {/if}
      <span class="count">{total} item{total !== 1 ? 's' : ''}</span>
    </div>

    {#if error}
      <div class="error-bar">{error}</div>
    {/if}

    {#if items.length === 0}
      <div class="empty">
        <p>This collection is empty.</p>
        {#if collection.type === 'playlist'}
          <p class="empty-sub">Add items from the library or player page.</p>
        {/if}
      </div>
    {:else}
      <div class="grid">
        {#each items as item (item.id)}
          <a class="card" href="/watch/{item.id}">
            {#if item.poster_path}
              <img class="poster" src="/artwork/{item.poster_path}" alt={item.title} loading="lazy" />
            {:else}
              <div class="poster placeholder">
                <span>{item.type === 'movie' ? '🎬' : '📺'}</span>
              </div>
            {/if}
            <div class="meta">
              <div class="title">{item.title}</div>
              <div class="sub">
                {#if item.year}{item.year}{/if}
                {#if item.rating}<span class="dot">·</span>{item.rating.toFixed(1)}{/if}
                {#if item.duration_ms}<span class="dot">·</span>{fmt(item.duration_ms)}{/if}
              </div>
            </div>
            {#if collection.type === 'playlist'}
              <button class="remove" title="Remove" on:click|preventDefault|stopPropagation={() => removeItem(item.id)}>×</button>
            {/if}
          </a>
        {/each}
      </div>
    {/if}
  {/if}
</div>

<style>
  .page { padding: 2.5rem; }
  .header { display: flex; align-items: center; gap: 0.75rem; margin-bottom: 2rem; flex-wrap: wrap; }
  .back {
    background: none; border: none; color: #66667a; font-size: 1.2rem; cursor: pointer; padding: 0;
  }
  .back:hover { color: #eeeef8; }
  h1 { font-size: 1.4rem; font-weight: 800; color: #eeeef8; margin: 0; }
  .count { font-size: 0.78rem; color: #44445a; margin-left: auto; }

  .btn-edit {
    padding: 0.3rem 0.65rem; background: rgba(255,255,255,0.04); border: 1px solid rgba(255,255,255,0.08);
    border-radius: 6px; color: #66667a; font-size: 0.72rem; cursor: pointer;
  }
  .btn-edit:hover { color: #aaaacc; border-color: rgba(255,255,255,0.15); }

  .edit-form { display: flex; gap: 0.5rem; align-items: center; flex: 1; }
  .edit-form input {
    background: rgba(255,255,255,0.04); border: 1px solid rgba(255,255,255,0.1);
    border-radius: 7px; padding: 0.42rem 0.75rem; color: #eeeef8; font-size: 0.85rem; flex: 1; max-width: 300px;
  }
  .edit-form input:focus { outline: none; border-color: #7c6af7; }
  .btn-save {
    padding: 0.35rem 0.7rem; background: #7c6af7; border: none; border-radius: 6px;
    color: #fff; font-size: 0.75rem; font-weight: 600; cursor: pointer;
  }
  .btn-cancel {
    padding: 0.35rem 0.7rem; background: rgba(255,255,255,0.04); border: 1px solid rgba(255,255,255,0.08);
    border-radius: 6px; color: #66667a; font-size: 0.75rem; cursor: pointer;
  }

  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(150px, 1fr));
    gap: 1rem;
  }

  .card {
    display: flex; flex-direction: column; text-decoration: none; color: inherit;
    border-radius: 8px; overflow: hidden; position: relative;
    background: rgba(255,255,255,0.03); border: 1px solid rgba(255,255,255,0.07);
    transition: border-color 0.15s, transform 0.15s;
  }
  .card:hover { border-color: rgba(124,106,247,0.3); transform: translateY(-2px); }

  .poster { width: 100%; aspect-ratio: 2/3; object-fit: cover; display: block; }
  .poster.placeholder {
    display: flex; align-items: center; justify-content: center;
    background: rgba(255,255,255,0.02); font-size: 2rem;
  }

  .meta { padding: 0.6rem 0.5rem; }
  .title { font-size: 0.78rem; font-weight: 600; color: #cccce0; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .sub { font-size: 0.68rem; color: #55556a; margin-top: 0.2rem; }
  .dot { margin: 0 0.25rem; }

  .remove {
    position: absolute; top: 0.35rem; right: 0.35rem;
    background: rgba(0,0,0,0.6); border: none; color: #888;
    width: 1.4rem; height: 1.4rem; border-radius: 50%; font-size: 0.9rem;
    cursor: pointer; opacity: 0; transition: opacity 0.15s, color 0.15s;
    display: flex; align-items: center; justify-content: center;
  }
  .card:hover .remove { opacity: 1; }
  .remove:hover { color: #f87171; }

  .error-bar {
    background: rgba(248,113,113,0.1); border: 1px solid rgba(248,113,113,0.2);
    color: #fca5a5; padding: 0.6rem 0.9rem; border-radius: 8px; font-size: 0.8rem; margin-bottom: 1.5rem;
  }
  .loading { color: #44445a; font-size: 0.85rem; }
  .empty { text-align: center; padding: 4rem 2rem; color: #44445a; }
  .empty-sub { font-size: 0.8rem; color: #33333d; margin-top: 0.5rem; }
</style>
