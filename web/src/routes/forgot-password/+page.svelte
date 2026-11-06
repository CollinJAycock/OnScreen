<script lang="ts">
  import { authApi } from '$lib/api';
  import { t, tr } from '$lib/i18n/index.svelte';

  let email = '';
  let sending = false;
  let sent = false;
  let error = '';

  async function handleSubmit() {
    error = '';
    sending = true;
    try {
      await authApi.forgotPassword(email);
      sent = true;
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : t('forgotPassword.genericError');
    } finally {
      sending = false;
    }
  }
</script>

<svelte:head>
  <title>{$tr('forgotPassword.pageTitle')}</title>
</svelte:head>

<div class="container">
  <div class="card">
    <div class="logo">
      <img src="/favicon-96x96.png" alt="OnScreen" width="40" height="40" class="logo-icon" />
      <h1>OnScreen</h1>
    </div>

    {#if sent}
      <div class="success-section">
        <div class="success-icon">
          <svg width="48" height="48" viewBox="0 0 48 48" fill="none">
            <circle cx="24" cy="24" r="24" fill="rgba(52,211,153,0.12)"/>
            <path d="M15 24l6 6 12-12" stroke="#34d399" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"/>
          </svg>
        </div>
        <h2>{$tr('forgotPassword.checkEmail')}</h2>
        <p class="desc">{$tr('forgotPassword.sentDesc')}</p>
        <a href="/login" class="btn-primary">{$tr('forgotPassword.backToSignIn')}</a>
      </div>
    {:else}
      <h2>{$tr('forgotPassword.title')}</h2>
      <p class="desc">{$tr('forgotPassword.desc')}</p>

      <form on:submit|preventDefault={handleSubmit}>
        <div class="field">
          <label for="fp-email">{$tr('forgotPassword.email')}</label>
          <input id="fp-email" bind:value={email} type="email" required autofocus placeholder={$tr('forgotPassword.emailPlaceholder')} />
        </div>
        {#if error}
          <div class="error-banner">{error}</div>
        {/if}
        <button type="submit" disabled={sending || !email} class="btn-primary">
          {sending ? $tr('forgotPassword.sending') : $tr('forgotPassword.send')}
        </button>
      </form>

      <p class="back-link">
        <a href="/login">{$tr('forgotPassword.backToSignIn')}</a>
      </p>
    {/if}
  </div>
</div>

<style>
  .container {
    min-height: 100vh;
    display: flex;
    align-items: center;
    justify-content: center;
    background: var(--bg-primary);
    font-family: system-ui, -apple-system, sans-serif;
  }

  .card {
    background: #0e0e18;
    border: 1px solid var(--border);
    border-radius: 16px;
    padding: 2.5rem;
    width: 100%;
    max-width: 380px;
    box-shadow: 0 24px 80px var(--shadow);
  }

  .logo {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 0.75rem;
    margin-bottom: 1.75rem;
  }

  .logo-icon { border-radius: 10px; }

  h1 {
    font-size: 1.5rem;
    font-weight: 700;
    color: var(--text-primary);
    margin: 0;
    letter-spacing: -0.02em;
  }

  h2 {
    font-size: 1.05rem;
    font-weight: 600;
    color: var(--text-primary);
    margin: 0 0 0.5rem;
  }

  .desc {
    color: var(--text-muted);
    font-size: 0.82rem;
    margin: 0 0 1.5rem;
    line-height: 1.5;
  }

  .field { margin-bottom: 1.1rem; }

  label {
    display: block;
    font-size: 0.8rem;
    font-weight: 500;
    color: #8888a0;
    margin-bottom: 0.4rem;
    letter-spacing: 0.02em;
  }

  input {
    width: 100%;
    padding: 0.7rem 0.85rem;
    background: #111120;
    border: 1px solid var(--border);
    border-radius: 8px;
    font-size: 0.95rem;
    color: var(--text-primary);
    outline: none;
    transition: border-color 0.15s, box-shadow 0.15s;
    box-sizing: border-box;
  }

  input::placeholder { color: var(--text-muted); }

  input:focus {
    border-color: rgba(124,106,247,0.5);
    box-shadow: 0 0 0 3px var(--accent-bg);
  }

  .error-banner {
    background: var(--error-bg);
    border: 1px solid var(--error-bg);
    border-radius: 8px;
    padding: 0.55rem 0.8rem;
    color: var(--error);
    font-size: 0.82rem;
    margin-bottom: 1rem;
  }

  .btn-primary {
    display: block;
    width: 100%;
    padding: 0.75rem;
    background: var(--accent);
    color: #fff;
    border: none;
    border-radius: 8px;
    font-size: 0.95rem;
    font-weight: 600;
    cursor: pointer;
    transition: background 0.15s, transform 0.1s;
    text-align: center;
    text-decoration: none;
  }

  .btn-primary:hover:not(:disabled) { background: #6b5ce6; }
  .btn-primary:active:not(:disabled) { transform: scale(0.98); }
  .btn-primary:disabled { opacity: 0.5; cursor: not-allowed; }

  .back-link {
    text-align: center;
    margin-top: 1.5rem;
    font-size: 0.8rem;
  }

  .back-link a {
    color: var(--text-muted);
    text-decoration: none;
    transition: color 0.15s;
  }

  .back-link a:hover { color: #8888a0; }

  .success-section { text-align: center; padding: 1rem 0; }
  .success-icon { margin-bottom: 1rem; }
  .success-section h2 { margin-bottom: 0.5rem; }
  .success-section .desc { margin: 0 0 1.5rem; }

  @media (max-width: 768px) {
    .card {
      max-width: 100%;
      padding: 2rem 1.5rem;
      margin: 0 1rem;
      border-radius: 14px;
    }
  }
</style>
