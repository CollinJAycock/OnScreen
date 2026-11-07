<script lang="ts">
  import { onMount } from 'svelte';
  import { settingsApi } from '$lib/api';
  import { confirmAction } from '$lib/native';
  import type { NodeSettings } from '$lib/api';
  import { toast } from '$lib/stores/toast';
  import { t, i18n } from '$lib/i18n/index.svelte';

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
      const ids = new Set<string>([currentNodeId, ...(list.nodes ?? []).map((n) => n.node_id)]);
      nodeIds = [...ids].filter(Boolean).sort();
      selected = currentNodeId;
      await loadNode();
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : t('settings.nodes.loadFailed');
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
      error = e instanceof Error ? e.message : t('settings.nodes.loadNodeFailed');
    }
  }

  async function refreshNodeList() {
    const list = await settingsApi.getNodes();
    const ids = new Set<string>([currentNodeId, ...(list.nodes ?? []).map((n) => n.node_id)]);
    nodeIds = [...ids].filter(Boolean).sort();
  }

  async function save() {
    if (!node) return;
    saving = true;
    try {
      await settingsApi.updateNode(selected, node);
      toast.success(t('settings.nodes.saved', { node: selected }));
      // Refresh the node list so a newly-configured node appears in the picker.
      await refreshNodeList();
      await loadNode();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : t('settings.nodes.saveFailed'));
    } finally {
      saving = false;
    }
  }

  async function removeNode() {
    if (!selected || selected === currentNodeId) return;
    if (!(await confirmAction(t('settings.nodes.confirmRemove', { node: selected })))) return;
    saving = true;
    try {
      await settingsApi.deleteNode(selected);
      toast.success(t('settings.nodes.removed', { node: selected }));
      await refreshNodeList();
      selected = currentNodeId;
      await loadNode();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : t('settings.nodes.removeFailed'));
    } finally {
      saving = false;
    }
  }
</script>

