<script lang="ts">
  import { onMount } from 'svelte';
  import { webhookApi, type WebhookEndpoint } from '$lib/api';
  import { toast } from '$lib/stores/toast';

  const ALL_EVENTS = [
    'media.play',
    'media.pause',
    'media.resume',
    'media.stop',
    'media.scrobble',
    'library.scan.complete'
  ];

  let loading = true;
  let error = '';
  let webhooks: WebhookEndpoint[] = [];

  // Add form state
  let showAdd = false;
  let addUrl = '';
  let addSecret = '';
  let addEvents: string[] = [];
  let addSaving = false;

  // Edit state
  let editId: string | null = null;
  let editUrl = '';
  let editSecret = '';
  let editEvents: string[] = [];
  let editEnabled = true;
  let editSaving = false;

  // Delete confirmation
  let deleteId: string | null = null;

  // Test state
  let testingId: string | null = null;
  let testResult: { id: string; ok: boolean; message: string } | null = null;

  onMount(async () => {
    await loadWebhooks();
  });

  async function loadWebhooks() {
    loading = true;
    error = '';
    try {
      const result = await webhookApi.list();
      webhooks = result.items;
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed to load webhooks';
    } finally {
      loading = false;
    }
  }

  function openAdd() {
    showAdd = true;
    addUrl = '';
    addSecret = '';
    addEvents = [];
    error = '';
  }

  function cancelAdd() {
    showAdd = false;
  }

  function toggleAddEvent(event: string) {
    if (addEvents.includes(event)) {
      addEvents = addEvents.filter(e => e !== event);
    } else {
      addEvents = [...addEvents, event];
    }
  }

  async function submitAdd() {
    if (!addUrl.trim()) { error = 'URL is required'; return; }
    if (addEvents.length === 0) { error = 'Select at least one event'; return; }
    error = '';
    addSaving = true;
    try {
      const body: { url: string; secret?: string; events: string[] } = {
        url: addUrl.trim(),
        events: addEvents
      };
      if (addSecret.trim()) body.secret = addSecret.trim();
      await webhookApi.create(body);
      showAdd = false;
      toast.success('Webhook created');
      await loadWebhooks();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Failed to create webhook');
    } finally {
      addSaving = false;
    }
  }

  function startEdit(wh: WebhookEndpoint) {
    editId = wh.id;
    editUrl = wh.url;
    editSecret = '';
    editEvents = [...wh.events];
    editEnabled = wh.enabled;
    error = '';
  }

  function cancelEdit() {
    editId = null;
  }

  function toggleEditEvent(event: string) {
    if (editEvents.includes(event)) {
      editEvents = editEvents.filter(e => e !== event);
    } else {
      editEvents = [...editEvents, event];
    }
  }

  async function submitEdit() {
    if (!editId) return;
    if (!editUrl.trim()) { error = 'URL is required'; return; }
    if (editEvents.length === 0) { error = 'Select at least one event'; return; }
    error = '';
    editSaving = true;
    try {
      const body: { url?: string; secret?: string; events?: string[]; enabled?: boolean } = {
        url: editUrl.trim(),
        events: editEvents,
        enabled: editEnabled
      };
      if (editSecret.trim()) body.secret = editSecret.trim();
      await webhookApi.update(editId, body);
      editId = null;
      toast.success('Webhook updated');
      await loadWebhooks();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Failed to update webhook');
    } finally {
      editSaving = false;
    }
  }

  async function confirmDelete() {
    if (!deleteId) return;
    error = '';
    try {
      await webhookApi.del(deleteId);
      deleteId = null;
      toast.success('Webhook deleted');
      await loadWebhooks();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Failed to delete webhook');
    }
  }

  async function testWebhook(id: string) {
    testingId = id;
    testResult = null;
    try {
      await webhookApi.test(id);
      testResult = { id, ok: true, message: 'Test payload sent successfully' };
      toast.success('Test payload sent');
    } catch (e: unknown) {
      testResult = { id, ok: false, message: e instanceof Error ? e.message : 'Test failed' };
      toast.error(e instanceof Error ? e.message : 'Test failed');
    } finally {
      testingId = null;
    }
  }

  async function toggleEnabled(wh: WebhookEndpoint) {
    error = '';
    try {
      await webhookApi.update(wh.id, { enabled: !wh.enabled });
      toast.info(`Webhook ${!wh.enabled ? 'enabled' : 'disabled'}`);
      await loadWebhooks();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Failed to update webhook');
    }
  }
