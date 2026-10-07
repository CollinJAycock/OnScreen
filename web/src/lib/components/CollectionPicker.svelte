<script lang="ts">
  import { createEventDispatcher } from 'svelte';
  import { collectionApi, type Collection } from '$lib/api';

  /** The movies / shows to add (one from an item page, several from a
   *  library selection). */
  export let mediaItemIds: string[] = [];
  export let open = false;

  const dispatch = createEventDispatcher<{ close: void; added: { collectionId: string; name: string } }>();

  let collections: Collection[] = [];
  let loading = true;
  let adding: string | null = null;
  let error = '';
  let success = '';

  let showCreate = false;
  let newName = '';
  let creating = false;

  $: if (open) load();
  $: count = mediaItemIds.length;
  $: what = count === 1 ? 'item' : `${count} items`;

  async function load() {
    loading = true;
    error = '';
    success = '';
    try {
      collections = (await collectionApi.list()).filter(c => c.type === 'manual');
      collections.sort((a, b) => a.name.localeCompare(b.name));
    } catch { collections = []; }
    finally { loading = false; }
  }

  async function addTo(c: Collection) {
    adding = c.id;
    error = '';
    success = '';
    try {
      await collectionApi.addItems(c.id, mediaItemIds);
      success = `Added ${what} to "${c.name}"`;
      dispatch('added', { collectionId: c.id, name: c.name });
      setTimeout(() => { dispatch('close'); }, 800);
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed to add';
    } finally { adding = null; }
  }

  async function createAndAdd() {
    if (!newName.trim()) return;
    creating = true;
    error = '';
    try {
      const c = await collectionApi.createManual(newName.trim());
      await collectionApi.addItems(c.id, mediaItemIds);
      success = `Created "${c.name}" with ${what}`;
      dispatch('added', { collectionId: c.id, name: c.name });
      newName = '';
      showCreate = false;
      setTimeout(() => { dispatch('close'); }, 800);
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed';
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
    <div class="panel" role="dialog" aria-label="Add to collection" tabindex="-1" on:click|stopPropagation>
      <div class="panel-header">
        <span>Add {count === 1 ? '' : `${count} items `}to Collection</span>
        <button class="close-btn" aria-label="Close" on:click={close}>×</button>
      </div>

      {#if error}
        <div class="msg error">{error}</div>
      {/if}
      {#if success}
        <div class="msg success">{success}</div>
      {/if}

      {#if loading}
        <div class="loading">Loading collections...</div>
      {:else}
        <div class="list">
          {#each collections as c (c.id)}
            <button class="row" disabled={adding === c.id || count === 0} on:click={() => addTo(c)}>
              <span class="icon">&#9638;</span>
              <span class="name">{c.name}</span>
              {#if c.item_count !== undefined}<span class="n">{c.item_count}</span>{/if}
              {#if adding === c.id}<span class="adding">...</span>{/if}
            </button>
          {/each}
          {#if collections.length === 0 && !showCreate}
            <div class="empty">No collections yet. Create one below.</div>
          {/if}
        </div>

        {#if showCreate}
          <form class="create-row" on:submit|preventDefault={createAndAdd}>
            <!-- svelte-ignore a11y-autofocus -->
            <input bind:value={newName} placeholder="New collection name" aria-label="New collection name" autofocus />
            <button type="submit" class="btn-go" disabled={creating || !newName.trim() || count === 0}>Create & Add</button>
            <button type="button" class="btn-x" on:click={() => showCreate = false}>Cancel</button>
          </form>
        {:else}
          <button class="new-btn" on:click={() => showCreate = true}>+ New Collection</button>
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

  .msg { padding: 0.5rem 1rem; font-size: 0.75rem; }
  .msg.error { color: #fca5a5; background: rgba(248,113,113,0.08); }
  .msg.success { color: #86efac; background: rgba(134,239,172,0.08); }

  .list { padding: 0.3rem; }

  .row {
    display: flex; align-items: center; gap: 0.6rem; width: 100%;
    padding: 0.5rem 0.7rem; background: none; border: none;
    border-radius: 7px; color: var(--text-primary); font-size: 0.82rem; cursor: pointer;
    text-align: left; transition: background 0.1s;
  }
  .row:hover { background: var(--bg-hover); }
  .row:disabled { opacity: 0.5; cursor: wait; }
  .icon { color: var(--accent); font-size: 1rem; }
  .name { flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .n { color: var(--text-muted); font-size: 0.72rem; }
  .adding { color: var(--accent); font-size: 0.72rem; }

  .empty { padding: 1rem; text-align: center; color: var(--text-muted); font-size: 0.78rem; }

  .new-btn {
    display: block; width: calc(100% - 0.6rem); margin: 0.3rem; padding: 0.45rem;
    background: rgba(124,106,247,0.08); border: 1px dashed rgba(124,106,247,0.25);
    border-radius: 7px; color: var(--accent-text); font-size: 0.78rem; font-weight: 600;
    cursor: pointer; text-align: center;
  }
  .new-btn:hover { background: rgba(124,106,247,0.15); }

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
    color: #66667a; font-size: 0.72rem; cursor: pointer;
  }

  .loading { padding: 1rem; text-align: center; color: var(--text-muted); font-size: 0.8rem; }
</style>