{#if loading}
  <div class="skeleton-block"></div>
{:else if error}
  <p class="error">{error}</p>
{:else}
  <div class="wrap">
    <p class="notice">
      {#each $i18n.rich('settings.nodes.notice', { restart: $i18n('settings.nodes.noticeRestart'), ignore: 'IGNORE_NODE_DB_CONFIG=true', db: 'DATABASE_URL', secret: 'SECRET_KEY', node: 'NODE_ID' }) as seg}{#if seg.param === 'restart'}<strong>{seg.text}</strong>{:else if seg.param === 'ignore'}<code>{seg.text}</code>{:else if seg.param === 'db'}<code>{seg.text}</code>{:else if seg.param === 'secret'}<code>{seg.text}</code>{:else if seg.param === 'node'}<code>{seg.text}</code>{:else}{seg.text}{/if}{/each}
    </p>

    <section>
      <header><h2>{$i18n('settings.nodes.node')}</h2></header>
      <div class="grid">
        <label class="full">
          {$i18n('settings.nodes.configuring')}
          <select bind:value={selected} on:change={onSelect}>
            {#each nodeIds as id}
              <option value={id}>{id === currentNodeId ? $i18n('settings.nodes.thisNode', { node: id }) : id}</option>
            {/each}
          </select>
        </label>
      </div>
    </section>

    {#if node}
      <section>
        <header>
          <h2>{$i18n('settings.nodes.bindTitle')}</h2>
          <p class="hint">{$i18n('settings.nodes.bindHint')}</p>
        </header>
        <div class="grid">
          <label>
            {$i18n('settings.nodes.apiAddr')}
            <input type="text" bind:value={node.listen_addr} placeholder=":7070" />
          </label>
          <label>
            {$i18n('settings.nodes.metricsAddr')}
            <input type="text" bind:value={node.metrics_addr} placeholder=":7071" />
          </label>
          <label>
            {$i18n('settings.nodes.workerHealthAddr')}
            <input type="text" bind:value={node.worker_health_addr} placeholder=":7074" />
          </label>
        </div>
      </section>

      <section>
        <header><h2>{$i18n('settings.nodes.paths')}</h2></header>
        <div class="grid">
          <label class="full">
            {$i18n('settings.nodes.cachePath')}
            <input type="text" bind:value={node.cache_path} />
          </label>
          <label class="full">
            {$i18n('settings.nodes.abrRoot')} <span class="hint">{$i18n('settings.nodes.abrRootHint')}</span>
            <input type="text" bind:value={node.static_abr_root} />
          </label>
        </div>
      </section>

      <section>
        <header><h2>{$i18n('settings.nodes.identity')}</h2></header>
        <div class="grid">
          <label>
            {$i18n('settings.nodes.siteId')} <span class="hint">{$i18n('settings.nodes.siteIdHint')}</span>
            <input type="text" bind:value={node.site_id} />
          </label>
        </div>
        <label class="check">
          <input type="checkbox" bind:checked={node.transcode_qsv_decode} />
          <span>{$i18n('settings.nodes.qsv')} <span class="hint">{$i18n('settings.nodes.qsvHint')}</span></span>
        </label>
        <label class="check">
          <input type="checkbox" bind:checked={node.disable_embedded_worker} />
          <span>{$i18n('settings.nodes.disableWorker')}</span>
        </label>
      </section>

      <div class="actions">
        <button class="btn btn-primary" disabled={saving} on:click={save}>
          {saving ? $i18n('settings.nodes.saving') : $i18n('settings.nodes.save', { node: selected })}
        </button>
        {#if selected !== currentNodeId}
          <button class="btn btn-danger" disabled={saving} on:click={removeNode}
            title={$i18n('settings.nodes.removeTitle')}>
            {$i18n('settings.nodes.remove')}
          </button>
        {/if}
      </div>
    {/if}
  </div>
{/if}

<style>
  .wrap { display: flex; flex-direction: column; gap: 1.5rem; }
  .notice {
    background: var(--accent-bg);
    border: 1px solid var(--accent);
    border-radius: 10px;
    padding: 0.75rem 1rem;
    font-size: 0.82rem;
    color: var(--text-secondary);
    line-height: 1.55;
  }
  section {
    background: var(--bg-elevated);
    border: 1px solid var(--border);
    border-radius: 10px;
    padding: 1.1rem 1.25rem;
  }
  h2 { font-size: 0.95rem; margin: 0 0 0.5rem; font-weight: 600; }
  .hint { color: var(--text-muted); font-weight: 400; font-size: 0.78rem; }
  .error { color: var(--error); }
  code { font-family: ui-monospace, monospace; font-size: 0.78rem; background: var(--bg-hover); padding: 0.05rem 0.3rem; border-radius: 4px; }

  .skeleton-block {
    height: 120px;
    border-radius: 10px;
    background: linear-gradient(90deg, var(--bg-elevated) 25%, var(--bg-hover) 50%, var(--bg-elevated) 75%);
    background-size: 200% 100%;
    animation: shimmer 1.4s infinite;
  }
  @keyframes shimmer {
    0% { background-position: 200% 0; }
    100% { background-position: -200% 0; }
  }

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
    padding: 0.48rem 0.7rem;
    border-radius: 7px;
    border: 1px solid var(--border-strong);
    background: var(--input-bg);
    color: var(--text-primary);
    font-family: inherit;
    font-size: 0.85rem;
  }
  input[type="text"]:focus, select:focus {
    outline: none;
    border-color: var(--accent);
    box-shadow: 0 0 0 3px var(--accent-bg);
  }
  .check input[type="checkbox"] { accent-color: var(--accent); }
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
    padding: 0.45rem 0.9rem;
    border-radius: 7px;
    font-size: 0.8rem;
    font-weight: 600;
    border: 1px solid var(--border-strong);
    background: transparent;
    color: var(--text-primary);
    cursor: pointer;
    transition: background 0.15s, border-color 0.15s, color 0.15s;
  }
  .btn:disabled { opacity: 0.5; cursor: not-allowed; }
  .btn-primary { background: var(--accent); color: #fff; border: none; }
  .btn-primary:hover:not(:disabled) { background: var(--accent-hover); }
  .btn-danger { background: var(--error-bg); color: var(--error); border-color: var(--error); }
  .btn-danger:hover:not(:disabled) { background: var(--error-bg); border-color: var(--error); }

  @media (max-width: 720px) {
    .grid { grid-template-columns: 1fr; }
  }
</style>
