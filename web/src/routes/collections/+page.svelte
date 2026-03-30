<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { collectionApi, type Collection } from '$lib/api';

  let collections: Collection[] = [];
  let loading = true;
  let error = '';

  // Create playlist
  let showCreate = false;
  let newName = '';
  let creating = false;

  onMount(async () => {
    if (!localStorage.getItem('onscreen_user')) { goto('/login'); return; }
    await load();
  });

  async function load() {
    loading = true;
    try { collections = await collectionApi.list(); }
    catch (e: unknown) { error = e instanceof Error ? e.message : 'Failed'; }
    finally { loading = false; }
  }

  async function createPlaylist() {
    if (!newName.trim()) return;
    creating = true;
    try {
      await collectionApi.create(newName.trim());
      newName = '';
      showCreate = false;
      await load();
    } catch (e: unknown) { error = e instanceof Error ? e.message : 'Failed'; }
    finally { creating = false; }
  }

  async function deleteCollection(id: string) {
    if (!confirm('Delete this collection? This cannot be undone.')) return;
    try {
      await collectionApi.delete(id);
      collections = collections.filter(c => c.id !== id);
    } catch (e: unknown) { error = e instanceof Error ? e.message : 'Failed'; }
  }

  $: genreCollections = collections.filter(c => c.type === 'auto_genre');
  $: playlists = collections.filter(c => c.type === 'playlist');
</script>

<svelte:head><title>Collections — OnScreen</title></svelte:head>

<div class="page">
  <div class="header">
    <h1>Collections</h1>
    <button class="btn-create" on:click={() => showCreate = !showCreate}>
      + New Playlist
    </button>
  </div>

  {#if error}
    <div class="error-bar">{error}</div>
  {/if}

  {#if showCreate}
    <form class="create-form" on:submit|preventDefault={createPlaylist}>
      <input bind:value={newName} placeholder="Playlist name" autofocus />
      <button type="submit" class="btn-save" disabled={creating || !newName.trim()}>Create</button>
      <button type="button" class="btn-cancel" on:click={() => showCreate = false}>Cancel</button>
    </form>
  {/if}

  {#if loading}
    <div class="loading">Loading...</div>
  {:else}
    {#if playlists.length > 0}
      <section>
        <h2>Playlists</h2>
        <div class="grid">
          {#each playlists as col (col.id)}
            <a class="card" href="/collections/{col.id}">
              <div class="card-icon">&#9835;</div>
              <div class="card-name">{col.name}</div>
              <button class="card-delete" title="Delete" on:click|preventDefault|stopPropagation={() => deleteCollection(col.id)}>×</button>
            </a>
          {/each}
        </div>
      </section>
    {/if}

    {#if genreCollections.length > 0}
      <section>
        <h2>Genres</h2>
        <div class="grid">
          {#each genreCollections as col (col.id)}
            <a class="card genre" href="/collections/{col.id}">
              <div class="card-name">{col.name}</div>
            </a>
          {/each}
        </div>
      </section>
    {/if}

    {#if collections.length === 0}
      <div class="empty">
        <p>No collections yet.</p>
        <p class="empty-sub">Create a playlist or run a scan to generate genre collections.</p>
      </div>
    {/if}
  {/if}
</div>

<style>
  .page { padding: 2.5rem; }
  .header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 2rem; }
  h1 { font-size: 1.4rem; font-weight: 800; color: #eeeef8; }
  h2 { font-size: 0.9rem; font-weight: 700; color: #66667a; text-transform: uppercase; letter-spacing: 0.06em; margin-bottom: 1rem; }
  section { margin-bottom: 2.5rem; }

  .btn-create {
    padding: 0.42rem 0.85rem;
    background: rgba(124,106,247,0.12);
    border: 1px solid rgba(124,106,247,0.25);
    border-radius: 7px;
    color: #a89ffa;
    font-size: 0.78rem;
    font-weight: 600;
    cursor: pointer;
  }
  .btn-create:hover { background: rgba(124,106,247,0.2); }

  .create-form {
    display: flex; gap: 0.5rem; align-items: center; margin-bottom: 1.5rem;
  }
  .create-form input {
    background: rgba(255,255,255,0.04);
    border: 1px solid rgba(255,255,255,0.1);
    border-radius: 7px;
    padding: 0.42rem 0.75rem;
    color: #eeeef8;
    font-size: 0.85rem;
    flex: 1;
    max-width: 300px;
  }
  .create-form input:focus { outline: none; border-color: #7c6af7; }
  .btn-save {
    padding: 0.42rem 0.85rem;
    background: #7c6af7;
    border: none;
    border-radius: 7px;
    color: #fff;
    font-size: 0.78rem;
    font-weight: 600;
    cursor: pointer;
  }
  .btn-save:disabled { opacity: 0.5; cursor: not-allowed; }
  .btn-cancel {
    padding: 0.42rem 0.85rem;
    background: rgba(255,255,255,0.04);
    border: 1px solid rgba(255,255,255,0.08);
    border-radius: 7px;
    color: #66667a;
    font-size: 0.78rem;
    cursor: pointer;
  }

  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(160px, 1fr));
    gap: 0.75rem;
  }

  .card {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    padding: 1.5rem 1rem;
    background: rgba(255,255,255,0.03);
    border: 1px solid rgba(255,255,255,0.07);
    border-radius: 10px;
    text-decoration: none;
    color: inherit;
    position: relative;
    transition: border-color 0.15s, background 0.15s;
  }
  .card:hover { border-color: rgba(124,106,247,0.3); background: rgba(124,106,247,0.05); }

  .card-icon { font-size: 1.5rem; margin-bottom: 0.5rem; color: #7c6af7; }
  .card-name { font-size: 0.82rem; font-weight: 600; color: #aaaacc; text-align: center; }

  .card.genre { padding: 1rem; }
  .card.genre .card-name { color: #8888aa; }

  .card-delete {
    position: absolute;
    top: 0.4rem;
    right: 0.4rem;
    background: none;
    border: none;
    color: #44445a;
    font-size: 1rem;
    cursor: pointer;
    opacity: 0;
    transition: opacity 0.15s, color 0.15s;
  }
  .card:hover .card-delete { opacity: 1; }
  .card-delete:hover { color: #f87171; }

  .error-bar {
    background: rgba(248,113,113,0.1);
    border: 1px solid rgba(248,113,113,0.2);
    color: #fca5a5;
    padding: 0.6rem 0.9rem;
    border-radius: 8px;
    font-size: 0.8rem;
    margin-bottom: 1.5rem;
  }

  .loading { color: #44445a; font-size: 0.85rem; }
  .empty { text-align: center; padding: 4rem 2rem; color: #44445a; }
  .empty-sub { font-size: 0.8rem; color: #33333d; margin-top: 0.5rem; }
</style>
