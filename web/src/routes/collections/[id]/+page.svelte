<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/stores';
  import { goto } from '$app/navigation';
  import { collectionApi, assetUrl, collectionPosterUrl, type Collection, type CollectionItem, type CollectionItemOrder, type CollectionRules } from '$lib/api';
  import { confirmAction } from '$lib/native';
  import { describeRules } from '$lib/collectionRules';
  import { fitCoverImage } from '$lib/coverImage';
  import CollectionRulesEditor from '$lib/components/CollectionRulesEditor.svelte';
  import FranchiseParts from './FranchiseParts.svelte';

  let collection: Collection | null = null;
  let items: CollectionItem[] = [];
  let total = 0;
  let loading = true;
  let error = '';
  let isAdmin = false;

  // Edit state
  let editing = false;
  let editName = '';
  let editDescription = '';
  let busy = false;

  $: id = $page.params.id!;
  $: manual = collection?.type === 'manual';
  // Playlists are their owner's; manual collections are edited by admins.
  $: canEdit = collection?.type === 'playlist' || (manual && isAdmin);
  // A smart collection's members follow its rules: no hand edits.
  $: smart = manual && !!collection?.rules;
  $: canEditMembers = canEdit && !smart;
  $: customOrder = manual && !smart && (collection?.item_order ?? 'custom') === 'custom';
  $: uploadedCover = collection ? collectionPosterUrl(collection) : null;
  let editingRules = false;
  let coverInput: HTMLInputElement;

  const orderLabels: Record<CollectionItemOrder, string> = {
    custom: 'Custom order',
    release: 'Release date',
    title: 'Title',
  };

  onMount(async () => {
    const raw = localStorage.getItem('onscreen_user');
    if (!raw) { goto('/login'); return; }
    try { isAdmin = !!JSON.parse(raw)?.is_admin; } catch { /* keep false */ }
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
      collection = manual
        ? await collectionApi.updateSettings(id, { name: editName.trim(), description: editDescription.trim() })
        : await collectionApi.update(id, editName.trim());
      editing = false;
    } catch (e: unknown) { error = e instanceof Error ? e.message : 'Failed'; }
  }

  async function removeItem(itemId: string) {
    try {
      await collectionApi.removeItem(id, itemId);
      items = items.filter(i => i.id !== itemId);
      total--;
      if (manual && collection?.poster_item_id === itemId) {
        collection = await collectionApi.get(id);
      }
    } catch (e: unknown) { error = e instanceof Error ? e.message : 'Failed'; }
  }

  function startEdit() {
    if (!collection) return;
    editName = collection.name;
    editDescription = collection.description ?? '';
    editing = true;
  }

  async function setOrder(order: CollectionItemOrder) {
    if (!collection || busy) return;
    busy = true;
    try {
      collection = await collectionApi.updateSettings(id, { item_order: order });
      const res = await collectionApi.items(id, 200, 0);
      items = res.items;
      total = res.total;
    } catch (e: unknown) { error = e instanceof Error ? e.message : 'Failed'; }
    finally { busy = false; }
  }

  async function setCover(itemId: string) {
    if (!collection || busy) return;
    busy = true;
    try {
      // Choosing the current cover again goes back to the default.
      const next = collection.poster_item_id === itemId ? '' : itemId;
      collection = await collectionApi.updateSettings(id, { poster_item_id: next });
    } catch (e: unknown) { error = e instanceof Error ? e.message : 'Failed'; }
    finally { busy = false; }
  }

  /** Moves a member one place in the custom order. */
  async function move(index: number, delta: number) {
    const target = index + delta;
    if (target < 0 || target >= items.length || busy) return;
    const prev = items;
    const next = [...items];
    [next[index], next[target]] = [next[target], next[index]];
    items = next;
    busy = true;
    try {
      await collectionApi.reorder(id, items.map(i => i.id));
    } catch (e: unknown) {
      items = prev;
      error = e instanceof Error ? e.message : 'Failed';
    } finally { busy = false; }
  }

  async function reloadItems() {
    const res = await collectionApi.items(id, 200, 0);
    items = res.items;
    total = res.total;
  }

  async function setPromoted(on: boolean) {
    if (!collection || busy) return;
    busy = true;
    try {
      collection = await collectionApi.updateSettings(id, { promoted: on });
    } catch (e: unknown) { error = e instanceof Error ? e.message : 'Failed'; }
    finally { busy = false; }
  }

  async function saveRules(rules: CollectionRules) {
    if (!collection || busy) return;
    busy = true;
    try {
      collection = await collectionApi.updateSettings(id, { rules });
      editingRules = false;
      await reloadItems();
    } catch (e: unknown) { error = e instanceof Error ? e.message : 'Failed'; }
    finally { busy = false; }
  }

  async function stopSmart() {
    if (!collection || busy) return;
    if (!(await confirmAction('Stop updating this collection from its rules? Its current movies and shows stay, and you can edit them by hand.'))) return;
    busy = true;
    try {
      collection = await collectionApi.updateSettings(id, { rules: null });
    } catch (e: unknown) { error = e instanceof Error ? e.message : 'Failed'; }
    finally { busy = false; }
  }

  async function uploadCover(e: Event) {
    const input = e.currentTarget as HTMLInputElement;
    const file = input.files?.[0];
    input.value = '';
    if (!file || !collection || busy) return;
    busy = true;
    try {
      const jpeg = await fitCoverImage(file);
      collection = await collectionApi.uploadPoster(id, jpeg);
    } catch (e: unknown) { error = e instanceof Error ? e.message : 'Could not upload the cover'; }
    finally { busy = false; }
  }

  async function removeCover() {
    if (!collection || busy) return;
    busy = true;
    try {
      await collectionApi.deletePoster(id);
      collection = await collectionApi.get(id);
    } catch (e: unknown) { error = e instanceof Error ? e.message : 'Failed'; }
    finally { busy = false; }
  }

  async function deleteCollection() {
    if (!collection) return;
    if (!(await confirmAction(`Delete the collection "${collection.name}"? Its movies and shows stay in your libraries.`))) return;
    try {
      await collectionApi.delete(id);
      goto('/collections');
    } catch (e: unknown) { error = e instanceof Error ? e.message : 'Failed'; }
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
          <!-- svelte-ignore a11y-autofocus -->
          <input bind:value={editName} aria-label="Name" autofocus />
          {#if manual}
            <input class="desc-input" bind:value={editDescription} placeholder="Description" aria-label="Description" />
          {/if}
          <button type="submit" class="btn-save">Save</button>
          <button type="button" class="btn-cancel" on:click={() => editing = false}>Cancel</button>
        </form>
      {:else}
        <h1>{collection.name}</h1>
        {#if canEdit}
          <button class="btn-edit" on:click={startEdit}>Edit</button>
        {/if}
        {#if manual && isAdmin}
          <button class="btn-edit danger" on:click={deleteCollection}>Delete</button>
        {/if}
      {/if}
      <span class="count">{total} item{total !== 1 ? 's' : ''}</span>
    </div>

    {#if error}
      <div class="error-bar">{error}</div>
    {/if}

    {#if manual}
      {#if collection.description && !editing}<p class="overview">{collection.description}</p>{/if}
      {#if smart && collection.rules}
        <p class="smart-line"><span class="smart-badge">Smart</span> {describeRules(collection.rules)} — updates on its own as your libraries change.</p>
      {/if}
      {#if collection.source === 'nfo'}
        <p class="smart-line"><span class="smart-badge nfo">NFO</span> Imported from movie.nfo files; movies whose NFO names it join automatically.</p>
      {/if}
      {#if isAdmin}
        <div class="toolbar">
          <label class="sort">
            <span>Sort by</span>
            <select value={collection.item_order ?? 'custom'} disabled={busy}
                    on:change={(e) => setOrder(e.currentTarget.value as CollectionItemOrder)}>
              {#each Object.entries(orderLabels) as [value, label]}
                <option {value}>{smart && value === 'custom' ? 'Rules order' : label}</option>
              {/each}
            </select>
          </label>
          <label class="sort">
            <input type="checkbox" checked={!!collection.promoted} disabled={busy}
                   on:change={(e) => setPromoted(e.currentTarget.checked)} />
            <span>Show on Home</span>
          </label>
          <span class="cover-ctl">
            {#if uploadedCover}
              <img class="cover-thumb" src={uploadedCover} alt="Cover" />
            {/if}
            <button class="btn-edit" disabled={busy} on:click={() => coverInput.click()}>
              {uploadedCover ? 'Replace cover' : 'Upload cover'}
            </button>
            {#if uploadedCover}
              <button class="btn-edit" disabled={busy} on:click={removeCover}>Remove cover</button>
            {/if}
            <input bind:this={coverInput} type="file" accept="image/*" hidden on:change={uploadCover} />
          </span>
          {#if smart}
            <button class="btn-edit" disabled={busy} on:click={() => editingRules = !editingRules}>Edit rules</button>
            <button class="btn-edit" disabled={busy} on:click={stopSmart}>Stop smart updates</button>
          {:else}
            <button class="btn-edit" disabled={busy} on:click={() => editingRules = !editingRules}>Make smart</button>
          {/if}
          <span class="hint">
            {#if smart}
              ★ makes a poster the cover.
            {:else}
              Add movies and shows from their page or a library's selection. ★ makes a poster the cover.
            {/if}
          </span>
        </div>
        {#if editingRules}
          <CollectionRulesEditor rules={collection.rules} {busy}
                                 submitLabel={smart ? 'Save rules' : 'Make smart'}
                                 on:save={(e) => saveRules(e.detail)}
                                 on:cancel={() => editingRules = false} />
          {#if !smart}
            <p class="hint rules-warn">A smart collection's members come from its rules: the current ones are replaced.</p>
          {/if}
        {/if}
      {/if}
    {/if}

    {#if collection.type === 'franchise'}
      <!-- TMDB franchise: every film of the series, owned or not; missing
           ones can be requested. -->
      {#if collection.description}<p class="overview">{collection.description}</p>{/if}
      <FranchiseParts parts={collection.parts} {items} />
    {:else if items.length === 0}
      <div class="empty">
        <p>This collection is empty.</p>
        {#if collection.type === 'playlist'}
          <p class="empty-sub">Add items from the library or player page.</p>
        {:else if manual && isAdmin}
          <p class="empty-sub">Add movies and shows with "Add to collection" on their page or a library selection.</p>
        {/if}
      </div>
    {:else}
      <div class="grid">
        {#each items as item, i (item.id)}
          <a class="card" href="/watch/{item.id}">
            {#if item.poster_path}
              <img class="poster" src={assetUrl(`/artwork/${encodeURI(item.poster_path)}?w=300`)}
                   srcset="{assetUrl(`/artwork/${encodeURI(item.poster_path)}?w=150`)} 150w, {assetUrl(`/artwork/${encodeURI(item.poster_path)}?w=300`)} 300w, {assetUrl(`/artwork/${encodeURI(item.poster_path)}?w=450`)} 450w"
                   sizes="(max-width: 768px) 100px, 180px"
                   alt={item.title} loading="lazy" />
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
            {#if canEditMembers}
              <button class="remove" title="Remove" aria-label="Remove {item.title}"
                      on:click|preventDefault|stopPropagation={() => removeItem(item.id)}>×</button>
            {/if}
            {#if manual && isAdmin}
              <div class="admin-ctl">
                <button class="ctl" class:on={collection.poster_item_id === item.id} disabled={busy}
                        title={collection.poster_item_id === item.id ? 'Cover (click to use the default)' : 'Use as cover'}
                        aria-label="Use {item.title} as the cover" aria-pressed={collection.poster_item_id === item.id}
                        on:click|preventDefault|stopPropagation={() => setCover(item.id)}>★</button>
                {#if customOrder}
                  <button class="ctl" disabled={i === 0 || busy} title="Move earlier" aria-label="Move {item.title} earlier"
                          on:click|preventDefault|stopPropagation={() => move(i, -1)}>‹</button>
                  <button class="ctl" disabled={i === items.length - 1 || busy} title="Move later" aria-label="Move {item.title} later"
                          on:click|preventDefault|stopPropagation={() => move(i, 1)}>›</button>
                {/if}
              </div>
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
    background: none; border: none; color: var(--text-muted); font-size: 1.2rem; cursor: pointer; padding: 0;
  }
  .back:hover { color: var(--text-primary); }
  h1 { font-size: 1.4rem; font-weight: 800; color: var(--text-primary); margin: 0; }
  .count { font-size: 0.78rem; color: var(--text-muted); margin-left: auto; }
  .overview { font-size: 0.82rem; color: var(--text-secondary); max-width: 60ch; margin: -1rem 0 1.5rem; line-height: 1.5; }

  .btn-edit {
    padding: 0.3rem 0.65rem; background: var(--bg-hover); border: 1px solid var(--border-strong);
    border-radius: 6px; color: var(--text-muted); font-size: 0.72rem; cursor: pointer;
  }
  .btn-edit:hover { color: var(--text-secondary); border-color: rgba(255,255,255,0.15); }
  .btn-edit.danger:hover { color: #f87171; border-color: rgba(248,113,113,0.3); }
  .desc-input { max-width: 420px !important; }

  .toolbar { display: flex; align-items: center; gap: 1rem; flex-wrap: wrap; margin: -1rem 0 1.5rem; }
  .sort { display: flex; align-items: center; gap: 0.45rem; font-size: 0.75rem; color: var(--text-muted); }
  .sort select {
    background: var(--bg-hover); border: 1px solid var(--border-strong); border-radius: 6px;
    color: var(--text-primary); font-size: 0.75rem; padding: 0.25rem 0.4rem;
  }
  .hint { font-size: 0.72rem; color: var(--text-muted); }
  .rules-warn { margin: -1rem 0 1.5rem; }
  .smart-line { font-size: 0.78rem; color: var(--text-secondary); margin: -1rem 0 1.25rem; }
  .smart-badge {
    display: inline-block; font-size: 0.62rem; font-weight: 700; letter-spacing: 0.04em; text-transform: uppercase;
    padding: 0.1rem 0.4rem; border-radius: 4px; background: rgba(124,106,247,0.18); color: var(--accent); margin-right: 0.3rem;
  }
  .smart-badge.nfo { background: rgba(250,204,21,0.15); color: #facc15; }
  .cover-ctl { display: inline-flex; align-items: center; gap: 0.4rem; }
  .cover-thumb { width: 1.6rem; height: 2.4rem; object-fit: cover; border-radius: 3px; border: 1px solid var(--border-strong); }
  .sort input[type='checkbox'] { accent-color: var(--accent); }

  .admin-ctl {
    position: absolute; top: 0.35rem; left: 0.35rem; display: flex; gap: 0.2rem;
    opacity: 0; transition: opacity 0.15s;
  }
  .card:hover .admin-ctl, .card:focus-within .admin-ctl { opacity: 1; }
  .ctl {
    background: rgba(0,0,0,0.6); border: none; color: #bbb;
    width: 1.4rem; height: 1.4rem; border-radius: 50%; font-size: 0.8rem; line-height: 1;
    cursor: pointer; display: flex; align-items: center; justify-content: center;
  }
  .ctl:hover:not(:disabled) { color: #fff; }
  .ctl:disabled { opacity: 0.35; cursor: default; }
  .ctl.on { color: #facc15; opacity: 1; }
  .card .admin-ctl:has(.ctl.on) { opacity: 1; }

  .edit-form { display: flex; gap: 0.5rem; align-items: center; flex: 1; }
  .edit-form input {
    background: var(--bg-hover); border: 1px solid var(--border-strong);
    border-radius: 7px; padding: 0.42rem 0.75rem; color: var(--text-primary); font-size: 0.85rem; flex: 1; max-width: 300px;
  }
  .edit-form input:focus { outline: none; border-color: var(--accent); }
  .btn-save {
    padding: 0.35rem 0.7rem; background: var(--accent); border: none; border-radius: 6px;
    color: #fff; font-size: 0.75rem; font-weight: 600; cursor: pointer;
  }
  .btn-cancel {
    padding: 0.35rem 0.7rem; background: var(--bg-hover); border: 1px solid var(--border-strong);
    border-radius: 6px; color: var(--text-muted); font-size: 0.75rem; cursor: pointer;
  }

  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(150px, 1fr));
    gap: 1rem;
  }

  .card {
    display: flex; flex-direction: column; text-decoration: none; color: inherit;
    border-radius: 8px; overflow: hidden; position: relative;
    background: var(--input-bg); border: 1px solid var(--border);
    transition: border-color 0.15s, transform 0.15s;
  }
  .card:hover { border-color: rgba(124,106,247,0.3); transform: translateY(-2px); }

  .poster { width: 100%; aspect-ratio: 2/3; object-fit: cover; display: block; }
  .poster.placeholder {
    display: flex; align-items: center; justify-content: center;
    background: rgba(255,255,255,0.02); font-size: 2rem;
  }

  .meta { padding: 0.6rem 0.5rem; }
  .title { font-size: 0.78rem; font-weight: 600; color: var(--text-primary); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .sub { font-size: 0.68rem; color: var(--text-muted); margin-top: 0.2rem; }
  .dot { margin: 0 0.25rem; }

  .remove {
    position: absolute; top: 0.35rem; right: 0.35rem;
    background: rgba(0,0,0,0.6); border: none; color: #888;
    width: 1.4rem; height: 1.4rem; border-radius: 50%; font-size: 0.9rem;
    cursor: pointer; opacity: 0; transition: opacity 0.15s, color 0.15s;
    display: flex; align-items: center; justify-content: center;
  }
  .card:hover .remove, .card:focus-within .remove { opacity: 1; }
  .remove:hover { color: #f87171; }

  .error-bar {
    background: var(--error-bg); border: 1px solid var(--error-bg);
    color: var(--error); padding: 0.6rem 0.9rem; border-radius: 8px; font-size: 0.8rem; margin-bottom: 1.5rem;
  }
  .loading { color: var(--text-muted); font-size: 0.85rem; }
  .empty { text-align: center; padding: 4rem 2rem; color: var(--text-muted); }
  .empty-sub { font-size: 0.8rem; color: var(--text-muted); margin-top: 0.5rem; }
</style>
