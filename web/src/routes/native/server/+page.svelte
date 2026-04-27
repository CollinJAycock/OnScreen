<script lang="ts">
  import { onMount } from 'svelte';
  import {
    isTauri, getServerUrl, setServerUrl, clearServerUrl,
    getStoredTokens, clearStoredTokens,
  } from '$lib/native';

  let loading = true;
  let currentUrl: string | null = null;
  let hasTokens = false;
  let urlInput = '';
  let saveError = '';
  let saving = false;
  let disconnecting = false;

  onMount(async () => {
    if (!isTauri()) { loading = false; return; }
    currentUrl = await getServerUrl();
    urlInput = currentUrl ?? '';
    const tokens = await getStoredTokens();
    hasTokens = !!(tokens.access_token && tokens.refresh_token);
    loading = false;
  });

  async function save() {
    saveError = '';
    if (!urlInput.trim()) { saveError = 'URL required'; return; }
    saving = true;
    try {
      await setServerUrl(urlInput.trim());
      // Hard reload so the api.ts apiBase rebinds + every cached
      // module pointing at the old server flushes. Cheaper than
      // re-wiring each consumer manually.
      window.location.reload();
    } catch (e: unknown) {
      saveError = e instanceof Error ? e.message : String(e);
      saving = false;
    }
  }

  async function disconnect() {
    if (!confirm('Sign out and clear the stored server URL? You will be returned to the first-run setup screen.')) return;
    disconnecting = true;
    try {
      await clearStoredTokens();
      await clearServerUrl();
      window.location.reload();
    } catch (e: unknown) {
      saveError = e instanceof Error ? e.message : String(e);
      disconnecting = false;
    }
  }
</script>

<svelte:head><title>Server connection — OnScreen</title></svelte:head>

<div class="page">
  <h1>Native client connection</h1>
  <p class="muted">
    Per-device settings for the OnScreen desktop client. Stored locally
    via the Tauri shell — not synced to the server.
  </p>

  {#if loading}
    <div class="muted">Loading…</div>
  {:else if !isTauri()}
    <div class="error-bar">This page is only meaningful inside the OnScreen desktop client.</div>
  {:else}
    <section class="card">
      <h2>Server URL</h2>
      <p class="muted">
        Where this client should send API calls. In dev mode point at
        the Vite dev server (<code>http://localhost:5173</code>) so its
        proxy handles CORS for you. Production / installer builds need
        the actual server URL plus the Tauri webview origin
        (<code>http://tauri.localhost</code> on Windows) on the
        server's CORS allow-list.
      </p>
      {#if currentUrl}
        <div class="current"><span class="muted">Current:</span> <code>{currentUrl}</code></div>
      {/if}
      <form on:submit|preventDefault={save}>
        <input
          type="url"
          bind:value={urlInput}
          placeholder="http://localhost:5173"
          autocomplete="off"
          required
        />
        {#if saveError}
          <div class="error-bar inline">{saveError}</div>
        {/if}
        <button type="submit" class="primary" disabled={saving}>
          {saving ? 'Saving…' : 'Save & reload'}
        </button>
      </form>
    </section>

    <section class="card">
      <h2>Disconnect</h2>
      <p class="muted">
        Clears the stored access + refresh tokens and the server URL,
        then returns you to the first-run setup screen. Use this if
        you typed the wrong URL, want to switch servers, or to sign
        out fully on this device.
      </p>
      <p class="muted">
        Tokens currently stored: <strong>{hasTokens ? 'yes' : 'no'}</strong>
      </p>
      <button type="button" class="danger" disabled={disconnecting} on:click={disconnect}>
        {disconnecting ? 'Disconnecting…' : 'Sign out + clear server URL'}
      </button>
    </section>
  {/if}
</div>

<style>
  .page { padding: 2.5rem; max-width: 640px; }
  h1 { font-size: 1.4rem; font-weight: 800; color: var(--text-primary); margin: 0 0 0.4rem; }
  h2 { font-size: 0.95rem; font-weight: 700; color: var(--text-primary); margin: 0 0 0.5rem; }
  .muted { font-size: 0.82rem; color: var(--text-muted); line-height: 1.55; margin: 0 0 1rem; }
  .muted code { background: var(--bg-hover); padding: 0.05rem 0.35rem; border-radius: 4px; font-size: 0.78rem; }

  .card {
    background: var(--bg-elevated); border: 1px solid var(--border);
    border-radius: 10px; padding: 1.25rem; margin-bottom: 1.25rem;
  }
  .current {
    margin-bottom: 0.75rem; padding: 0.4rem 0.65rem;
    background: var(--bg-hover); border-radius: 6px;
    font-size: 0.78rem; color: var(--text-secondary);
  }
  .current code { font-family: monospace; }
  .card form { display: flex; flex-direction: column; gap: 0.6rem; }
  .card input {
    background: var(--bg-hover); border: 1px solid var(--border-strong);
    border-radius: 7px; padding: 0.5rem 0.75rem; color: var(--text-primary);
    font-size: 0.85rem;
  }
  .card input:focus { outline: none; border-color: var(--accent); box-shadow: 0 0 0 3px var(--accent-bg); }
  .primary {
    align-self: flex-start; padding: 0.5rem 0.95rem;
    background: var(--accent); border: none; border-radius: 7px;
    color: #fff; font-size: 0.82rem; font-weight: 600; cursor: pointer; transition: background 0.15s;
  }
  .primary:hover { background: var(--accent-hover); }
  .primary:disabled { opacity: 0.5; cursor: not-allowed; }
  .danger {
    padding: 0.5rem 0.95rem;
    background: rgba(248,113,113,0.15); border: 1px solid rgba(248,113,113,0.3);
    border-radius: 7px; color: #f87171; font-size: 0.82rem; font-weight: 600;
    cursor: pointer; transition: background 0.12s;
  }
  .danger:hover { background: rgba(248,113,113,0.25); }
  .danger:disabled { opacity: 0.5; cursor: not-allowed; }

  .error-bar {
    background: var(--error-bg); border: 1px solid var(--error-bg); color: var(--error);
    padding: 0.5rem 0.75rem; border-radius: 7px; font-size: 0.78rem;
  }
  .error-bar.inline { margin: 0; }
</style>
