<script lang="ts">
  // "Add to album…" dialog: lists the caller's photo albums (albums are
  // owner-only, so these are the only ones they can add to) and adds the
  // given photos to the one picked, or to a new album named here. Used by
  // the photo viewer (one photo) and photo-grid multi-select (several).
  // Results are reported as toasts; the dialog closes once the add is done.
  import { photoAlbumApi, type PhotoAlbum } from '$lib/api';
  import { toast } from '$lib/stores/toast';
  import { bulkMessage, normalizeAlbumName, photoCountLabel, runBulk } from '$lib/photoAlbums';

  interface Props {
    open: boolean;
    mediaItemIds: string[];
    onclose: () => void;
    /** After an add that reached the server (fully or partly). */
    ondone?: (album: { id: string; name: string }) => void;
  }

  let { open, mediaItemIds, onclose, ondone }: Props = $props();

  let albums = $state<PhotoAlbum[]>([]);
  let loading = $state(true);
  let loadError = $state('');
  let busy = $state(false);
  let progress = $state('');
  let showCreate = $state(false);
  let newName = $state('');
  let seq = 0;

  const count = $derived(new Set(mediaItemIds).size);

  async function load() {
    const mine = ++seq;
    loading = true;
    loadError = '';
    showCreate = false;
    newName = '';
    progress = '';
    try {
      const rows = await photoAlbumApi.list();
      if (mine !== seq) return;
      albums = rows ?? [];
    } catch (e) {
      if (mine !== seq) return;
      albums = [];
      loadError = e instanceof Error && e.message ? e.message : 'Could not load albums';
    } finally {
      if (mine === seq) loading = false;
    }
  }

  $effect(() => {
    if (open) load();
  });

  async function addTo(album: { id: string; name: string }) {
    if (busy || count === 0) return;
    busy = true;
    try {
      const r = await runBulk(
        mediaItemIds,
        (id) => photoAlbumApi.addItem(album.id, id),
        (n, total) => { if (total > 1) progress = `Adding ${n} of ${total}…`; },
      );
      const msg = bulkMessage('add', r, album.name);
      if (msg.ok) toast.success(msg.message);
      else toast.error(msg.message);
      if (r.done > 0) ondone?.(album);
      onclose();
    } finally {
      busy = false;
      progress = '';
    }
  }

  async function createAndAdd(e: SubmitEvent) {
    e.preventDefault();
    const name = normalizeAlbumName(newName);
    if (!name || busy) return;
    busy = true;
    let created: PhotoAlbum;
    try {
      created = await photoAlbumApi.create(name);
    } catch (err) {
      toast.error(err instanceof Error && err.message ? err.message : 'Could not create the album');
      busy = false;
      return;
    }
    busy = false;
    await addTo(created);
  }

  function onKey(e: KeyboardEvent) {
    if (open && e.key === 'Escape' && !busy) {
      e.preventDefault();
      onclose();
    }
  }

  function autofocus(node: HTMLInputElement) {
    node.focus();
  }
</script>

<svelte:window onkeydown={onKey} />

{#if open}
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div class="backdrop" onclick={() => { if (!busy) onclose(); }}>
    <!-- svelte-ignore a11y_click_events_have_key_events -->
    <div
      class="panel"
      role="dialog"
      aria-modal="true"
      aria-labelledby="album-picker-title"
      tabindex="-1"
      onclick={(e) => e.stopPropagation()}
    >
      <div class="panel-header">
        <span id="album-picker-title">
          Add {count === 1 ? 'photo' : photoCountLabel(count)} to album
        </span>
        <button type="button" class="close-btn" aria-label="Close" disabled={busy} onclick={onclose}>×</button>
      </div>

      {#if progress}
        <div class="msg" role="status">{progress}</div>
      {/if}

      {#if loading}
        <div class="loading">Loading albums…</div>
      {:else if loadError}
        <div class="msg error" role="alert">{loadError}</div>
      {:else}
        <div class="list">
          {#each albums as a (a.id)}
            <button type="button" class="album-row" disabled={busy} onclick={() => addTo(a)}>
              <span class="album-name">{a.name}</span>
              <span class="album-count">{a.item_count}</span>
            </button>
          {/each}
          {#if albums.length === 0 && !showCreate}
            <div class="empty">No albums yet. Create one below.</div>
          {/if}
        </div>
      {/if}

      {#if !loading}
        {#if showCreate}
          <form class="create-row" onsubmit={createAndAdd}>
            <input bind:value={newName} placeholder="New album name" aria-label="New album name" use:autofocus />
            <button type="submit" class="btn-go" disabled={busy || !normalizeAlbumName(newName)}>Create &amp; add</button>
            <button type="button" class="btn-x" disabled={busy} onclick={() => (showCreate = false)}>Cancel</button>
          </form>
        {:else}
          <button type="button" class="new-btn" disabled={busy} onclick={() => (showCreate = true)}>+ New album</button>
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
    border-radius: 12px; width: 320px; max-width: calc(100vw - 2rem); max-height: 420px; overflow-y: auto;
    box-shadow: 0 20px 60px var(--shadow);
    color: var(--text-primary);
  }
  .panel:focus { outline: none; }

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

  .msg { padding: 0.5rem 1rem; font-size: 0.75rem; color: var(--text-secondary); }
  .msg.error { color: var(--error); background: var(--error-bg); }

  .list { padding: 0.3rem; }
  .album-row {
    display: flex; align-items: center; gap: 0.6rem; width: 100%;
    padding: 0.5rem 0.7rem; background: none; border: none;
    border-radius: 7px; color: var(--text-primary); font-size: 0.82rem; cursor: pointer;
    text-align: left; transition: background 0.1s;
  }
  .album-row:hover:not(:disabled) { background: var(--bg-hover); }
  .album-row:disabled { opacity: 0.5; cursor: wait; }
  .album-name { flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .album-count { font-size: 0.7rem; color: var(--text-muted); font-variant-numeric: tabular-nums; }

  .empty { padding: 1rem; text-align: center; color: var(--text-muted); font-size: 0.78rem; }
  .loading { padding: 1rem; text-align: center; color: var(--text-muted); font-size: 0.8rem; }

  .new-btn {
    display: block; width: calc(100% - 0.6rem); margin: 0.3rem; padding: 0.45rem;
    background: rgba(124,106,247,0.08); border: 1px dashed rgba(124,106,247,0.25);
    border-radius: 7px; color: var(--accent-text); font-size: 0.78rem; font-weight: 600;
    cursor: pointer; text-align: center;
  }
  .new-btn:hover:not(:disabled) { background: rgba(124,106,247,0.15); }

  .create-row { display: flex; gap: 0.4rem; padding: 0.5rem; flex-wrap: wrap; }
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
    color: var(--text-muted); font-size: 0.72rem; cursor: pointer;
  }
</style>
