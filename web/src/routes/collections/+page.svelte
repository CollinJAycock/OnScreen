<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { collectionApi, assetUrl, collectionPosterUrl, type Collection } from '$lib/api';
  import { confirmAction } from '$lib/native';
  import { t, tr } from '$lib/i18n/index.svelte';

  let collections: Collection[] = [];
  let loading = true;
  let error = '';
  let isAdmin = false;

  // Create a playlist (anyone) or a shared collection (admins).
  let showCreate = false;
  let createKind: 'playlist' | 'manual' = 'playlist';
  let newName = '';
  let creating = false;

  onMount(async () => {
    const raw = localStorage.getItem('onscreen_user');
    if (!raw) { goto('/login'); return; }
    try { isAdmin = !!JSON.parse(raw)?.is_admin; } catch { /* keep false */ }
    await load();
  });

  function openCreate(kind: 'playlist' | 'manual') {
    showCreate = !(showCreate && createKind === kind);
    createKind = kind;
  }

  async function load() {
    loading = true;
    // Franchises are opt-in on GET /collections; this page's "Film series"
    // section is where they live.
    try { collections = await collectionApi.list({ includeFranchise: true }); }
    catch (e: unknown) { error = e instanceof Error ? e.message : t('collections.failed'); }
    finally { loading = false; }
  }

  async function createPlaylist() {
    if (!newName.trim()) return;
    creating = true;
    try {
      if (createKind === 'manual') {
        // Straight to the new collection: it's filled from item pages and
        // library selections, and its page explains how.
        const c = await collectionApi.createManual(newName.trim());
        goto(`/collections/${c.id}`);
        return;
      }
      await collectionApi.create(newName.trim());
      newName = '';
      showCreate = false;
      await load();
    } catch (e: unknown) { error = e instanceof Error ? e.message : t('collections.failed'); }
    finally { creating = false; }
  }

  async function deleteCollection(id: string) {
    if (!(await confirmAction(t('collections.deleteConfirm')))) return;
    try {
      await collectionApi.delete(id);
      collections = collections.filter(c => c.id !== id);
    } catch (e: unknown) { error = e instanceof Error ? e.message : t('collections.failed'); }
  }

  $: genreCollections = collections.filter(c => c.type === 'auto_genre');
  // TMDB film series the server holds at least two films of.
  $: franchises = collections.filter(c => c.type === 'franchise');
  $: playlists = collections.filter(c => c.type === 'playlist');
  // Shared, admin-curated movie / TV collections.
  $: manualCollections = collections.filter(c => c.type === 'manual');
</script>

<svelte:head><title>{$tr('collections.pageTitle')}</title></svelte:head>

