<script lang="ts">
  import { createEventDispatcher } from 'svelte';
  import { playlistApi, type Playlist } from '$lib/api';
  import { t, i18n } from '$lib/i18n/index.svelte';

  export let mediaItemId: string;
  export let open = false;

  const dispatch = createEventDispatcher<{ close: void; added: { playlistId: string; name: string } }>();

  let playlists: Playlist[] = [];
  let loading = true;
  let adding: string | null = null;
  let error = '';
  let success = '';

  let showCreate = false;
  let newName = '';
  let creating = false;

  $: if (open) loadPlaylists();

  async function loadPlaylists() {
    loading = true;
    error = '';
    success = '';
    try {
      playlists = await playlistApi.list();
    } catch { playlists = []; }
    finally { loading = false; }
  }

  async function addTo(pl: Playlist) {
    adding = pl.id;
    error = '';
    success = '';
    try {
      await playlistApi.addItem(pl.id, mediaItemId);
      success = t('ui.playlistPicker.added', { name: pl.name });
      dispatch('added', { playlistId: pl.id, name: pl.name });
      setTimeout(() => { dispatch('close'); }, 800);
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : t('ui.collectionPicker.addFailed');
    } finally { adding = null; }
  }

  async function createAndAdd() {
    if (!newName.trim()) return;
    creating = true;
    error = '';
    try {
      const pl = await playlistApi.create(newName.trim());
      await playlistApi.addItem(pl.id, mediaItemId);
      success = t('ui.playlistPicker.created', { name: pl.name });
      dispatch('added', { playlistId: pl.id, name: pl.name });
      newName = '';
      showCreate = false;
      setTimeout(() => { dispatch('close'); }, 800);
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : t('ui.collectionPicker.failed');
    } finally { creating = false; }
  }

  function close() {
    dispatch('close');
  }
</script>

{#if open}
  <!-- svelte-ignore a11y-click-events-have-key-events -->
  <!-- svelte-ignore a11y-no-static-element-interactions -->
  <div class="backdrop" on:click={close}>
    <!-- svelte-ignore a11y-click-events-have-key-events -->
    <!-- svelte-ignore a11y-no-static-element-interactions -->
    <div class="panel" on:click|stopPropagation>
      <div class="panel-header">
        <span>{$i18n('ui.playlistPicker.title')}</span>
        <button class="close-btn" on:click={close}>×</button>
      </div>

      {#if error}
        <div class="msg error">{error}</div>
      {/if}
      {#if success}
        <div class="msg success">{success}</div>
      {/if}

      {#if loading}
        <div class="loading">{$i18n('ui.playlistPicker.loading')}</div>
      {:else}
        <div class="list">
          {#each playlists as pl (pl.id)}
            <button
              class="playlist-row"
              disabled={adding === pl.id}
              on:click={() => addTo(pl)}
            >
              <span class="playlist-icon">&#9835;</span>
              <span class="playlist-name">{pl.name}</span>
              {#if adding === pl.id}
                <span class="adding">...</span>
              {/if}
            </button>
          {/each}
          {#if playlists.length === 0 && !showCreate}
            <div class="empty">{$i18n('ui.playlistPicker.empty')}</div>
          {/if}
        </div>

        {#if showCreate}
          <form class="create-row" on:submit|preventDefault={createAndAdd}>
            <input bind:value={newName} placeholder={$i18n('ui.playlistPicker.newName')} autofocus />
            <button type="submit" class="btn-go" disabled={creating || !newName.trim()}>{$i18n('ui.collectionPicker.createAdd')}</button>
            <button type="button" class="btn-x" on:click={() => showCreate = false}>{$i18n('player.cancel')}</button>
          </form>
        {:else}
          <button class="new-btn" on:click={() => showCreate = true}>+ {$i18n('ui.playlistPicker.new')}</button>
        {/if}
      {/if}
    </div>
  </div>
{/if}

<style>
  .backdrop {
    position: fixed; inset: 0; background: var(--shadow);
    display: flex; align-items: center; justify-content: center;
    z-index: 2000; animation: fadeIn 0.1s ease-out;
  }
  @keyframes fadeIn { from { opacity: 0; } to { opacity: 1; } }

  .panel {
    background: var(--bg-elevated); border: 1px solid var(--border);
    border-radius: 12px; width: 320px; max-height: 420px; overflow-y: auto;
    box-shadow: 0 20px 60px var(--shadow);
  }

  .panel-header {
    display: flex; align-items: center; justify-content: space-between;
    padding: 0.8rem 1rem; border-bottom: 1px solid var(--border);
    font-size: 0.85rem; font-weight: 600; color: var(--text-primary);
  }
  .close-btn {
    background: none; border: none; color: var(--text-muted); font-size: 1.1rem;
    cursor: pointer; padding: 0 0.2rem; line-height: 1;
  }
  .close-btn:hover { color: var(--text-secondary); }

  .msg {
    padding: 0.5rem 1rem; font-size: 0.75rem;
  }
  .msg.error { color: #fca5a5; background: rgba(248,113,113,0.08); }
  .msg.success { color: #86efac; background: rgba(134,239,172,0.08); }

  .list { padding: 0.3rem; }

  .playlist-row {
    display: flex; align-items: center; gap: 0.6rem; width: 100%;
    padding: 0.5rem 0.7rem; background: none; border: none;
    border-radius: 7px; color: var(--text-primary); font-size: 0.82rem; cursor: pointer;
    text-align: left; transition: background 0.1s;
  }
  .playlist-row:hover { background: var(--bg-hover); }
  .playlist-row:disabled { opacity: 0.5; cursor: wait; }
  .playlist-icon { color: var(--accent); font-size: 1rem; }
  .playlist-name { flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .adding { color: var(--accent); font-size: 0.72rem; }

  .empty { padding: 1rem; text-align: center; color: var(--text-muted); font-size: 0.78rem; }

  .new-btn {
    display: block; width: calc(100% - 0.6rem); margin: 0.3rem; padding: 0.45rem;
    background: rgba(124,106,247,0.08); border: 1px dashed rgba(124,106,247,0.25);
    border-radius: 7px; color: var(--accent-text); font-size: 0.78rem; font-weight: 600;
    cursor: pointer; text-align: center;
  }
  .new-btn:hover { background: rgba(124,106,247,0.15); }

  .create-row {
    display: flex; gap: 0.4rem; padding: 0.5rem; flex-wrap: wrap;
  }
  .create-row input {
    flex: 1; min-width: 120px; background: var(--input-bg);
    border: 1px solid var(--border-strong); border-radius: 6px;
    padding: 0.35rem 0.6rem; color: var(--text-primary); font-size: 0.8rem;
  }
  .create-row input:focus { outline: none; border-color: var(--accent); }
  .btn-go {
    padding: 0.35rem 0.6rem; background: var(--accent); border: none; border-radius: 6px;
    color: #fff; font-size: 0.72rem; font-weight: 600; cursor: pointer; white-space: nowrap;
  }
  .btn-go:disabled { opacity: 0.5; cursor: not-allowed; }
  .btn-x {
    padding: 0.35rem 0.5rem; background: var(--input-bg);
    border: 1px solid var(--border); border-radius: 6px;
    color: #66667a; font-size: 0.72rem; cursor: pointer;
  }

  .loading { padding: 1rem; text-align: center; color: var(--text-muted); font-size: 0.8rem; }
</style>
