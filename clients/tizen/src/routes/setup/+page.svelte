<script lang="ts">
  import { goto } from '$app/navigation';
  import { api } from '$lib/api';
  import OnScreenKeyboard from '$lib/components/OnScreenKeyboard.svelte';

  let url = $state('http://');
  let error = $state('');
  let testing = $state(false);

  async function submit() {
    error = '';
    let clean = url.trim();
    if (!clean) return;
    if (!clean.startsWith('http://') && !clean.startsWith('https://')) {
      clean = 'http://' + clean;
    }
    clean = clean.replace(/\/$/, '');

    testing = true;
    try {
      // `/health/live` bypasses every middleware (registered on the
      // bare http.ServeMux ahead of the chi router for safety) — so
      // a cross-origin fetch never gets a CORS header back even when
      // the server's allowlist is right. Probe a chi-routed endpoint
      // instead. See clients/webos/src/routes/setup/+page.svelte for
      // the full rationale.
      const resp = await fetch(`${clean}/api/v1/system/capabilities`);
      if (!resp.ok) throw new Error(`server replied ${resp.status}`);
      // Persist the origin the server ANSWERED on, not the one we asked with.
      // fetch follows a cleartext→TLS 301 transparently, so this probe (a
      // safe GET) succeeds against `http://host` — but persisting that http
      // origin breaks every later POST (login, pair/code): the redirect
      // rewrites POST to GET and the API 405s. Same-host https upgrades only.
      try {
        const answered = new URL(resp.url);
        if (clean.startsWith('http://') && answered.protocol === 'https:' &&
            new URL(clean).hostname.toLowerCase() === answered.hostname.toLowerCase()) {
          clean = answered.origin;
        }
      } catch {
        // keep the typed origin
      }
      api.setOrigin(clean);
      goto('#/login');
    } catch (e) {
      error = `Could not reach server: ${(e as Error).message}`;
    } finally {
      testing = false;
    }
  }
</script>

<div class="page">
  <h1>Add your OnScreen server</h1>
  <p class="hint">Enter the full URL, e.g. <code>http://192.168.1.10:7070</code></p>

  <OnScreenKeyboard bind:value={url} onchange={(v) => (url = v)} onsubmit={submit} layout="url" />

  {#if testing}
    <p class="status">Testing connection…</p>
  {:else if error}
    <p class="error">{error}</p>
  {/if}
</div>

<style>
  .page {
    padding: var(--page-pad);
    display: flex;
    flex-direction: column;
    gap: 32px;
  }

  h1 {
    font-size: var(--font-2xl);
    margin: 0;
  }

  .hint {
    font-size: var(--font-md);
    color: var(--text-secondary);
    margin: 0;
  }

  code {
    background: var(--bg-elevated);
    padding: 4px 12px;
    border-radius: 6px;
    font-family: ui-monospace, monospace;
  }

  .status, .error {
    font-size: var(--font-md);
    margin: 0;
  }

  .error {
    color: #fca5a5;
  }
</style>
