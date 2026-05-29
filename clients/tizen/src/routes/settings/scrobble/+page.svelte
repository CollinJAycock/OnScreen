<script lang="ts">
  // ListenBrainz scrobble link. Mirrors the web client's
  // /settings/scrobble flow within the TV focus model: a full-page
  // on-screen-keyboard token entry (same pattern as the login screen).
  // The listen-submit trigger is entirely server-side (fires on a
  // 'stopped' watch event past the listen threshold), so this page is
  // purely the per-user link toggle — link, replace, or unlink.

  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { endpoints } from '$lib/api';
  import { focusable } from '$lib/focus/focusable';
  import { focusManager } from '$lib/focus/manager';
  import OnScreenKeyboard from '$lib/components/OnScreenKeyboard.svelte';
  import TopNav from '$lib/components/TopNav.svelte';

  type View = 'loading' | 'unlinked' | 'linked';

  let view = $state<View>('loading');
  let enabled = $state(false);
  // When linked, the user can reveal the keyboard again to swap in a
  // fresh token. The token is write-only — we only ever hold what the
  // user just typed; the server never returns it.
  let replacing = $state(false);
  let token = $state('');
  let busy = $state(false);
  let error = $state('');

  onMount(() => {
    void loadStatus();
    // Back: step out of the replace-token keyboard first, otherwise
    // return to the settings page.
    return focusManager.pushBack(() => {
      if (replacing) {
        replacing = false;
        token = '';
        return true;
      }
      goto('#/settings');
      return true;
    });
  });

  async function loadStatus() {
    error = '';
    try {
      const s = await endpoints.scrobble.status();
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
      // Linking implies enabling — there's nothing to pause until a
      // token exists, and the server forces enabled=false on empty.
      await endpoints.scrobble.setListenBrainz(t, true);
      token = '';
      replacing = false;
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
      await endpoints.scrobble.setListenBrainz('', false);
      token = '';
      replacing = false;
      await loadStatus();
    } catch (e) {
      error = e instanceof Error ? e.message : 'Could not unlink account.';
    } finally {
      busy = false;
    }
  }

  const statusLabel = $derived(
    view === 'linked' ? (enabled ? 'Linked' : 'Paused') : 'Off'
  );
</script>

<div class="page">
  <TopNav />
  <h1>Scrobbling</h1>

  <div class="card">
    <div class="card-head">
      <div class="card-title">ListenBrainz</div>
      {#if view !== 'loading'}
        <span class="badge {view === 'linked' ? 'on' : 'off'}">{statusLabel}</span>
      {/if}
    </div>

    <p class="sub">
      When you finish a music track, OnScreen submits a listen to your
      ListenBrainz account. A play counts once you're past half the
      track, or four minutes — whichever comes first. One-way and
      best-effort: it never reads anything back and never affects
      playback.
    </p>

    {#if error}
      <div class="banner">{error}</div>
    {/if}

    {#if view === 'loading'}
      <p class="muted">Loading…</p>

    {:else if view === 'unlinked' || replacing}
      <ol class="steps">
        <li>
          On a phone or computer, open
          <span class="mono">listenbrainz.org/settings</span>
          and copy your <strong>user token</strong>.
        </li>
        <li>Enter it below and link.</li>
      </ol>

      <OnScreenKeyboard bind:value={token} onchange={(v) => (token = v)} onsubmit={link} />

      <div class="actions">
        <button use:focusable class="btn primary" onclick={link} disabled={busy || !token.trim()}>
          {busy ? 'Linking…' : 'Link account'}
        </button>
        {#if replacing}
          <button use:focusable class="btn" onclick={() => { replacing = false; token = ''; }} disabled={busy}>
            Cancel
          </button>
        {/if}
      </div>

    {:else if view === 'linked'}
      <p class="muted">
        {#if enabled}
          Your ListenBrainz account is linked and listens are being submitted.
        {:else}
          A token is linked but scrobbling is currently off. Replace the token to re-enable it.
        {/if}
      </p>

      <div class="actions">
        <button use:focusable={{ autofocus: true }} class="btn" onclick={() => { replacing = true; token = ''; }} disabled={busy}>
          Replace token
        </button>
        <button use:focusable class="btn danger" onclick={unlink} disabled={busy}>
          {busy ? 'Unlinking…' : 'Unlink account'}
        </button>
      </div>

      <p class="hint">Your token is stored encrypted and is never shown again. Unlinking removes it.</p>
    {/if}
  </div>
</div>

<style>
  .page {
    padding: 0 var(--page-pad);
  }
  h1 {
    font-size: var(--font-2xl);
    margin: 24px 0 32px;
  }

  .card {
    max-width: 980px;
    background: var(--bg-secondary, #1f1f24);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 28px 32px;
  }
  .card-head {
    display: flex;
    align-items: center;
    gap: 16px;
  }
  .card-title {
    font-size: var(--font-lg);
    color: var(--text-primary);
  }
  .badge {
    font-size: var(--font-sm);
    font-weight: 700;
    padding: 4px 12px;
    border-radius: 999px;
    text-transform: uppercase;
    letter-spacing: 0.05em;
  }
  .badge.on {
    background: rgba(52, 211, 153, 0.15);
    color: #34d399;
  }
  .badge.off {
    background: var(--bg-elevated, #2a2a32);
    color: var(--text-secondary);
  }

  .sub {
    color: var(--text-secondary);
    font-size: var(--font-md);
    line-height: 1.5;
    max-width: 70ch;
    margin: 16px 0 0;
  }
  .muted {
    color: var(--text-secondary);
    font-size: var(--font-md);
    margin: 20px 0 0;
  }
  .hint {
    color: var(--text-secondary);
    font-size: var(--font-sm);
    margin: 16px 0 0;
  }

  .steps {
    margin: 20px 0;
    padding-left: 24px;
    color: var(--text-primary);
    font-size: var(--font-md);
    line-height: 1.6;
  }
  .steps li {
    margin-bottom: 8px;
  }
  .mono {
    font-family: monospace;
    color: var(--text-primary);
  }
  strong {
    color: var(--text-primary);
  }

  .banner {
    margin: 16px 0 0;
    border-radius: 8px;
    padding: 12px 16px;
    font-size: var(--font-sm);
    line-height: 1.45;
    background: rgba(248, 113, 113, 0.12);
    color: #fca5a5;
  }

  .actions {
    display: flex;
    gap: 12px;
    flex-wrap: wrap;
    margin-top: 20px;
  }
  .btn {
    padding: 14px 28px;
    font-size: var(--font-sm);
    font-family: inherit;
    border-radius: 8px;
    cursor: pointer;
    background: var(--bg-elevated, #2a2a32);
    color: var(--text-primary);
    border: 2px solid transparent;
  }
  .btn:focus,
  .btn:focus-visible {
    border-color: var(--accent);
    outline: none;
  }
  .btn.primary:focus,
  .btn.primary:focus-visible {
    background: var(--accent);
    color: #fff;
  }
  .btn.danger {
    color: #fca5a5;
  }
  .btn.danger:focus,
  .btn.danger:focus-visible {
    border-color: #fca5a5;
  }
  .btn:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }
</style>
