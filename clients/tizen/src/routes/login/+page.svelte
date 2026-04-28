<script lang="ts">
  import { goto } from '$app/navigation';
  import { api, ApiError } from '$lib/api';
  import OnScreenKeyboard from '$lib/components/OnScreenKeyboard.svelte';
  import { focusable } from '$lib/focus/focusable';

  let step = $state<'username' | 'password'>('username');
  let username = $state('');
  let password = $state('');
  let error = $state('');
  let submitting = $state(false);

  async function submit() {
    if (step === 'username') {
      if (!username.trim()) return;
      step = 'password';
      return;
    }
    error = '';
    submitting = true;
    try {
      await api.login(username.trim(), password);
      goto('/hub');
    } catch (e) {
      error = e instanceof ApiError ? e.message : 'Login failed';
      password = '';
    } finally {
      submitting = false;
    }
  }
</script>

<div class="page">
  <h1>Sign in</h1>

  {#if step === 'username'}
    <div class="label">Username</div>
    <OnScreenKeyboard bind:value={username} onchange={(v) => (username = v)} onsubmit={submit} />
  {:else}
    <div class="label">Password for <strong>{username}</strong></div>
    <OnScreenKeyboard bind:value={password} onchange={(v) => (password = v)} onsubmit={submit} />
    <button use:focusable class="back-btn" onclick={() => (step = 'username')}>
      change user
    </button>
  {/if}

  {#if submitting}
    <p class="status">Signing in…</p>
  {:else if error}
    <p class="error">{error}</p>
  {/if}
</div>

<style>
  .page {
    padding: var(--page-pad);
    display: flex;
    flex-direction: column;
    gap: 24px;
  }

  h1 {
    font-size: var(--font-2xl);
    margin: 0;
  }

  .label {
    font-size: var(--font-md);
    color: var(--text-secondary);
  }

  strong {
    color: var(--text-primary);
  }

  .back-btn {
    align-self: flex-start;
    margin-top: 12px;
    padding: 12px 24px;
    font-size: var(--font-sm);
    font-family: inherit;
    background: var(--bg-elevated);
    color: var(--text-primary);
    border: 2px solid var(--border);
    border-radius: 8px;
    cursor: pointer;
  }

  .status, .error { font-size: var(--font-md); margin: 0; }
  .error { color: #fca5a5; }
</style>
