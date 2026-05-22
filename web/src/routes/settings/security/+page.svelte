<script lang="ts">
  import { onMount } from 'svelte';
  import { authApi } from '$lib/api';
  import { toast } from '$lib/stores/toast';

  type View = 'loading' | 'disabled' | 'enrolling' | 'recovery' | 'enabled';

  let view: View = 'loading';
  let recoveryRemaining = 0;
  let busy = false;
  let error = '';

  // Enrolment state.
  let otpauthUrl = '';
  let secret = '';
  let qrDataUrl = '';
  let enrollCode = '';
  let recoveryCodes: string[] = [];

  // Disable state.
  let disableCode = '';

  onMount(loadStatus);

  async function loadStatus() {
    error = '';
    try {
      const s = await authApi.totp.status();
      recoveryRemaining = s.recovery_codes_remaining;
      view = s.enabled ? 'enabled' : 'disabled';
    } catch (e) {
      error = e instanceof Error ? e.message : 'Could not load 2FA status.';
      view = 'disabled';
    }
  }

  async function startEnrol() {
    error = '';
    busy = true;
    try {
      const s = await authApi.totp.setup();
      otpauthUrl = s.otpauth_url;
      secret = s.secret;
      // Render the otpauth URI to a QR the authenticator app can scan.
      // Dynamic import keeps qrcode out of the SSR/prerender path.
      const QRCode = (await import('qrcode')).default;
      qrDataUrl = await QRCode.toDataURL(otpauthUrl, { margin: 1, width: 200 });
      enrollCode = '';
      view = 'enrolling';
    } catch (e) {
      error = e instanceof Error ? e.message : 'Could not start setup.';
    } finally {
      busy = false;
    }
  }

  async function confirmEnrol() {
    error = '';
    busy = true;
    try {
      const r = await authApi.totp.activate(enrollCode.trim());
      recoveryCodes = r.recovery_codes;
      view = 'recovery';
    } catch (e) {
      error = e instanceof Error ? e.message : 'That code did not match.';
      enrollCode = '';
    } finally {
      busy = false;
    }
  }

  async function finishEnrol() {
    recoveryCodes = [];
    secret = '';
    otpauthUrl = '';
    qrDataUrl = '';
    await loadStatus();
  }

  async function disable() {
    error = '';
    busy = true;
    try {
      await authApi.totp.disable(disableCode.trim());
      disableCode = '';
      toast.success('Two-factor authentication disabled.');
      await loadStatus();
    } catch (e) {
      error = e instanceof Error ? e.message : 'That code did not match.';
      disableCode = '';
    } finally {
      busy = false;
    }
  }

  function copyRecovery() {
    navigator.clipboard?.writeText(recoveryCodes.join('\n')).then(
      () => toast.success('Recovery codes copied.'),
      () => toast.error('Copy failed — select and copy manually.'),
    );
  }

  function downloadRecovery() {
    const blob = new Blob(
      [
        'OnScreen two-factor recovery codes\n',
        'Each code works once. Store them somewhere safe.\n\n',
        recoveryCodes.join('\n'),
        '\n',
      ],
      { type: 'text/plain' },
    );
    const a = document.createElement('a');
    a.href = URL.createObjectURL(blob);
    a.download = 'onscreen-recovery-codes.txt';
    a.click();
    URL.revokeObjectURL(a.href);
  }
</script>

<svelte:head><title>OnScreen — Security</title></svelte:head>

