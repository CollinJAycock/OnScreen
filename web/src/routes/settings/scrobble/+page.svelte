<script lang="ts">
  import { onMount } from 'svelte';
  import { scrobbleApi } from '$lib/api';
  import { toast } from '$lib/stores/toast';

  type View = 'loading' | 'unlinked' | 'linked';

  let view: View = 'loading';
  let enabled = false;
  let busy = false;
  let error = '';

  // Link form. The token is write-only — the server never returns it, so we
  // only ever hold what the user just typed.
  let token = '';
  // When linked, the user can reveal the field again to swap in a new token.
  let replacing = false;

  onMount(loadStatus);

  async function loadStatus() {
    error = '';
    try {
      const s = await scrobbleApi.status();
      enabled = s.listenbrainz_enabled;
      view = s.listenbrainz_linked ? 'linked' : 'unlinked';
    } catch (e) {
      error = e instanceof Error ? e.message : 'Could not load scrobble status.';
      view = 'unlinked';
    }
  }

  async function link() {
    const t = token.trim();
    if (!t) return;
    busy = true;
    error = '';
    try {
      // Linking implies enabling — there's nothing to pause until a token
      // exists, and the server forces enabled=false on an empty token.
      await scrobbleApi.setListenBrainz(t, true);
      token = '';
      replacing = false;
      toast.success('ListenBrainz account linked.');
      await loadStatus();
    } catch (e) {
      error = e instanceof Error ? e.message : 'Could not link account.';
    } finally {
      busy = false;
    }
  }

  async function unlink() {
    busy = true;
    error = '';
    try {
      // Empty token clears the link and forces enabled=false server-side.
      await scrobbleApi.setListenBrainz('', false);
      token = '';
      replacing = false;
      toast.success('ListenBrainz account unlinked.');
      await loadStatus();
    } catch (e) {
      error = e instanceof Error ? e.message : 'Could not unlink account.';
    } finally {
      busy = false;
    }
  }
</script>

<svelte:head><title>OnScreen — Scrobbling</title></svelte:head>

