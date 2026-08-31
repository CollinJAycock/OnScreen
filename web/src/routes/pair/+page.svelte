<script lang="ts">
  import { onMount } from 'svelte';
  import { authApi, api } from '$lib/api';

  let pin = '';
  let deviceName = '';
  let error = '';
  let success = false;
  let submittedDevice = '';
  let loading = false;
  // Device name passed in the deep link by the requesting client. A hint
  // only — it is attacker-controllable in the same way the PIN is, so it is
  // labelled as "claims to be" rather than presented as verified fact.
  let hintedDevice = '';
  // Server-attested facts about the device that asked for this PIN, fetched
  // from /auth/pair/pending. This is the part the user can actually trust.
  let pending: { ip?: string; user_agent?: string; requested_at?: string } | null = null;

  // Pre-fill the PIN from a query param so a TV-app QR code can deep-link
  // straight to the pre-filled form.
  onMount(async () => {
    if (!api.getUser()) {
      const next = encodeURIComponent(window.location.pathname + window.location.search);
      window.location.href = `/login?next=${next}`;
      return;
    }
    const params = new URLSearchParams(window.location.search);
    const seed = params.get('pin') ?? params.get('code') ?? '';
    pin = seed.replace(/\D/g, '').slice(0, 6);
    // The device name the requesting client passed along, shown on the
    // Authorise button so the user can see what they are approving.
    const dn = (params.get('device_name') ?? '').trim();
    if (dn) hintedDevice = dn.slice(0, 64);
    // NO AUTO-CLAIM. There used to be an `auto=1` branch here that called
    // submit() straight from onMount, on the reasoning that a signed-in user
    // with a 6-digit PIN already present had no reason to tap "Authorize".
    //
    // That tap IS the consent, and it was the only thing standing between a
    // legitimate pairing and an attacker-supplied one. POST /auth/pair/code
    // needs no credential, so anyone can mint a PIN; the session cookie is
    // SameSite=Lax, so it rides along on a top-level GET navigation; and the
    // poll endpoint hands the code's creator a full token pair including a
    // 30-day refresh token and the account's is_admin flag. One forced
    // navigation to /pair?code=NNNNNN&auto=1 therefore converted a victim's
    // live session into a durable attacker session for their account, with no
    // click, no dialog, and no audit row. Being signed out did not help — the
    // redirect above returns the user right back here after login.
    //
    // It also broke the invariant internal/api/middleware/auth.go states for
    // the whole product: no top-level GET may have side effects. The server
    // route is correctly a POST; this page was turning a Lax-permitted
    // navigation into exactly the side effect that invariant exists to stop.
    //
    // The native flow still works: the user lands on a pre-filled form and
    // taps once. Do not reintroduce a parameter that skips the tap.
  });

  function onPinInput(e: Event) {
    const v = (e.target as HTMLInputElement).value.replace(/\D/g, '').slice(0, 6);
    pin = v;
    if (error) error = '';
    pending = null;
    if (v.length === 6) void loadPending(v);
  }

  /** Fetch what the server knows about the device that requested this PIN, so
   *  the user is approving something described rather than a bare number. A
   *  failure here is not an error the user needs to see — the form still
   *  works, they just get less context. */
  async function loadPending(code: string) {
    try {
      const p = await authApi.pendingPair(code);
      if (pin === code) pending = p;
    } catch {
      if (pin === code) pending = null;
    }
  }

  async function submit() {
    error = '';
    success = false;
    if (pin.length !== 6) {
      error = 'Enter the 6-digit code shown on your TV.';
      return;
    }
    loading = true;
    try {
      const res = await authApi.claimPair(pin, deviceName.trim() || undefined);
      submittedDevice = res.device_name || deviceName.trim() || 'your device';
      success = true;
      pin = '';
      deviceName = '';
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Could not authorise this device.';
    } finally {
      loading = false;
    }
  }
</script>

<svelte:head>
  <title>OnScreen — Pair Device</title>
</svelte:head>