<div class="security">
  <h1>Security</h1>
  <section class="card">
    <div class="card-head">
      <div>
        <h2>Two-factor authentication</h2>
        <p class="sub">
          Protect your password login with a 6-digit code from an authenticator
          app (Google Authenticator, Authy, 1Password, …).
        </p>
      </div>
      {#if view === 'enabled'}
        <span class="badge on">On</span>
      {:else if view === 'disabled'}
        <span class="badge off">Off</span>
      {/if}
    </div>

    {#if error}
      <div class="banner error">{error}</div>
    {/if}

    {#if view === 'loading'}
      <p class="muted">Loading…</p>

    {:else if view === 'disabled'}
      <button class="btn-primary" on:click={startEnrol} disabled={busy}>
        {busy ? 'Starting…' : 'Enable 2FA'}
      </button>

    {:else if view === 'enrolling'}
      <ol class="steps">
        <li>
          <strong>Scan this QR code</strong> in your authenticator app, or enter
          the key manually.
          {#if qrDataUrl}
            <img class="qr" src={qrDataUrl} alt="2FA QR code" width="200" height="200" />
          {/if}
          <code class="secret">{secret}</code>
        </li>
        <li>
          <strong>Enter the 6-digit code</strong> the app shows to confirm.
          <form on:submit|preventDefault={confirmEnrol} class="code-row">
            <input
              bind:value={enrollCode}
              type="text"
              inputmode="numeric"
              autocomplete="one-time-code"
              placeholder="123456"
              maxlength="6"
            />
            <button class="btn-primary" type="submit" disabled={busy || !enrollCode.trim()}>
              {busy ? 'Verifying…' : 'Confirm'}
            </button>
          </form>
        </li>
      </ol>
      <button class="link" on:click={finishEnrol} disabled={busy}>Cancel</button>

    {:else if view === 'recovery'}
      <div class="banner ok">
        2FA is on. Save these <strong>recovery codes</strong> now — each works
        once and they're your way back in if you lose your authenticator.
        They won't be shown again.
      </div>
      <ul class="codes">
        {#each recoveryCodes as c}<li>{c}</li>{/each}
      </ul>
      <div class="code-actions">
        <button class="btn-secondary" on:click={copyRecovery}>Copy</button>
        <button class="btn-secondary" on:click={downloadRecovery}>Download</button>
        <button class="btn-primary" on:click={finishEnrol}>I've saved them</button>
      </div>

    {:else if view === 'enabled'}
      <p class="muted">
        Two-factor authentication is on.
        {recoveryRemaining} recovery code{recoveryRemaining === 1 ? '' : 's'} remaining.
      </p>
      <form on:submit|preventDefault={disable} class="code-row">
        <input
          bind:value={disableCode}
          type="text"
          inputmode="text"
          autocomplete="one-time-code"
          placeholder="Code to disable"
        />
        <button class="btn-danger" type="submit" disabled={busy || !disableCode.trim()}>
          {busy ? 'Disabling…' : 'Disable 2FA'}
        </button>
      </form>
      <p class="hint">Enter a current authenticator code or a recovery code to turn 2FA off.</p>
    {/if}
  </section>
</div>

<style>
  .security { max-width: 640px; }
  h1 { font-size: 1.25rem; font-weight: 700; color: var(--text-primary); margin: 0 0 1.25rem; }
  .card {
    background: var(--bg-card, #0e0e18);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 1.25rem 1.4rem;
  }
  .card-head { display: flex; justify-content: space-between; align-items: flex-start; gap: 1rem; }
  h2 { font-size: 1rem; font-weight: 600; color: var(--text-primary); margin: 0 0 0.3rem; }
  .sub { color: var(--text-muted); font-size: 0.82rem; margin: 0 0 1rem; line-height: 1.45; max-width: 46ch; }
  .badge { font-size: 0.7rem; font-weight: 700; padding: 0.2rem 0.5rem; border-radius: 999px; text-transform: uppercase; letter-spacing: 0.04em; white-space: nowrap; }
  .badge.on { background: var(--success-bg); color: var(--success); }
  .badge.off { background: var(--bg-hover); color: var(--text-muted); }
  .muted { color: var(--text-muted); font-size: 0.85rem; }
  .hint { color: var(--text-muted); font-size: 0.74rem; margin: 0.5rem 0 0; }
  .steps { margin: 0; padding-left: 1.2rem; color: var(--text-primary); font-size: 0.88rem; }
  .steps li { margin-bottom: 1.1rem; line-height: 1.5; }
  .qr { display: block; margin: 0.7rem 0; border-radius: 8px; background: #fff; padding: 6px; }
  .secret {
    display: inline-block; font-family: ui-monospace, monospace; font-size: 0.85rem;
    letter-spacing: 0.1em; background: var(--bg-hover); border: 1px solid var(--border);
    border-radius: 6px; padding: 0.35rem 0.6rem; color: var(--text-primary); word-break: break-all;
  }
  .code-row { display: flex; gap: 0.5rem; margin-top: 0.6rem; }
  .code-row input {
    flex: 1; padding: 0.6rem 0.7rem; background: #111120; border: 1px solid var(--border);
    border-radius: 8px; color: var(--text-primary); font-size: 0.95rem; outline: none;
  }
  .code-row input:focus { border-color: rgba(124,106,247,0.5); box-shadow: 0 0 0 3px var(--accent-bg); }
  .codes {
    list-style: none; margin: 0.8rem 0; padding: 0.8rem 1rem; display: grid;
    grid-template-columns: 1fr 1fr; gap: 0.4rem 1.5rem; background: var(--bg-hover);
    border: 1px solid var(--border); border-radius: 8px;
    font-family: ui-monospace, monospace; font-size: 0.9rem; letter-spacing: 0.08em; color: var(--text-primary);
  }
  .code-actions { display: flex; gap: 0.5rem; flex-wrap: wrap; }
  .banner { border-radius: 8px; padding: 0.6rem 0.85rem; font-size: 0.82rem; margin-bottom: 1rem; line-height: 1.45; }
  .banner.error { background: var(--error-bg); color: var(--error); }
  .banner.ok { background: var(--success-bg); color: var(--success); }
  .btn-primary { padding: 0.6rem 1rem; background: var(--accent); color: #fff; border: none; border-radius: 8px; font-size: 0.88rem; font-weight: 600; cursor: pointer; }
  .btn-primary:disabled { opacity: 0.5; cursor: not-allowed; }
  .btn-secondary { padding: 0.6rem 1rem; background: var(--bg-hover); color: var(--text-primary); border: 1px solid var(--border-strong); border-radius: 8px; font-size: 0.88rem; font-weight: 500; cursor: pointer; }
  .btn-danger { padding: 0.6rem 1rem; background: var(--error-bg); color: var(--error); border: 1px solid var(--error); border-radius: 8px; font-size: 0.88rem; font-weight: 600; cursor: pointer; }
  .btn-danger:disabled { opacity: 0.5; cursor: not-allowed; }
  .link { background: none; border: none; color: var(--text-muted); font-size: 0.78rem; cursor: pointer; margin-top: 0.75rem; padding: 0; }
  .link:hover { color: var(--text-primary); }
</style>
