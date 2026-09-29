<script lang="ts">
  // One photo album: its photos newest-taken first (the server filters them
  // by the caller's library access and content-rating ceiling), opening in
  // the photo viewer with Previous / Next walking the album. Rename or
  // delete the album, and Select photos to remove them. There is no single-
  // album GET, so the name and count come from the caller's album list; an
  // album that isn't theirs is simply not found (the server 404s it too).
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { photoAlbumApi, assetUrl, type PhotoAlbum, type PhotoAlbumItem } from '$lib/api';
  import { toast } from '$lib/stores/toast';
  import {
    ALBUM_PAGE_LIMIT,
    bulkMessage,
    normalizeAlbumName,
    photoCountLabel,
    runBulk,
    toggleSelected,
  } from '$lib/photoAlbums';
  import { photoViewerHref } from '$lib/photoViewerContext';

  const albumId = $derived($page.params.id ?? '');

  let album = $state<PhotoAlbum | null>(null);
  let items = $state.raw<PhotoAlbumItem[]>([]);
  let total = $state(0);
  let loading = $state(true);
  let error = $state('');
  let notFound = $state(false);

  let renaming = $state(false);
  let renameDraft = $state('');
  let saving = $state(false);

  let selecting = $state(false);
  let selected = $state.raw<Set<string>>(new Set());
  let removing = $state(false);
  let progress = $state('');

  let seq = 0;

  // The server's total is rating-filtered but not library-filtered, so it
  // can count photos this user can no longer open; the loaded list is the
  // honest count unless it hit the page cap.
  const truncated = $derived(items.length >= ALBUM_PAGE_LIMIT && total > items.length);

  function errText(e: unknown, fallback: string): string {
    return e instanceof Error && e.message ? e.message : fallback;
  }

  function isNotFound(e: unknown): boolean {
    return !!e && typeof e === 'object' && 'status' in e && (e as { status: number }).status === 404;
  }

  async function load(id: string) {
    const mine = ++seq;
    loading = true;
    error = '';
    notFound = false;
    selecting = false;
    selected = new Set();
    try {
      const [albums, list] = await Promise.all([photoAlbumApi.list(), photoAlbumApi.items(id)]);
      if (mine !== seq) return;
      album = (albums ?? []).find((a) => a.id === id) ?? null;
      notFound = !album;
      items = list.items ?? [];
      total = list.total;
    } catch (e) {
      if (mine !== seq) return;
      album = null;
      items = [];
      if (isNotFound(e)) notFound = true;
      else error = errText(e, 'Could not load the album');
    } finally {
      if (mine === seq) loading = false;
    }
  }

  $effect(() => {
    if (!localStorage.getItem('onscreen_user')) { goto('/login'); return; }
    load(albumId);
  });

  function startRename() {
    if (!album) return;
    renameDraft = album.name;
    renaming = true;
  }

  async function saveRename(e: SubmitEvent) {
    e.preventDefault();
    const name = normalizeAlbumName(renameDraft);
    if (!album || !name || saving) return;
    if (name === album.name) { renaming = false; return; }
    saving = true;
    try {
      const updated = await photoAlbumApi.rename(album.id, name);
      album = { ...album, name: updated?.name ?? name };
      renaming = false;
    } catch (err) {
      toast.error(errText(err, 'Could not rename the album'));
    } finally {
      saving = false;
    }
  }

  async function deleteAlbum() {
    if (!album) return;
    if (!confirm(`Delete the album "${album.name}"? The photos stay in your library.`)) return;
    try {
      await photoAlbumApi.delete(album.id);
      toast.success(`Deleted "${album.name}"`);
      goto('/photos/albums');
    } catch (err) {
      toast.error(errText(err, 'Could not delete the album'));
    }
  }

  function toggleSelecting() {
    selecting = !selecting;
    selected = new Set();
  }

  // In Select mode a tile toggles instead of opening the viewer.
  function onTileClick(e: MouseEvent, id: string) {
    if (!selecting) return;
    e.preventDefault();
    selected = toggleSelected(selected, id);
  }

  async function removeSelected() {
    if (!album || selected.size === 0 || removing) return;
    const ids = [...selected];
    const removedIds = new Set<string>();
    removing = true;
    try {
      const r = await runBulk(
        ids,
        async (id) => {
          await photoAlbumApi.removeItem(album!.id, id);
          removedIds.add(id);
        },
        (n, count) => { if (count > 1) progress = `Removing ${n} of ${count}…`; },
      );
      items = items.filter((it) => !removedIds.has(it.id));
      total = Math.max(0, total - removedIds.size);
      album = { ...album, item_count: Math.max(0, album.item_count - removedIds.size) };
      const msg = bulkMessage('remove', r, album.name);
      if (msg.ok) toast.success(msg.message);
      else toast.error(msg.message);
      selected = new Set([...selected].filter((id) => !removedIds.has(id)));
      if (selected.size === 0) selecting = false;
    } finally {
      removing = false;
      progress = '';
    }
  }

  function onRenameKey(e: KeyboardEvent) {
    if (e.key === 'Escape') { e.preventDefault(); renaming = false; }
  }

  function autofocus(node: HTMLInputElement) {
    node.focus();
    node.select();
  }
