<script lang="ts">
  // Settings → Notifications: server-wide outbound notification agents — a
  // household Discord channel, a Telegram group, an ntfy topic, a Gotify
  // server or an email list — and which events each one hears (requests,
  // problem reports, new content, ops failures). Secrets are write-only: the
  // server only says whether one is configured.
  import { onMount } from 'svelte';
  import {
    notificationAgentApi,
    type NotificationAgent,
    type NotificationAgentEvent,
    type NotificationAgentKind,
  } from '$lib/api';
  import { toast } from '$lib/stores/toast';
  import { t, i18n } from '$lib/i18n/index.svelte';
  import {
    EVENT_GROUPS,
    KINDS,
    agentStatus,
    createPayload,
    destinationSummary,
    emptyForm,
    eventLabel,
    eventsInGroup,
    formFromAgent,
    kindLabel,
    secretField,
    selfHostable,
    toggleEvent,
    updatePayload,
    validateForm,
    type AgentForm,
  } from './notifications';

  let agents = $state<NotificationAgent[]>([]);
  let catalog = $state<NotificationAgentEvent[]>([]);
  let loading = $state(true);
  let loadError = $state('');

  // The add / edit form. editing = the agent being edited, null when adding.
  let form = $state<AgentForm | null>(null);
  let editing = $state<NotificationAgent | null>(null);
  let formError = $state('');
  let saving = $state(false);

  let testing = $state<Record<string, boolean>>({});
  let testResults = $state<Record<string, { ok: boolean; message: string }>>({});
  let deleteTarget = $state<NotificationAgent | null>(null);
  let now = $state(Date.now());

  let secret = $derived(form ? secretField(form.kind) : null);

  function errText(e: unknown, fallback: string): string {
    return e instanceof Error && e.message ? e.message : fallback;
  }

  async function load() {
    loadError = '';
    try {
      const [list, events] = await Promise.all([notificationAgentApi.list(), notificationAgentApi.events()]);
      agents = list.items;
      catalog = events;
      now = Date.now();
    } catch (e) {
      loadError = errText(e, t('settings.notifications.loadFailed'));
    } finally {
      loading = false;
    }
  }

  onMount(load);

  function startAdd(kind: NotificationAgentKind) {
    editing = null;
    form = emptyForm(kind, catalog);
    formError = '';
  }

  function startEdit(a: NotificationAgent) {
    editing = a;
    form = formFromAgent(a);
    formError = '';
  }

  function cancelForm() {
    form = null;
    editing = null;
    formError = '';
  }

  async function save() {
    if (!form) return;
    const problem = validateForm(form, editing);
    if (problem) {
      formError = problem;
      return;
    }
    formError = '';
    saving = true;
    try {
      if (editing) {
        await notificationAgentApi.update(editing.id, updatePayload(form));
        toast.success(t('settings.notifications.updated'));
      } else {
        await notificationAgentApi.create(createPayload(form));
        toast.success(t('settings.notifications.added'));
      }
      form = null;
      editing = null;
      await load();
    } catch (e) {
      formError = errText(e, t('settings.notifications.saveFailed'));
    } finally {
      saving = false;
    }
  }

  async function toggleEnabled(a: NotificationAgent) {
    try {
      await notificationAgentApi.update(a.id, { enabled: !a.enabled });
      await load();
    } catch (e) {
      toast.error(errText(e, t('settings.notifications.updateFailed')));
    }
  }

  async function sendTest(a: NotificationAgent) {
    testing[a.id] = true;
    delete testResults[a.id];
    try {
      const res = await notificationAgentApi.test(a.id);
      testResults[a.id] = res.ok
        ? { ok: true, message: t('settings.notifications.testSent') }
        : { ok: false, message: res.error || t('settings.notifications.testUndelivered') };
      await load();
    } catch (e) {
      testResults[a.id] = { ok: false, message: errText(e, t('settings.notifications.testFailed')) };
    } finally {
      testing[a.id] = false;
    }
  }

  async function confirmDelete() {
    const target = deleteTarget;
    if (!target) return;
    deleteTarget = null;
    try {
      await notificationAgentApi.del(target.id);
      toast.success(t('settings.notifications.deleted'));
      if (editing?.id === target.id) cancelForm();
      await load();
    } catch (e) {
      toast.error(errText(e, t('settings.notifications.deleteFailed')));
    }
  }
