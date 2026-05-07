<script lang="ts">
  import { goto } from '$app/navigation';
  import { onMount } from 'svelte';
  import { authApi, settingsApi, api } from '$lib/api';

  let ready = false; // false until we confirm setup is still needed
  let step = 1; // 1 = create account, 2 = enrichment keys (optional), 3 = done

  onMount(async () => {
    try {
      const status = await authApi.setupStatus();
      if (!status.setup_required) {
        goto('/login');
        return;
      }
    } catch {
      // If the endpoint fails, allow setup to proceed
    }
    ready = true;
  });

  // Step 1 fields
  let username = '';
  let email = '';
  let password = '';
  let confirmPassword = '';
  let registerError = '';
  let registering = false;

  // Step 2 fields — metadata enrichment keys. Both optional; libraries
  // still scan without them, posters/summaries just won't backfill.
  let tmdbKey = '';
  let tvdbKey = '';
  let savingKeys = false;
  let keysError = '';

  async function handleRegister() {
    if (password !== confirmPassword) {
      registerError = 'Passwords do not match.';
      return;
    }
    registerError = '';
    registering = true;
    try {
      await authApi.register(username, password, email || undefined);
      // Auto-login after registration.
      const pair = await authApi.login(username, password);
      api.setUser({ user_id: pair.user_id, username: pair.username, is_admin: pair.is_admin });
      step = 2;
    } catch (e: unknown) {
      registerError = e instanceof Error ? e.message : 'Registration failed.';
    } finally {
      registering = false;
    }
  }

  async function handleSaveKeys() {
    savingKeys = true;
    keysError = '';
    try {
      const body: Record<string, string> = {};
      if (tmdbKey.trim()) body.tmdb_api_key = tmdbKey.trim();
      if (tvdbKey.trim()) body.tvdb_api_key = tvdbKey.trim();
      if (Object.keys(body).length > 0) {
        await settingsApi.update(body);
      }
      step = 3;
    } catch (e: unknown) {
      keysError = e instanceof Error ? e.message : 'Failed to save keys.';
    } finally {
      savingKeys = false;
    }
  }

  function skipKeys() {
    step = 3;
  }

  function finish() {
    goto('/');
  }
</script>

<svelte:head>
  <title>OnScreen — Setup</title>
</svelte:head>

