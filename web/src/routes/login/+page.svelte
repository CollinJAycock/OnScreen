<script lang="ts">
  import { goto } from '$app/navigation';
  import { onMount } from 'svelte';
  import { authApi, api } from '$lib/api';

  let username = '';
  let password = '';
  let error = '';
  let loading = false;
  let googleEnabled = false;
  let githubEnabled = false;
  let discordEnabled = false;
  let setupRequired = false;
  let forgotEnabled = false;

  $: anySSOEnabled = googleEnabled || githubEnabled || discordEnabled;

  onMount(async () => {
    // Redirect to setup if no users exist yet.
    try {
      const status = await authApi.setupStatus();
      if (status.setup_required) {
        setupRequired = true;
        goto('/setup');
        return;
      }
    } catch { /* proceed to login */ }

    const params = new URLSearchParams(window.location.search);
    const oauthError = params.get('error');
    if (oauthError === 'google_denied') error = 'Google sign-in was cancelled.';
    else if (oauthError === 'github_denied') error = 'GitHub sign-in was cancelled.';
    else if (oauthError === 'discord_denied') error = 'Discord sign-in was cancelled.';
    else if (oauthError === 'email_unverified') error = 'Your email is not verified.';
    else if (oauthError?.endsWith('_failed')) error = 'Sign-in failed. Please try again.';

    const [google, github, discord, forgot] = await Promise.allSettled([
      authApi.googleEnabled(),
      authApi.githubEnabled(),
      authApi.discordEnabled(),
      authApi.forgotPasswordEnabled()
    ]);
    if (google.status === 'fulfilled') googleEnabled = google.value.enabled;
    if (github.status === 'fulfilled') githubEnabled = github.value.enabled;
    if (discord.status === 'fulfilled') discordEnabled = discord.value.enabled;
    if (forgot.status === 'fulfilled') forgotEnabled = forgot.value.enabled;
  });

  async function handleLogin() {
    error = '';
    loading = true;
    try {
      const pair = await authApi.login(username, password);
      api.setUser({ user_id: pair.user_id, username: pair.username, is_admin: pair.is_admin });
      goto('/');
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Login failed.';
      password = '';
    } finally {
      loading = false;
    }
  }
</script>

<svelte:head>
  <title>OnScreen — Sign In</title>
</svelte:head>

