<script lang="ts">
  import { goto } from '$app/navigation';
  import { api, isCleartextRemote } from '$lib/api';
  import OnScreenKeyboard from '$lib/components/OnScreenKeyboard.svelte';

  // Empty by default: a bare host is tried over https:// first (see submit).
  let url = $state('');
  let error = $state('');
  let warning = $state('');
  let testing = $state(false);
  // Origin the user explicitly agreed to use over cleartext http:// to a
  // non-local host (pressing "done" a second time on the same URL).
  let confirmedInsecure = '';

  /** Probe a candidate origin; resolves to the origin to persist. */
  async function probe(candidate: string, timeoutMs = 0): Promise<string> {
    const controller = new AbortController();
    const timer = timeoutMs ? setTimeout(() => controller.abort(), timeoutMs) : 0;
    try {
      // Probe `/api/v1/system/capabilities` (public, chi-routed) instead of
      // `/health/live`: the health endpoint sits on the bare ServeMux ahead
      // of the chi router and bypasses the CORS middleware, so a cross-origin
      // fetch from the TV webview could not read its response.
      const resp = await fetch(`${candidate}/api/v1/system/capabilities`, {
        signal: controller.signal
      });
      if (!resp.ok) throw new Error(`server replied ${resp.status}`);
      // Persist the origin the server ANSWERED on, not the one we asked with.
      // fetch follows a cleartext→TLS 301 transparently, so this probe (a
      // safe GET, no credentials) succeeds against `http://host` — but
      // persisting that http origin breaks every later POST (login,
      // pair/code). Same-host https upgrades only; never a downgrade.
      try {
        const answered = new URL(resp.url);
        if (/^http:\/\//i.test(candidate) && answered.protocol === 'https:' &&
            new URL(candidate).hostname.toLowerCase() === answered.hostname.toLowerCase()) {
          return answered.origin;
        }
      } catch {
        // keep the typed origin
      }
      return candidate;
    } finally {
      if (timer) clearTimeout(timer);
    }
  }

  async function submit() {
    error = '';
    warning = '';
    const typed = url.trim().replace(/\/$/, '');
    if (!typed) return;

    testing = true;
    try {
      let origin: string;
      if (/^https?:\/\//i.test(typed)) {
        // Explicit scheme: use exactly that. An explicit https:// is never
        // retried over http://.
        origin = await probe(typed);
      } else {
        // No scheme: prefer TLS. Fall back to http:// only when https is
        // unreachable — and that fallback still goes through the cleartext
        // check below, so it can't silently land on a remote http host.
        try {
          origin = await probe('https://' + typed, 8000);
        } catch {
          origin = await probe('http://' + typed);
        }
      }
      // Plain http:// on the LAN is the common self-hosted setup and stays
      // allowed. Plain http:// to anything else would send the password,
      // refresh token and pairing token across the internet in cleartext —
      // require an explicit second confirmation.
      if (isCleartextRemote(origin) && confirmedInsecure !== origin) {
        confirmedInsecure = origin;
        warning =
          `${origin} is not encrypted and is not on your local network. Your password ` +
          'and sign-in tokens would be sent in plain text. Use an https:// address, or ' +
          'press done again to connect anyway.';
        return;
      }
      api.setOrigin(origin);
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
  <p class="hint">Enter the server address, e.g. <code>192.168.1.10:7070</code> or <code>https://media.example.com</code>. Without a scheme, https:// is tried first.</p>

  <OnScreenKeyboard bind:value={url} onchange={(v) => (url = v)} onsubmit={submit} layout="url" />

  {#if testing}
    <p class="status">Testing connection…</p>
  {:else if error}
    <p class="error">{error}</p>
  {:else if warning}
    <p class="warning">{warning}</p>
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

  .status, .error, .warning {
    font-size: var(--font-md);
    margin: 0;
  }

  .warning {
    color: #fcd34d;
  }

  .error {
    color: #fca5a5;
  }
</style>