{#if ready}
<div class="setup-container">
  <div class="setup-card">
    <div class="logo">
      <img src="/favicon-96x96.png" alt="OnScreen" width="36" height="36" class="logo-icon" />
      <h1>OnScreen</h1>
    </div>

    <div class="steps">
      <div class="step" class:active={step === 1} class:done={step > 1}>
        <span class="step-dot">{step > 1 ? '✓' : '1'}</span>
        <span>Account</span>
      </div>
      <div class="step-line" class:done={step > 1}></div>
      <div class="step" class:active={step === 2} class:done={step > 2}>
        <span class="step-dot">{step > 2 ? '✓' : '2'}</span>
        <span>API Keys</span>
      </div>
      <div class="step-line" class:done={step > 2}></div>
      <div class="step" class:active={step === 3}>
        <span class="step-dot">3</span>
        <span>Done</span>
      </div>
    </div>

    {#if step === 1}
      <form on:submit|preventDefault={handleRegister}>
        <h2>Create Admin Account</h2>
        <div class="field">
          <label for="s-username">Username</label>
          <input id="s-username" bind:value={username} type="text" required autocomplete="username" placeholder="Choose a username" />
        </div>
        <div class="field">
          <label for="s-email">Email <span class="optional">(optional)</span></label>
          <input id="s-email" bind:value={email} type="email" autocomplete="email" placeholder="you@example.com" />
        </div>
        <div class="field">
          <label for="s-password">Password</label>
          <input id="s-password" bind:value={password} type="password" required autocomplete="new-password" placeholder="Min 8 characters" />
        </div>
        <div class="field">
          <label for="s-confirm">Confirm Password</label>
          <input id="s-confirm" bind:value={confirmPassword} type="password" required autocomplete="new-password" placeholder="Repeat password" />
        </div>
        {#if registerError}
          <div class="error-banner">{registerError}</div>
        {/if}
        <button type="submit" disabled={registering} class="btn-primary">
          {registering ? 'Creating...' : 'Create Account'}
        </button>
      </form>
    {:else if step === 2}
      <form on:submit|preventDefault={handleSaveKeys}>
        <h2>Metadata API Keys</h2>
        <p class="step-desc">Posters, summaries, and ratings come from TMDB and TheTVDB. Both are free and optional — you can add them later in Settings.</p>
        <div class="field">
          <label for="s-tmdb">
            TMDB API Key
            <a href="https://www.themoviedb.org/settings/api" target="_blank" rel="noopener noreferrer" class="get-link">Get a key →</a>
          </label>
          <input id="s-tmdb" bind:value={tmdbKey} type="text" autocomplete="off" placeholder="32-character v3 API key" />
        </div>
        <div class="field">
          <label for="s-tvdb">
            TheTVDB API Key
            <a href="https://thetvdb.com/api-information" target="_blank" rel="noopener noreferrer" class="get-link">Get a key →</a>
          </label>
          <input id="s-tvdb" bind:value={tvdbKey} type="text" autocomplete="off" placeholder="UUID-format key (v4 API)" />
        </div>
        {#if keysError}
          <div class="error-banner">{keysError}</div>
        {/if}
        <div class="btn-row">
          <button type="submit" disabled={savingKeys} class="btn-primary">
            {savingKeys ? 'Saving...' : (tmdbKey || tvdbKey ? 'Save & Continue' : 'Continue')}
          </button>
          {#if tmdbKey || tvdbKey}
            <button type="button" class="btn-secondary" on:click={skipKeys}>
              Skip
            </button>
          {/if}
        </div>
      </form>
    {:else}
      <div class="done-section">
        <div class="done-icon">
          <svg width="48" height="48" viewBox="0 0 48 48" fill="none">
            <circle cx="24" cy="24" r="24" fill="rgba(52,211,153,0.12)"/>
            <path d="M15 24l6 6 12-12" stroke="#34d399" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"/>
          </svg>
        </div>
        <h2>You're all set</h2>
        <p class="step-desc">Add libraries from <strong>Settings → Libraries</strong> to start scanning.</p>
        <button class="btn-primary" on:click={finish}>Go to Dashboard</button>
      </div>
    {/if}
  </div>
</div>
{/if}

<style>
  .setup-container {
    min-height: 100vh;
    display: flex;
    align-items: center;
    justify-content: center;
    background: var(--bg-primary);
    font-family: system-ui, -apple-system, sans-serif;
  }

  .setup-card {
    background: #0e0e18;
    border: 1px solid var(--border);
    border-radius: 16px;
    padding: 2.5rem;
    width: 100%;
    max-width: 420px;
    box-shadow: 0 24px 80px var(--shadow);
  }

  .logo {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 0.65rem;
    margin-bottom: 1.75rem;
  }

  .logo-icon { border-radius: 8px; }

  h1 {
    font-size: 1.4rem;
    font-weight: 700;
    color: var(--text-primary);
    margin: 0;
    letter-spacing: -0.02em;
  }

  h2 {
    font-size: 1.05rem;
    font-weight: 600;
    color: var(--text-primary);
    margin: 0 0 1.25rem;
  }

  /* ── Step indicator ─────────────────────────────────────────────────── */
  .steps {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 0;
    margin-bottom: 2rem;
  }

  .step {
    display: flex;
    align-items: center;
    gap: 0.35rem;
    font-size: 0.75rem;
    color: var(--text-muted);
    font-weight: 500;
  }

  .step.active { color: var(--accent-text); }
  .step.done { color: var(--success); }

  .step-dot {
    width: 22px;
    height: 22px;
    border-radius: 50%;
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 0.65rem;
    font-weight: 700;
    background: var(--bg-hover);
    border: 1px solid var(--border-strong);
    flex-shrink: 0;
  }

  .step.active .step-dot {
    background: var(--accent-bg);
    border-color: rgba(124,106,247,0.4);
    color: var(--accent-text);
  }

  .step.done .step-dot {
    background: var(--success-bg);
    border-color: rgba(52,211,153,0.3);
    color: var(--success);
  }

  .step-line {
    width: 32px;
    height: 1px;
    background: var(--border);
    margin: 0 0.5rem;
  }

  .step-line.done {
    background: rgba(52,211,153,0.3);
  }

  /* ── Form fields ────────────────────────────────────────────────────── */
  .field {
    margin-bottom: 1rem;
  }

  label {
    display: block;
    font-size: 0.8rem;
    font-weight: 500;
    color: #8888a0;
    margin-bottom: 0.4rem;
    letter-spacing: 0.02em;
  }

  .optional {
    color: var(--text-muted);
    font-weight: 400;
  }

  .get-link {
    float: right;
    font-size: 0.72rem;
    color: var(--accent-text);
    text-decoration: none;
    font-weight: 500;
    letter-spacing: 0;
  }

  .get-link:hover { text-decoration: underline; }

  input, select {
    width: 100%;
    padding: 0.7rem 0.85rem;
    background: #111120;
    border: 1px solid var(--border-strong);
    border-radius: 8px;
    font-size: 0.95rem;
    color: var(--text-primary);
    outline: none;
    font-family: inherit;
    transition: border-color 0.15s, box-shadow 0.15s;
    box-sizing: border-box;
  }

  input::placeholder { color: var(--text-muted); }

  input:focus, select:focus {
    border-color: rgba(124,106,247,0.5);
    box-shadow: 0 0 0 3px var(--accent-bg);
  }

  select {
    appearance: none;
    background-image: url("data:image/svg+xml,%3Csvg width='10' height='6' viewBox='0 0 10 6' fill='none' xmlns='http://www.w3.org/2000/svg'%3E%3Cpath d='M1 1l4 4 4-4' stroke='%2355556a' stroke-width='1.5' stroke-linecap='round' stroke-linejoin='round'/%3E%3C/svg%3E");
    background-repeat: no-repeat;
    background-position: right 0.85rem center;
    padding-right: 2.5rem;
  }

  select option {
    background: #111120;
    color: var(--text-primary);
  }

  .step-desc {
    color: var(--text-muted);
    font-size: 0.82rem;
    margin: -0.75rem 0 1.25rem;
    line-height: 1.5;
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

  /* ── Buttons ────────────────────────────────────────────────────────── */
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
  }

  .btn-primary:hover:not(:disabled) { background: #6b5ce6; }
  .btn-primary:active:not(:disabled) { transform: scale(0.98); }
  .btn-primary:disabled { opacity: 0.5; cursor: not-allowed; }

  .btn-secondary {
    padding: 0.75rem 1.5rem;
    background: var(--bg-hover);
    border: 1px solid var(--border-strong);
    border-radius: 8px;
    color: #8888a0;
    font-size: 0.95rem;
    font-weight: 500;
    cursor: pointer;
    transition: background 0.15s, color 0.15s;
  }

  .btn-secondary:hover {
    background: var(--border);
    color: #b0b0c8;
  }

  .btn-row {
    display: flex;
    gap: 0.75rem;
  }

  .btn-row .btn-primary { flex: 1; }

  /* ── Done section ───────────────────────────────────────────────────── */
  .done-section {
    text-align: center;
    padding: 1rem 0;
  }

  .done-icon {
    margin-bottom: 1rem;
  }

  .done-section h2 {
    margin-bottom: 0.5rem;
  }

  .done-section .step-desc {
    margin: 0 0 1.5rem;
  }

  /* ── Mobile ─────────────────────────────────────────────────────────── */
  @media (max-width: 768px) {
    .setup-card {
      max-width: 100%;
      padding: 2rem 1.5rem;
      margin: 0 1rem;
      border-radius: 14px;
    }
  }
</style>
