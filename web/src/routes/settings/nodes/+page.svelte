<script lang="ts">
  import { onMount } from 'svelte';
  import { settingsApi } from '$lib/api';
  import type { NodeSettings } from '$lib/api';
  import { toast } from '$lib/stores/toast';

  let loading = true;
  let saving = false;
  let error = '';

  let currentNodeId = '';
  let nodeIds: string[] = [];
  let selected = '';
  let node: NodeSettings | null = null;

  onMount(async () => {
    try {
      const list = await settingsApi.getNodes();
      currentNodeId = list.current_node_id;
      // The current node may have no stored row yet — always include it.
      const ids = new Set<string>([currentNodeId, ...list.nodes.map((n) => n.node_id)]);
      nodeIds = [...ids].filter(Boolean).sort();
      selected = currentNodeId;
      await loadNode();
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed to load nodes';
    } finally {
      loading = false;
    }
  });

  async function loadNode() {
    if (!selected) return;
    node = await settingsApi.getNode(selected);
  }

  async function onSelect() {
    error = '';
    try {
      await loadNode();
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed to load node config';
    }
  }

  async function save() {
    if (!node) return;
    saving = true;
    try {
      await settingsApi.updateNode(selected, node);
      toast.success(`Saved ${selected} — restart that node to apply`);
      // Refresh the node list so a newly-configured node appears in the picker.
      const list = await settingsApi.getNodes();
      const ids = new Set<string>([currentNodeId, ...list.nodes.map((n) => n.node_id)]);
      nodeIds = [...ids].filter(Boolean).sort();
      await loadNode();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Save failed');
    } finally {
      saving = false;
    }
  }
</script>

{#if loading}
  <p class="muted">Loading…</p>
{:else if error}
  <p class="error">{error}</p>
{:else}
  <div class="wrap">
    <p class="notice">
      Per-node configuration. Unlike System settings (which every node shares),
      each node reads only its own row, so these are safe to set per machine.
      <strong>Restart the target node to apply.</strong> If a bad bind address
      locks a node out, set <code>IGNORE_NODE_DB_CONFIG=true</code> in its
      environment to boot from env only. The bootstrap set
      (<code>DATABASE_URL</code>, <code>SECRET_KEY</code>, <code>NODE_ID</code>)
      must stay in the environment — it's needed before this table is reachable.
    </p>

    <section>
      <header><h2>Node</h2></header>
      <div class="grid">
        <label class="full">
          Configuring
          <select bind:value={selected} on:change={onSelect}>
            {#each nodeIds as id}
              <option value={id}>{id}{id === currentNodeId ? ' (this node)' : ''}</option>
            {/each}
          </select>
        </label>
      </div>
    </section>

    {#if node}
      <section>
        <header>
          <h2>Bind addresses</h2>
          <p class="hint">You're connected through the API address — changing it needs a matching restart and reconnect.</p>
        </header>
        <div class="grid">
          <label>
            API listen address
            <input type="text" bind:value={node.listen_addr} placeholder=":7070" />
          </label>
          <label>
            Metrics address
            <input type="text" bind:value={node.metrics_addr} placeholder=":7071" />
          </label>
          <label>
            Worker health address
            <input type="text" bind:value={node.worker_health_addr} placeholder=":7074" />
          </label>
        </div>
      </section>

      <section>
        <header><h2>Paths</h2></header>
        <div class="grid">
          <label class="full">
            Media path <span class="hint">(deprecated — artwork now stored next to media)</span>
            <input type="text" bind:value={node.media_path} />
          </label>
          <label class="full">
            Artwork cache path
            <input type="text" bind:value={node.cache_path} />
          </label>
          <label class="full">
            Static-ABR root <span class="hint">(empty = bucket-relative)</span>
            <input type="text" bind:value={node.static_abr_root} />
          </label>
        </div>
      </section>

      <section>
        <header><h2>Identity & role</h2></header>
        <div class="grid">
          <label>
            Site ID <span class="hint">(multi-site DR label)</span>
            <input type="text" bind:value={node.site_id} />
          </label>
        </div>
        <label class="check">
          <input type="checkbox" bind:checked={node.transcode_qsv_decode} />
          <span>Intel QSV hardware HEVC decode <span class="hint">— this worker's GPU only</span></span>
        </label>
        <label class="check">
          <input type="checkbox" bind:checked={node.disable_embedded_worker} />
          <span>Disable the in-process transcode worker on this node</span>
        </label>
      </section>

      <div class="actions">
        <button class="btn btn-primary" disabled={saving} on:click={save}>
          {saving ? 'Saving…' : `Save ${selected}`}
        </button>
      </div>
    {/if}
  </div>
{/if}

<style>
  .wrap { display: flex; flex-direction: column; gap: 1.5rem; }
  .notice {
    background: rgba(124,106,247,0.12);
    border: 1px solid rgba(124,106,247,0.35);
    border-radius: 8px;
    padding: 0.75rem 1rem;
    font-size: 0.82rem;
    color: var(--text-secondary);
    line-height: 1.55;
  }
  section {
    background: var(--surface);
    border: 1px solid rgba(255,255,255,0.05);
    border-radius: 8px;
    padding: 1.25rem 1.5rem;
  }
  h2 { font-size: 0.95rem; margin: 0 0 0.5rem; font-weight: 600; }
  .hint { color: var(--text-muted); font-weight: 400; font-size: 0.78rem; }
  .muted { color: var(--text-muted); }
  .error { color: var(--error); }
  code { font-family: ui-monospace, monospace; font-size: 0.78rem; background: rgba(255,255,255,0.06); padding: 0.05rem 0.3rem; border-radius: 3px; }

  .grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 0.75rem 1rem;
    margin: 1rem 0 0;
  }
  .grid .full { grid-column: 1 / -1; }
  label {
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
    font-size: 0.78rem;
    color: var(--text-secondary);
  }
  input[type="text"], select {
    padding: 0.45rem 0.6rem;
    border-radius: 4px;
    border: 1px solid rgba(255,255,255,0.1);
    background: var(--bg);
    color: var(--text-primary);
    font-family: inherit;
    font-size: 0.85rem;
  }
  .check {
    flex-direction: row;
    align-items: flex-start;
    gap: 0.5rem;
    margin-top: 0.6rem;
    color: var(--text-secondary);
    font-size: 0.82rem;
    cursor: pointer;
  }
  .check input { margin-top: 0.15rem; }

  .actions { display: flex; gap: 0.5rem; }
  .btn {
    padding: 0.55rem 1.1rem;
    border-radius: 4px;
    font-size: 0.82rem;
    font-weight: 500;
    border: 1px solid rgba(255,255,255,0.1);
    background: transparent;
    color: var(--text-primary);
    cursor: pointer;
    transition: background 0.12s, filter 0.12s;
  }
  .btn:disabled { opacity: 0.55; cursor: not-allowed; }
  .btn-primary { background: var(--accent); color: var(--accent-text); border-color: transparent; }
  .btn-primary:hover:not(:disabled) { filter: brightness(1.1); }

  @media (max-width: 720px) {
    .grid { grid-template-columns: 1fr; }
  }
</style>
