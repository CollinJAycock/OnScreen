<script lang="ts">
  import { goto } from '$app/navigation';
  import { onMount } from 'svelte';
  import { authApi, api } from '$lib/api';

  let username = '';
  let password = '';
  let error = '';
  let loading = false;
  let oidcEnabled = false;
  let oidcDisplayName = 'SSO';
  let ldapEnabled = false;
  let ldapDisplayName = 'LDAP';
  let useLdap = false; // toggle the password form to LDAP mode
  let setupRequired = false;
  let forgotEnabled = false;

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
    if (oauthError === 'oidc_denied') error = 'Sign-in was cancelled.';
    else if (oauthError === 'oidc_disabled') error = 'OIDC sign-in is not configured.';
    else if (oauthError === 'email_unverified') error = 'Your email is not verified.';
    else if (oauthError?.endsWith('_failed')) error = 'Sign-in failed. Please try again.';

    const [oidc, ldap, forgot] = await Promise.allSettled([
      authApi.oidcEnabled(),
      authApi.ldapEnabled(),
      authApi.forgotPasswordEnabled()
    ]);
    if (oidc.status === 'fulfilled') {
      oidcEnabled = oidc.value.enabled;
      oidcDisplayName = oidc.value.display_name || 'SSO';
    }
    if (ldap.status === 'fulfilled') {
      ldapEnabled = ldap.value.enabled;
      ldapDisplayName = ldap.value.display_name || 'LDAP';
    }
    if (forgot.status === 'fulfilled') forgotEnabled = forgot.value.enabled;
  });

  async function handleLogin() {
    error = '';
    loading = true;
    try {
      const pair = useLdap
        ? await authApi.ldapLogin(username, password)
        : await authApi.login(username, password);
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
        {loading ? 'Signing in...' : useLdap ? `Sign in with ${ldapDisplayName}` : 'Sign In'}
      </button>
      {#if ldapEnabled}
        <button type="button" class="link-toggle" on:click={() => { useLdap = !useLdap; error = ''; }}>
          {useLdap ? 'Use local account' : `Sign in with ${ldapDisplayName}`}
        </button>
      {/if}
    </form>

    {#if oidcEnabled}
      <div class="divider"><span>or continue with</span></div>
      <div class="sso-buttons">
        <button class="sso-btn" on:click={() => window.location.href = '/api/v1/auth/oidc'}>
          <svg viewBox="0 0 24 24" width="18" height="18" fill="currentColor" aria-hidden="true">
            <path d="M12 2a10 10 0 100 20 10 10 0 000-20zm-1 4h2v8h-2V6zm0 10h2v2h-2v-2z"/>
          </svg>
          {oidcDisplayName}
        </button>
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

  .link-toggle {
    display: block;
    width: 100%;
    margin-top: 0.65rem;
    background: none;
    border: none;
    color: var(--text-muted);
    font-size: 0.78rem;
    cursor: pointer;
    text-align: center;
    padding: 0.25rem 0;
    transition: color 0.15s;
  }
  .link-toggle:hover { color: #b0b0c8; }

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
