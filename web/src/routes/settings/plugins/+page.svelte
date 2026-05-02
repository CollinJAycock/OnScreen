<script lang="ts">
  import { onMount } from 'svelte';
  import { pluginApi, type Plugin, type PluginRole } from '$lib/api';
  import { toast } from '$lib/stores/toast';

  // Only notification is wired end-to-end in v1; metadata/task are reserved.
  const ROLES: { value: PluginRole; label: string; enabled: boolean; hint: string }[] = [
    { value: 'notification', label: 'Notification', enabled: true, hint: 'Receives media.play / pause / stop / scrobble events' },
    { value: 'metadata', label: 'Metadata', enabled: false, hint: 'Coming soon' },
    { value: 'task', label: 'Task', enabled: false, hint: 'Coming soon' }
  ];

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
      error = e instanceof Error ? e.message : 'Failed to load plugins';
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
    if (!addName.trim()) { error = 'Name is required'; return; }
    if (!addEndpoint.trim()) { error = 'Endpoint URL is required'; return; }
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
      toast.success('Plugin created');
      await loadPlugins();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Failed to create plugin');
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
    if (!editName.trim()) { error = 'Name is required'; return; }
    if (!editEndpoint.trim()) { error = 'Endpoint URL is required'; return; }
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
      toast.success('Plugin updated');
      await loadPlugins();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Failed to update plugin');
    } finally {
      editSaving = false;
    }
  }

  async function confirmDelete() {
    if (!deleteId) return;
    try {
      await pluginApi.del(deleteId);
      deleteId = null;
      toast.success('Plugin deleted');
      await loadPlugins();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Failed to delete plugin');
    }
  }

  async function toggleEnabled(p: Plugin) {
    try {
      await pluginApi.update(p.id, { enabled: !p.enabled });
      toast.info(`Plugin ${!p.enabled ? 'enabled' : 'disabled'}`);
      await loadPlugins();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Failed to update plugin');
    }
  }

  async function testPlugin(id: string) {
    testingId = id;
    testResult = null;
    try {
      await pluginApi.test(id);
      testResult = { id, ok: true, message: 'Test event delivered successfully' };
      toast.success('Test event delivered');
    } catch (e: unknown) {
      testResult = { id, ok: false, message: e instanceof Error ? e.message : 'Test failed' };
      toast.error(e instanceof Error ? e.message : 'Test failed');
    } finally {
      testingId = null;
    }
  }
</script>

<svelte:head><title>Plugins — OnScreen</title></svelte:head>

