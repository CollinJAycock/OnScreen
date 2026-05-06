<script lang="ts">
  // Device-pairing sign-in screen — TV equivalent of opening
  // /pair on a phone. Mirrors the Android PairingFragment flow:
  //
  //   1. POST /auth/pair/code  -> { pin, device_token, poll_after }
  //   2. Render the PIN + the URL the user opens on a second device.
  //   3. Long-poll /auth/pair/poll until the server returns the
  //      signed-in token pair (Done) or the code expires (Expired).
  //   4. On Done -> persist tokens via api.setTokens, navigate to /hub.
  //   5. On Expired -> request a fresh code automatically; user never
  //      sees a dead-end "code expired" terminal screen.
  //
  // The reason this exists: TV remotes can't do OIDC / SAML / OAuth
  // redirect dances. Pairing punts those flows to a real browser on
  // the user's phone, then hands the resulting tokens back here.
  // Plex / Disney+ / Netflix all use the same approach for the same
  // reason.

  import { onMount, onDestroy } from 'svelte';
  import { goto } from '$app/navigation';
  import { api, endpoints, type EnabledProvider } from '$lib/api';
  import { focusable } from '$lib/focus/focusable';
  import { focusManager } from '$lib/focus/manager';

  type Phase = 'creating' | 'waiting' | 'expired_recycling' | 'failed';

  let phase = $state<Phase>('creating');
  let pin = $state('');
  let deviceToken = $state('');
  let pollSeconds = $state(2);
  let serverUrl = $state(api.getOrigin() ?? '');
  let pairUrl = $derived(serverUrl ? `${serverUrl}/pair` : '/pair');
  let errorMsg = $state('');

  // Federated providers configured on this server. Surfaced as a
  // hint so a laptop user knows they can pick "Sign in with X" on
  // the web pair page — the underlying flow doesn't change (PIN
  // claim is auth-agnostic), this is a TV-side affordance only.
  let providers = $state<EnabledProvider[]>([]);

  let pollTimer: ReturnType<typeof setTimeout> | null = null;
  let cancelled = false;

  async function startCycle() {
    phase = 'creating';
    errorMsg = '';
    try {
      const code = await endpoints.pair.start();
      pin = code.pin;
      deviceToken = code.device_token;
      // Same clamp as the Android client — tighter than the server-
      // suggested cadence so the screen dismisses ~1 s after the user
      // types the PIN on their phone.
      pollSeconds = Math.max(1, Math.min(3, code.poll_after));
      phase = 'waiting';
      schedulePoll();
    } catch (e) {
      errorMsg = (e as Error).message ?? 'Could not reach server';
      phase = 'failed';
      // Auto-retry so a transient network blip doesn't strand the
      // user on a dead screen — same behaviour as the Android client.
      pollTimer = setTimeout(() => { if (!cancelled) startCycle(); }, 5000);
    }
  }

  function schedulePoll() {
    pollTimer = setTimeout(pollOnce, pollSeconds * 1000);
  }

  async function pollOnce() {
    if (cancelled) return;
    try {
      const result = await endpoints.pair.poll(deviceToken);
      if (cancelled) return;
      switch (result.status) {
        case 'done':
          if (result.pair) api.setTokens(result.pair);
          goto('/hub');
          return;
        case 'pending':
          schedulePoll();
          return;
        case 'expired':
          phase = 'expired_recycling';
          await startCycle();
          return;
      }
    } catch (e) {
      // Network blip — keep polling, surface a soft retry message.
      errorMsg = `Couldn't reach server, retrying — ${(e as Error).message ?? ''}`;
      schedulePoll();
    }
  }

  onMount(() => {
    startCycle();
    // Best-effort provider probe in parallel with the pair-code
    // request — never blocks the PIN render. Failure → no hint.
    void endpoints.auth.providers().then((p) => { providers = p; }).catch(() => {});
    // Back button leaves pairing and returns to the local-credentials
    // login flow.
    return focusManager.pushBack(() => {
      goto('/login');
      return true;
    });
  });

  onDestroy(() => {
    cancelled = true;
    if (pollTimer) clearTimeout(pollTimer);
  });
</script>

<div class="page">
  <h1>Sign in</h1>

  <p class="instructions">
    Open this URL on a phone or computer:
  </p>
  <div class="url">{pairUrl}</div>

  <div class="pin-label">Enter the PIN</div>
  <div class="pin">{pin || '------'}</div>

  <p class="status">
    {#if phase === 'creating'}
      Generating code…
    {:else if phase === 'waiting'}
      Waiting for sign-in…
    {:else if phase === 'expired_recycling'}
      Code expired — refreshing…
    {:else}
      {errorMsg}
    {/if}
  </p>

  {#if providers.length > 0}
    <p class="sso-hint">
      Includes <strong>{providers.map((p) => p.display_name).join(' / ')}</strong> sign-in on the web pair page.
    </p>
  {/if}

  <button use:focusable class="cancel-btn" onclick={() => goto('/login')}>
    Use password instead
  </button>
</div>

<style>
  .page {
    padding: var(--page-pad);
    display: flex;
    flex-direction: column;
    align-items: center;
    text-align: center;
    gap: 18px;
    min-height: 100vh;
    box-sizing: border-box;
    justify-content: center;
  }
  h1 {
    font-size: var(--font-2xl);
    margin: 0 0 12px;
  }
  .instructions {
    font-size: var(--font-md);
    color: var(--text-secondary);
    margin: 0;
  }
  .url {
    font-family: monospace;
    font-size: var(--font-lg);
    color: var(--accent, #7c6af7);
  }
  .pin-label {
    margin-top: 32px;
    font-size: var(--font-sm);
    color: var(--text-secondary);
    text-transform: uppercase;
    letter-spacing: 0.15em;
  }
  .pin {
    font-family: monospace;
    font-size: 96px;
    font-weight: 700;
    letter-spacing: 0.2em;
    color: var(--text-primary);
  }
  .status {
    margin-top: 24px;
    font-size: var(--font-md);
    color: var(--text-secondary);
    min-height: 1.5em;
  }
  .sso-hint {
    margin: 4px 0 0;
    font-size: var(--font-sm);
    color: var(--text-secondary);
    max-width: 700px;
  }
  .cancel-btn {
    margin-top: 24px;
    padding: 12px 28px;
    font-size: var(--font-sm);
  }
</style>