</script>

<svelte:head><title>Webhooks — OnScreen</title></svelte:head>

<div class="page">
  <div class="header">
    <div class="header-left">
    </div>
    {#if !showAdd && !loading}
      <button class="btn-add" on:click={openAdd}>
        <svg viewBox="0 0 20 20" fill="currentColor" width="14" height="14">
          <path d="M10.75 4.75a.75.75 0 00-1.5 0v4.5h-4.5a.75.75 0 000 1.5h4.5v4.5a.75.75 0 001.5 0v-4.5h4.5a.75.75 0 000-1.5h-4.5v-4.5z"/>
        </svg>
        Add Webhook
      </button>
    {/if}
  </div>

  {#if error}
    <div class="banner error">{error}</div>
  {/if}

  {#if loading}
    <div class="skeleton-block"></div>
  {:else}
    <!-- Add form -->
    {#if showAdd}
      <div class="card form-card">
        <div class="card-title">New Webhook</div>
        <div class="field">
          <label for="add-url">URL</label>
          <input
            id="add-url"
            type="url"
            bind:value={addUrl}
            placeholder="https://example.com/webhook"
            autocomplete="off"
            spellcheck="false"
          />
        </div>
        <div class="field">
          <label for="add-secret">Secret <span class="optional">(optional)</span></label>
          <input
            id="add-secret"
            type="password"
            bind:value={addSecret}
            placeholder="HMAC signing secret"
            autocomplete="off"
            spellcheck="false"
          />
          <div class="hint">Used to sign payloads with HMAC-SHA256. The signature is sent in the X-OnScreen-Signature header.</div>
        </div>
        <div class="field">
          <label>Events</label>
          <div class="event-grid">
            {#each ALL_EVENTS as event}
              <label class="event-check">
                <input type="checkbox" checked={addEvents.includes(event)} on:change={() => toggleAddEvent(event)} />
                <span class="event-badge">{event}</span>
              </label>
            {/each}
          </div>
        </div>
        <div class="form-actions">
          <button class="btn-cancel" on:click={cancelAdd}>Cancel</button>
          <button class="btn-save" on:click={submitAdd} disabled={addSaving}>
            {addSaving ? 'Creating...' : 'Create Webhook'}
          </button>
        </div>
      </div>
    {/if}

    <!-- Webhook list -->
    {#if webhooks.length === 0 && !showAdd}
      <div class="empty">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" width="40" height="40">
          <path stroke-linecap="round" stroke-linejoin="round" d="M13.19 8.688a4.5 4.5 0 011.242 7.244l-4.5 4.5a4.5 4.5 0 01-6.364-6.364l1.757-1.757m9.86-2.93a4.5 4.5 0 00-1.242-7.244l-4.5-4.5a4.5 4.5 0 00-6.364 6.364L5.25 9.432" transform="translate(2,2) scale(0.83)"/>
        </svg>
        <p>No webhooks configured</p>
        <p class="empty-sub">Webhooks send HTTP POST notifications when media events occur in OnScreen.</p>
        <button class="btn-add" on:click={openAdd}>
          <svg viewBox="0 0 20 20" fill="currentColor" width="14" height="14">
            <path d="M10.75 4.75a.75.75 0 00-1.5 0v4.5h-4.5a.75.75 0 000 1.5h4.5v4.5a.75.75 0 001.5 0v-4.5h4.5a.75.75 0 000-1.5h-4.5v-4.5z"/>
          </svg>
          Add Webhook
        </button>
      </div>
    {:else}
      {#each webhooks as wh (wh.id)}
        <div class="card" class:disabled-card={!wh.enabled}>
          {#if editId === wh.id}
            <!-- Edit mode -->
            <div class="card-title">Edit Webhook</div>
            <div class="field">
              <label for="edit-url">URL</label>
              <input
                id="edit-url"
                type="url"
                bind:value={editUrl}
                placeholder="https://example.com/webhook"
                autocomplete="off"
                spellcheck="false"
              />
            </div>
            <div class="field">
              <label for="edit-secret">Secret <span class="optional">(optional, leave blank to keep current)</span></label>
              <input
                id="edit-secret"
                type="password"
                bind:value={editSecret}
                placeholder="New HMAC signing secret"
                autocomplete="off"
                spellcheck="false"
              />
            </div>
            <div class="field">
              <label>Events</label>
              <div class="event-grid">
                {#each ALL_EVENTS as event}
                  <label class="event-check">
                    <input type="checkbox" checked={editEvents.includes(event)} on:change={() => toggleEditEvent(event)} />
                    <span class="event-badge">{event}</span>
                  </label>
                {/each}
              </div>
            </div>
            <div class="field">
              <label class="toggle-label">
                <span>Enabled</span>
                <button
                  class="toggle"
                  class:toggle-on={editEnabled}
                  on:click={() => editEnabled = !editEnabled}
                  type="button"
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
            <!-- Display mode -->
            <div class="wh-row">
              <div class="wh-info">
                <div class="wh-url">
                  <span class="url-text">{wh.url}</span>
                  <button
                    class="toggle-sm"
                    class:toggle-on={wh.enabled}
                    on:click={() => toggleEnabled(wh)}
                    title={wh.enabled ? 'Disable' : 'Enable'}
                    type="button"
                  >
                    <span class="toggle-knob"></span>
                  </button>
                </div>
                <div class="wh-events">
                  {#each wh.events as event}
                    <span class="event-badge-sm">{event}</span>
                  {/each}
                </div>
              </div>
              <div class="wh-actions">
                <button class="btn-icon" title="Test" on:click={() => testWebhook(wh.id)} disabled={testingId === wh.id}>
                  {#if testingId === wh.id}
                    <svg class="spinner" viewBox="0 0 20 20" width="15" height="15"><circle cx="10" cy="10" r="7" fill="none" stroke="currentColor" stroke-width="2" stroke-dasharray="30 14"/></svg>
                  {:else}
                    <svg viewBox="0 0 20 20" fill="currentColor" width="15" height="15">
                      <path fill-rule="evenodd" d="M10 18a8 8 0 100-16 8 8 0 000 16zM6.75 9.25a.75.75 0 000 1.5h4.59l-2.1 1.95a.75.75 0 001.02 1.1l3.5-3.25a.75.75 0 000-1.1l-3.5-3.25a.75.75 0 10-1.02 1.1l2.1 1.95H6.75z" clip-rule="evenodd"/>
                    </svg>
                  {/if}
                </button>
                <button class="btn-icon" title="Edit" on:click={() => startEdit(wh)}>
                  <svg viewBox="0 0 20 20" fill="currentColor" width="15" height="15">
                    <path d="M2.695 14.763l-1.262 3.154a.5.5 0 00.65.65l3.155-1.262a4 4 0 001.343-.885L17.5 5.5a2.121 2.121 0 00-3-3L3.58 13.42a4 4 0 00-.885 1.343z"/>
                  </svg>
                </button>
                <button class="btn-icon btn-danger" title="Delete" on:click={() => deleteId = wh.id}>
                  <svg viewBox="0 0 20 20" fill="currentColor" width="15" height="15">
                    <path fill-rule="evenodd" d="M8.75 1A2.75 2.75 0 006 3.75v.443c-.795.077-1.584.176-2.365.298a.75.75 0 10.23 1.482l.149-.022.841 10.518A2.75 2.75 0 007.596 19h4.807a2.75 2.75 0 002.742-2.53l.841-10.519.149.023a.75.75 0 00.23-1.482A41.03 41.03 0 0014 4.193V3.75A2.75 2.75 0 0011.25 1h-2.5zM10 4c.84 0 1.673.025 2.5.075V3.75c0-.69-.56-1.25-1.25-1.25h-2.5c-.69 0-1.25.56-1.25 1.25v.325C8.327 4.025 9.16 4 10 4zM8.58 7.72a.75.75 0 00-1.5.06l.3 7.5a.75.75 0 101.5-.06l-.3-7.5zm4.34.06a.75.75 0 10-1.5-.06l-.3 7.5a.75.75 0 101.5.06l.3-7.5z" clip-rule="evenodd"/>
                  </svg>
                </button>
              </div>
            </div>
            {#if testResult && testResult.id === wh.id}
              <div class="test-result" class:test-ok={testResult.ok} class:test-fail={!testResult.ok}>
                {testResult.message}
              </div>
            {/if}
          {/if}
        </div>

        <!-- Delete confirmation modal -->
        {#if deleteId === wh.id}
          <div class="modal-overlay" on:click={() => deleteId = null} on:keydown={e => e.key === 'Escape' && (deleteId = null)} role="button" tabindex="-1">
            <div class="modal" on:click|stopPropagation role="dialog" aria-label="Confirm delete">
              <p class="modal-text">Delete this webhook?</p>
              <p class="modal-sub">{wh.url}</p>
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
    align-items: flex-end;
    justify-content: space-between;
    margin-bottom: 1.75rem;
    gap: 1rem;
  }
  .header-left { display: flex; flex-direction: column; gap: 0.5rem; }

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

  /* Cards */
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

  /* Form fields */
  .field { display: flex; flex-direction: column; gap: 0.3rem; margin-bottom: 1rem; }

  label { font-size: 0.75rem; font-weight: 500; color: var(--text-muted); }
  .optional { font-weight: 400; color: var(--text-muted); }

  input[type="url"],
  input[type="password"] {
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
  input:focus { outline: none; border-color: var(--accent); box-shadow: 0 0 0 3px var(--accent-bg); }
  ::placeholder { color: #2a2a3d; }

  .hint {
    font-size: 0.72rem;
    color: var(--text-muted);
    line-height: 1.5;
    margin-top: 0.15rem;
  }

  /* Event checkboxes */
  .event-grid {
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem;
    margin-top: 0.25rem;
  }
  .event-check {
    display: flex;
    align-items: center;
    gap: 0.35rem;
    cursor: pointer;
  }
  .event-check input[type="checkbox"] {
    accent-color: var(--accent);
    width: 14px;
    height: 14px;
    cursor: pointer;
  }
  .event-badge {
    font-size: 0.72rem;
    color: var(--text-secondary);
    font-family: monospace;
  }

  /* Form actions */
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

  /* Webhook row */
  .wh-row {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 1rem;
  }
  .wh-info { flex: 1; min-width: 0; }
  .wh-url {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    margin-bottom: 0.45rem;
  }
  .url-text {
    font-size: 0.85rem;
    color: var(--text-primary);
    font-family: monospace;
    word-break: break-all;
  }
  .wh-events {
    display: flex;
    flex-wrap: wrap;
    gap: 0.35rem;
  }
  .event-badge-sm {
    font-size: 0.65rem;
    font-family: monospace;
    padding: 0.15rem 0.45rem;
    border-radius: 4px;
    background: var(--accent-bg);
    color: var(--accent-text);
  }

  .wh-actions {
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

  /* Toggle */
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

  /* Test result */
  .test-result {
    margin-top: 0.6rem;
    padding: 0.4rem 0.7rem;
    border-radius: 6px;
    font-size: 0.75rem;
  }
  .test-ok { background: rgba(52,211,153,0.1); border: 1px solid rgba(52,211,153,0.2); color: #6ee7b7; }
  .test-fail { background: rgba(248,113,113,0.1); border: 1px solid rgba(248,113,113,0.2); color: #fca5a5; }

  /* Spinner */
  .spinner { animation: spin 0.7s linear infinite; }
  @keyframes spin { from { transform: rotate(0deg); } to { transform: rotate(360deg); } }

  /* Empty state */
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
  .empty .empty-sub { font-size: 0.75rem; color: var(--text-muted); max-width: 320px; }

  /* Delete modal */
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
