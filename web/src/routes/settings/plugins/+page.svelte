<script lang="ts">
  import { onMount } from 'svelte';
  import { pluginApi, type Plugin, type PluginRole } from '$lib/api';
  import { toast } from '$lib/stores/toast';
  import { t, tr, type MessageKey } from '$lib/i18n/index.svelte';

  // Each role's contract in brief: the tool the plugin must advertise and
  // what OnScreen does with it. docs/plugins.md has the full JSON.
  // label / hint / summary are catalog keys.
  type RoleInfo = { value: PluginRole; label: MessageKey; tool: string; hint: MessageKey; summary: MessageKey };
  const ROLES: RoleInfo[] = [
    {
      value: 'notification', label: 'settings.plugins.roleNotification', tool: 'notify',
      hint: 'settings.plugins.roleNotificationHint',
      summary: 'settings.plugins.roleNotificationSummary'
    },
    {
      value: 'metadata', label: 'settings.plugins.roleMetadata', tool: 'enrich_item',
      hint: 'settings.plugins.roleMetadataHint',
      summary: 'settings.plugins.roleMetadataSummary'
    },
    {
      value: 'task', label: 'settings.plugins.roleTask', tool: 'run_task',
      hint: 'settings.plugins.roleTaskHint',
      summary: 'settings.plugins.roleTaskSummary'
    }
  ];

  function roleInfo(role: PluginRole): RoleInfo | undefined {
    return ROLES.find(r => r.value === role);
  }

  let loading = true;
  let error = '';
  let plugins: Plugin[] = [];

  // Add form state
  let showAdd = false;
  let addName = '';
  let addRole: PluginRole = 'notification';
  let addEndpoint = '';
  let addHosts = '';
  let addSaving = false;

  // Edit state
  let editId: string | null = null;
  let editName = '';
  let editEndpoint = '';
  let editHosts = '';
  let editEnabled = true;
  let editSaving = false;

  // Delete confirmation
  let deleteId: string | null = null;

  // Test state
  let testingId: string | null = null;
  let testResult: { id: string; ok: boolean; message: string } | null = null;

  onMount(loadPlugins);

  async function loadPlugins() {
    loading = true;
    error = '';
    try {
      const result = await pluginApi.list();
      plugins = result.items;
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : t('settings.plugins.loadFailed');
    } finally {
      loading = false;
    }
  }

  function parseHosts(raw: string): string[] {
    return raw
      .split(/[\s,]+/)
      .map(h => h.trim())
      .filter(h => h.length > 0);
  }

  function openAdd() {
    showAdd = true;
    addName = '';
    addRole = 'notification';
    addEndpoint = '';
    addHosts = '';
    error = '';
  }

  function cancelAdd() {
    showAdd = false;
  }

  async function submitAdd() {
    if (!addName.trim()) { error = t('settings.plugins.nameRequired'); return; }
    if (!addEndpoint.trim()) { error = t('settings.plugins.endpointRequired'); return; }
    error = '';
    addSaving = true;
    try {
      await pluginApi.create({
        name: addName.trim(),
        role: addRole,
        endpoint_url: addEndpoint.trim(),
        allowed_hosts: parseHosts(addHosts),
        enabled: true
      });
      showAdd = false;
      toast.success(t('settings.plugins.created'));
      await loadPlugins();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : t('settings.plugins.createFailed'));
    } finally {
      addSaving = false;
    }
  }

  function startEdit(p: Plugin) {
    editId = p.id;
    editName = p.name;
    editEndpoint = p.endpoint_url;
    editHosts = p.allowed_hosts.join('\n');
    editEnabled = p.enabled;
    error = '';
  }

  function cancelEdit() {
    editId = null;
  }

  async function submitEdit() {
    if (!editId) return;
    if (!editName.trim()) { error = t('settings.plugins.nameRequired'); return; }
    if (!editEndpoint.trim()) { error = t('settings.plugins.endpointRequired'); return; }
    error = '';
    editSaving = true;
    try {
      await pluginApi.update(editId, {
        name: editName.trim(),
        endpoint_url: editEndpoint.trim(),
        allowed_hosts: parseHosts(editHosts),
        enabled: editEnabled
      });
      editId = null;
      toast.success(t('settings.plugins.updated'));
      await loadPlugins();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : t('settings.plugins.updateFailed'));
    } finally {
      editSaving = false;
    }
  }

  async function confirmDelete() {
    if (!deleteId) return;
    try {
      await pluginApi.del(deleteId);
      deleteId = null;
      toast.success(t('settings.plugins.deleted'));
      await loadPlugins();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : t('settings.plugins.deleteFailed'));
    }
  }

  async function toggleEnabled(p: Plugin) {
    try {
      await pluginApi.update(p.id, { enabled: !p.enabled });
      toast.info(!p.enabled ? t('settings.plugins.enabledToast') : t('settings.plugins.disabledToast'));
      await loadPlugins();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : t('settings.plugins.updateFailed'));
    }
  }

  async function testPlugin(p: Plugin) {
    const id = p.id;
    testingId = id;
    testResult = null;
    try {
      await pluginApi.test(id);
      // Notification plugins get a real test event; metadata and task
      // plugins are only checked for their tool (a real call would change
      // an item or run the task).
      const message = p.role === 'notification'
        ? t('settings.plugins.testDelivered')
        : roleInfo(p.role)
          ? t('settings.plugins.reachableAdvertises', { tool: roleInfo(p.role)!.tool })
          : t('settings.plugins.reachableGeneric');
      testResult = { id, ok: true, message };
      toast.success(p.role === 'notification' ? t('settings.plugins.testDeliveredShort') : t('settings.plugins.reachable'));
    } catch (e: unknown) {
      testResult = { id, ok: false, message: e instanceof Error ? e.message : t('settings.plugins.testFailed') };
      toast.error(e instanceof Error ? e.message : t('settings.plugins.testFailed'));
    } finally {
      testingId = null;
    }
  }
