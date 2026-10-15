<script lang="ts">
  // "Add label…" for a library selection (admin): add or remove one or more
  // labels on every selected item at once (POST /labels/apply, up to 500).
  import { createEventDispatcher } from 'svelte';
  import { labelApi, type LabelInUse } from '$lib/api';
  import LabelEditor from './LabelEditor.svelte';

  export let mediaItemIds: string[] = [];
  export let open = false;

  const dispatch = createEventDispatcher<{ close: void; applied: { added: number; removed: number } }>();

  let inUse: LabelInUse[] = [];
  let chosen: string[] = [];
  let busy = false;
  let error = '';
  let success = '';

  $: if (open) load();
  $: count = mediaItemIds.length;
  $: what = count === 1 ? '1 item' : `${count} items`;
  $: tooMany = count > 500;

  async function load() {
    chosen = [];
    error = '';
    success = '';
    try { inUse = await labelApi.list(); } catch { inUse = []; }
  }

  async function run(mode: 'add' | 'remove') {
    if (chosen.length === 0 || count === 0 || tooMany) return;
    busy = true;
    error = '';
    success = '';
    try {
      const res = await labelApi.apply(mediaItemIds, mode === 'add' ? { add: chosen } : { remove: chosen });
      success = mode === 'add'
        ? `Labelled ${res.added} of ${what}`
        : `Removed from ${res.removed} of ${what}`;
      dispatch('applied', res);
      setTimeout(() => dispatch('close'), 900);
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed';
    } finally { busy = false; }
  }

  function close() { dispatch('close'); }
</script>

{#if open}
  <!-- svelte-ignore a11y-click-events-have-key-events -->
  <!-- svelte-ignore a11y-no-static-element-interactions -->
  <div class="backdrop" on:click={close}>
    <!-- svelte-ignore a11y-click-events-have-key-events -->
    <!-- svelte-ignore a11y-no-static-element-interactions -->
    <div class="panel" role="dialog" aria-label="Label items" tabindex="-1" on:click|stopPropagation>
      <div class="panel-header">
        <span>Label {what}</span>
        <button class="close-btn" aria-label="Close" on:click={close}>×</button>
      </div>
      {#if error}<div class="msg error" role="alert">{error}</div>{/if}
      {#if success}<div class="msg success" role="status">{success}</div>{/if}
      <div class="body">
        <LabelEditor
          labels={chosen}
          suggestions={inUse.map((l) => l.label)}
          disabled={busy}
          label="Labels to apply"
          idSuffix="bulk"
          on:change={(e) => (chosen = e.detail.labels)}
        />
        {#if tooMany}
          <p class="hint warn">Select at most 500 items at a time.</p>
        {:else}
          <p class="hint">Labels restrict who sees a title — set blocked or allowed labels per user in Settings ▸ Users.</p>
        {/if}
        <div class="actions">
          <button class="btn-x" disabled={busy || chosen.length === 0 || count === 0 || tooMany} on:click={() => run('remove')}>Remove</button>
          <button class="btn-go" disabled={busy || chosen.length === 0 || count === 0 || tooMany} on:click={() => run('add')}>Add</button>
        </div>
      </div>
    </div>
  </div>
{/if}

<style>
  .backdrop {
    position: fixed; inset: 0; background: var(--shadow);
    display: flex; align-items: center; justify-content: center;
    z-index: 2000;
  }
  .panel {
    background: var(--bg-elevated); border: 1px solid var(--border);
    border-radius: 12px; width: 340px; max-width: calc(100vw - 2rem);
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
  .msg { padding: 0.5rem 1rem; font-size: 0.75rem; }
  .msg.error { color: #fca5a5; background: rgba(248,113,113,0.08); }
  .msg.success { color: #86efac; background: rgba(134,239,172,0.08); }
  .body { padding: 0.8rem 1rem 1rem; }
  .hint { font-size: 0.72rem; color: var(--text-muted); margin: 0.5rem 0 0; }
  .hint.warn { color: #fcd34d; }
  .actions { display: flex; justify-content: flex-end; gap: 0.5rem; margin-top: 0.8rem; }
  .btn-go {
    padding: 0.4rem 0.9rem; background: var(--accent); border: none; border-radius: 6px;
    color: #fff; font-size: 0.78rem; font-weight: 600; cursor: pointer;
  }
  .btn-x {
    padding: 0.4rem 0.9rem; background: var(--input-bg); border: 1px solid var(--border);
    border-radius: 6px; color: var(--text-secondary); font-size: 0.78rem; cursor: pointer;
  }
  .btn-go:disabled, .btn-x:disabled { opacity: 0.5; cursor: not-allowed; }
</style>
