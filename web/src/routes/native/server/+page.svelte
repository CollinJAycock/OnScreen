<script lang="ts">
  // Desktop client: which OnScreen server this device talks to. Reached from
  // the sidebar's "Server" link and the sign-in page's "Change server" link
  // (signed out, the root layout renders it without the app navigation).
  import { onMount } from 'svelte';
  import {
    isTauri, getServerUrl, setServerUrl, clearServerUrl,
    getStoredTokens, clearStoredTokens, reloadApp,
  } from '$lib/native';
  import { nativeEngine } from '$lib/stores/nativeEngine';
  import { api, authApi } from '$lib/api';
  import { cleartextWarning, errorText, originOf, type ConnectResult } from '$lib/serverConnect';
  import ServerConnectForm from '$lib/components/ServerConnectForm.svelte';
  import { i18n } from '$lib/i18n/index.svelte';

  let loading = true;
  let currentUrl: string | null = null;
  let hasTokens = false;
  let signedIn = false;
  let pageError = '';
  let disconnecting = false;
  // Disconnect asks for a second click in the page itself: window.confirm
  // doesn't block in the desktop webview (the dialog plugin replaces it
  // with an async call), so `if (!confirm(…)) return` never stopped it.
  let confirmingDisconnect = false;

  $: warning = cleartextWarning(currentUrl);

  onMount(async () => {
    if (!isTauri()) { loading = false; return; }
    signedIn = !!api.getUser();
    try {
      currentUrl = await getServerUrl();
      const tokens = await getStoredTokens();
      hasTokens = !!(tokens.access_token && tokens.refresh_token);
    } catch (e: unknown) {
      pageError = errorText(e);
    }
    loading = false;
  });

  // ServerConnectForm has already validated and tested `result` — nothing
  // irreversible happened before this point, so a URL the shell rejects or a
  // server that doesn't answer can't sign the user out of the one they are
  // still using.
  async function switchServer(result: ConnectResult) {
    // Switching to a different server: revoke this session on the OLD one
    // (best-effort; an unreachable old server must not block the switch),
    // then drop the user metadata. api.ts still holds the old apiBase and
    // refresh token in memory (they only rebind on the reload below), so
    // the revoke reaches the server that issued it.
    if (hasTokens && originOf(result.url) !== originOf(currentUrl)) {
      try { await authApi.logout(); } catch { /* unreachable old server */ }
      api.setUser(null);
    }
    // Persist. On an origin change Rust wipes the stored tokens again,
    // after the revoke above, so nothing the old server issued (not even a
    // refresh token the revoke just rotated) is ever sent to the new host.
    await setServerUrl(result.url);
    // Start over at the root: the new server's sign-in (or setup) screen.
    reloadApp();
  }

  async function disconnect() {
    if (!confirmingDisconnect) { confirmingDisconnect = true; return; }
    disconnecting = true;
    pageError = '';
    try {
      // Revoke the refresh token on the server BEFORE forgetting it —
      // clearing only the local copy left it valid server-side for 30 days.
      try { await authApi.logout(); } catch { /* best-effort; still clear locally */ }
      api.setUser(null);
      await clearStoredTokens();
      await clearServerUrl();
      // Back to the first-run "connect to your server" screen.
      reloadApp();
    } catch (e: unknown) {
      pageError = errorText(e);
      disconnecting = false;
      confirmingDisconnect = false;
    }
  }
</script>

<svelte:head><title>{$i18n('native.server.title')} — OnScreen</title></svelte:head>