<div class="login-container">
  <div class="login-card">
    <div class="logo">
      <img src="/favicon-96x96.png" alt="OnScreen" width="40" height="40" class="logo-icon" />
      <h1>OnScreen</h1>
    </div>
    <p class="subtitle">Sign in to your media server</p>

    <form on:submit|preventDefault={handleLogin}>
      <div class="field">
        <label for="username">Username</label>
        <input id="username" bind:value={username} type="text" required autocomplete="username" autofocus placeholder="Enter username" />
      </div>
      <div class="field">
        <div class="label-row">
          <label for="password">Password</label>
          {#if forgotEnabled}
            <a href="/forgot-password" class="forgot-link">Forgot password?</a>
          {/if}
        </div>
        <input id="password" bind:value={password} type="password" required autocomplete="current-password" placeholder="Enter password" />
      </div>
      {#if error}
        <div class="error-banner">{error}</div>
      {/if}
      <button type="submit" disabled={loading} class="btn-primary">
        {loading ? 'Signing in...' : 'Sign In'}
      </button>
    </form>

    {#if anySSOEnabled}
      <div class="divider"><span>or continue with</span></div>
      <div class="sso-buttons">
        {#if googleEnabled}
          <button class="sso-btn" on:click={() => window.location.href = '/api/v1/auth/google'}>
            <svg viewBox="0 0 24 24" width="18" height="18">
              <path fill="#4285F4" d="M22.56 12.25c0-.78-.07-1.53-.2-2.25H12v4.26h5.92a5.06 5.06 0 0 1-2.2 3.32v2.77h3.57c2.08-1.92 3.28-4.74 3.28-8.1z"/>
              <path fill="#34A853" d="M12 23c2.97 0 5.46-.98 7.28-2.66l-3.57-2.77c-.98.66-2.23 1.06-3.71 1.06-2.86 0-5.29-1.93-6.16-4.53H2.18v2.84C3.99 20.53 7.7 23 12 23z"/>
              <path fill="#FBBC05" d="M5.84 14.09c-.22-.66-.35-1.36-.35-2.09s.13-1.43.35-2.09V7.07H2.18C1.43 8.55 1 10.22 1 12s.43 3.45 1.18 4.93l2.85-2.22.81-.62z"/>
              <path fill="#EA4335" d="M12 5.38c1.62 0 3.06.56 4.21 1.64l3.15-3.15C17.45 2.09 14.97 1 12 1 7.7 1 3.99 3.47 2.18 7.07l3.66 2.84c.87-2.6 3.3-4.53 6.16-4.53z"/>
            </svg>
            Google
          </button>
        {/if}
        {#if githubEnabled}
          <button class="sso-btn" on:click={() => window.location.href = '/api/v1/auth/github'}>
            <svg viewBox="0 0 24 24" width="18" height="18" fill="currentColor">
              <path d="M12 2C6.477 2 2 6.484 2 12.017c0 4.425 2.865 8.18 6.839 9.504.5.092.682-.217.682-.483 0-.237-.008-.868-.013-1.703-2.782.605-3.369-1.343-3.369-1.343-.454-1.158-1.11-1.466-1.11-1.466-.908-.62.069-.608.069-.608 1.003.07 1.531 1.032 1.531 1.032.892 1.53 2.341 1.088 2.91.832.092-.647.35-1.088.636-1.338-2.22-.253-4.555-1.113-4.555-4.951 0-1.093.39-1.988 1.029-2.688-.103-.253-.446-1.272.098-2.65 0 0 .84-.27 2.75 1.026A9.564 9.564 0 0112 6.844c.85.004 1.705.115 2.504.337 1.909-1.296 2.747-1.027 2.747-1.027.546 1.379.202 2.398.1 2.651.64.7 1.028 1.595 1.028 2.688 0 3.848-2.339 4.695-4.566 4.943.359.309.678.92.678 1.855 0 1.338-.012 2.419-.012 2.747 0 .268.18.58.688.482A10.019 10.019 0 0022 12.017C22 6.484 17.522 2 12 2z"/>
            </svg>
            GitHub
          </button>
        {/if}
        {#if discordEnabled}
          <button class="sso-btn" on:click={() => window.location.href = '/api/v1/auth/discord'}>
            <svg viewBox="0 0 24 24" width="18" height="18" fill="currentColor">
              <path d="M20.317 4.37a19.791 19.791 0 00-4.885-1.515.074.074 0 00-.079.037c-.21.375-.444.864-.608 1.25a18.27 18.27 0 00-5.487 0 12.64 12.64 0 00-.617-1.25.077.077 0 00-.079-.037A19.736 19.736 0 003.677 4.37a.07.07 0 00-.032.027C.533 9.046-.32 13.58.099 18.057a.082.082 0 00.031.057 19.9 19.9 0 005.993 3.03.078.078 0 00.084-.028c.462-.63.874-1.295 1.226-1.994a.076.076 0 00-.041-.106 13.107 13.107 0 01-1.872-.892.077.077 0 01-.008-.128 10.2 10.2 0 00.372-.292.074.074 0 01.077-.01c3.928 1.793 8.18 1.793 12.062 0a.074.074 0 01.078.01c.12.098.246.198.373.292a.077.077 0 01-.006.127 12.299 12.299 0 01-1.873.892.077.077 0 00-.041.107c.36.698.772 1.362 1.225 1.993a.076.076 0 00.084.028 19.839 19.839 0 006.002-3.03.077.077 0 00.032-.054c.5-5.177-.838-9.674-3.549-13.66a.061.061 0 00-.031-.03zM8.02 15.33c-1.183 0-2.157-1.085-2.157-2.419 0-1.333.956-2.419 2.157-2.419 1.21 0 2.176 1.096 2.157 2.42 0 1.333-.956 2.418-2.157 2.418zm7.975 0c-1.183 0-2.157-1.085-2.157-2.419 0-1.333.955-2.419 2.157-2.419 1.21 0 2.176 1.096 2.157 2.42 0 1.333-.946 2.418-2.157 2.418z"/>
            </svg>
            Discord
          </button>
        {/if}
      </div>
    {/if}

    {#if setupRequired}
      <p class="setup-link">
        <a href="/setup">First time? Set up OnScreen</a>
      </p>
    {/if}
  </div>
</div>

<style>
  .login-container {
    min-height: 100vh;
    display: flex;
    align-items: center;
    justify-content: center;
    background: var(--bg-primary);
    font-family: system-ui, -apple-system, sans-serif;
  }

  .login-card {
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
    margin-bottom: 0.5rem;
  }

  .logo-icon {
    border-radius: 10px;
  }

  h1 {
    font-size: 1.5rem;
    font-weight: 700;
    color: var(--text-primary);
    margin: 0;
    letter-spacing: -0.02em;
  }

  .subtitle {
    text-align: center;
    color: var(--text-muted);
    font-size: 0.85rem;
    margin: 0 0 2rem;
  }

  .field {
    margin-bottom: 1.1rem;
  }

  .label-row {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-bottom: 0.4rem;
  }

  .label-row label {
    margin-bottom: 0;
  }

  .forgot-link {
    font-size: 0.75rem;
    color: var(--text-muted);
    text-decoration: none;
    transition: color 0.15s;
  }

  .forgot-link:hover {
    color: #8888a0;
  }

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

  input::placeholder {
    color: var(--text-muted);
  }

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
    letter-spacing: 0.01em;
  }

  .btn-primary:hover:not(:disabled) {
    background: #6b5ce6;
  }

  .btn-primary:active:not(:disabled) {
    transform: scale(0.98);
  }

  .btn-primary:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  .divider {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    margin: 1.5rem 0;
    color: var(--text-muted);
    font-size: 0.75rem;
    text-transform: uppercase;
    letter-spacing: 0.05em;
  }

  .divider::before, .divider::after {
    content: '';
    flex: 1;
    height: 1px;
    background: var(--border);
  }

  .sso-buttons {
    display: flex;
    gap: 0.5rem;
  }

  .sso-btn {
    flex: 1;
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 0.45rem;
    padding: 0.65rem 0.5rem;
    background: var(--bg-hover);
    border: 1px solid var(--border-strong);
    border-radius: 8px;
    color: #b0b0c8;
    font-size: 0.8rem;
    font-weight: 500;
    cursor: pointer;
    transition: background 0.15s, border-color 0.15s, color 0.15s;
  }

  .sso-btn:hover {
    background: var(--border);
    border-color: rgba(255,255,255,0.14);
    color: #ddddf0;
  }

  .setup-link {
    text-align: center;
    margin-top: 1.5rem;
    font-size: 0.8rem;
  }

  .setup-link a {
    color: var(--text-muted);
    text-decoration: none;
    transition: color 0.15s;
  }

  .setup-link a:hover {
    color: #8888a0;
  }

  @media (max-width: 768px) {
    .login-card {
      max-width: 100%;
      padding: 2rem 1.5rem;
      margin: 0 1rem;
      border-radius: 14px;
    }

    .sso-buttons {
      flex-direction: column;
    }
  }
</style>