<div class="page">
  <div class="header">
    <div class="header-left">
      <p class="intro">
        Plugins are external MCP servers OnScreen calls out to when events fire.
        Endpoints must speak Streamable HTTP and expose a <code>notify</code> tool.
      </p>
    </div>
    {#if !showAdd && !loading}
      <button class="btn-add" on:click={openAdd}>
        <svg viewBox="0 0 20 20" fill="currentColor" width="14" height="14">
          <path d="M10.75 4.75a.75.75 0 00-1.5 0v4.5h-4.5a.75.75 0 000 1.5h4.5v4.5a.75.75 0 001.5 0v-4.5h4.5a.75.75 0 000-1.5h-4.5v-4.5z"/>
        </svg>
        Add Plugin
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
        <div class="card-title">New Plugin</div>
        <div class="field">
          <label for="add-name">Name</label>
          <input
            id="add-name"
            type="text"
            bind:value={addName}
            placeholder="My Slack notifier"
            autocomplete="off"
            spellcheck="false"
          />
        </div>
        <div class="field">
          <label for="add-role">Role</label>
          <select id="add-role" bind:value={addRole}>
            {#each ROLES as r}
              <option value={r.value} disabled={!r.enabled}>
                {r.label}{r.enabled ? '' : ' — coming soon'}
              </option>
            {/each}
          </select>
          <div class="hint">{ROLES.find(r => r.value === addRole)?.hint ?? ''}</div>
        </div>
        <div class="field">
          <label for="add-endpoint">Endpoint URL</label>
          <input
            id="add-endpoint"
            type="url"
            bind:value={addEndpoint}
            placeholder="https://mcp.example.com/mcp"
            autocomplete="off"
            spellcheck="false"
          />
          <div class="hint">Full URL to the plugin's Streamable HTTP MCP endpoint.</div>
        </div>
        <div class="field">
          <label for="add-hosts">Allowed hosts <span class="optional">(optional)</span></label>
          <textarea
            id="add-hosts"
            bind:value={addHosts}
            placeholder="api.example.com&#10;cdn.example.com"
            rows="3"
            autocomplete="off"
            spellcheck="false"
          ></textarea>
          <div class="hint">
            Extra hostnames this plugin may contact, one per line or comma-separated.
            The endpoint's own host is always allowed. Private/loopback IPs are always blocked.
          </div>
        </div>
        <div class="form-actions">
          <button class="btn-cancel" on:click={cancelAdd}>Cancel</button>
          <button class="btn-save" on:click={submitAdd} disabled={addSaving}>
            {addSaving ? 'Creating...' : 'Create Plugin'}
          </button>
        </div>
      </div>
    {/if}

    {#if plugins.length === 0 && !showAdd}
      <div class="empty">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" width="40" height="40">
          <path stroke-linecap="round" stroke-linejoin="round" d="M14.25 6.087c0-.355.186-.676.401-.959.221-.29.349-.634.349-1.003 0-1.036-1.007-1.875-2.25-1.875s-2.25.84-2.25 1.875c0 .369.128.713.349 1.003.215.283.401.604.401.959v0a.64.64 0 01-.657.643 48.39 48.39 0 01-4.163-.3c.186 1.613.293 3.25.315 4.907a.656.656 0 01-.658.663v0c-.355 0-.676-.186-.959-.401a1.647 1.647 0 00-1.003-.349c-1.036 0-1.875 1.007-1.875 2.25s.84 2.25 1.875 2.25c.369 0 .713-.128 1.003-.349.283-.215.604-.401.959-.401v0c.31 0 .555.26.532.57a48.039 48.039 0 01-.642 5.056c1.518.19 3.058.309 4.616.354a.64.64 0 00.657-.643v0c0-.355-.186-.676-.401-.959a1.647 1.647 0 01-.349-1.003c0-1.035 1.008-1.875 2.25-1.875 1.243 0 2.25.84 2.25 1.875 0 .369-.128.713-.349 1.003-.215.283-.4.604-.4.959v0c0 .333.277.599.61.58a48.1 48.1 0 005.427-.63 48.05 48.05 0 00.582-4.717.532.532 0 00-.533-.57v0c-.355 0-.676.186-.959.401-.29.221-.634.349-1.003.349-1.035 0-1.875-1.007-1.875-2.25s.84-2.25 1.875-2.25c.37 0 .713.128 1.003.349.283.215.604.401.96.401v0a.656.656 0 00.658-.663 48.422 48.422 0 00-.37-5.36c-1.886.342-3.81.574-5.766.689a.578.578 0 01-.61-.58v0z"/>
        </svg>
        <p>No plugins configured</p>
        <p class="empty-sub">Plugins extend OnScreen with external MCP servers for notifications and automation.</p>
        <button class="btn-add" on:click={openAdd}>
          <svg viewBox="0 0 20 20" fill="currentColor" width="14" height="14">
            <path d="M10.75 4.75a.75.75 0 00-1.5 0v4.5h-4.5a.75.75 0 000 1.5h4.5v4.5a.75.75 0 001.5 0v-4.5h4.5a.75.75 0 000-1.5h-4.5v-4.5z"/>
          </svg>
          Add Plugin
        </button>
      </div>
    {:else}
      {#each plugins as p (p.id)}
        <div class="card" class:disabled-card={!p.enabled}>
          {#if editId === p.id}
            <div class="card-title">Edit Plugin</div>
            <div class="field">
              <label for="edit-name">Name</label>
              <input
                id="edit-name"
                type="text"
                bind:value={editName}
                autocomplete="off"
                spellcheck="false"
              />
            </div>
            <div class="field">
              <span class="field-heading">Role</span>
              <div class="readonly-field">
                <span class="role-pill role-{p.role}">{p.role}</span>
                <span class="hint inline">Role is fixed after creation</span>
              </div>
            </div>
            <div class="field">
              <label for="edit-endpoint">Endpoint URL</label>
              <input
                id="edit-endpoint"
                type="url"
                bind:value={editEndpoint}
                autocomplete="off"
                spellcheck="false"
              />
            </div>
            <div class="field">
              <label for="edit-hosts">Allowed hosts</label>
              <textarea
                id="edit-hosts"
                bind:value={editHosts}
                placeholder="One host per line"
                rows="3"
                autocomplete="off"
                spellcheck="false"
              ></textarea>
            </div>
            <div class="field">
              <label class="toggle-label">
                <span>Enabled</span>
                <button
                  class="toggle"
                  class:toggle-on={editEnabled}
                  on:click={() => editEnabled = !editEnabled}
                  type="button"
                  title="Enabled"
                  aria-label="Enabled"
                  aria-pressed={editEnabled}
                >
                  <span class="toggle-knob"></span>
                </button>
              </label>
            </div>
            <div class="form-actions">
              <button class="btn-cancel" on:click={cancelEdit}>Cancel</button>
              <button class="btn-save" on:click={submitEdit} disabled={editSaving}>
                {editSaving ? 'Saving...' : 'Save Changes'}
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
                    title={p.enabled ? 'Disable' : 'Enable'}
                    type="button"
                  >
                    <span class="toggle-knob"></span>
                  </button>
                </div>
                <div class="p-endpoint">{p.endpoint_url}</div>
                {#if p.allowed_hosts.length > 0}
                  <div class="p-hosts">
                    {#each p.allowed_hosts as host}
                      <span class="host-badge">{host}</span>
                    {/each}
                  </div>
                {/if}
              </div>
              <div class="p-actions">
                <button class="btn-icon" title="Test" on:click={() => testPlugin(p.id)} disabled={testingId === p.id}>
                  {#if testingId === p.id}
                    <svg class="spinner" viewBox="0 0 20 20" width="15" height="15"><circle cx="10" cy="10" r="7" fill="none" stroke="currentColor" stroke-width="2" stroke-dasharray="30 14"/></svg>
                  {:else}
                    <svg viewBox="0 0 20 20" fill="currentColor" width="15" height="15">
                      <path fill-rule="evenodd" d="M10 18a8 8 0 100-16 8 8 0 000 16zM6.75 9.25a.75.75 0 000 1.5h4.59l-2.1 1.95a.75.75 0 001.02 1.1l3.5-3.25a.75.75 0 000-1.1l-3.5-3.25a.75.75 0 10-1.02 1.1l2.1 1.95H6.75z" clip-rule="evenodd"/>
                    </svg>
                  {/if}
                </button>
                <button class="btn-icon" title="Edit" on:click={() => startEdit(p)}>
                  <svg viewBox="0 0 20 20" fill="currentColor" width="15" height="15">
                    <path d="M2.695 14.763l-1.262 3.154a.5.5 0 00.65.65l3.155-1.262a4 4 0 001.343-.885L17.5 5.5a2.121 2.121 0 00-3-3L3.58 13.42a4 4 0 00-.885 1.343z"/>
                  </svg>
                </button>
                <button class="btn-icon btn-danger" title="Delete" on:click={() => deleteId = p.id}>
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
            <div class="modal" on:click|stopPropagation role="dialog" aria-label="Confirm delete">
              <p class="modal-text">Delete this plugin?</p>
              <p class="modal-sub">{p.name}</p>
              <div class="modal-actions">
                <button class="btn-cancel" on:click={() => deleteId = null}>Cancel</button>
                <button class="btn-delete" on:click={confirmDelete}>Delete</button>
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
  .banner.error { background: rgba(248,113,113,0.1); border: 1px solid rgba(248,113,113,0.2); color: #fca5a5; }

  .skeleton-block {
    height: 100px; border-radius: 10px;
    background: linear-gradient(90deg, var(--bg-elevated) 25%, var(--bg-hover) 50%, var(--bg-elevated) 75%);
    background-size: 200% 100%;
    animation: shimmer 1.4s infinite;
  }
  @keyframes shimmer { 0% { background-position: 200% 0; } 100% { background-position: -200% 0; } }

  .card {
    background: rgba(255,255,255,0.025);
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
  .form-card { border-color: rgba(124,106,247,0.2); }

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
    font-family: monospace;
    transition: border-color 0.15s;
    width: 100%;
  }
  textarea { resize: vertical; min-height: 60px; }
  select { font-family: inherit; cursor: pointer; }
  input:focus, select:focus, textarea:focus {
    outline: none; border-color: var(--accent); box-shadow: 0 0 0 3px var(--accent-bg);
  }
  ::placeholder { color: #2a2a3d; }

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
    padding: 0.42rem 0.9rem;
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
    padding: 0.42rem 0.9rem;
    background: transparent;
    border: 1px solid var(--border-strong);
    border-radius: 7px;
    color: #66667a;
    font-size: 0.8rem;
    font-weight: 500;
    cursor: pointer;
    transition: border-color 0.15s, color 0.15s;
  }
  .btn-cancel:hover { border-color: var(--border-strong); color: var(--text-secondary); }

  .btn-add {
    display: inline-flex;
    align-items: center;
    gap: 0.35rem;
    padding: 0.42rem 0.8rem;
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
  .p-hosts {
    display: flex;
    flex-wrap: wrap;
    gap: 0.35rem;
  }
  .host-badge {
    font-size: 0.65rem;
    font-family: monospace;
    padding: 0.15rem 0.45rem;
    border-radius: 4px;
    background: rgba(255,255,255,0.05);
    color: var(--text-muted);
  }
  .role-pill {
    font-size: 0.62rem;
    font-weight: 600;
    padding: 0.15rem 0.5rem;
    border-radius: 10px;
    text-transform: uppercase;
    letter-spacing: 0.05em;
  }
  .role-notification { background: rgba(124,106,247,0.15); color: var(--accent-text); }
  .role-metadata { background: rgba(52,211,153,0.12); color: #6ee7b7; }
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
    background: transparent;
    border: 1px solid var(--border);
    border-radius: 6px;
    color: var(--text-muted);
    cursor: pointer;
    transition: background 0.12s, color 0.12s, border-color 0.12s;
  }
  .btn-icon:hover { background: var(--bg-hover); color: var(--text-secondary); border-color: var(--border-strong); }
  .btn-icon:disabled { opacity: 0.4; cursor: not-allowed; }
  .btn-icon.btn-danger:hover { background: rgba(248,113,113,0.1); color: #fca5a5; border-color: rgba(248,113,113,0.2); }

  .test-result {
    margin-top: 0.6rem;
    padding: 0.4rem 0.7rem;
    border-radius: 6px;
    font-size: 0.75rem;
  }
  .test-ok { background: rgba(52,211,153,0.1); border: 1px solid rgba(52,211,153,0.2); color: #6ee7b7; }
  .test-fail { background: rgba(248,113,113,0.1); border: 1px solid rgba(248,113,113,0.2); color: #fca5a5; }

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
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 1000;
  }
  .modal {
    background: var(--bg-elevated);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 1.5rem;
    max-width: 380px;
    width: 90%;
  }
  .modal-text { font-size: 0.92rem; font-weight: 600; color: var(--text-primary); margin-bottom: 0.35rem; }
  .modal-sub { font-size: 0.78rem; color: var(--text-muted); font-family: monospace; word-break: break-all; margin-bottom: 1.25rem; }
  .modal-actions { display: flex; justify-content: flex-end; gap: 0.6rem; }
  .btn-delete {
    padding: 0.42rem 0.9rem;
    background: rgba(248,113,113,0.15);
    border: 1px solid rgba(248,113,113,0.25);
    border-radius: 7px;
    color: #fca5a5;
    font-size: 0.8rem;
    font-weight: 600;
    cursor: pointer;
    transition: background 0.15s;
  }
  .btn-delete:hover { background: rgba(248,113,113,0.25); }
</style>