</script>

<svelte:head><title>{$i18n('settings.notifications.pageTitle')}</title></svelte:head>

<div class="page">
  <p class="intro">{$i18n('settings.notifications.intro')}</p>

  {#if loadError}
    <div class="banner error">{loadError}</div>
  {/if}

  {#if !form && !loading}
    <div class="add-row" role="group" aria-label={$i18n('settings.notifications.addAgentGroup')}>
      {#each KINDS as k (k.kind)}
        <button type="button" class="kind-btn" onclick={() => startAdd(k.kind)} title={$i18n(k.blurb)}>
          + {k.labelKey ? $i18n(k.labelKey) : k.label}
        </button>
      {/each}
    </div>
  {/if}

  {#if form}
    <div class="card form-card">
      <div class="card-title">{editing ? $i18n('settings.notifications.editAgent', { kind: kindLabel(form.kind) }) : $i18n('settings.notifications.newAgent', { kind: kindLabel(form.kind) })}</div>

      <div class="field">
        <label for="na-name">{$i18n('settings.notifications.name')}</label>
        <input id="na-name" type="text" bind:value={form.name} maxlength="100" autocomplete="off" />
      </div>

      {#if form.kind === 'telegram'}
        <div class="field">
          <label for="na-chat">{$i18n('settings.notifications.chatId')}</label>
          <input id="na-chat" type="text" bind:value={form.chatId} placeholder={$i18n('settings.notifications.chatIdPlaceholder', { id: '-1001234567890', channel: '@channel' })} autocomplete="off" spellcheck="false" />
          <div class="hint">{$i18n('settings.notifications.chatIdHint')}</div>
        </div>
      {/if}

      {#if form.kind === 'ntfy' || form.kind === 'gotify'}
        <div class="field">
          <label for="na-server">{$i18n('settings.notifications.serverUrl')}</label>
          <input
            id="na-server"
            type="url"
            bind:value={form.serverUrl}
            placeholder={form.kind === 'ntfy' ? 'https://ntfy.sh' : 'https://gotify.example.com'}
            autocomplete="off"
            spellcheck="false"
          />
        </div>
      {/if}

      {#if form.kind === 'ntfy'}
        <div class="field">
          <label for="na-topic">{$i18n('settings.notifications.topic')}</label>
          <input id="na-topic" type="text" bind:value={form.topic} placeholder="onscreen-household" autocomplete="off" spellcheck="false" />
          <div class="hint">{$i18n('settings.notifications.topicHint')}</div>
        </div>
      {/if}

      {#if secret}
        <div class="field">
          <label for="na-secret">
            {secret.label}
            {#if !secret.required}<span class="optional">{$i18n('settings.notifications.optional')}</span>{/if}
          </label>
          <input
            id="na-secret"
            type="password"
            bind:value={form.secret}
            placeholder={editing?.secret_configured ? $i18n('settings.notifications.secretKeep') : secret.placeholder}
            autocomplete="new-password"
            spellcheck="false"
          />
          <div class="hint">{secret.hint}</div>
          {#if editing?.secret_configured && !secret.required}
            <label class="check">
              <input type="checkbox" bind:checked={form.clearSecret} disabled={form.secret.trim() !== ''} />
              {$i18n('settings.notifications.removeSecret', { field: secret.label.toLowerCase() })}
            </label>
          {/if}
        </div>
      {/if}

      {#if form.kind === 'email'}
        <div class="field">
          <label for="na-recipients">{$i18n('settings.notifications.recipients')}</label>
          <textarea id="na-recipients" rows="2" bind:value={form.recipients} placeholder="you@example.com, partner@example.com" spellcheck="false"></textarea>
          <div class="hint">{$i18n('settings.notifications.recipientsHint', { max: 10 })}</div>
        </div>
      {/if}

      {#if selfHostable(form.kind)}
        <label class="check">
          <input type="checkbox" bind:checked={form.allowPrivateNetwork} />
          {$i18n('settings.notifications.allowPrivate')}
        </label>
        <div class="hint indent">
          {$i18n('settings.notifications.allowPrivateHint', { kind: kindLabel(form.kind) })}
        </div>
      {/if}

      <fieldset class="field events">
        <legend>{$i18n('settings.notifications.events')}</legend>
        {#each EVENT_GROUPS as g (g.group)}
          {#if eventsInGroup(catalog, g.group).length > 0}
            <div class="event-group">
              <div class="group-label">{$i18n(g.label)}</div>
              {#each eventsInGroup(catalog, g.group) as ev (ev.key)}
                <label class="event-check">
                  <input
                    type="checkbox"
                    checked={form.events.includes(ev.key)}
                    onchange={() => form && (form.events = toggleEvent(form.events, ev.key))}
                  />
                  <span>
                    <span class="event-name">{ev.label}</span>
                    <span class="event-desc">{ev.description}</span>
                  </span>
                </label>
              {/each}
            </div>
          {/if}
        {/each}
      </fieldset>

      <label class="check">
        <input type="checkbox" bind:checked={form.enabled} />
        {$i18n('settings.notifications.enabled')}
      </label>

      {#if formError}
        <div class="banner error form-error" role="alert">{formError}</div>
      {/if}

      <div class="form-actions">
        <button type="button" class="btn" onclick={cancelForm}>{$i18n('settings.notifications.cancel')}</button>
        <button type="button" class="btn primary" onclick={save} disabled={saving}>
          {saving ? $i18n('settings.notifications.saving') : editing ? $i18n('settings.notifications.saveChanges') : $i18n('settings.notifications.addAgent')}
        </button>
      </div>
    </div>
  {/if}

  {#if loading}
    <div class="skeleton-block"></div>
  {:else if agents.length === 0 && !form}
    <div class="empty">
      <p>{$i18n('settings.notifications.empty')}</p>
      <p class="empty-sub">{$i18n('settings.notifications.emptySub')}</p>
    </div>
  {:else}
    {#each agents as a (a.id)}
      {@const st = agentStatus(a, now)}
      <div class="card agent" class:disabled-card={!a.enabled} data-testid="agent-row">
        <div class="agent-row">
          <div class="agent-info">
            <div class="agent-head">
              <span class="kind-badge kind-{a.kind}">{kindLabel(a.kind)}</span>
              <span class="agent-name">{a.name}</span>
            </div>
            <div class="agent-dest">{destinationSummary(a)}</div>
            <div class="status status-{st.tone}">{st.text}</div>
            {#if st.detail}
              <div class="status-detail">{st.detail}</div>
            {/if}
            <div class="agent-events">
              {#each a.events as key (key)}
                <span class="event-badge">{eventLabel(catalog, key)}</span>
              {/each}
            </div>
          </div>
          <div class="agent-actions">
            <label class="check small" title={a.enabled ? $i18n('settings.notifications.disable') : $i18n('settings.notifications.enable')}>
              <input type="checkbox" checked={a.enabled} onchange={() => toggleEnabled(a)} aria-label={$i18n('settings.notifications.enabled')} />
              {$i18n('settings.notifications.on')}
            </label>
            <button type="button" class="btn" disabled={testing[a.id]} onclick={() => sendTest(a)}>
              {testing[a.id] ? $i18n('settings.notifications.sending') : $i18n('settings.notifications.sendTest')}
            </button>
            <button type="button" class="btn" onclick={() => startEdit(a)}>{$i18n('settings.notifications.edit')}</button>
            <button type="button" class="btn danger" onclick={() => (deleteTarget = a)}>{$i18n('settings.notifications.delete')}</button>
          </div>
        </div>
        {#if testResults[a.id]}
          <div class="test-result" class:test-ok={testResults[a.id].ok} class:test-fail={!testResults[a.id].ok} role="status">
            {testResults[a.id].message}
          </div>
        {/if}
      </div>
    {/each}
  {/if}

  {#if deleteTarget}
    <div class="modal-overlay" role="presentation" onclick={() => (deleteTarget = null)}>
      <div
        class="modal"
        role="dialog"
        aria-modal="true"
        aria-label={$i18n('settings.notifications.confirmDelete')}
        tabindex="-1"
        onclick={(e) => e.stopPropagation()}
        onkeydown={(e) => e.key === 'Escape' && (deleteTarget = null)}
      >
        <p class="modal-text">{$i18n('settings.notifications.deleteQuestion', { name: deleteTarget.name })}</p>
        <p class="modal-sub">{$i18n('settings.notifications.deleteSub')}</p>
        <div class="modal-actions">
          <button type="button" class="btn" onclick={() => (deleteTarget = null)}>{$i18n('settings.notifications.cancel')}</button>
          <button type="button" class="btn danger solid" onclick={confirmDelete}>{$i18n('settings.notifications.delete')}</button>
        </div>
      </div>
    </div>
  {/if}
</div>

<style>
  .page { max-width: 720px; }
  .intro { font-size: 0.82rem; color: var(--text-muted); line-height: 1.55; margin: 0 0 1.25rem; }

  .banner { padding: 0.6rem 0.9rem; border-radius: 7px; font-size: 0.8rem; margin-bottom: 1rem; }
  .banner.error { background: var(--error-bg); border: 1px solid var(--error); color: var(--error); }
  .form-error { margin: 0.5rem 0 0; }

  .add-row { display: flex; flex-wrap: wrap; gap: 0.5rem; margin-bottom: 1.25rem; }
  .kind-btn {
    padding: 0.42rem 0.8rem; border-radius: 7px; border: 1px solid var(--border-strong);
    background: var(--bg-elevated); color: var(--text-secondary); font-size: 0.8rem; font-weight: 600; cursor: pointer;
  }
  .kind-btn:hover { border-color: var(--accent); color: var(--text-primary); }

  .skeleton-block {
    height: 100px; border-radius: 10px;
    background: linear-gradient(90deg, var(--bg-elevated) 25%, var(--bg-hover) 50%, var(--bg-elevated) 75%);
    background-size: 200% 100%; animation: shimmer 1.4s infinite;
  }
  @keyframes shimmer { 0% { background-position: 200% 0; } 100% { background-position: -200% 0; } }

  .card {
    background: var(--bg-elevated); border: 1px solid var(--border); border-radius: 10px;
    padding: 1.1rem 1.25rem; margin-bottom: 0.75rem;
  }
  .card.disabled-card { opacity: 0.6; }
  .form-card { border-color: var(--accent); }
  .card-title {
    font-size: 0.78rem; font-weight: 700; color: var(--accent-text); margin-bottom: 1rem;
    text-transform: uppercase; letter-spacing: 0.06em;
  }

  .field { display: flex; flex-direction: column; gap: 0.3rem; margin-bottom: 1rem; }
  label, legend { font-size: 0.75rem; font-weight: 500; color: var(--text-muted); }
  .optional { font-weight: 400; }
  input[type='text'], input[type='url'], input[type='password'], textarea {
    background: var(--input-bg); border: 1px solid var(--border-strong); border-radius: 7px;
    padding: 0.48rem 0.7rem; font-size: 0.85rem; color: var(--text-primary); width: 100%; font-family: inherit;
  }
  input:focus, textarea:focus { outline: none; border-color: var(--accent); box-shadow: 0 0 0 3px var(--accent-bg); }
  ::placeholder { color: var(--text-muted); }
  .hint { font-size: 0.72rem; color: var(--text-muted); line-height: 1.5; }
  .hint.indent { margin: -0.1rem 0 1rem 1.4rem; }

  .check { display: flex; align-items: center; gap: 0.4rem; font-size: 0.8rem; color: var(--text-secondary); cursor: pointer; margin-bottom: 0.4rem; }
  .check input { accent-color: var(--accent); }
  .check.small { font-size: 0.72rem; margin: 0; }

  .events { border: 0; padding: 0; min-width: 0; }
  .event-group { margin-top: 0.5rem; }
  .group-label { font-size: 0.68rem; font-weight: 700; text-transform: uppercase; letter-spacing: 0.06em; color: var(--text-muted); margin-bottom: 0.25rem; }
  .event-check { display: flex; align-items: flex-start; gap: 0.5rem; padding: 0.25rem 0; cursor: pointer; }
  .event-check input { margin-top: 0.15rem; accent-color: var(--accent); }
  .event-name { display: block; font-size: 0.8rem; color: var(--text-primary); font-weight: 500; }
  .event-desc { display: block; font-size: 0.72rem; color: var(--text-muted); }

  .form-actions { display: flex; justify-content: flex-end; gap: 0.6rem; margin-top: 0.75rem; }
  .btn {
    padding: 0.4rem 0.8rem; border-radius: 7px; border: 1px solid var(--border-strong); background: transparent;
    color: var(--text-secondary); font-size: 0.78rem; font-weight: 500; cursor: pointer; white-space: nowrap;
  }
  .btn:hover:not(:disabled) { background: var(--bg-hover); color: var(--text-primary); }
  .btn.primary { background: var(--accent); border-color: var(--accent); color: #fff; font-weight: 600; }
  .btn.primary:hover:not(:disabled) { background: var(--accent-hover); }
  .btn.danger { color: var(--error); }
  .btn.danger.solid { background: var(--error); border-color: var(--error); color: #fff; }
  .btn:disabled { opacity: 0.5; cursor: progress; }

  .agent-row { display: flex; justify-content: space-between; gap: 1rem; align-items: flex-start; flex-wrap: wrap; }
  .agent-info { flex: 1; min-width: 220px; }
  .agent-head { display: flex; align-items: center; gap: 0.5rem; margin-bottom: 0.2rem; }
  .agent-name { font-size: 0.9rem; font-weight: 600; color: var(--text-primary); }
  .agent-dest { font-size: 0.75rem; color: var(--text-muted); font-family: monospace; word-break: break-all; margin-bottom: 0.3rem; }
  .kind-badge { font-size: 0.65rem; font-weight: 700; padding: 0.12rem 0.45rem; border-radius: 4px; background: var(--accent-bg); color: var(--accent-text); text-transform: uppercase; letter-spacing: 0.04em; }
  .status { font-size: 0.75rem; margin-bottom: 0.3rem; }
  .status-ok { color: var(--success, #2ecc71); }
  .status-error { color: var(--error); }
  .status-idle, .status-disabled { color: var(--text-muted); }
  .status-detail { font-size: 0.72rem; color: var(--error); font-family: monospace; word-break: break-word; margin-bottom: 0.3rem; }
  .agent-events { display: flex; flex-wrap: wrap; gap: 0.3rem; }
  .event-badge { font-size: 0.65rem; padding: 0.12rem 0.45rem; border-radius: 4px; background: var(--bg-hover); color: var(--text-secondary); }
  .agent-actions { display: flex; align-items: center; gap: 0.35rem; flex-wrap: wrap; }

  .test-result { margin-top: 0.6rem; font-size: 0.75rem; padding: 0.45rem 0.7rem; border-radius: 6px; }
  .test-ok { background: var(--success-bg, rgba(46, 204, 113, 0.1)); color: var(--success, #2ecc71); }
  .test-fail { background: var(--error-bg); color: var(--error); }

  .empty { text-align: center; padding: 2rem 1rem; color: var(--text-secondary); font-size: 0.85rem; }
  .empty-sub { color: var(--text-muted); font-size: 0.78rem; }

  .modal-overlay { position: fixed; inset: 0; background: rgba(0, 0, 0, 0.55); display: flex; align-items: center; justify-content: center; z-index: 50; }
  .modal { background: var(--bg-elevated); border: 1px solid var(--border); border-radius: 10px; padding: 1.25rem; max-width: 360px; width: calc(100% - 2rem); }
  .modal-text { font-size: 0.9rem; color: var(--text-primary); margin: 0 0 0.3rem; }
  .modal-sub { font-size: 0.75rem; color: var(--text-muted); margin: 0 0 1rem; }
  .modal-actions { display: flex; justify-content: flex-end; gap: 0.5rem; }
</style>