<div class="scrobble">
  <h1>Scrobbling</h1>
  <section class="card">
    <div class="card-head">
      <div>
        <h2>ListenBrainz</h2>
        <p class="sub">
          When you finish a music track, OnScreen submits a listen to your
          <a href="https://listenbrainz.org" target="_blank" rel="noopener noreferrer">ListenBrainz</a>
          account. A play counts once you're past half the track, or four
          minutes — whichever comes first. One-way and best-effort: it never
          reads anything back and never affects playback.
        </p>
      </div>
      {#if view === 'linked'}
        <span class="badge on">{enabled ? 'Linked' : 'Paused'}</span>
      {:else if view === 'unlinked'}
        <span class="badge off">Off</span>
      {/if}
    </div>

    {#if error}
      <div class="banner error">{error}</div>
    {/if}

    {#if view === 'loading'}
      <p class="muted">Loading…</p>

    {:else if view === 'unlinked' || replacing}
      <ol class="steps">
        <li>
          Open your
          <a href="https://listenbrainz.org/settings/" target="_blank" rel="noopener noreferrer">ListenBrainz settings</a>
          and copy your <strong>user token</strong>.
        </li>
        <li>
          Paste it here and link.
          <form on:submit|preventDefault={link} class="code-row">
            <input
              bind:value={token}
              type="password"
              autocomplete="off"
              spellcheck="false"
              placeholder="ListenBrainz user token"
            />
            <button class="btn-primary" type="submit" disabled={busy || !token.trim()}>
              {busy ? 'Linking…' : 'Link account'}
            </button>
          </form>
        </li>
      </ol>
      {#if replacing}
        <button class="link" on:click={() => { replacing = false; token = ''; }} disabled={busy}>Cancel</button>
      {/if}

    {:else if view === 'linked'}
      <p class="muted">
        {#if enabled}
          Your ListenBrainz account is linked and listens are being submitted.
        {:else}
          A token is linked but scrobbling is currently off. Replace the token to re-enable it.
        {/if}
      </p>
      <div class="row-actions">
        <button class="btn-secondary" on:click={() => { replacing = true; }} disabled={busy}>Replace token</button>
        <button class="btn-danger" on:click={unlink} disabled={busy}>
          {busy ? 'Unlinking…' : 'Unlink account'}
        </button>
      </div>
      <p class="hint">Your token is stored encrypted and is never shown again. Unlinking removes it.</p>
    {/if}
  </section>
</div>

<style>
  .scrobble { max-width: 640px; }
  h1 { font-size: 1.25rem; font-weight: 700; color: var(--text-primary); margin: 0 0 1.25rem; }
  .card {
    background: var(--bg-card, #0e0e18);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 1.25rem 1.4rem;
  }
  .card-head { display: flex; justify-content: space-between; align-items: flex-start; gap: 1rem; }
  h2 { font-size: 1rem; font-weight: 600; color: var(--text-primary); margin: 0 0 0.3rem; }
  .sub { color: var(--text-muted); font-size: 0.82rem; margin: 0 0 1rem; line-height: 1.45; max-width: 50ch; }
  .sub a { color: var(--accent-text); text-decoration: none; }
  .sub a:hover { text-decoration: underline; }
  .badge { font-size: 0.7rem; font-weight: 700; padding: 0.2rem 0.5rem; border-radius: 999px; text-transform: uppercase; letter-spacing: 0.04em; white-space: nowrap; }
  .badge.on { background: var(--success-bg); color: var(--success); }
  .badge.off { background: var(--bg-hover); color: var(--text-muted); }
  .muted { color: var(--text-muted); font-size: 0.85rem; }
  .hint { color: var(--text-muted); font-size: 0.74rem; margin: 0.6rem 0 0; }
  .steps { margin: 0; padding-left: 1.2rem; color: var(--text-primary); font-size: 0.88rem; }
  .steps li { margin-bottom: 1rem; line-height: 1.5; }
  .steps a { color: var(--accent-text); text-decoration: none; }
  .steps a:hover { text-decoration: underline; }
  .code-row { display: flex; gap: 0.5rem; margin-top: 0.6rem; }
  .code-row input {
    flex: 1; padding: 0.6rem 0.7rem; background: #111120; border: 1px solid var(--border);
    border-radius: 8px; color: var(--text-primary); font-size: 0.95rem; outline: none;
  }
  .code-row input:focus { border-color: rgba(124,106,247,0.5); box-shadow: 0 0 0 3px var(--accent-bg); }
  .row-actions { display: flex; gap: 0.5rem; flex-wrap: wrap; }
  .banner { border-radius: 8px; padding: 0.6rem 0.85rem; font-size: 0.82rem; margin-bottom: 1rem; line-height: 1.45; }
  .banner.error { background: var(--error-bg); color: var(--error); }
  .btn-primary { padding: 0.6rem 1rem; background: var(--accent); color: #fff; border: none; border-radius: 8px; font-size: 0.88rem; font-weight: 600; cursor: pointer; }
  .btn-primary:disabled { opacity: 0.5; cursor: not-allowed; }
  .btn-secondary { padding: 0.6rem 1rem; background: var(--bg-hover); color: var(--text-primary); border: 1px solid var(--border-strong); border-radius: 8px; font-size: 0.88rem; font-weight: 500; cursor: pointer; }
  .btn-secondary:disabled { opacity: 0.5; cursor: not-allowed; }
  .btn-danger { padding: 0.6rem 1rem; background: var(--error-bg); color: var(--error); border: 1px solid var(--error); border-radius: 8px; font-size: 0.88rem; font-weight: 600; cursor: pointer; }
  .btn-danger:disabled { opacity: 0.5; cursor: not-allowed; }
  .link { background: none; border: none; color: var(--text-muted); font-size: 0.78rem; cursor: pointer; margin-top: 0.75rem; padding: 0; }
  .link:hover { color: var(--text-primary); }
</style>