</script>

<svelte:head><title>{album?.name ?? 'Album'} — OnScreen</title></svelte:head>

<div class="page">
  <nav class="crumb">
    <a href="/photos/albums">Photo albums</a>
    <span>/</span>
    <span>{album?.name ?? '…'}</span>
  </nav>

  {#if loading}
    <p class="muted" role="status">Loading album…</p>
  {:else if notFound}
    <div class="empty">
      <p class="empty-t">Album not found</p>
      <p class="empty-s"><a href="/photos/albums">Back to your albums</a></p>
    </div>
  {:else if error}
    <div class="error-bar" role="alert">
      {error}
      <button type="button" class="retry" onclick={() => load(albumId)}>Retry</button>
    </div>
  {:else if album}
    <div class="head">
      {#if renaming}
        <form class="rename-form" onsubmit={saveRename}>
          <input bind:value={renameDraft} aria-label="Album name" onkeydown={onRenameKey} use:autofocus />
          <button type="submit" class="btn-save" disabled={saving || !normalizeAlbumName(renameDraft)}>Save</button>
          <button type="button" class="btn-cancel" onclick={() => (renaming = false)}>Cancel</button>
        </form>
      {:else}
        <div class="title-block">
          <h1>{album.name}</h1>
          <div class="count">{photoCountLabel(truncated ? total : items.length)}</div>
        </div>
        <div class="actions">
          {#if items.length > 0}
            <button type="button" class="btn" class:on={selecting} aria-pressed={selecting} onclick={toggleSelecting}>
              {selecting ? 'Done' : 'Select'}
            </button>
          {/if}
          <button type="button" class="btn" onclick={startRename}>Rename</button>
          <button type="button" class="btn danger" onclick={deleteAlbum}>Delete</button>
        </div>
      {/if}
    </div>

    {#if items.length === 0}
      <div class="empty">
        <p class="empty-t">This album is empty</p>
        <p class="empty-s">Add photos with "Add to album" in the photo viewer, or Select in a photo library.</p>
      </div>
    {:else}
      <div class="grid" class:selecting>
        {#each items as it (it.id)}
          {@const isSel = selected.has(it.id)}
          <a
            class="tile"
            class:selected={isSel}
            href={photoViewerHref(it.id, { kind: 'album', albumId: album.id })}
            title={it.title}
            aria-label={selecting ? `${isSel ? 'Deselect' : 'Select'} ${it.title}` : it.title}
            onclick={(e) => onTileClick(e, it.id)}
          >
            {#if it.poster_path}
              <img src={assetUrl(`/artwork/${encodeURI(it.poster_path)}?w=300`)} alt="" loading="lazy" />
            {:else}
              <span class="tile-blank" aria-hidden="true">{it.title[0]?.toUpperCase() ?? '◇'}</span>
            {/if}
            {#if selecting}
              <span class="check" aria-hidden="true">{isSel ? '✓' : ''}</span>
            {/if}
          </a>
        {/each}
      </div>
      {#if truncated}
        <p class="muted more">Showing the newest {items.length.toLocaleString()} of {total.toLocaleString()} photos.</p>
      {/if}
    {/if}
  {/if}
</div>

{#if selecting}
  <div class="select-bar" role="toolbar" aria-label="Selected photos">
    <span class="sel-count" role="status">{progress || `${selected.size} selected`}</span>
    <button
      type="button"
      class="btn danger"
      disabled={selected.size === 0 || removing}
      onclick={removeSelected}
    >Remove from album</button>
    <button type="button" class="btn" disabled={removing} onclick={toggleSelecting}>Cancel</button>
  </div>
{/if}

<style>
  .page { padding: 2.5rem 2.5rem 6rem; }
  .crumb {
    display: flex; align-items: center; gap: 0.4rem;
    font-size: 0.75rem; color: var(--text-muted); margin-bottom: 1.5rem;
  }
  .crumb a { color: var(--text-muted); text-decoration: none; }
  .crumb a:hover { color: var(--text-secondary); }

  .head {
    display: flex; align-items: flex-end; justify-content: space-between; gap: 1rem; flex-wrap: wrap;
    margin-bottom: 1.5rem; padding-bottom: 1.25rem; border-bottom: 1px solid var(--border);
  }
  .title-block { min-width: 0; }
  h1 {
    font-size: 1.4rem; font-weight: 800; color: var(--text-primary); letter-spacing: -0.025em;
    word-break: break-word;
  }
  .count { font-size: 0.75rem; color: var(--text-muted); margin-top: 0.25rem; }
  .actions { display: flex; gap: 0.5rem; }

  .btn {
    padding: 0.42rem 0.85rem; background: var(--bg-hover);
    border: 1px solid var(--border-strong); border-radius: 7px;
    color: var(--text-secondary); font-size: 0.78rem; cursor: pointer;
    transition: border-color 0.12s, color 0.12s;
  }
  .btn:hover:not(:disabled) { border-color: var(--accent); color: var(--text-primary); }
  .btn:disabled { opacity: 0.5; cursor: not-allowed; }
  .btn.on { background: var(--accent-bg); border-color: rgba(124,106,247,0.3); color: var(--accent-text); }
  .btn.danger:hover:not(:disabled) { border-color: var(--error); color: var(--error); }

  .rename-form { display: flex; gap: 0.4rem; flex-wrap: wrap; flex: 1; max-width: 520px; }
  .rename-form input {
    flex: 1; min-width: 140px; background: var(--input-bg);
    border: 1px solid var(--border-strong); border-radius: 6px;
    padding: 0.45rem 0.65rem; color: var(--text-primary); font-size: 0.9rem;
  }
  .rename-form input:focus { outline: none; border-color: var(--accent); }
  .btn-save {
    padding: 0.35rem 0.7rem; background: var(--accent); border: none; border-radius: 6px;
    color: #fff; font-size: 0.75rem; font-weight: 600; cursor: pointer;
  }
  .btn-save:disabled { opacity: 0.5; cursor: not-allowed; }
  .btn-cancel {
    padding: 0.35rem 0.6rem; background: var(--input-bg); border: 1px solid var(--border);
    border-radius: 6px; color: var(--text-muted); font-size: 0.75rem; cursor: pointer;
  }

  .muted { color: var(--text-muted); font-size: 0.85rem; }
  .more { margin-top: 1rem; font-size: 0.75rem; }
  .error-bar {
    background: var(--error-bg); color: var(--error); padding: 0.6rem 0.9rem;
    border-radius: 8px; font-size: 0.8rem; display: flex; gap: 0.75rem; align-items: center;
  }
  .retry {
    background: none; border: 1px solid currentColor; border-radius: 6px; color: inherit;
    font-size: 0.72rem; padding: 0.15rem 0.5rem; cursor: pointer;
  }
  .empty { text-align: center; padding: 3.5rem 1rem; color: var(--text-muted); }
  .empty-t { font-weight: 700; color: var(--text-secondary); }
  .empty-s { font-size: 0.8rem; margin-top: 0.4rem; }
  .empty-s a { color: var(--accent-text); }

  /* Same tile shape as a photo library's grid. */
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(180px, 1fr));
    gap: 0.5rem;
  }
  .tile {
    position: relative; display: block; aspect-ratio: 4 / 3;
    border-radius: 8px; overflow: hidden; background: var(--bg-elevated);
  }
  .tile img { width: 100%; height: 100%; object-fit: cover; display: block; transition: transform 0.3s; }
  .tile:hover img { transform: scale(1.04); }
  .tile:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
  .tile-blank {
    width: 100%; height: 100%; display: flex; align-items: center; justify-content: center;
    font-size: 2rem; font-weight: 800; color: var(--text-muted);
  }
  .grid.selecting .tile img { transform: none; }
  .tile.selected { box-shadow: 0 0 0 3px var(--accent); }
  .tile.selected img { opacity: 0.8; }
  .check {
    position: absolute; top: 0.4rem; left: 0.4rem;
    width: 22px; height: 22px; border-radius: 50%;
    display: flex; align-items: center; justify-content: center;
    border: 2px solid #fff; background: rgba(0,0,0,0.35);
    color: #fff; font-size: 0.75rem; font-weight: 800;
  }
  .tile.selected .check { background: var(--accent); border-color: var(--accent); }

  .select-bar {
    position: fixed; left: 50%; bottom: 1.25rem; transform: translateX(-50%);
    z-index: 950;
    display: flex; align-items: center; gap: 0.6rem;
    padding: 0.55rem 0.7rem 0.55rem 1rem;
    background: var(--bg-elevated); border: 1px solid var(--border-strong);
    border-radius: 12px; box-shadow: 0 12px 32px var(--shadow);
  }
  .sel-count { font-size: 0.8rem; color: var(--text-secondary); margin-right: 0.4rem; white-space: nowrap; }

  @media (max-width: 768px) {
    .page { padding: 1.25rem 1rem 8rem; }
    .grid { grid-template-columns: repeat(auto-fill, minmax(110px, 1fr)); gap: 0.35rem; }
    .select-bar { bottom: 72px; max-width: calc(100% - 1.5rem); }
  }
</style>
