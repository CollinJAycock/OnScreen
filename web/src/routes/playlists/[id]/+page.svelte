<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/stores';
  import { goto } from '$app/navigation';
  import { playlistApi, assetUrl, type Playlist, type PlaylistItem } from '$lib/api';

  let playlist: Playlist | null = null;
  let items: PlaylistItem[] = [];
  let loading = true;
  let error = '';

  let editing = false;
  let editName = '';
  let editDescription = '';
  let saving = false;

  let reorderPending = false;

  $: id = $page.params.id!;

  onMount(async () => {
    if (!localStorage.getItem('onscreen_user')) { goto('/login'); return; }
    await load();
  });

  async function load() {
    loading = true;
    try {
      const list = await playlistApi.list();
      playlist = list.find(p => p.id === id) ?? null;
      if (!playlist) {
        error = 'Playlist not found';
        return;
      }
      const res = await playlistApi.items(id);
      items = res.items.sort((a, b) => a.position - b.position);
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed';
    } finally {
      loading = false;
    }
  }

  function startEdit() {
    if (!playlist) return;
    editName = playlist.name;
    editDescription = playlist.description ?? '';
    editing = true;
  }

  async function saveEdit() {
    if (!playlist || !editName.trim()) return;
    saving = true;
    try {
      playlist = await playlistApi.update(id, editName.trim(), editDescription.trim() || undefined);
      editing = false;
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed';
    } finally {
      saving = false;
    }
  }

  async function deletePlaylist() {
    if (!confirm('Delete this playlist? This cannot be undone.')) return;
    try {
      await playlistApi.delete(id);
      goto('/playlists');
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed';
    }
  }

  async function removeItem(itemId: string) {
    try {
      await playlistApi.removeItem(id, itemId);
      items = items.filter(i => i.id !== itemId);
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed';
    }
  }

  async function move(index: number, delta: number) {
    const target = index + delta;
    if (target < 0 || target >= items.length || reorderPending) return;
    const next = items.slice();
    [next[index], next[target]] = [next[target], next[index]];
    items = next;
    await persistOrder();
  }

  async function persistOrder() {
    if (!playlist) return;
    reorderPending = true;
    try {
      await playlistApi.reorder(id, items.map(i => i.id));
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed';
      await load();
    } finally {
      reorderPending = false;
    }
  }

  function fmt(ms: number): string {
    const m = Math.floor(ms / 60000);
    if (m < 60) return `${m}m`;
    const h = Math.floor(m / 60);
    return `${h}h ${m % 60}m`;
  }
</script>

<svelte:head>
  <title>{playlist?.name ?? 'Playlist'} — OnScreen</title>
</svelte:head>

