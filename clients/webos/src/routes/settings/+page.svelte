<script lang="ts">
  // Account / app settings. Two destructive actions:
  //   - Sign out: clears tokens, keeps the server URL so the user
  //     just re-enters their password.
  //   - Forget server: clears everything (origin + tokens + user)
  //     and routes back through the URL prompt — used when switching
  //     to a different OnScreen deployment.
  // Both confirm before firing because the remote's OK button is
  // easy to mash from across the room.

  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { api } from '$lib/api';
  import { focusable } from '$lib/focus/focusable';
  import { focusManager } from '$lib/focus/manager';
  import { APP_VERSION } from '$lib/version';

  const username = $derived(api.getUser()?.username ?? '');
  const serverUrl = $derived(api.getOrigin() ?? '');

  let confirming = $state<'signOut' | 'forgetServer' | null>(null);

  onMount(() => {
    return focusManager.pushBack(() => {
      if (confirming) {
        confirming = null;
        return true;
      }
      goto('/hub');
      return true;
    });
  });

  async function doSignOut() {
    // Best-effort — the network call invalidates the refresh token
    // server-side, but `clearTokens()` runs in `finally` so the local
    // session ends cleanly even if the request fails.
    await api.logout();
    goto('/login');
  }

  async function doForgetServer() {
    // logout() already clears tokens; we follow up by removing the
    // origin so the next launch starts at /setup again. There's no
    // dedicated `clearOrigin()`; setting to "" then redirecting works
    // because /setup overwrites it with the user's new entry.
    try {
      await api.logout();
    } finally {
      localStorage.removeItem('onscreen.api_origin');
    }
    goto('/setup');
  }
</script>

<div class="page">
  <header>
    <h1>Settings</h1>
    <nav class="links">
      <a href="/hub/" data-sveltekit-preload-data="false">home</a>
    </nav>
  </header>

  <section>
    <div class="section-title">Account</div>
    {#if username || serverUrl}
      <div class="identity">
        {#if username}
          <div class="identity-line">Signed in as <strong>{username}</strong></div>
        {/if}
        {#if serverUrl}
          <div class="identity-server">{serverUrl}</div>
        {/if}
      </div>
    {/if}

    <button
      use:focusable={{ autofocus: true }}
      class="action-row"
      onclick={() => (confirming = 'signOut')}
    >
      <div class="action-title">Sign out</div>
      <div class="action-desc">
        Clear your session on this TV. The server URL is kept so you can sign in again with the same account.
      </div>
    </button>

    <button
      use:focusable
      class="action-row"
      onclick={() => (confirming = 'forgetServer')}
    >
      <div class="action-title">Forget server</div>
      <div class="action-desc">
        Remove the server URL and all session state. Use when switching to a different OnScreen deployment.
      </div>
    </button>
  </section>

  <section>
    <div class="section-title">About</div>
    <div class="info-row">
      <div class="info-label">Version</div>
      <div class="info-value">{APP_VERSION}</div>
    </div>
    {#if username}
      <div class="info-row">
        <div class="info-label">Signed in as</div>
        <div class="info-value">{username}</div>
      </div>
    {/if}
    {#if serverUrl}
      <div class="info-row">
        <div class="info-label">Server</div>
        <div class="info-value mono">{serverUrl}</div>
      </div>
    {/if}
  </section>
</div>

{#if confirming === 'signOut'}
  <div class="modal-backdrop" role="dialog" aria-modal="true" aria-labelledby="confirm-title">
    <div class="modal">
      <div class="modal-title" id="confirm-title">Sign out?</div>
      <div class="modal-body">
        You'll need to sign in again to continue using OnScreen on this TV.
      </div>
      <div class="modal-actions">
        <button use:focusable={{ autofocus: true }} class="btn-confirm" onclick={doSignOut}>
          Sign out
        </button>
        <button use:focusable class="btn-cancel" onclick={() => (confirming = null)}>
          Cancel
        </button>
      </div>
    </div>
  </div>
{:else if confirming === 'forgetServer'}
  <div class="modal-backdrop" role="dialog" aria-modal="true" aria-labelledby="confirm-title">
    <div class="modal">
      <div class="modal-title" id="confirm-title">Forget server?</div>
      <div class="modal-body">
        This removes the server URL and all session state. You'll start over from the server-URL prompt.
      </div>
      <div class="modal-actions">
        <button use:focusable={{ autofocus: true }} class="btn-confirm" onclick={doForgetServer}>
          Forget
        </button>
        <button use:focusable class="btn-cancel" onclick={() => (confirming = null)}>
          Cancel
        </button>
      </div>
    </div>
  </div>
{/if}

<style>
  .page {
    padding: 32px var(--page-pad) 0;
  }
  header {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    margin-bottom: 48px;
  }
  h1 {
    font-size: var(--font-xl);
    margin: 0;
    color: var(--accent);
  }
  .links {
    display: flex;
    gap: 32px;
    font-size: var(--font-md);
    color: var(--text-secondary);
  }
  .links a {
    color: inherit;
    text-decoration: none;
  }

  section {
    max-width: 1200px;
    margin: 0 0 48px;
  }
  .section-title {
    font-size: var(--font-sm);
    color: var(--accent);
    text-transform: uppercase;
    letter-spacing: 0.15em;
    margin-bottom: 16px;
  }

  .identity {
    margin-bottom: 16px;
  }
  .identity-line {
    font-size: var(--font-md);
    color: var(--text-primary);
  }
  .identity-server {
    margin-top: 4px;
    font-family: monospace;
    font-size: var(--font-sm);
    color: var(--text-secondary);
  }

  .action-row {
    display: block;
    width: 100%;
    text-align: left;
    background: transparent;
    border: 2px solid transparent;
    color: inherit;
    padding: 16px 20px;
    margin-bottom: 8px;
    border-radius: 8px;
    cursor: pointer;
    font-family: inherit;
  }
  .action-row:focus,
  .action-row:focus-visible {
    border-color: var(--accent);
    outline: none;
  }
  .action-title {
    font-size: var(--font-md);
    margin-bottom: 4px;
  }
  .action-desc {
    font-size: var(--font-sm);
    color: var(--text-secondary);
  }

  .info-row {
    display: flex;
    gap: 32px;
    padding: 8px 0;
    font-size: var(--font-md);
  }
  .info-label {
    color: var(--text-secondary);
    min-width: 200px;
  }
  .info-value.mono {
    font-family: monospace;
  }

  .modal-backdrop {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.7);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 100;
  }
  .modal {
    background: var(--bg-secondary, #1f1f24);
    border-radius: 12px;
    padding: 32px;
    max-width: 600px;
    box-shadow: 0 8px 32px rgba(0, 0, 0, 0.5);
  }
  .modal-title {
    font-size: var(--font-lg);
    margin-bottom: 16px;
  }
  .modal-body {
    font-size: var(--font-md);
    color: var(--text-secondary);
    margin-bottom: 24px;
    line-height: 1.4;
  }
  .modal-actions {
    display: flex;
    gap: 12px;
    justify-content: flex-end;
  }
  .modal-actions button {
    padding: 12px 28px;
    font-size: var(--font-sm);
    border-radius: 6px;
    cursor: pointer;
    background: var(--bg-primary, #0a0a0e);
    border: 2px solid transparent;
    color: var(--text-primary);
    font-family: inherit;
  }
  .modal-actions button:focus,
  .modal-actions button:focus-visible {
    border-color: var(--accent);
    outline: none;
  }
  .btn-confirm:focus,
  .btn-confirm:focus-visible {
    background: var(--accent);
    color: white;
  }
</style>