<div class="page" class:standalone={!signedIn}>
  <h1>{$i18n('native.server.title')}</h1>
  <p class="muted">
    {$i18n('native.server.intro')}
  </p>

  {#if loading}
    <div class="muted">{$i18n('player.loading')}</div>
  {:else if !isTauri()}
    <div class="error-bar">{$i18n('native.server.desktopOnly')}</div>
  {:else}
    {#if pageError}
      <div class="error-bar" role="alert">{pageError}</div>
    {/if}

    <section class="card">
      <h2>{$i18n('player.subStyle.server')}</h2>
      {#if currentUrl}
        <div class="current"><span class="muted-inline">{$i18n('native.server.connectedTo')}</span> <code>{currentUrl}</code></div>
        {#if warning}
          <div class="warn-bar" role="note">{warning}</div>
        {/if}
      {/if}
      <p class="muted">
        {#each $i18n.rich('native.server.switchHint', { example1: '192.168.1.50:7070', example2: 'https://onscreen.example.com' }) as seg}{#if seg.param}<code>{seg.text}</code>{:else}{seg.text}{/if}{/each}
      </p>
      <ServerConnectForm
        initial={currentUrl ?? ''}
        submitLabel={$i18n('native.server.saveReconnect')}
        onConnected={switchServer}
      />
      {#if !signedIn}
        <a href="/login" class="back-link">{$i18n('native.server.backToSignIn')}</a>
      {/if}
    </section>

    {#if signedIn}
      <section class="card">
        <h2>{$i18n('native.server.engine')}</h2>
        <p class="muted">
          {#each $i18n.rich('native.server.engineDesc', { element: '<audio>' }) as seg}{#if seg.param}<code>{seg.text}</code>{:else}{seg.text}{/if}{/each}
        </p>
        <p class="muted">
          {$i18n('native.server.engineLimits')}
        </p>
        <label class="toggle">
          <input type="checkbox" checked={$nativeEngine}
            on:change={(e) => nativeEngine.set((e.target as HTMLInputElement).checked)} />
          <span>{$i18n('native.server.engineToggle')}</span>
        </label>
      </section>
    {/if}

    <section class="card">
      <h2>{$i18n('native.server.disconnect')}</h2>
      <p class="muted">
        {$i18n('native.server.disconnectHint')}
      </p>
      <p class="muted">
        {$i18n('native.server.signedInHere')} <strong>{hasTokens ? $i18n('native.server.yes') : $i18n('native.server.no')}</strong>
      </p>
      {#if confirmingDisconnect}
        <p class="confirm-text">{currentUrl ? $i18n('native.server.confirmForget', { server: currentUrl }) : $i18n('native.server.confirmForgetThis')}</p>
        <div class="row">
          <button type="button" class="danger" disabled={disconnecting} on:click={disconnect}>
            {disconnecting ? $i18n('native.server.disconnecting') : $i18n('native.server.yesDisconnect')}
          </button>
          <button type="button" class="plain" disabled={disconnecting}
            on:click={() => { confirmingDisconnect = false; }}>
            {$i18n('player.cancel')}
          </button>
        </div>
      {:else}
        <button type="button" class="danger" on:click={disconnect}>
          {$i18n('native.server.signOutClear')}
        </button>
      {/if}
    </section>
  {/if}
</div>

<style>
  .page { padding: 2.5rem; max-width: 640px; }
  .page.standalone { margin: 0 auto; padding: 2.5rem 1rem; }
  h1 { font-size: 1.4rem; font-weight: 800; color: var(--text-primary); margin: 0 0 0.4rem; }
  h2 { font-size: 0.95rem; font-weight: 700; color: var(--text-primary); margin: 0 0 0.5rem; }
  .muted { font-size: 0.82rem; color: var(--text-muted); line-height: 1.55; margin: 0 0 1rem; }
  .muted code { background: var(--bg-hover); padding: 0.05rem 0.35rem; border-radius: 4px; font-size: 0.78rem; }
  .muted-inline { color: var(--text-muted); }

  .card {
    background: var(--bg-elevated); border: 1px solid var(--border);
    border-radius: 10px; padding: 1.25rem; margin-bottom: 1.25rem;
  }
  .current {
    margin-bottom: 0.75rem; padding: 0.4rem 0.65rem;
    background: var(--bg-hover); border-radius: 6px;
    font-size: 0.78rem; color: var(--text-secondary);
    overflow-wrap: anywhere;
  }
  .current code { font-family: monospace; }
  .warn-bar {
    margin-bottom: 0.9rem; padding: 0.5rem 0.75rem; border-radius: 7px;
    border: 1px solid rgba(217,119,6,0.35); background: rgba(251,191,36,0.10);
    color: var(--text-secondary); font-size: 0.78rem; line-height: 1.5;
  }
  .back-link {
    display: inline-block; margin-top: 0.9rem;
    font-size: 0.82rem; color: var(--accent-text); text-decoration: none;
  }
  .back-link:hover { text-decoration: underline; }
  .confirm-text { font-size: 0.85rem; color: var(--text-primary); margin: 0 0 0.75rem; overflow-wrap: anywhere; }
  .row { display: flex; gap: 0.6rem; flex-wrap: wrap; }
  .danger {
    padding: 0.5rem 0.95rem;
    background: rgba(248,113,113,0.15); border: 1px solid rgba(248,113,113,0.3);
    border-radius: 7px; color: #f87171; font-size: 0.82rem; font-weight: 600;
    cursor: pointer; transition: background 0.12s;
  }
  .danger:hover { background: rgba(248,113,113,0.25); }
  .danger:disabled { opacity: 0.5; cursor: not-allowed; }
  .plain {
    padding: 0.5rem 0.95rem; background: transparent;
    border: 1px solid var(--border-strong); border-radius: 7px;
    color: var(--text-primary); font-size: 0.82rem; font-weight: 600; cursor: pointer;
  }
  .plain:hover { background: var(--bg-hover); }
  .plain:disabled { opacity: 0.5; cursor: not-allowed; }

  .toggle {
    display: flex; align-items: center; gap: 0.6rem;
    cursor: pointer; font-size: 0.85rem; color: var(--text-secondary);
  }
  .toggle input[type="checkbox"] { width: 1rem; height: 1rem; cursor: pointer; }

  .error-bar {
    background: var(--error-bg); border: 1px solid var(--error-bg); color: var(--error);
    padding: 0.5rem 0.75rem; border-radius: 7px; font-size: 0.78rem;
    margin-bottom: 1rem; overflow-wrap: anywhere;
  }

  @media (max-width: 600px) {
    .page { padding: 1.5rem 1rem; }
  }
</style>
