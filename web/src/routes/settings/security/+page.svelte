<script lang="ts">
  import { onMount } from 'svelte';
  import { settingsApi, userApi } from '$lib/api';
  import SignedInDevices from '$lib/components/SignedInDevices.svelte';
  import ApiKeys from '$lib/components/ApiKeys.svelte';
  import TwoFactor from '$lib/components/TwoFactor.svelte';
  import type { TLSStatus } from '$lib/api';
  import { toast } from '$lib/stores/toast';

  // HTTPS / TLS state. Admin-uploaded certificate served from an in-memory
  // tls.Config (no file on disk); env TLS_CERT_FILE / TLS_KEY_FILE still win
  // and lock the form when set.
  let tls: TLSStatus = { configured: false, source: 'none' };
  let tlsCertPem = '';
  let tlsKeyPem = '';
  let tlsSaving = false;

  onMount(async () => {
    try { tls = await settingsApi.getTLS(); } catch { /* non-fatal */ }
  });

  async function saveTLS() {
    tlsSaving = true;
    try {
      await settingsApi.updateTLS({ cert_pem: tlsCertPem, key_pem: tlsKeyPem });
      toast.success(tlsCertPem ? 'TLS certificate saved — restart to serve HTTPS' : 'TLS certificate cleared — restart to apply');
      tlsCertPem = '';
      tlsKeyPem = '';
      tls = await settingsApi.getTLS();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Save failed');
    } finally {
      tlsSaving = false;
    }
  }
</script>

<svelte:head><title>OnScreen — Security</title></svelte:head>