<div class="page">
  {#if loading}
    <div class="loading">Loading…</div>
  {:else if !playlist}
    <div class="empty"><p>{error || 'Playlist not found.'}</p></div>
  {:else}
    <div class="header">
      <button class="back" on:click={() => goto('/playlists')} aria-label="Back">&larr;</button>
      {#if editing}
        <form class="edit-form" on:submit|preventDefault={saveEdit}>
          <input bind:value={editName} placeholder="Name" autofocus />
          <input bind:value={editDescription} placeholder="Description (optional)" />
          <button type="submit" class="btn-save" disabled={saving || !editName.trim()}>Save</button>
          <button type="button" class="btn-cancel" on:click={() => (editing = false)}>Cancel</button>
        </form>
      {:else}
        <div class="title-block">
          <h1>{playlist.name}</h1>
          {#if playlist.description}<p class="description">{playlist.description}</p>{/if}
        </div>
        <button class="btn-edit" on:click={startEdit}>Edit</button>
        <button class="btn-delete" on:click={deletePlaylist}>Delete</button>
      {/if}
      <span class="count">{items.length} item{items.length !== 1 ? 's' : ''}</span>
    </div>

    {#if error}<div class="error-bar">{error}</div>{/if}

    {#if items.length === 0}
      <div class="empty">
        <p>This playlist is empty.</p>
        <p class="empty-sub">Open a movie or episode and use "Add to playlist".</p>
      </div>
    {:else}
      <ol class="list">
        {#each items as item, i (item.id)}
          <li class="row">
            <div class="reorder">
              <button
                class="arrow"
                title="Move up"
                aria-label="Move up"
                disabled={i === 0 || reorderPending}
                on:click={() => move(i, -1)}
              >&uarr;</button>
              <button
                class="arrow"
                title="Move down"
                aria-label="Move down"
                disabled={i === items.length - 1 || reorderPending}
                on:click={() => move(i, 1)}
              >&darr;</button>
            </div>

            <a class="card" href="/watch/{item.id}">
              {#if item.poster_path}
                <img
                  class="poster"
                  src={assetUrl(`/artwork/${encodeURI(item.poster_path)}?w=150`)}
                  alt={item.title}
                  loading="lazy"
                />
              {:else}
                <div class="poster placeholder">
                  <span>{item.type === 'movie' ? '🎬' : '📺'}</span>
                </div>
              {/if}
              <div class="meta">
                <div class="row-title">{item.title}</div>
                <div class="sub">
                  {#if item.year}{item.year}{/if}
                  {#if item.rating}<span class="dot">·</span>{item.rating.toFixed(1)}{/if}
                  {#if item.duration_ms}<span class="dot">·</span>{fmt(item.duration_ms)}{/if}
                </div>
              </div>
            </a>

            <button class="remove" title="Remove" aria-label="Remove" on:click={() => removeItem(item.id)}>×</button>
          </li>
        {/each}
      </ol>
    {/if}
  {/if}
</div>

<style>
  .page { padding: 2.5rem; max-width: 900px; margin: 0 auto; }
  .header { display: flex; align-items: center; gap: 0.75rem; margin-bottom: 2rem; flex-wrap: wrap; }
  .back { background: none; border: none; color: var(--text-muted); font-size: 1.2rem; cursor: pointer; padding: 0; }
  .back:hover { color: var(--text-primary); }
  .title-block { display: flex; flex-direction: column; gap: 0.2rem; }
  h1 { font-size: 1.4rem; font-weight: 800; color: var(--text-primary); margin: 0; }
  .description { font-size: 0.82rem; color: var(--text-muted); margin: 0; }
  .count { font-size: 0.78rem; color: var(--text-muted); margin-left: auto; }

  .btn-edit, .btn-delete {
    padding: 0.3rem 0.65rem;
    background: var(--bg-hover);
    border: 1px solid var(--border-strong);
    border-radius: 6px;
    color: var(--text-muted);
    font-size: 0.72rem;
    cursor: pointer;
  }
  .btn-edit:hover { color: var(--text-secondary); }
  .btn-delete:hover { color: #f87171; border-color: rgba(248,113,113,0.3); }

  .edit-form { display: flex; gap: 0.5rem; align-items: center; flex: 1; flex-wrap: wrap; }
  .edit-form input {
    background: var(--bg-hover);
    border: 1px solid var(--border-strong);
    border-radius: 7px;
    padding: 0.42rem 0.75rem;
    color: var(--text-primary);
    font-size: 0.85rem;
    flex: 1;
    min-width: 140px;
  }
  .edit-form input:focus { outline: none; border-color: var(--accent); }
  .btn-save {
    padding: 0.35rem 0.7rem; background: var(--accent); border: none; border-radius: 6px;
    color: #fff; font-size: 0.75rem; font-weight: 600; cursor: pointer;
  }
  .btn-save:disabled { opacity: 0.5; cursor: not-allowed; }
  .btn-cancel {
    padding: 0.35rem 0.7rem; background: var(--bg-hover); border: 1px solid var(--border-strong); border-radius: 6px;
    color: var(--text-muted); font-size: 0.75rem; cursor: pointer;
  }

  .list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 0.6rem; }

  .row {
    display: grid;
    grid-template-columns: auto 1fr auto;
    align-items: center;
    gap: 0.8rem;
    background: var(--input-bg);
    border: 1px solid var(--border);
    border-radius: 10px;
    padding: 0.6rem;
  }
  .row:hover { border-color: rgba(124,106,247,0.3); }

  .reorder { display: flex; flex-direction: column; gap: 0.15rem; }
  .arrow {
    width: 1.8rem; height: 1.4rem;
    background: var(--bg-hover);
    border: 1px solid var(--border-strong);
    border-radius: 5px;
    color: var(--text-muted);
    font-size: 0.75rem;
    cursor: pointer;
    padding: 0;
    display: flex; align-items: center; justify-content: center;
  }
  .arrow:hover:not(:disabled) { color: var(--text-primary); border-color: var(--accent); }
  .arrow:disabled { opacity: 0.3; cursor: not-allowed; }

  .card {
    display: grid;
    grid-template-columns: 60px 1fr;
    align-items: center;
    gap: 0.75rem;
    text-decoration: none;
    color: inherit;
    min-width: 0;
  }
  .poster { width: 60px; height: 90px; object-fit: cover; border-radius: 6px; display: block; }
  .poster.placeholder {
    display: flex; align-items: center; justify-content: center;
    background: rgba(255,255,255,0.02);
    font-size: 1.5rem;
  }
  .meta { min-width: 0; }
  .row-title { font-size: 0.88rem; font-weight: 600; color: var(--text-primary); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .sub { font-size: 0.72rem; color: var(--text-muted); margin-top: 0.2rem; }
  .dot { margin: 0 0.25rem; }

  .remove {
    width: 1.8rem; height: 1.8rem;
    background: var(--bg-hover);
    border: 1px solid var(--border-strong);
    border-radius: 5px;
    color: var(--text-muted);
    font-size: 1rem;
    cursor: pointer;
    display: flex; align-items: center; justify-content: center;
  }
  .remove:hover { color: #f87171; border-color: rgba(248,113,113,0.3); }

  .error-bar {
    background: var(--error-bg); border: 1px solid var(--error-bg); color: var(--error);
    padding: 0.6rem 0.9rem; border-radius: 8px; font-size: 0.8rem; margin-bottom: 1.5rem;
  }
  .loading { color: var(--text-muted); font-size: 0.85rem; }
  .empty { text-align: center; padding: 4rem 2rem; color: var(--text-muted); }
  .empty-sub { font-size: 0.8rem; color: var(--text-muted); margin-top: 0.5rem; }
</style>
