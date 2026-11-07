<script lang="ts">
  import { goto } from '$app/navigation';
  import { onMount } from 'svelte';
  import { authApi, api, userApi, ApiRequestError } from '$lib/api';
  import { applyAccountLocale, t, i18n } from '$lib/i18n/index.svelte';
  import LanguagePicker from '$lib/components/LanguagePicker.svelte';
  import { isTauri, getServerUrl } from '$lib/native';
  import { cleartextWarning } from '$lib/serverConnect';

  let username = '';
  let password = '';
  let error = '';
  let loading = false;
  let oidcEnabled = false;
  let oidcDisplayName = 'SSO';
  let ldapEnabled = false;
  let ldapDisplayName = 'LDAP';
  let samlEnabled = false;
  let samlDisplayName = 'SAML';
  let useLdap = false; // toggle the password form to LDAP mode
  let setupRequired = false;
  let forgotEnabled = false;
  // Two-factor step: set when login returns totp_required. The password
  // form is swapped for a code entry that posts against the challenge.
  let totpRequired = false;
  let challengeToken = '';
  let totpCode = '';
  let useRecoveryCode = false;
  // Post-login redirect target. Set when the login page is reached
  // via ?next= (e.g. native client opens /pair?code=N → not signed
  // in → /login?next=%2Fpair%3Fcode%3DN). For local + LDAP we honor
  // it directly after sign-in. For OIDC / SAML — which round-trip
  // through the IdP and come back to / via cookie-driven state — we
  // stash the redirect in sessionStorage so the / route's bootstrap
  // can pick it up after the OIDC/SAML callback redirects there.
  let nextRedirect: string | null = null;
  // Storage key the / route looks at to honor the post-OIDC bounce.
  // Kept in one spot so client + server-redirect target agree.
  const NEXT_REDIRECT_KEY = 'onscreen_post_login_redirect';

  // Desktop client: which server this sign-in goes to, with the way to
  // change it (otherwise a wrong or dead server leaves the user stuck here —
  // the desktop app has no address bar).
  const desktop = isTauri();
  let serverUrl: string | null = null;
  $: serverWarning = cleartextWarning(serverUrl, $i18n);

  onMount(async () => {
    if (desktop) {
      getServerUrl().then((u) => { serverUrl = u; }).catch(() => {});
    }
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
    // Capture ?next= for the post-login redirect. Validate it stays
    // same-origin (starts with `/`) so an attacker can't craft a
    // /login?next=https://evil.example link that bounces the user
    // off-site after they sign in. open redirect classic.
    const rawNext = params.get('next');
    if (rawNext && rawNext.startsWith('/') && !rawNext.startsWith('//')) {
      nextRedirect = rawNext;
    }
    const oauthError = params.get('error');
    if (oauthError === 'oidc_denied') error = t('login.cancelled');
    else if (oauthError === 'oidc_disabled') error = t('login.oidcDisabled');
    else if (oauthError === 'saml_disabled') error = t('login.samlDisabled');
    else if (oauthError === 'email_unverified') error = t('login.emailUnverified');
    else if (oauthError?.endsWith('_failed')) error = t('login.failedTryAgain');

    const [oidc, ldap, saml, forgot] = await Promise.allSettled([
      authApi.oidcEnabled(),
      authApi.ldapEnabled(),
      authApi.samlEnabled(),
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
    if (saml.status === 'fulfilled') {
      samlEnabled = saml.value.enabled;
      samlDisplayName = saml.value.display_name || 'SAML';
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
      if (pair.totp_required) {
        // Password OK, second factor owed. Hold the challenge and show
        // the code step — no session exists yet.
        challengeToken = pair.login_challenge_token ?? '';
        totpRequired = true;
        password = '';
        loading = false;
        return;
      }
      api.setUser({ user_id: pair.user_id, username: pair.username, is_admin: pair.is_admin });
      syncAccountLanguage();
      goto(nextRedirect ?? '/');
    } catch (e: unknown) {
      // A 401 on the login endpoint is always "wrong username/password" —
      // show a clear, non-leaky message instead of the raw server string
      // (which never reveals which field was wrong, by design).
      error = e instanceof ApiRequestError && e.status === 401
        ? t('login.incorrect')
        : e instanceof Error ? e.message : t('login.failed');
      password = '';
    } finally {
      loading = false;
    }
  }

  async function handleTotpVerify() {
    error = '';
    loading = true;
    try {
      const pair = await authApi.totp.verify(challengeToken, totpCode.trim());
      api.setUser({ user_id: pair.user_id, username: pair.username, is_admin: pair.is_admin });
      syncAccountLanguage();
      goto(nextRedirect ?? '/');
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : t('login.verificationFailed');
      totpCode = '';
    } finally {
      loading = false;
    }
  }

  /** The account's interface language, once signed in (the layout read it
   *  only at startup, before there was an account). Non-blocking. */
  function syncAccountLanguage() {
    Promise.resolve()
      .then(() => userApi.getPreferences())
      .then((p) => applyAccountLocale(p.ui_language))
      .catch(() => {});
  }

  function cancelTotp() {
    totpRequired = false;
    challengeToken = '';
    totpCode = '';
    error = '';
    useRecoveryCode = false;
  }

  /** Called before kicking off an OIDC or SAML redirect. The IdP
   *  round-trip discards URL state (the server-side handler ends at
   *  `/?oidc_auth=1` regardless), so we stash the post-login target
   *  in sessionStorage and let the / route's bootstrap pick it up
   *  after the bounce. Cleared after use to prevent stale redirects. */
  function persistNextForFederated() {
    if (nextRedirect && typeof sessionStorage !== 'undefined') {
      sessionStorage.setItem(NEXT_REDIRECT_KEY, nextRedirect);
    }
  }
</script>

<svelte:head>
  <title>{$i18n('login.pageTitle')}</title>
</svelte:head>

<div class="login-container">
  <div class="login-card">
    <div class="logo">
      <img src="/favicon-96x96.png" alt="OnScreen" width="40" height="40" class="logo-icon" />
      <h1>OnScreen</h1>
    </div>
    <p class="subtitle">{totpRequired ? $i18n('login.twoFactor') : $i18n('login.subtitle')}</p>

    {#if desktop}
      <div class="server-line">
        <span class="server-url" title={serverUrl ?? ''}>{serverUrl ?? '…'}</span>
        <a href="/native/server" class="server-change">{$i18n('login.changeServer')}</a>
      </div>
      {#if serverWarning}
        <div class="cleartext-warning" role="note">{serverWarning}</div>
      {/if}
    {/if}

    {#if totpRequired}
      <form on:submit|preventDefault={handleTotpVerify}>
        <div class="field">
          <label for="totp">{useRecoveryCode ? $i18n('login.recoveryCode') : $i18n('login.authCode')}</label>
          <input
            id="totp"
            bind:value={totpCode}
            type="text"
            inputmode={useRecoveryCode ? 'text' : 'numeric'}
            autocomplete="one-time-code"
            autofocus
            placeholder={useRecoveryCode ? 'XXXXX-XXXXX' : '123456'}
          />
          <p class="hint">
            {useRecoveryCode ? $i18n('login.recoveryHelp') : $i18n('login.authHelp')}
          </p>
        </div>
        {#if error}
          <div class="error-banner">{error}</div>
        {/if}
        <button type="submit" disabled={loading || !totpCode.trim()} class="btn-primary">
          {loading ? $i18n('login.verifying') : $i18n('login.verify')}
        </button>
        <button type="button" class="link-toggle" on:click={() => { useRecoveryCode = !useRecoveryCode; totpCode = ''; error = ''; }}>
          {useRecoveryCode ? $i18n('login.useAuthCode') : $i18n('login.useRecoveryCode')}
        </button>
        <button type="button" class="link-toggle" on:click={cancelTotp}>{$i18n('login.backToSignIn')}</button>
      </form>
    {:else}
    <form on:submit|preventDefault={handleLogin}>
      <div class="field">
        <label for="username">{$i18n('login.username')}</label>
        <input id="username" bind:value={username} type="text" required autocomplete="username" autofocus placeholder={$i18n('login.usernamePlaceholder')} />
      </div>
      <div class="field">
        <div class="label-row">
          <label for="password">{$i18n('login.password')}</label>
          {#if forgotEnabled}
            <a href="/forgot-password" class="forgot-link">{$i18n('login.forgotPassword')}</a>
          {/if}
        </div>
        <input id="password" bind:value={password} type="password" required autocomplete="current-password" placeholder={$i18n('login.passwordPlaceholder')} />
      </div>
      {#if error}
        <div class="error-banner">{error}</div>
      {/if}
      <button type="submit" disabled={loading} class="btn-primary">
        {loading ? $i18n('login.signingIn') : useLdap ? $i18n('login.signInWith', { provider: ldapDisplayName }) : $i18n('login.signIn')}
      </button>
      {#if ldapEnabled}
        <button type="button" class="link-toggle" on:click={() => { useLdap = !useLdap; error = ''; }}>
          {useLdap ? $i18n('login.useLocalAccount') : $i18n('login.signInWith', { provider: ldapDisplayName })}
        </button>
      {/if}
    </form>

    {#if oidcEnabled || samlEnabled}
      <div class="divider"><span>{$i18n('login.orContinueWith')}</span></div>
      <div class="sso-buttons">
        {#if oidcEnabled}
          <button class="sso-btn" on:click={() => { persistNextForFederated(); window.location.href = '/api/v1/auth/oidc'; }}>
            <!-- Key — the conventional SSO mark (the old glyph was an
                 exclamation-circle, which read as an error badge). -->
            <svg viewBox="0 0 24 24" width="18" height="18" fill="currentColor" aria-hidden="true">
              <path d="M12.65 10C11.83 7.67 9.61 6 7 6c-3.31 0-6 2.69-6 6s2.69 6 6 6c2.61 0 4.83-1.67 5.65-4H17v4h4v-4h2v-4H12.65zM7 14c-1.1 0-2-.9-2-2s.9-2 2-2 2 .9 2 2-.9 2-2 2z"/>
            </svg>
            {oidcDisplayName}
          </button>
        {/if}
        {#if samlEnabled}
          <button class="sso-btn" on:click={() => { persistNextForFederated(); window.location.href = '/api/v1/auth/saml'; }}>
            <svg viewBox="0 0 24 24" width="18" height="18" fill="currentColor" aria-hidden="true">
              <path d="M12 1a4 4 0 00-4 4v3H6a2 2 0 00-2 2v10a2 2 0 002 2h12a2 2 0 002-2V10a2 2 0 00-2-2h-2V5a4 4 0 00-4-4zm-2 7V5a2 2 0 014 0v3h-4z"/>
            </svg>
            {samlDisplayName}
          </button>
        {/if}
      </div>
    {/if}

    {#if setupRequired}
      <p class="setup-link">
        <a href="/setup">{$i18n('login.firstTime')}</a>
      </p>
    {/if}
    {/if}
    <div class="language-row">
      <LanguagePicker compact />
    </div>
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
    background: var(--bg-elevated);
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
    color: var(--text-secondary);
  }

  label {
    display: block;
    font-size: 0.8rem;
    font-weight: 500;
    color: var(--text-secondary);
    margin-bottom: 0.4rem;
    letter-spacing: 0.02em;
  }

  input {
    width: 100%;
    padding: 0.7rem 0.85rem;
    background: var(--input-bg);
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
    border-color: var(--accent);
    box-shadow: 0 0 0 3px var(--accent-bg);
  }

  .hint {
    margin: 0.45rem 0 0;
    font-size: 0.72rem;
    color: var(--text-muted);
    line-height: 1.4;
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
    background: var(--accent-hover);
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
  .link-toggle:hover { color: var(--text-secondary); }

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
    color: var(--text-secondary);
    font-size: 0.8rem;
    font-weight: 500;
    cursor: pointer;
    transition: background 0.15s, border-color 0.15s, color 0.15s;
  }

  .sso-btn:hover {
    background: var(--border);
    border-color: var(--border-strong);
    color: var(--text-primary);
  }

  .server-line {
    display: flex;
    align-items: baseline;
    justify-content: center;
    gap: 0.6rem;
    margin: -1.25rem 0 1.25rem;
    font-size: 0.78rem;
    min-width: 0;
  }
  .server-url {
    color: var(--text-secondary);
    font-family: ui-monospace, monospace;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    min-width: 0;
  }
  .server-change {
    color: var(--accent-text);
    text-decoration: none;
    white-space: nowrap;
  }
  .server-change:hover { text-decoration: underline; }
  .cleartext-warning {
    margin: 0 0 1.25rem;
    padding: 0.55rem 0.75rem;
    border-radius: 8px;
    border: 1px solid rgba(217,119,6,0.35);
    background: rgba(251,191,36,0.10);
    color: var(--text-secondary);
    font-size: 0.76rem;
    line-height: 1.5;
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
    color: var(--text-secondary);
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
  .language-row { margin-top: 1.25rem; }
</style>