<div class="container">
  <div class="card">
    <h1>Pair a device</h1>
    <p class="subtitle">
      Enter the 6-digit code displayed on your TV, console, or mobile app to
      authorise it for your account.
    </p>

    {#if success}
      <div class="success">
        <strong>Device authorised.</strong>
        <p>{submittedDevice} should sign in within a few seconds.</p>
        <button type="button" class="btn-secondary" on:click={() => { success = false; }}>
          Pair another
        </button>
      </div>
    {:else}
      <form on:submit|preventDefault={submit}>
        <div class="field">
          <label for="pin">Pairing code</label>
          <input
            id="pin"
            type="text"
            inputmode="numeric"
            autocomplete="one-time-code"
            placeholder="000000"
            value={pin}
            on:input={onPinInput}
            class="pin-input"
            maxlength="6"
            autofocus
          />
        </div>
        <div class="field">
          <label for="device-name">Device name (optional)</label>
          <input
            id="device-name"
            bind:value={deviceName}
            type="text"
            placeholder="Living Room TV"
            maxlength="64"
          />
        </div>
        {#if pending}
          <div class="attest">
            <strong>You are about to authorise:</strong>
            <dl>
              {#if hintedDevice}
                <dt>Claims to be</dt><dd>{hintedDevice}</dd>
              {/if}
              {#if pending.ip}
                <dt>Requested from</dt><dd>{pending.ip}</dd>
              {/if}
              {#if pending.user_agent}
                <dt>Device</dt><dd>{pending.user_agent}</dd>
              {/if}
            </dl>
            <p class="attest-warn">
              If you did not start this on a device you own, do not continue —
              whoever requested this code gets access to your account.
            </p>
          </div>
        {/if}
        {#if error}
          <div class="error-banner">{error}</div>
        {/if}
        <button type="submit" disabled={loading || pin.length !== 6} class="btn-primary">
          {loading ? 'Authorising…' : 'Authorise device'}
        </button>
      </form>
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
    padding: 2rem 1rem;
    font-family: system-ui, -apple-system, sans-serif;
  }

  .card {
    background: #0e0e18;
    border: 1px solid var(--border);
    border-radius: 16px;
    padding: 2.5rem;
    width: 100%;
    max-width: 420px;
    box-shadow: 0 24px 80px var(--shadow);
  }

  h1 {
    font-size: 1.5rem;
    font-weight: 700;
    color: var(--text-primary);
    margin: 0 0 0.5rem;
    letter-spacing: -0.02em;
  }

  .subtitle {
    color: var(--text-muted);
    font-size: 0.9rem;
    margin: 0 0 1.75rem;
    line-height: 1.5;
  }

  .field {
    margin-bottom: 1.1rem;
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
    transition: border-color 0.15s;
  }

  input:focus {
    border-color: var(--accent, #6366f1);
  }

  .pin-input {
    font-family: 'SF Mono', ui-monospace, Menlo, Consolas, monospace;
    font-size: 1.6rem;
    letter-spacing: 0.6em;
    padding-left: 1.1rem;
    text-align: center;
  }

  .error-banner {
    background: rgba(239, 68, 68, 0.1);
    border: 1px solid rgba(239, 68, 68, 0.3);
    color: #fca5a5;
    border-radius: 8px;
    padding: 0.6rem 0.85rem;
    font-size: 0.85rem;
    margin-bottom: 1rem;
  }

  .btn-primary {
    width: 100%;
    padding: 0.75rem;
    background: var(--accent, #6366f1);
    color: white;
    border: none;
    border-radius: 8px;
    font-size: 0.95rem;
    font-weight: 600;
    cursor: pointer;
    transition: filter 0.15s, transform 0.05s;
  }

  .btn-primary:hover:not(:disabled) {
    filter: brightness(1.1);
  }

  .btn-primary:active:not(:disabled) {
    transform: translateY(1px);
  }

  .btn-primary:disabled {
    opacity: 0.55;
    cursor: not-allowed;
  }

  .btn-secondary {
    margin-top: 1rem;
    padding: 0.55rem 0.9rem;
    background: transparent;
    color: var(--text-primary);
    border: 1px solid var(--border);
    border-radius: 8px;
    font-size: 0.85rem;
    cursor: pointer;
  }

  .btn-secondary:hover {
    background: rgba(255, 255, 255, 0.04);
  }

  .success {
    background: rgba(34, 197, 94, 0.08);
    border: 1px solid rgba(34, 197, 94, 0.3);
    color: #86efac;
    border-radius: 8px;
    padding: 1rem;
    text-align: center;
  }

  .success p {
    margin: 0.4rem 0 0;
    color: var(--text-muted);
    font-size: 0.85rem;
  }

  .attest {
    background: var(--bg-secondary, #1a1d23);
    border: 1px solid var(--border);
    border-left: 3px solid var(--accent, #4a9eff);
    border-radius: 6px;
    padding: 0.85rem 1rem;
    margin-bottom: 1rem;
    font-size: 0.88rem;
    color: var(--text-primary);
  }
  .attest strong { display: block; margin-bottom: 0.5rem; }
  .attest dl {
    display: grid;
    grid-template-columns: auto 1fr;
    gap: 0.25rem 0.75rem;
    margin: 0;
  }
  .attest dt { color: var(--text-muted); }
  .attest dd { margin: 0; word-break: break-word; }
  .attest-warn {
    margin: 0.65rem 0 0;
    color: var(--text-muted);
    font-size: 0.82rem;
    line-height: 1.45;
  }
</style>
