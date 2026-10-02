<script lang="ts">
  // Photo albums: the caller's own albums (the server scopes /photo-albums
  // to its owner; nobody sees or edits anyone else's), each with its cover
  // (the newest photo) and photo count. Create, rename and delete here;
  // photos are added from the photo viewer or a photo library's Select mode
  // and removed on the album's page.
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { photoAlbumApi, assetUrl, type PhotoAlbum } from '$lib/api';
  import { confirmAction } from '$lib/native';
  import { toast } from '$lib/stores/toast';
  import CardMenu from '$lib/components/CardMenu.svelte';
  import {
    normalizeAlbumName,
    photoCountLabel,
    withCreated,
    withRenamed,
    withoutAlbum,
  } from '$lib/photoAlbums';

  let albums = $state<PhotoAlbum[]>([]);
  let loading = $state(true);
  let error = $state('');

  let showCreate = $state(false);
  let newName = $state('');
  let creating = $state(false);

  // The album being renamed inline, and its draft name.
  let renamingId = $state('');
  let renameDraft = $state('');
  let saving = $state(false);

  function errText(e: unknown, fallback: string): string {
    return e instanceof Error && e.message ? e.message : fallback;
  }

  async function load() {
    loading = true;
    error = '';
    try {
      albums = (await photoAlbumApi.list()) ?? [];
    } catch (e) {
      error = errText(e, 'Could not load albums');
    } finally {
      loading = false;
    }
  }

  async function create(e: SubmitEvent) {
    e.preventDefault();
    const name = normalizeAlbumName(newName);
    if (!name || creating) return;
    creating = true;
    try {
      const created = await photoAlbumApi.create(name);
      albums = withCreated(albums, created);
      newName = '';
      showCreate = false;
      toast.success(`Created "${created.name}"`);
    } catch (err) {
      toast.error(errText(err, 'Could not create the album'));
    } finally {
      creating = false;
    }
  }

  function startRename(a: PhotoAlbum) {
    renamingId = a.id;
    renameDraft = a.name;
  }

  function cancelRename() {
    renamingId = '';
    renameDraft = '';
  }

  async function saveRename(e: SubmitEvent) {
    e.preventDefault();
    const album = albums.find((a) => a.id === renamingId);
    const name = normalizeAlbumName(renameDraft);
    if (!album || !name || saving) return;
    if (name === album.name) { cancelRename(); return; }
    saving = true;
    try {
      const updated = await photoAlbumApi.rename(album.id, name);
      albums = withRenamed(albums, updated ?? { id: album.id, name });
      cancelRename();
    } catch (err) {
      toast.error(errText(err, 'Could not rename the album'));
    } finally {
      saving = false;
    }
  }

  async function remove(a: PhotoAlbum) {
    // The photos stay in their libraries; only the album goes.
    if (!(await confirmAction(`Delete the album "${a.name}"? The photos stay in your library.`))) return;
    try {
      await photoAlbumApi.delete(a.id);
      albums = withoutAlbum(albums, a.id);
      toast.success(`Deleted "${a.name}"`);
    } catch (err) {
      toast.error(errText(err, 'Could not delete the album'));
    }
  }

  function onRenameKey(e: KeyboardEvent) {
    if (e.key === 'Escape') { e.preventDefault(); cancelRename(); }
  }

  function autofocus(node: HTMLInputElement) {
    node.focus();
    node.select();
  }

  onMount(() => {
    if (!localStorage.getItem('onscreen_user')) { goto('/login'); return; }
    load();
  });
</script>

<svelte:head><title>Photo albums — OnScreen</title></svelte:head>

