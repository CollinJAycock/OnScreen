<script lang="ts">
  // Personal API keys: create (the key is shown once, with a copy button),
  // list and revoke. `admin` switches to the server-wide list an admin uses to
  // revoke anyone's key (no create there — keys are made by their owner).
  //
  // The create dialog is a native <dialog> opened with showModal(), like
  // ReportProblemButton: top layer, focus trap and Escape for free.
  import { onMount } from 'svelte';
  import { apiKeyApi, type ApiKey, type CreatedApiKey } from '$lib/api';
  import { confirmAction } from '$lib/native';
  import { toast } from '$lib/stores/toast';
  import { relativeTime } from '$lib/signedInDevices';
  import {
    EXPIRY_OPTIONS,
    expiryFromDays,
    expiryLabel,
    scopeLabel,
    sortKeys,
    validKeyName,
  } from '$lib/apiKeys';

  interface Props {
    admin?: boolean;
  }

  let { admin = false }: Props = $props();

  const uid = Math.random().toString(36).slice(2, 8);

  let keys = $state<ApiKey[]>([]);
  let loading = $state(true);
  let error = $state('');
  let busy = $state<Set<string>>(new Set());

  let dialogEl = $state<HTMLDialogElement>();
  let name = $state('');
  let scope = $state<ApiKey['scope']>('full');
  let expiryDays = $state(0);
  let creating = $state(false);
  let createError = $state('');
  let created = $state<CreatedApiKey | null>(null);
  let copied = $state(false);

  async function refresh() {
    error = '';
    try {
      keys = sortKeys((admin ? await apiKeyApi.adminList() : await apiKeyApi.listMine()) ?? []);
    } catch (e) {
      error = e instanceof Error ? e.message : 'Could not load API keys.';
    } finally {
      loading = false;
    }
  }

  onMount(refresh);

  function openCreate() {
    name = '';
    scope = 'full';
    expiryDays = 0;
    createError = '';
    created = null;
    copied = false;
    dialogEl?.showModal();
  }

  function closeCreate() {
    dialogEl?.close();
  }

  async function create(e: SubmitEvent) {
    e.preventDefault();
    if (!validKeyName(name)) {
      createError = 'Give the key a name (up to 100 characters).';
      return;
    }
    creating = true;
    createError = '';
    try {
      created = await apiKeyApi.create(name.trim(), scope, expiryFromDays(expiryDays));
      await refresh();
    } catch (err) {
      createError = err instanceof Error ? err.message : 'Could not create the key.';
    } finally {
      creating = false;
    }
  }

  async function copyKey() {
    if (!created) return;
    try {
      await navigator.clipboard.writeText(created.key);
      copied = true;
    } catch {
      toast.error('Copy failed — select the key and copy it by hand');
    }
  }

  async function revoke(k: ApiKey) {
    const owner = admin && k.username ? ` (${k.username})` : '';
    if (!(await confirmAction(`Revoke the API key "${k.name}"${owner}? Anything using it stops working at once.`))) return;
    busy = new Set(busy).add(k.id);
    try {
      if (admin) await apiKeyApi.adminRevoke(k.id);
      else await apiKeyApi.revoke(k.id);
      keys = keys.filter((x) => x.id !== k.id);
      toast.success(`Revoked "${k.name}"`);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : 'Revoke failed');
    } finally {
      const next = new Set(busy);
      next.delete(k.id);
      busy = next;
    }
  }

  function onBackdropClick(e: MouseEvent) {
    if (e.target === dialogEl && !created) closeCreate();
  }
</script>