<div class="security">
  <h1>Security</h1>
  <section class="card">
    <TwoFactor />
  </section>

  <section class="card">
    <div class="card-head">
      <div>
        <h2>Signed-in devices</h2>
        <p class="sub">
          Browsers and apps signed in to your account. Signing one out ends its
          session at once; your other devices stay signed in.
        </p>
      </div>
    </div>
    <SignedInDevices
      load={() => userApi.listMySessions()}
      revoke={(id) => userApi.revokeMySession(id)}
      revokeAll={() => userApi.revokeMyOtherSessions()}
      revokeAllLabel="Sign out everywhere else"
      revokeAllConfirm="Sign out every other device signed in to your account? This browser stays signed in."
      keepCurrent
    />
  </section>

  <section class="card">
    <div class="card-head">
      <div>
        <h2>API keys</h2>
        <p class="sub">
          Your long-lived keys for scripts and integrations (Home Assistant,
          Kometa). A key acts as you, but can't sign in or change account
          security. See docs/api/api-keys.md for usage.
        </p>
      </div>
    </div>
    <ApiKeys />
  </section>

  <section class="card">
    <div class="card-head">
      <div>
        <h2>All API keys</h2>
        <p class="sub">
          Every user's API keys. Revoke one that is leaked or no longer needed;
          it stops working on its next request.
        </p>
      </div>
    </div>
    <ApiKeys admin />
  </section>

  <section class="card">
    <div class="card-head">
      <div>
        <h2>HTTPS / TLS certificate</h2>
        <p class="sub">
          Serve HTTPS directly without a reverse proxy. The key is stored
          encrypted and loaded into memory at startup — no cert file on disk.
          Restart-required. For per-host certs in a cluster, use a reverse proxy
          or the <code>TLS_CERT_FILE</code> env path instead.
        </p>
      </div>
      {#if tls.configured}
        <span class="badge on">{tls.source === 'env-file' ? 'env file' : 'uploaded'}</span>
      {:else}
        <span class="badge off">plain HTTP</span>
      {/if}
    </div>

    {#if tls.source === 'env-file'}
      <p class="muted">
        TLS is configured via <code>TLS_CERT_FILE</code> / <code>TLS_KEY_FILE</code>.
        Unset those env vars to manage the certificate here.
      </p>
    {:else}
      {#if tls.configured}
        <p class="muted">
          Serving an uploaded certificate{#if tls.subject} for <strong>{tls.subject}</strong>{/if}{#if tls.not_after} — expires {new Date(tls.not_after).toLocaleDateString()}{/if}.
        </p>
      {/if}
      <label class="tls-field">
        <span>Certificate (PEM, full chain)</span>
        <textarea rows="5" bind:value={tlsCertPem} placeholder="-----BEGIN CERTIFICATE-----"></textarea>
      </label>
      <label class="tls-field">
        <span>Private key (PEM)</span>
        <textarea rows="5" bind:value={tlsKeyPem} placeholder="-----BEGIN PRIVATE KEY-----"></textarea>
      </label>
      <div class="tls-actions">
        <button class="btn-primary" disabled={tlsSaving || !tlsCertPem || !tlsKeyPem} on:click={saveTLS}>
          {tlsSaving ? 'Saving…' : 'Upload certificate'}
        </button>
        {#if tls.configured}
          <button class="btn-secondary" disabled={tlsSaving} on:click={() => { tlsCertPem = ''; tlsKeyPem = ''; saveTLS(); }}>
            Clear certificate
          </button>
        {/if}
      </div>
    {/if}
  </section>
</div>

<style>
  .security { max-width: 640px; }
  h1 { font-size: 1.25rem; font-weight: 700; color: var(--text-primary); margin: 0 0 1.25rem; }
  .card {
    background: var(--bg-elevated);
    border: 1px solid var(--border);
    border-radius: 10px;
    padding: 1.1rem 1.25rem;
  }
  .card-head { display: flex; justify-content: space-between; align-items: flex-start; gap: 1rem; }
  h2 { font-size: 1rem; font-weight: 600; color: var(--text-primary); margin: 0 0 0.3rem; }
  .sub { color: var(--text-muted); font-size: 0.82rem; margin: 0 0 1rem; line-height: 1.45; max-width: 46ch; }
  .badge { font-size: 0.7rem; font-weight: 700; padding: 0.2rem 0.5rem; border-radius: 999px; text-transform: uppercase; letter-spacing: 0.04em; white-space: nowrap; }
  .badge.on { background: var(--success-bg); color: var(--success); }
  .badge.off { background: var(--bg-hover); color: var(--text-muted); }
  .muted { color: var(--text-muted); font-size: 0.85rem; }
  .btn-primary { padding: 0.45rem 0.9rem; background: var(--accent); color: #fff; border: none; border-radius: 7px; font-size: 0.8rem; font-weight: 600; cursor: pointer; transition: background .15s, border-color .15s, color .15s; }
  .btn-primary:hover { background: var(--accent-hover); }
  .btn-primary:disabled { opacity: 0.5; cursor: not-allowed; }
  .btn-secondary { padding: 0.45rem 0.9rem; background: transparent; color: var(--text-secondary); border: 1px solid var(--border-strong); border-radius: 7px; font-size: 0.78rem; font-weight: 500; cursor: pointer; transition: background .15s, border-color .15s, color .15s; }
  .btn-secondary:hover { background: var(--bg-hover); border-color: var(--text-muted); }

  /* Spacing between sibling cards (added when TLS card landed alongside 2FA). */
  section.card + section.card { margin-top: 1rem; }

  /* TLS / HTTPS section. */
  .tls-field { display: block; margin-top: 0.7rem; }
  .tls-field > span { display: block; font-size: 0.78rem; color: var(--text-muted); margin-bottom: 0.3rem; }
  .tls-field textarea {
    width: 100%;
    font-family: ui-monospace, monospace;
    font-size: 0.75rem;
    padding: 0.48rem 0.7rem;
    background: var(--input-bg);
    border: 1px solid var(--border-strong);
    border-radius: 7px;
    color: var(--text-primary);
    resize: vertical;
    box-sizing: border-box;
    outline: none;
  }
  .tls-field textarea:focus { border-color: var(--accent); box-shadow: 0 0 0 3px var(--accent-bg); }
  .tls-actions { display: flex; gap: 0.5rem; margin-top: 0.8rem; }
  code { font-family: ui-monospace, monospace; font-size: 0.78rem; background: var(--bg-hover); padding: 0.05rem 0.3rem; border-radius: 3px; }
</style>
