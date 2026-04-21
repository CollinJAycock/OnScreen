<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { playlistApi, type Playlist } from '$lib/api';

  let playlists: Playlist[] = [];
  let loading = true;
  let error = '';

  let showCreate = false;
  let newName = '';
  let newDescription = '';
  let creating = false;

  onMount(async () => {
    if (!localStorage.getItem('onscreen_user')) { goto('/login'); return; }
    await load();
  });

  async function load() {
    loading = true;
    try { playlists = await playlistApi.list(); }
    catch (e: unknown) { error = e instanceof Error ? e.message : 'Failed'; }
    finally { loading = false; }
  }

  async function createPlaylist() {
    if (!newName.trim()) return;
    creating = true;
    try {
      await playlistApi.create(newName.trim(), newDescription.trim() || undefined);
      newName = '';
      newDescription = '';
      showCreate = false;
      await load();
    } catch (e: unknown) { error = e instanceof Error ? e.message : 'Failed'; }
    finally { creating = false; }
  }

  async function deletePlaylist(id: string) {
    if (!confirm('Delete this playlist? This cannot be undone.')) return;
    try {
      await playlistApi.delete(id);
      playlists = playlists.filter(p => p.id !== id);
    } catch (e: unknown) { error = e instanceof Error ? e.message : 'Failed'; }
  }

  function fmtDate(iso: string): string {
    try { return new Date(iso).toLocaleDateString(); } catch { return ''; }
  }
</script>

<svelte:head><title>Playlists — OnScreen</title></svelte:head>

<div class="page">
  <div class="header">
    <h1>My Playlists</h1>
    <button class="btn-create" on:click={() => (showCreate = !showCreate)}>+ New Playlist</button>
  </div>

  {#if error}<div class="error-bar">{error}</div>{/if}

  {#if showCreate}
    <form class="create-form" on:submit|preventDefault={createPlaylist}>
      <input bind:value={newName} placeholder="Playlist name" autofocus />
      <input bind:value={newDescription} placeholder="Description (optional)" />
      <button type="submit" class="btn-save" disabled={creating || !newName.trim()}>Create</button>
      <button type="button" class="btn-cancel" on:click={() => (showCreate = false)}>Cancel</button>
    </form>
  {/if}

  {#if loading}
    <div class="loading">Loading…</div>
  {:else if playlists.length === 0}
    <div class="empty">
      <p>No playlists yet.</p>
      <p class="empty-sub">Create one, then add items from any movie or episode page.</p>
    </div>
  {:else}
    <div class="grid">
      {#each playlists as pl (pl.id)}
        <a class="card" href="/playlists/{pl.id}">
          <div class="card-icon">&#9835;</div>
          <div class="card-name">{pl.name}</div>
          {#if pl.description}<div class="card-desc">{pl.description}</div>{/if}
          <div class="card-meta">Updated {fmtDate(pl.updated_at)}</div>
          <button
            class="card-delete"
            title="Delete playlist"
            aria-label="Delete playlist"
            on:click|preventDefault|stopPropagation={() => deletePlaylist(pl.id)}
          >×</button>
        </a>
      {/each}
    </div>
  {/if}
</div>

<style>
  .page { padding: 2.5rem; }
  .header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 2rem; }
  h1 { font-size: 1.4rem; font-weight: 800; color: var(--text-primary); }

  .btn-create {
    padding: 0.42rem 0.85rem;
    background: var(--accent-bg);
    border: 1px solid rgba(124,106,247,0.25);
    border-radius: 7px;
    color: var(--accent-text);
    font-size: 0.78rem;
    font-weight: 600;
    cursor: pointer;
  }
  .btn-create:hover { background: rgba(124,106,247,0.2); }

  .create-form { display: flex; gap: 0.5rem; align-items: center; margin-bottom: 1.5rem; flex-wrap: wrap; }
  .create-form input {
    background: var(--bg-hover);
    border: 1px solid var(--border-strong);
    border-radius: 7px;
    padding: 0.42rem 0.75rem;
    color: var(--text-primary);
    font-size: 0.85rem;
    flex: 1;
    min-width: 160px;
  }
  .create-form input:focus { outline: none; border-color: var(--accent); }
  .btn-save {
    padding: 0.42rem 0.85rem;
    background: var(--accent); border: none; border-radius: 7px;
    color: #fff; font-size: 0.78rem; font-weight: 600; cursor: pointer;
  }
  .btn-save:disabled { opacity: 0.5; cursor: not-allowed; }
  .btn-cancel {
    padding: 0.42rem 0.85rem;
    background: var(--bg-hover); border: 1px solid var(--border-strong); border-radius: 7px;
    color: var(--text-muted); font-size: 0.78rem; cursor: pointer;
  }

  .grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(200px, 1fr)); gap: 0.9rem; }

  .card {
    display: flex; flex-direction: column; align-items: center; justify-content: center;
    padding: 1.5rem 1rem;
    background: var(--input-bg);
    border: 1px solid var(--border);
    border-radius: 10px;
    text-decoration: none; color: inherit;
    position: relative;
    transition: border-color 0.15s, background 0.15s;
    min-height: 140px;
  }
  .card:hover { border-color: rgba(124,106,247,0.3); background: rgba(124,106,247,0.05); }
  .card-icon { font-size: 1.6rem; margin-bottom: 0.5rem; color: var(--accent); }
  .card-name { font-size: 0.9rem; font-weight: 700; color: var(--text-primary); text-align: center; }
  .card-desc { font-size: 0.75rem; color: var(--text-muted); margin-top: 0.3rem; text-align: center; }
  .card-meta { font-size: 0.7rem; color: var(--text-muted); margin-top: 0.6rem; }

  .card-delete {
    position: absolute; top: 0.4rem; right: 0.4rem;
    background: none; border: none; color: var(--text-muted); font-size: 1rem;
    cursor: pointer; opacity: 0; transition: opacity 0.15s, color 0.15s;
  }
  .card:hover .card-delete { opacity: 1; }
  .card-delete:hover { color: #f87171; }

  .error-bar {
    background: var(--error-bg); border: 1px solid var(--error-bg); color: var(--error);
    padding: 0.6rem 0.9rem; border-radius: 8px; font-size: 0.8rem; margin-bottom: 1.5rem;
  }
  .loading { color: var(--text-muted); font-size: 0.85rem; }
  .empty { text-align: center; padding: 4rem 2rem; color: var(--text-muted); }
  .empty-sub { font-size: 0.8rem; color: var(--text-muted); margin-top: 0.5rem; }
</style>