<div class="page">
  <div class="header">
    <h1>{$tr('collections.title')}</h1>
    <div class="header-actions">
      {#if isAdmin}
        <button class="btn-create" on:click={() => openCreate('manual')}>{$tr('collections.newCollection')}</button>
      {/if}
      <button class="btn-create" on:click={() => openCreate('playlist')}>{$tr('collections.newPlaylist')}</button>
    </div>
  </div>

  {#if error}
    <div class="error-bar">{error}</div>
  {/if}

  {#if showCreate}
    <form class="create-form" on:submit|preventDefault={createPlaylist}>
      <!-- svelte-ignore a11y-autofocus -->
      <input bind:value={newName} autofocus
             placeholder={createKind === 'manual' ? $tr('collections.collectionName') : $tr('collections.playlistName')}
             aria-label={createKind === 'manual' ? $tr('collections.collectionName') : $tr('collections.playlistName')} />
      <button type="submit" class="btn-save" disabled={creating || !newName.trim()}>{$tr('browse.create')}</button>
      <button type="button" class="btn-cancel" on:click={() => showCreate = false}>{$tr('browse.cancel')}</button>
      {#if createKind === 'manual'}
        <span class="create-hint">{$tr('collections.sharedHint')}</span>
      {/if}
    </form>
  {/if}

  {#if loading}
    <div class="loading">{$tr('browse.loading')}</div>
  {:else}
    {#if manualCollections.length > 0}
      <section>
        <h2>{$tr('collections.sectionCollections')}</h2>
        <div class="grid">
          {#each manualCollections as col (col.id)}
            <a class="card franchise" href="/collections/{col.id}" title={col.name}>
              {#if collectionPosterUrl(col)}
                <img class="franchise-poster" src={collectionPosterUrl(col)} alt={col.name} loading="lazy" />
              {:else if col.poster_path}
                <img class="franchise-poster" src={assetUrl(`/artwork/${encodeURI(col.poster_path)}?w=300`)} alt={col.name} loading="lazy" />
              {:else}
                <div class="card-icon">&#9638;</div>
              {/if}
              <div class="card-name">{col.name}{#if col.rules}<span class="smart-tag">{$tr('collections.smart')}</span>{/if}</div>
              {#if col.item_count !== undefined}
                <div class="card-sub">{$tr.n('collections.titles', col.item_count)}</div>
              {/if}
            </a>
          {/each}
        </div>
      </section>
    {/if}

    {#if playlists.length > 0}
      <section>
        <h2>{$tr('collections.playlists')}</h2>
        <div class="grid">
          {#each playlists as col (col.id)}
            <a class="card" href="/collections/{col.id}">
              <div class="card-icon">&#9835;</div>
              <div class="card-name">{col.name}</div>
              <button class="card-delete" title={$tr('browse.delete')} on:click|preventDefault|stopPropagation={() => deleteCollection(col.id)}>×</button>
            </a>
          {/each}
        </div>
      </section>
    {/if}

    {#if franchises.length > 0}
      <section>
        <h2>{$tr('collections.filmSeries')}</h2>
        <div class="grid">
          {#each franchises as col (col.id)}
            <a class="card franchise" href="/collections/{col.id}" title={col.name}>
              {#if col.poster_url}
                <img class="franchise-poster" src={col.poster_url} alt={col.name} loading="lazy" referrerpolicy="no-referrer" />
              {:else}
                <div class="card-icon">&#9707;</div>
              {/if}
              <div class="card-name">{col.name}</div>
            </a>
          {/each}
        </div>
      </section>
    {/if}

    {#if genreCollections.length > 0}
      <section>
        <h2>{$tr('collections.genres')}</h2>
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
        <p>{$tr('collections.empty')}</p>
        <p class="empty-sub">{$tr('collections.emptyHint')}</p>
      </div>
    {/if}
  {/if}
</div>

<style>
  .page { padding: 2.5rem; }
  .header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 2rem; }
  h1 { font-size: 1.4rem; font-weight: 800; color: var(--text-primary); }
  h2 { font-size: 0.9rem; font-weight: 700; color: var(--text-muted); text-transform: uppercase; letter-spacing: 0.06em; margin-bottom: 1rem; }
  section { margin-bottom: 2.5rem; }

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
  .header-actions { display: flex; gap: 0.5rem; }
  .create-hint { font-size: 0.72rem; color: var(--text-muted); }
  .card-sub { font-size: 0.7rem; color: var(--text-muted); margin-top: 0.2rem; }

  .create-form {
    display: flex; gap: 0.5rem; align-items: center; margin-bottom: 1.5rem;
  }
  .create-form input {
    background: var(--bg-hover);
    border: 1px solid var(--border-strong);
    border-radius: 7px;
    padding: 0.42rem 0.75rem;
    color: var(--text-primary);
    font-size: 0.85rem;
    flex: 1;
    max-width: 300px;
  }
  .create-form input:focus { outline: none; border-color: var(--accent); }
  .btn-save {
    padding: 0.42rem 0.85rem;
    background: var(--accent);
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
    background: var(--bg-hover);
    border: 1px solid var(--border-strong);
    border-radius: 7px;
    color: var(--text-muted);
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
    background: var(--input-bg);
    border: 1px solid var(--border);
    border-radius: 10px;
    text-decoration: none;
    color: inherit;
    position: relative;
    transition: border-color 0.15s, background 0.15s;
  }
  .card:hover { border-color: rgba(124,106,247,0.3); background: rgba(124,106,247,0.05); }

  .card-icon { font-size: 1.5rem; margin-bottom: 0.5rem; color: var(--accent); }
  .card-name { font-size: 0.82rem; font-weight: 600; color: var(--text-secondary); text-align: center; }
  .smart-tag { margin-left: 0.35rem; font-size: 0.6rem; font-weight: 700; text-transform: uppercase; color: var(--accent); }

  .card.genre { padding: 1rem; }
  .card.franchise { padding: 0 0 0.75rem; overflow: hidden; justify-content: flex-start; }
  .card.franchise .card-icon { margin: 1.5rem 0 0.5rem; }
  .franchise-poster { width: 100%; aspect-ratio: 2/3; object-fit: cover; display: block; margin-bottom: 0.6rem; }
  .card.genre .card-name { color: var(--text-secondary); }

  .card-delete {
    position: absolute;
    top: 0.4rem;
    right: 0.4rem;
    background: none;
    border: none;
    color: var(--text-muted);
    font-size: 1rem;
    cursor: pointer;
    opacity: 0;
    transition: opacity 0.15s, color 0.15s;
  }
  .card:hover .card-delete { opacity: 1; }
  .card-delete:hover { color: #f87171; }

  .error-bar {
    background: var(--error-bg);
    border: 1px solid var(--error-bg);
    color: var(--error);
    padding: 0.6rem 0.9rem;
    border-radius: 8px;
    font-size: 0.8rem;
    margin-bottom: 1.5rem;
  }

  .loading { color: var(--text-muted); font-size: 0.85rem; }
  .empty { text-align: center; padding: 4rem 2rem; color: var(--text-muted); }
  .empty-sub { font-size: 0.8rem; color: var(--text-muted); margin-top: 0.5rem; }
</style>