</script>

<svelte:head><title>{$tr('settings.plugins.pageTitle')}</title></svelte:head>

<div class="page">
  <div class="header">
    <div class="header-left">
      <p class="intro">
        {#each $tr.rich('settings.plugins.intro', { notify: 'notify', enrich: 'enrich_item', task: 'run_task' }) as seg}{#if seg.param}<code>{seg.text}</code>{:else}{seg.text}{/if}{/each}
      </p>
    </div>
    {#if !showAdd && !loading}
      <button class="btn-add" on:click={openAdd}>
        <svg viewBox="0 0 20 20" fill="currentColor" width="14" height="14">
          <path d="M10.75 4.75a.75.75 0 00-1.5 0v4.5h-4.5a.75.75 0 000 1.5h4.5v4.5a.75.75 0 001.5 0v-4.5h4.5a.75.75 0 000-1.5h-4.5v-4.5z"/>
        </svg>
        {$tr('settings.plugins.addPlugin')}
      </button>
    {/if}
  </div>

  {#if error}
    <div class="banner error">{error}</div>
  {/if}

  {#if loading}
    <div class="skeleton-block"></div>
  {:else}
    {#if showAdd}
      <div class="card form-card">
        <div class="card-title">{$tr('settings.plugins.newPlugin')}</div>
        <div class="field">
          <label for="add-name">{$tr('settings.plugins.name')}</label>
          <input
            id="add-name"
            type="text"
            bind:value={addName}
            placeholder={$tr('settings.plugins.namePlaceholder')}
            autocomplete="off"
            spellcheck="false"
          />
        </div>
        <div class="field">
          <label for="add-role">{$tr('settings.plugins.role')}</label>
          <select
            id="add-role"
            value={addRole}
            on:change={(e) => (addRole = e.currentTarget.value as PluginRole)}
          >
            {#each ROLES as r}
              <option value={r.value}>{$tr(r.label)} — {r.tool}</option>
            {/each}
          </select>
          <div class="hint" data-testid="role-contract">{roleInfo(addRole) ? $tr(roleInfo(addRole)!.hint) : ''}</div>
        </div>
        <div class="field">
          <label for="add-endpoint">{$tr('settings.plugins.endpointUrl')}</label>
          <input
            id="add-endpoint"
            type="url"
            bind:value={addEndpoint}
            placeholder="https://mcp.example.com/mcp"
            autocomplete="off"
            spellcheck="false"
          />
          <div class="hint">{$tr('settings.plugins.endpointHint')}</div>
        </div>
        <div class="field">
          <label for="add-hosts">{$tr('settings.plugins.allowedHosts')} <span class="optional">{$tr('settings.plugins.optional')}</span></label>
          <textarea
            id="add-hosts"
            bind:value={addHosts}
            placeholder="api.example.com&#10;cdn.example.com"
            rows="3"
            autocomplete="off"
            spellcheck="false"
          ></textarea>
          <div class="hint">{$tr('settings.plugins.hostsHint')}</div>
        </div>
        <div class="form-actions">
          <button class="btn-cancel" on:click={cancelAdd}>{$tr('settings.plugins.cancel')}</button>
          <button class="btn-save" on:click={submitAdd} disabled={addSaving}>
            {addSaving ? $tr('settings.plugins.creating') : $tr('settings.plugins.createPlugin')}
          </button>
        </div>
      </div>
    {/if}

    {#if plugins.length === 0 && !showAdd}
      <div class="empty">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" width="40" height="40">
          <path stroke-linecap="round" stroke-linejoin="round" d="M14.25 6.087c0-.355.186-.676.401-.959.221-.29.349-.634.349-1.003 0-1.036-1.007-1.875-2.25-1.875s-2.25.84-2.25 1.875c0 .369.128.713.349 1.003.215.283.401.604.401.959v0a.64.64 0 01-.657.643 48.39 48.39 0 01-4.163-.3c.186 1.613.293 3.25.315 4.907a.656.656 0 01-.658.663v0c-.355 0-.676-.186-.959-.401a1.647 1.647 0 00-1.003-.349c-1.036 0-1.875 1.007-1.875 2.25s.84 2.25 1.875 2.25c.369 0 .713-.128 1.003-.349.283-.215.604-.401.959-.401v0c.31 0 .555.26.532.57a48.039 48.039 0 01-.642 5.056c1.518.19 3.058.309 4.616.354a.64.64 0 00.657-.643v0c0-.355-.186-.676-.401-.959a1.647 1.647 0 01-.349-1.003c0-1.035 1.008-1.875 2.25-1.875 1.243 0 2.25.84 2.25 1.875 0 .369-.128.713-.349 1.003-.215.283-.4.604-.4.959v0c0 .333.277.599.61.58a48.1 48.1 0 005.427-.63 48.05 48.05 0 00.582-4.717.532.532 0 00-.533-.57v0c-.355 0-.676.186-.959.401-.29.221-.634.349-1.003.349-1.035 0-1.875-1.007-1.875-2.25s.84-2.25 1.875-2.25c.37 0 .713.128 1.003.349.283.215.604.401.96.401v0a.656.656 0 00.658-.663 48.422 48.422 0 00-.37-5.36c-1.886.342-3.81.574-5.766.689a.578.578 0 01-.61-.58v0z"/>
        </svg>
        <p>{$tr('settings.plugins.empty')}</p>
        <p class="empty-sub">{$tr('settings.plugins.emptySub')}</p>
        <button class="btn-add" on:click={openAdd}>
          <svg viewBox="0 0 20 20" fill="currentColor" width="14" height="14">
            <path d="M10.75 4.75a.75.75 0 00-1.5 0v4.5h-4.5a.75.75 0 000 1.5h4.5v4.5a.75.75 0 001.5 0v-4.5h4.5a.75.75 0 000-1.5h-4.5v-4.5z"/>
          </svg>
          {$tr('settings.plugins.addPlugin')}
        </button>
      </div>
    {:else}
      {#each plugins as p (p.id)}
        <div class="card" class:disabled-card={!p.enabled}>
          {#if editId === p.id}
            <div class="card-title">{$tr('settings.plugins.editPlugin')}</div>
            <div class="field">
              <label for="edit-name">{$tr('settings.plugins.name')}</label>
              <input
                id="edit-name"
                type="text"
                bind:value={editName}
                autocomplete="off"
                spellcheck="false"
              />
            </div>
            <div class="field">
              <span class="field-heading">{$tr('settings.plugins.role')}</span>
              <div class="readonly-field">
                <span class="role-pill role-{p.role}">{p.role}</span>
                <span class="hint inline">{$tr('settings.plugins.roleFixed')}</span>
              </div>
            </div>
            <div class="field">
              <label for="edit-endpoint">{$tr('settings.plugins.endpointUrl')}</label>
              <input
                id="edit-endpoint"
                type="url"
                bind:value={editEndpoint}
                autocomplete="off"
                spellcheck="false"
              />
            </div>
            <div class="field">
              <label for="edit-hosts">{$tr('settings.plugins.allowedHosts')}</label>
              <textarea
                id="edit-hosts"
                bind:value={editHosts}
                placeholder={$tr('settings.plugins.hostsPlaceholder')}
                rows="3"
                autocomplete="off"
                spellcheck="false"
              ></textarea>
            </div>
            <div class="field">
              <label class="toggle-label">
                <span>{$tr('settings.plugins.enabled')}</span>
                <button
                  class="toggle"
                  class:toggle-on={editEnabled}
                  on:click={() => editEnabled = !editEnabled}
                  type="button"
                  title={$tr('settings.plugins.enabled')}
                  aria-label={$tr('settings.plugins.enabled')}
                  aria-pressed={editEnabled}
                >
                  <span class="toggle-knob"></span>
                </button>
              </label>
            </div>
            <div class="form-actions">
              <button class="btn-cancel" on:click={cancelEdit}>{$tr('settings.plugins.cancel')}</button>
              <button class="btn-save" on:click={submitEdit} disabled={editSaving}>
                {editSaving ? $tr('settings.plugins.saving') : $tr('settings.plugins.saveChanges')}
              </button>
            </div>
          {:else}
            <div class="p-row">
              <div class="p-info">
                <div class="p-header">
                  <span class="p-name">{p.name}</span>
                  <span class="role-pill role-{p.role}">{p.role}</span>
                  <button
                    class="toggle-sm"
                    class:toggle-on={p.enabled}
                    on:click={() => toggleEnabled(p)}
                    title={p.enabled ? $tr('settings.plugins.disable') : $tr('settings.plugins.enable')}
                    type="button"
                  >
                    <span class="toggle-knob"></span>
                  </button>
                </div>
                <div class="p-endpoint">{p.endpoint_url}</div>
                {#if roleInfo(p.role)}
                  <div class="p-contract">
                    {#each $tr.rich('settings.plugins.calls', { tool: roleInfo(p.role)?.tool ?? '', summary: $tr(roleInfo(p.role)!.summary) }) as seg}{#if seg.param === 'tool'}<code>{seg.text}</code>{:else}{seg.text}{/if}{/each}
                    {#if p.role === 'task'}
                      · <a href="/settings/tasks">{$tr('settings.plugins.schedule')}</a>
                    {/if}
                  </div>
                {/if}
                {#if p.allowed_hosts.length > 0}
                  <div class="p-hosts">
                    {#each p.allowed_hosts as host}
                      <span class="host-badge">{host}</span>
                    {/each}
                  </div>
                {/if}
              </div>
              <div class="p-actions">
                <button class="btn-icon" title={$tr('settings.plugins.test')} on:click={() => testPlugin(p)} disabled={testingId === p.id}>
                  {#if testingId === p.id}
                    <svg class="spinner" viewBox="0 0 20 20" width="15" height="15"><circle cx="10" cy="10" r="7" fill="none" stroke="currentColor" stroke-width="2" stroke-dasharray="30 14"/></svg>
                  {:else}
                    <svg viewBox="0 0 20 20" fill="currentColor" width="15" height="15">
                      <path fill-rule="evenodd" d="M10 18a8 8 0 100-16 8 8 0 000 16zM6.75 9.25a.75.75 0 000 1.5h4.59l-2.1 1.95a.75.75 0 001.02 1.1l3.5-3.25a.75.75 0 000-1.1l-3.5-3.25a.75.75 0 10-1.02 1.1l2.1 1.95H6.75z" clip-rule="evenodd"/>
                    </svg>
                  {/if}
                </button>
                <button class="btn-icon" title={$tr('settings.plugins.edit')} on:click={() => startEdit(p)}>
                  <svg viewBox="0 0 20 20" fill="currentColor" width="15" height="15">
                    <path d="M2.695 14.763l-1.262 3.154a.5.5 0 00.65.65l3.155-1.262a4 4 0 001.343-.885L17.5 5.5a2.121 2.121 0 00-3-3L3.58 13.42a4 4 0 00-.885 1.343z"/>
                  </svg>
                </button>
                <button class="btn-icon btn-danger" title={$tr('settings.plugins.delete')} on:click={() => deleteId = p.id}>
                  <svg viewBox="0 0 20 20" fill="currentColor" width="15" height="15">
                    <path fill-rule="evenodd" d="M8.75 1A2.75 2.75 0 006 3.75v.443c-.795.077-1.584.176-2.365.298a.75.75 0 10.23 1.482l.149-.022.841 10.518A2.75 2.75 0 007.596 19h4.807a2.75 2.75 0 002.742-2.53l.841-10.519.149.023a.75.75 0 00.23-1.482A41.03 41.03 0 0014 4.193V3.75A2.75 2.75 0 0011.25 1h-2.5zM10 4c.84 0 1.673.025 2.5.075V3.75c0-.69-.56-1.25-1.25-1.25h-2.5c-.69 0-1.25.56-1.25 1.25v.325C8.327 4.025 9.16 4 10 4zM8.58 7.72a.75.75 0 00-1.5.06l.3 7.5a.75.75 0 101.5-.06l-.3-7.5zm4.34.06a.75.75 0 10-1.5-.06l-.3 7.5a.75.75 0 101.5.06l.3-7.5z" clip-rule="evenodd"/>
                  </svg>
                </button>
              </div>
            </div>
            {#if testResult && testResult.id === p.id}
              <div class="test-result" class:test-ok={testResult.ok} class:test-fail={!testResult.ok}>
                {testResult.message}
              </div>
            {/if}
          {/if}
        </div>

        {#if deleteId === p.id}
          <div class="modal-overlay" on:click={() => deleteId = null} on:keydown={e => e.key === 'Escape' && (deleteId = null)} role="button" tabindex="-1">
            <!-- svelte-ignore a11y_click_events_have_key_events -->
            <div class="modal" on:click|stopPropagation role="dialog" aria-label={$tr('settings.plugins.confirmDelete')}>
              <p class="modal-text">{$tr('settings.plugins.deleteQuestion')}</p>
              <p class="modal-sub">{p.name}</p>
              <div class="modal-actions">
                <button class="btn-cancel" on:click={() => deleteId = null}>{$tr('settings.plugins.cancel')}</button>
                <button class="btn-delete" on:click={confirmDelete}>{$tr('settings.plugins.delete')}</button>
              </div>
            </div>
          </div>
        {/if}
      {/each}
    {/if}
  {/if}
</div>

<style>
  .page { max-width: 640px; }

  .header {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    margin-bottom: 1.75rem;
    gap: 1rem;
  }
  .header-left { display: flex; flex-direction: column; gap: 0.5rem; flex: 1; min-width: 0; }
  .intro { font-size: 0.78rem; color: var(--text-muted); line-height: 1.5; margin: 0; }
  .intro code {
    background: var(--bg-elevated);
    padding: 0.1rem 0.3rem;
    border-radius: 4px;
    font-size: 0.72rem;
  }

  .banner {
    padding: 0.6rem 0.9rem;
    border-radius: 8px;
    font-size: 0.8rem;
    margin-bottom: 1.25rem;
  }
  .banner.error { background: var(--error-bg); border: 1px solid var(--error); color: var(--error); }

  .skeleton-block {
    height: 100px; border-radius: 10px;
    background: linear-gradient(90deg, var(--bg-elevated) 25%, var(--bg-hover) 50%, var(--bg-elevated) 75%);
    background-size: 200% 100%;
    animation: shimmer 1.4s infinite;
  }
  @keyframes shimmer { 0% { background-position: 200% 0; } 100% { background-position: -200% 0; } }

  .card {
    background: var(--bg-elevated);
    border: 1px solid var(--border);
    border-radius: 10px;
    padding: 1.1rem 1.25rem;
    margin-bottom: 0.75rem;
  }
  .card.disabled-card { opacity: 0.55; }
  .card-title {
    font-size: 0.78rem;
    font-weight: 700;
    color: var(--accent-text);
    margin-bottom: 1rem;
    text-transform: uppercase;
    letter-spacing: 0.06em;
  }
  .form-card { border-color: var(--accent); }

  .field { display: flex; flex-direction: column; gap: 0.3rem; margin-bottom: 1rem; }
  label, .field-heading { font-size: 0.75rem; font-weight: 500; color: var(--text-muted); }
  .optional { font-weight: 400; color: var(--text-muted); }

  input[type="url"],
  input[type="text"],
  select,
  textarea {
    background: var(--input-bg);
    border: 1px solid var(--border-strong);
    border-radius: 7px;
    padding: 0.48rem 0.7rem;
    font-size: 0.85rem;
    color: var(--text-primary);
    font-family: inherit;
    transition: border-color 0.15s;
    width: 100%;
  }
  input[type="url"],
  textarea {
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  }
  textarea { resize: vertical; min-height: 60px; }
  select { cursor: pointer; }
  input:focus, select:focus, textarea:focus {
    outline: none; border-color: var(--accent); box-shadow: 0 0 0 3px var(--accent-bg);
  }
  ::placeholder { color: var(--text-muted); }

  .hint {
    font-size: 0.72rem;
    color: var(--text-muted);
    line-height: 1.5;
    margin-top: 0.15rem;
  }
  .hint.inline { margin-top: 0; margin-left: 0.6rem; }

  .readonly-field { display: flex; align-items: center; }

  .form-actions {
    display: flex;
    justify-content: flex-end;
    gap: 0.6rem;
    margin-top: 0.5rem;
  }
  .btn-save {
    padding: 0.45rem 0.9rem;
    background: var(--accent);
    border: none;
    border-radius: 7px;
    color: #fff;
    font-size: 0.8rem;
    font-weight: 600;
    cursor: pointer;
    transition: background 0.15s;
  }
  .btn-save:hover { background: var(--accent-hover); }
  .btn-save:disabled { opacity: 0.5; cursor: not-allowed; }

  .btn-cancel {
    padding: 0.45rem 0.9rem;
    background: transparent;
    border: 1px solid var(--border-strong);
    border-radius: 7px;
    color: var(--text-secondary);
    font-size: 0.78rem;
    font-weight: 500;
    cursor: pointer;
    transition: background 0.15s, border-color 0.15s, color 0.15s;
  }
  .btn-cancel:hover { background: var(--bg-hover); border-color: var(--text-muted); color: var(--text-secondary); }

  .btn-add {
    display: inline-flex;
    align-items: center;
    gap: 0.35rem;
    padding: 0.45rem 0.9rem;
    background: var(--accent);
    border: none;
    border-radius: 7px;
    color: #fff;
    font-size: 0.8rem;
    font-weight: 600;
    cursor: pointer;
    transition: background 0.15s;
    white-space: nowrap;
  }
  .btn-add:hover { background: var(--accent-hover); }

  .p-row {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 1rem;
  }
  .p-info { flex: 1; min-width: 0; }
  .p-header {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    margin-bottom: 0.35rem;
    flex-wrap: wrap;
  }
  .p-name {
    font-size: 0.92rem;
    font-weight: 600;
    color: var(--text-primary);
  }
  .p-endpoint {
    font-size: 0.78rem;
    font-family: monospace;
    color: var(--text-secondary);
    word-break: break-all;
    margin-bottom: 0.45rem;
  }
  .p-contract {
    font-size: 0.75rem;
    color: var(--text-muted);
    margin-bottom: 0.45rem;
    line-height: 1.45;
  }
  .p-contract code {
    background: var(--bg-hover);
    padding: 0.05rem 0.3rem;
    border-radius: 4px;
    font-size: 0.72rem;
  }
  .p-contract a { color: var(--accent-text); }
  .p-hosts {
    display: flex;
    flex-wrap: wrap;
    gap: 0.35rem;
  }
  .host-badge {
    font-size: 0.65rem;
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
    padding: 0.15rem 0.45rem;
    border-radius: 4px;
    background: var(--bg-hover);
    color: var(--text-muted);
  }
  .role-pill {
    font-size: 0.62rem;
    font-weight: 600;
    padding: 0.15rem 0.5rem;
    border-radius: 4px;
    text-transform: uppercase;
    letter-spacing: 0.05em;
  }
  .role-notification { background: var(--accent-bg); color: var(--accent-text); }
  .role-metadata { background: var(--success-bg); color: var(--success); }
  .role-task { background: rgba(251,191,36,0.12); color: #fcd34d; }

  .p-actions {
    display: flex;
    gap: 0.25rem;
    flex-shrink: 0;
  }
  .btn-icon {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 30px;
    height: 30px;
    background: none;
    border: 1px solid var(--border-strong);
    border-radius: 7px;
    color: var(--text-muted);
    cursor: pointer;
    transition: background 0.12s, color 0.12s, border-color 0.12s;
  }
  .btn-icon:hover { background: var(--bg-hover); color: var(--text-primary); border-color: var(--accent); }
  .btn-icon:disabled { opacity: 0.4; cursor: not-allowed; }
  .btn-icon.btn-danger:hover { background: var(--error-bg); color: var(--error); border-color: var(--error); }

  .test-result {
    margin-top: 0.6rem;
    padding: 0.4rem 0.7rem;
    border-radius: 6px;
    font-size: 0.75rem;
  }
  .test-ok { background: var(--success-bg); border: 1px solid var(--success); color: var(--success); }
  .test-fail { background: var(--error-bg); border: 1px solid var(--error); color: var(--error); }

  .spinner { animation: spin 0.7s linear infinite; }
  @keyframes spin { from { transform: rotate(0deg); } to { transform: rotate(360deg); } }

  .toggle, .toggle-sm {
    position: relative;
    width: 36px;
    height: 20px;
    border-radius: 10px;
    border: none;
    background: rgba(255,255,255,0.1);
    cursor: pointer;
    transition: background 0.2s;
    flex-shrink: 0;
  }
  .toggle-sm { width: 30px; height: 16px; border-radius: 8px; }
  .toggle.toggle-on, .toggle-sm.toggle-on { background: var(--accent); }
  .toggle-knob {
    position: absolute;
    top: 3px;
    left: 3px;
    width: 14px;
    height: 14px;
    border-radius: 50%;
    background: #fff;
    transition: transform 0.2s;
  }
  .toggle-sm .toggle-knob { width: 10px; height: 10px; }
  .toggle.toggle-on .toggle-knob { transform: translateX(16px); }
  .toggle-sm.toggle-on .toggle-knob { transform: translateX(14px); }

  .toggle-label {
    display: flex;
    align-items: center;
    justify-content: space-between;
    color: var(--text-secondary);
    font-size: 0.8rem;
  }

  .empty {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 0.6rem;
    padding: 3rem 1rem;
    color: var(--text-muted);
    text-align: center;
  }
  .empty p { font-size: 0.88rem; color: var(--text-muted); }
  .empty .empty-sub { font-size: 0.75rem; color: var(--text-muted); max-width: 360px; }

  .modal-overlay {
    position: fixed;
    inset: 0;
    background: var(--shadow);
    backdrop-filter: blur(4px);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 1000;
  }
  .modal {
    background: var(--bg-elevated);
    border: 1px solid var(--border-strong);
    border-radius: 12px;
    padding: 1.5rem;
    max-width: 400px;
    width: 90%;
    box-shadow: 0 12px 32px var(--shadow);
  }
  .modal-text { font-size: 0.92rem; font-weight: 600; color: var(--text-primary); margin-bottom: 0.35rem; }
  .modal-sub { font-size: 0.78rem; color: var(--text-muted); font-family: ui-monospace, SFMono-Regular, Menlo, monospace; word-break: break-all; margin-bottom: 1.25rem; }
  .modal-actions { display: flex; justify-content: flex-end; gap: 0.5rem; }
  .btn-delete {
    padding: 0.45rem 0.9rem;
    background: var(--error-bg);
    border: 1px solid var(--error);
    border-radius: 7px;
    color: var(--error);
    font-size: 0.8rem;
    font-weight: 600;
    cursor: pointer;
    transition: background 0.15s, border-color 0.15s;
  }
  .btn-delete:hover { background: var(--error-bg); border-color: var(--error); }
</style>