{#if loading}
  <div class="skeleton"></div>
{:else if error}
  <div class="banner">{error}</div>
{:else}
  {#if keys.length === 0}
    <p class="muted">{admin ? 'No one has an API key.' : 'You have no API keys.'}</p>
  {:else}
    <ul class="keys">
      {#each keys as k (k.id)}
        <li class="key" class:expired={k.expired}>
          <div class="info">
            <div class="name">
              {k.name}
              <span class="scope" class:ro={k.scope === 'read_only'}>{scopeLabel(k.scope)}</span>
            </div>
            <div class="meta">
              {#if admin && k.username}<span class="owner">{k.username}</span>{/if}
              <code>{k.prefix}…</code>
              <span title={new Date(k.created_at).toLocaleString()}>Created {relativeTime(k.created_at)}</span>
              <span>{k.last_used_at ? `Last used ${relativeTime(k.last_used_at)}` : 'Never used'}</span>
              <span>{expiryLabel(k)}</span>
            </div>
          </div>
          <button class="btn-revoke" disabled={busy.has(k.id)} onclick={() => revoke(k)}>
            {busy.has(k.id) ? 'Revoking…' : 'Revoke'}
          </button>
        </li>
      {/each}
    </ul>
  {/if}
  {#if !admin}
    <button class="btn-primary" onclick={openCreate}>Create API key</button>
  {/if}
  <p class="hint">
    Send a key as <code>Authorization: Bearer osk_…</code> or <code>X-Api-Key: osk_…</code>.
    It acts as {admin ? 'its owner' : 'you'}, with the same libraries and restrictions, but can't
    sign in, change passwords or 2FA, or manage keys and devices.
  </p>
{/if}

<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_noninteractive_element_interactions -->
<dialog
  bind:this={dialogEl}
  class="ak-dialog"
  aria-labelledby="ak-title-{uid}"
  onclick={onBackdropClick}
  onclose={() => { created = null; }}
>
  <div class="ak-body">
    {#if created}
      <h2 id="ak-title-{uid}">Copy your new key</h2>
      <p class="warn">This is the only time the key is shown. Store it somewhere safe; if you lose it, revoke it and create another.</p>
      <div class="secret">
        <code class="secret-value" aria-label="New API key">{created.key}</code>
        <button type="button" class="btn-copy" onclick={copyKey}>{copied ? 'Copied' : 'Copy'}</button>
      </div>
      <div class="actions">
        <button type="button" class="btn-primary" onclick={closeCreate}>Done</button>
      </div>
    {:else}
      <h2 id="ak-title-{uid}">Create API key</h2>
      <form onsubmit={create}>
        <label>
          <span>Name</span>
          <input type="text" bind:value={name} maxlength="100" placeholder="Home Assistant" required />
        </label>
        <label>
          <span>Access</span>
          <select bind:value={scope}>
            <option value="full">Full access</option>
            <option value="read_only">Read-only (GET requests only)</option>
          </select>
        </label>
        <label>
          <span>Expires</span>
          <select bind:value={expiryDays}>
            {#each EXPIRY_OPTIONS as o (o.days)}
              <option value={o.days}>{o.label}</option>
            {/each}
          </select>
        </label>
        {#if createError}<div class="banner">{createError}</div>{/if}
        <div class="actions">
          <button type="button" class="btn-ghost" onclick={closeCreate}>Cancel</button>
          <button type="submit" class="btn-primary" disabled={creating}>{creating ? 'Creating…' : 'Create key'}</button>
        </div>
      </form>
    {/if}
  </div>
</dialog>

<style>
  .keys { list-style: none; margin: 0 0 0.9rem; padding: 0; display: flex; flex-direction: column; gap: 0.45rem; }
  .key {
    display: flex; align-items: center; justify-content: space-between; gap: 0.75rem;
    padding: 0.6rem 0.75rem; border: 1px solid var(--border); border-radius: 8px; background: var(--bg-hover);
  }
  .key.expired { opacity: 0.65; }
  .info { min-width: 0; }
  .name { font-size: 0.86rem; font-weight: 600; color: var(--text-primary); display: flex; align-items: center; gap: 0.45rem; flex-wrap: wrap; word-break: break-word; }
  .scope { font-size: 0.66rem; font-weight: 700; padding: 0.12rem 0.45rem; border-radius: 999px; background: var(--bg-elevated); color: var(--text-secondary); text-transform: uppercase; letter-spacing: 0.04em; }
  .scope.ro { background: var(--success-bg); color: var(--success); }
  .meta { font-size: 0.74rem; color: var(--text-muted); display: flex; flex-wrap: wrap; gap: 0.2rem 0.8rem; margin-top: 0.15rem; }
  .owner { color: var(--text-secondary); font-weight: 600; }
  code { font-size: 0.74rem; }
  .muted { color: var(--text-muted); font-size: 0.85rem; }
  .hint { color: var(--text-muted); font-size: 0.74rem; margin: 0.6rem 0 0; line-height: 1.45; }
  .banner { border-radius: 7px; padding: 0.6rem 0.85rem; font-size: 0.82rem; background: var(--error-bg); color: var(--error); }
  .warn { font-size: 0.82rem; color: var(--text-secondary); margin: 0; line-height: 1.45; }
  .skeleton {
    height: 90px; border-radius: 10px;
    background: linear-gradient(90deg, var(--bg-elevated) 25%, var(--bg-hover) 50%, var(--bg-elevated) 75%);
    background-size: 200% 100%; animation: shimmer 1.4s infinite;
  }
  @keyframes shimmer { 0% { background-position: 200% 0; } 100% { background-position: -200% 0; } }
  .btn-revoke {
    flex-shrink: 0; padding: 0.35rem 0.75rem; background: transparent; color: var(--error);
    border: 1px solid var(--border-strong); border-radius: 7px; font-size: 0.76rem; font-weight: 500; cursor: pointer;
  }
  .btn-revoke:hover { border-color: var(--error); }
  .btn-revoke:disabled, .btn-primary:disabled { opacity: 0.5; cursor: not-allowed; }
  .btn-primary {
    padding: 0.45rem 0.9rem; background: var(--accent); color: var(--accent-contrast, #fff); border: none;
    border-radius: 7px; font-size: 0.8rem; font-weight: 600; cursor: pointer;
  }
  .btn-ghost {
    padding: 0.45rem 0.9rem; background: transparent; color: var(--text-secondary);
    border: 1px solid var(--border-strong); border-radius: 7px; font-size: 0.8rem; cursor: pointer;
  }
  .btn-copy {
    flex-shrink: 0; padding: 0.35rem 0.75rem; background: var(--bg-hover); color: var(--text-primary);
    border: 1px solid var(--border-strong); border-radius: 7px; font-size: 0.76rem; cursor: pointer;
  }
  .ak-dialog {
    width: min(460px, calc(100vw - 2rem));
    max-height: calc(100vh - 2rem);
    padding: 0;
    border: 1px solid var(--border, rgba(255, 255, 255, 0.12));
    border-radius: 10px;
    background: var(--bg-elevated, var(--bg-secondary, #16161f));
    color: var(--text-primary, #eee);
    box-shadow: 0 12px 40px rgba(0, 0, 0, 0.45);
  }
  .ak-dialog::backdrop { background: rgba(0, 0, 0, 0.55); }
  .ak-body { padding: 1.1rem 1.2rem 1rem; display: flex; flex-direction: column; gap: 0.7rem; text-align: left; }
  .ak-body h2 { font-size: 1rem; font-weight: 600; margin: 0; }
  form { display: flex; flex-direction: column; gap: 0.65rem; }
  label { display: flex; flex-direction: column; gap: 0.25rem; font-size: 0.78rem; color: var(--text-secondary); }
  input, select {
    background: var(--input-bg); color: var(--text-primary); border: 1px solid var(--border);
    border-radius: 7px; padding: 0.45rem 0.6rem; font-size: 0.85rem;
  }
  .secret { display: flex; align-items: center; gap: 0.5rem; }
  .secret-value {
    flex: 1; min-width: 0; padding: 0.5rem 0.6rem; border-radius: 7px; background: var(--bg-hover);
    border: 1px solid var(--border); font-size: 0.78rem; word-break: break-all; user-select: all;
  }
  .actions { display: flex; justify-content: flex-end; gap: 0.5rem; margin-top: 0.2rem; }
</style>