<div class="page">
  <div class="header">
    <h1>Photo albums</h1>
    <a class="head-link" href="/photos/map">Map</a>
    <button type="button" class="btn-create" onclick={() => (showCreate = !showCreate)}>+ New album</button>
  </div>

  {#if showCreate}
    <form class="create-form" onsubmit={create}>
      <input bind:value={newName} placeholder="Album name" aria-label="Album name" use:autofocus />
      <button type="submit" class="btn-save" disabled={creating || !normalizeAlbumName(newName)}>Create</button>
      <button type="button" class="btn-cancel" onclick={() => (showCreate = false)}>Cancel</button>
    </form>
  {/if}

  {#if error}
    <div class="error-bar" role="alert">
      {error}
      <button type="button" class="retry" onclick={load}>Retry</button>
    </div>
  {/if}

  {#if loading}
    <p class="muted" role="status">Loading albums…</p>
  {:else if !error && albums.length === 0}
    <div class="empty">
      <p class="empty-t">No albums yet</p>
      <p class="empty-s">
        Create one here, or add photos with "Add to album" in the photo viewer or Select in a photo library.
      </p>
    </div>
  {:else}
    <div class="grid">
      {#each albums as a (a.id)}
        <div class="card">
          <a class="cover" href="/photos/albums/{a.id}" aria-label="{a.name}, {photoCountLabel(a.item_count)}">
            {#if a.cover_path}
              <img src={assetUrl(`/artwork/${encodeURI(a.cover_path)}?w=300`)} alt="" loading="lazy" />
            {:else}
              <span class="cover-blank" aria-hidden="true">◇</span>
            {/if}
          </a>
          <div class="foot">
            {#if renamingId === a.id}
              <form class="rename-form" onsubmit={saveRename}>
                <input
                  bind:value={renameDraft}
                  aria-label="Album name"
                  onkeydown={onRenameKey}
                  use:autofocus
                />
                <button type="submit" class="btn-save" disabled={saving || !normalizeAlbumName(renameDraft)}>Save</button>
                <button type="button" class="btn-cancel" onclick={cancelRename}>Cancel</button>
              </form>
            {:else}
              <div class="meta">
                <a class="name" href="/photos/albums/{a.id}">{a.name}</a>
                <div class="count">{photoCountLabel(a.item_count)}</div>
              </div>
              <CardMenu
                label="More actions for {a.name}"
                actions={[
                  { label: 'Rename', onSelect: () => startRename(a) },
                  { label: 'Delete', onSelect: () => remove(a) },
                ]}
              />
            {/if}
          </div>
        </div>
      {/each}
    </div>
  {/if}
</div>

<style>
  .page { padding: 2.5rem 2.5rem 5rem; }
  .header { display: flex; align-items: center; gap: 0.75rem; margin-bottom: 1.5rem; }
  h1 { font-size: 1.4rem; font-weight: 800; color: var(--text-primary); letter-spacing: -0.025em; margin-right: auto; }

  .head-link {
    font-size: 0.78rem; color: var(--text-secondary); text-decoration: none;
    padding: 0.3rem 0.55rem; border-radius: 6px; border: 1px solid var(--border);
  }
  .head-link:hover { background: var(--bg-hover); color: var(--text-primary); border-color: var(--border-strong); }

  .btn-create {
    padding: 0.42rem 0.85rem; background: var(--accent-bg);
    border: 1px solid rgba(124,106,247,0.25); border-radius: 7px;
    color: var(--accent-text); font-size: 0.78rem; font-weight: 600; cursor: pointer;
  }
  .btn-create:hover { background: rgba(124,106,247,0.2); }

  .create-form, .rename-form { display: flex; gap: 0.4rem; flex-wrap: wrap; }
  .create-form { margin-bottom: 1.5rem; max-width: 460px; }
  .create-form input, .rename-form input {
    flex: 1; min-width: 120px; background: var(--input-bg);
    border: 1px solid var(--border-strong); border-radius: 6px;
    padding: 0.4rem 0.6rem; color: var(--text-primary); font-size: 0.8rem;
  }
  .create-form input:focus, .rename-form input:focus { outline: none; border-color: var(--accent); }
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
  .error-bar {
    background: var(--error-bg); color: var(--error); padding: 0.6rem 0.9rem;
    border-radius: 8px; font-size: 0.8rem; display: flex; gap: 0.75rem; align-items: center;
    margin-bottom: 1rem;
  }
  .retry {
    background: none; border: 1px solid currentColor; border-radius: 6px; color: inherit;
    font-size: 0.72rem; padding: 0.15rem 0.5rem; cursor: pointer;
  }

  .empty { text-align: center; padding: 3.5rem 1rem; color: var(--text-muted); }
  .empty-t { font-weight: 700; color: var(--text-secondary); }
  .empty-s { font-size: 0.8rem; margin-top: 0.4rem; }

  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(180px, 1fr));
    gap: 1rem;
  }
  /* No overflow: hidden on the card: the ⋯ menu opens above its foot. */
  .card {
    display: flex; flex-direction: column; min-width: 0;
    border-radius: 8px;
    background: var(--input-bg); border: 1px solid var(--border);
    transition: border-color 0.15s;
  }
  .card:hover { border-color: rgba(124,106,247,0.3); }
  .cover {
    display: block; aspect-ratio: 4 / 3; background: var(--bg-elevated);
    border-radius: 7px 7px 0 0; overflow: hidden;
  }
  .cover img { width: 100%; height: 100%; object-fit: cover; display: block; }
  .cover-blank {
    width: 100%; height: 100%; display: flex; align-items: center; justify-content: center;
    font-size: 2rem; color: var(--text-muted);
  }
  .foot { display: flex; align-items: center; gap: 0.25rem; padding: 0.5rem 0.4rem 0.55rem 0.6rem; min-width: 0; }
  .meta { flex: 1; min-width: 0; }
  .name {
    display: block; font-size: 0.82rem; font-weight: 600; color: var(--text-primary); text-decoration: none;
    white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
  }
  .name:hover { color: var(--accent-text); }
  .count { font-size: 0.7rem; color: var(--text-muted); margin-top: 0.1rem; }

  @media (max-width: 768px) {
    .page { padding: 1.25rem 1rem 5rem; }
    .grid { grid-template-columns: repeat(auto-fill, minmax(140px, 1fr)); gap: 0.75rem; }
  }
</style>
