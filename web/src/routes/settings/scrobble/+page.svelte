<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { scrobbleApi, type ScrobbleStatus, type LastFMLinkStart, type TraktLinkStart } from '$lib/api';
  import { toast } from '$lib/stores/toast';
  import { pollLink, type LinkOutcome } from '$lib/scrobbleLink';

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

  // Last.fm / Trakt. Both link through an approval on the service's own site;
  // while one is waiting its start response is held here and the page polls.
  let status: ScrobbleStatus | null = null;
  let lastfmStart: LastFMLinkStart | null = null;
  let traktStart: TraktLinkStart | null = null;
  let lastfmBusy = false;
  let traktBusy = false;
  let lastfmError = '';
  let traktError = '';
  let cancelLastFMPoll: (() => void) | null = null;
  let cancelTraktPoll: (() => void) | null = null;

  // Last.fm's request token lives an hour, but nobody takes that long to
  // click Allow; stop asking after ten minutes.
  const LASTFM_POLL_MS = 3000;
  const LASTFM_TIMEOUT_MS = 10 * 60 * 1000;

  onMount(loadStatus);
  onDestroy(() => {
    cancelLastFMPoll?.();
    cancelTraktPoll?.();
  });

  async function loadStatus() {
    error = '';
    try {
      const s = await scrobbleApi.status();
      status = s;
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

  function errMessage(e: unknown, fallback: string): string {
    return e instanceof Error ? e.message : fallback;
  }

  function outcomeMessage(service: string, outcome: LinkOutcome): string {
    return outcome === 'denied'
      ? `Access was declined on ${service}.`
      : `The ${service} approval wasn't finished in time. Connect again to retry.`;
  }

  async function connectLastFM() {
    lastfmBusy = true;
    lastfmError = '';
    try {
      const start = await scrobbleApi.startLastFM();
      lastfmStart = start;
      cancelLastFMPoll = pollLink({
        complete: () => scrobbleApi.completeLastFM(start.pending),
        intervalMs: LASTFM_POLL_MS,
        timeoutMs: LASTFM_TIMEOUT_MS,
        onDone: async (outcome, username) => {
          lastfmStart = null;
          if (outcome === 'linked') {
            toast.success(username ? `Last.fm linked as ${username}.` : 'Last.fm linked.');
            await loadStatus();
          } else {
            lastfmError = outcomeMessage('Last.fm', outcome);
          }
        },
        onError: (e) => {
          lastfmStart = null;
          lastfmError = errMessage(e, 'Could not link Last.fm.');
        },
      });
    } catch (e) {
      lastfmError = errMessage(e, 'Could not reach Last.fm.');
    } finally {
      lastfmBusy = false;
    }
  }

  function cancelLastFM() {
    cancelLastFMPoll?.();
    lastfmStart = null;
  }

  async function disconnectLastFM() {
    lastfmBusy = true;
    lastfmError = '';
    try {
      await scrobbleApi.unlinkLastFM();
      toast.success('Last.fm unlinked.');
      await loadStatus();
    } catch (e) {
      lastfmError = errMessage(e, 'Could not unlink Last.fm.');
    } finally {
      lastfmBusy = false;
    }
  }

  async function connectTrakt() {
    traktBusy = true;
    traktError = '';
    try {
      const start = await scrobbleApi.startTrakt();
      traktStart = start;
      cancelTraktPoll = pollLink({
        complete: () => scrobbleApi.completeTrakt(start.pending),
        intervalMs: start.interval * 1000,
        timeoutMs: start.expires_in * 1000,
        onDone: async (outcome, username) => {
          traktStart = null;
          if (outcome === 'linked') {
            toast.success(username ? `Trakt linked as ${username}.` : 'Trakt linked.');
            await loadStatus();
          } else {
            traktError = outcomeMessage('Trakt', outcome);
          }
        },
        onError: (e) => {
          traktStart = null;
          traktError = errMessage(e, 'Could not link Trakt.');
        },
      });
    } catch (e) {
      traktError = errMessage(e, 'Could not reach Trakt.');
    } finally {
      traktBusy = false;
    }
  }

  function cancelTrakt() {
    cancelTraktPoll?.();
    traktStart = null;
  }

  async function disconnectTrakt() {
    traktBusy = true;
    traktError = '';
    try {
      await scrobbleApi.unlinkTrakt();
      toast.success('Trakt unlinked.');
      await loadStatus();
    } catch (e) {
      traktError = errMessage(e, 'Could not unlink Trakt.');
    } finally {
      traktBusy = false;
    }
  }

  // "https://trakt.tv/activate" reads better as "trakt.tv/activate".
  function bareURL(u: string): string {
    return u.replace(/^https?:\/\//, '').replace(/\/$/, '');
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
        <span class="badge {enabled ? 'on' : 'paused'}">{enabled ? 'Linked' : 'Paused'}</span>
      {:else if view === 'unlinked'}
        <span class="badge off">Off</span>
      {/if}
    </div>

    {#if error}
      <div class="banner error">{error}</div>
    {/if}

    {#if view === 'loading'}
      <div class="skeleton-block"></div>

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

  <section class="card">
    <div class="card-head">
      <div>
        <h2>Last.fm</h2>
        <p class="sub">
          Finished tracks are scrobbled to your
          <a href="https://www.last.fm" target="_blank" rel="noopener noreferrer">Last.fm</a>
          profile — same rule as above, half the track or four minutes — and the
          track you're playing shows as scrobbling now.
        </p>
      </div>
      {#if status?.lastfm_linked}
        <span class="badge on">Linked</span>
      {:else if view !== 'loading'}
        <span class="badge off">Off</span>
      {/if}
    </div>

    {#if lastfmError}
      <div class="banner error">{lastfmError}</div>
    {/if}

    {#if view === 'loading'}
      <div class="skeleton-block"></div>
    {:else if status?.lastfm_linked}
      <p class="muted">
        Linked{#if status.lastfm_username}&nbsp;as <strong>{status.lastfm_username}</strong>{/if}.
      </p>
      <div class="row-actions">
        <button class="btn-danger" on:click={disconnectLastFM} disabled={lastfmBusy}>
          {lastfmBusy ? 'Disconnecting…' : 'Disconnect Last.fm'}
        </button>
      </div>
      <p class="hint">
        Disconnecting forgets the link here. To revoke OnScreen's access on Last.fm too,
        remove it under Settings › Applications on last.fm.
      </p>
    {:else if !status?.lastfm_available}
      <p class="muted">
        Last.fm isn't set up on this server yet: an admin adds a Last.fm API account in Settings.
      </p>
    {:else if lastfmStart}
      <ol class="steps">
        <li>
          <a href={lastfmStart.auth_url} target="_blank" rel="noopener noreferrer">Open Last.fm</a>
          and allow OnScreen access to your account.
        </li>
        <li>Come back here. This page finishes linking on its own.</li>
      </ol>
      <p class="waiting" role="status">Waiting for Last.fm…</p>
      <button class="link" on:click={cancelLastFM}>Cancel</button>
    {:else}
      <button class="btn-primary" on:click={connectLastFM} disabled={lastfmBusy}>
        {lastfmBusy ? 'Connecting…' : 'Connect Last.fm'}
      </button>
    {/if}
  </section>

  <section class="card">
    <div class="card-head">
      <div>
        <h2>Trakt</h2>
        <p class="sub">
          Movies and episodes you watch are scrobbled to your
          <a href="https://trakt.tv" target="_blank" rel="noopener noreferrer">Trakt</a>
          history: a play that reaches 80% counts as watched, and a paused one keeps
          its progress there. While something plays, your profile shows it. Marking a
          movie, episode, season or show watched adds it to your history too.
        </p>
      </div>
      {#if status?.trakt_linked}
        <span class="badge on">Linked</span>
      {:else if view !== 'loading'}
        <span class="badge off">Off</span>
      {/if}
    </div>

    {#if traktError}
      <div class="banner error">{traktError}</div>
    {/if}

    {#if view === 'loading'}
      <div class="skeleton-block"></div>
    {:else if status?.trakt_linked}
      <p class="muted">
        Linked{#if status.trakt_username}&nbsp;as <strong>{status.trakt_username}</strong>{/if}.
      </p>
      <div class="row-actions">
        <button class="btn-danger" on:click={disconnectTrakt} disabled={traktBusy}>
          {traktBusy ? 'Disconnecting…' : 'Disconnect Trakt'}
        </button>
      </div>
      <p class="hint">Disconnecting also revokes OnScreen's access on Trakt.</p>
    {:else if !status?.trakt_available}
      <p class="muted">
        Trakt isn't set up on this server yet: an admin adds a Trakt API app in Settings.
      </p>
    {:else if traktStart}
      <p class="muted">
        On any device, go to
        <a href={traktStart.verification_url} target="_blank" rel="noopener noreferrer">{bareURL(traktStart.verification_url)}</a>
        and enter this code:
      </p>
      <div class="user-code">{traktStart.user_code}</div>
      <p class="waiting" role="status">Waiting for Trakt…</p>
      <button class="link" on:click={cancelTrakt}>Cancel</button>
    {:else}
      <button class="btn-primary" on:click={connectTrakt} disabled={traktBusy}>
        {traktBusy ? 'Connecting…' : 'Connect Trakt'}
      </button>
    {/if}
  </section>
</div>

<style>
  .scrobble { max-width: 640px; display: flex; flex-direction: column; gap: 1rem; }
  h1 { font-size: 1.25rem; font-weight: 700; color: var(--text-primary); margin: 0 0 0.25rem; }
  .card {
    background: var(--bg-elevated);
    border: 1px solid var(--border);
    border-radius: 10px;
    padding: 1.25rem 1.4rem;
  }
  .card-head { display: flex; justify-content: space-between; align-items: flex-start; gap: 1rem; }
  h2 { font-size: 1rem; font-weight: 600; color: var(--text-primary); margin: 0 0 0.3rem; }
  .sub { color: var(--text-muted); font-size: 0.82rem; margin: 0 0 1rem; line-height: 1.45; max-width: 50ch; }
  .sub a { color: var(--accent-text); text-decoration: none; }
  .sub a:hover { text-decoration: underline; }
  .badge { font-size: 0.7rem; font-weight: 700; padding: 0.2rem 0.5rem; border-radius: 999px; text-transform: uppercase; letter-spacing: 0.04em; white-space: nowrap; }
  .badge.on { background: var(--success-bg); color: var(--success); }
  .badge.paused { background: rgba(251,191,36,0.12); color: #fcd34d; }
  .badge.off { background: var(--bg-hover); color: var(--text-muted); }
  .muted { color: var(--text-muted); font-size: 0.85rem; }
  .hint { color: var(--text-muted); font-size: 0.74rem; margin: 0.6rem 0 0; }
  .steps { margin: 0; padding-left: 1.2rem; color: var(--text-primary); font-size: 0.88rem; }
  .steps li { margin-bottom: 1rem; line-height: 1.5; }
  .steps a { color: var(--accent-text); text-decoration: none; }
  .steps a:hover { text-decoration: underline; }
  .code-row { display: flex; gap: 0.5rem; margin-top: 0.6rem; }
  .code-row input {
    flex: 1; padding: 0.6rem 0.7rem; background: var(--input-bg); border: 1px solid var(--border-strong);
    border-radius: 7px; color: var(--text-primary); font-size: 0.95rem; outline: none;
  }
  .code-row input:focus { border-color: var(--accent); box-shadow: 0 0 0 3px var(--accent-bg); }
  .row-actions { display: flex; gap: 0.5rem; flex-wrap: wrap; }
  .banner { border-radius: 7px; padding: 0.6rem 0.85rem; font-size: 0.82rem; margin-bottom: 1rem; line-height: 1.45; }
  .banner.error { background: var(--error-bg); color: var(--error); }
  .btn-primary { padding: 0.45rem 0.9rem; background: var(--accent); color: #fff; border: none; border-radius: 7px; font-size: 0.88rem; font-weight: 600; cursor: pointer; }
  .btn-primary:disabled { opacity: 0.5; cursor: not-allowed; }
  .btn-secondary { padding: 0.45rem 0.9rem; background: var(--bg-hover); color: var(--text-primary); border: 1px solid var(--border-strong); border-radius: 7px; font-size: 0.88rem; font-weight: 500; cursor: pointer; }
  .btn-secondary:disabled { opacity: 0.5; cursor: not-allowed; }
  .btn-danger { padding: 0.45rem 0.9rem; background: var(--error-bg); color: var(--error); border: 1px solid var(--error); border-radius: 7px; font-size: 0.88rem; font-weight: 600; cursor: pointer; }
  .btn-danger:disabled { opacity: 0.5; cursor: not-allowed; }
  .link { background: none; border: none; color: var(--text-muted); font-size: 0.78rem; cursor: pointer; margin-top: 0.75rem; padding: 0; }
  .link:hover { color: var(--text-primary); }
  .waiting { color: var(--text-muted); font-size: 0.82rem; margin: 0.75rem 0 0; }
  .user-code {
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 1.6rem; font-weight: 700;
    letter-spacing: 0.18em; color: var(--text-primary); background: var(--bg-hover);
    border: 1px solid var(--border-strong); border-radius: 8px; padding: 0.6rem 1rem;
    display: inline-block; margin: 0.25rem 0 0; user-select: all;
  }
  .muted a { color: var(--accent-text); text-decoration: none; }
  .muted a:hover { text-decoration: underline; }
  .skeleton-block {
    height: 120px; border-radius: 10px;
    background: linear-gradient(90deg, var(--bg-elevated) 25%, var(--bg-hover) 50%, var(--bg-elevated) 75%);
    background-size: 200% 100%; animation: shimmer 1.4s infinite;
  }
  @keyframes shimmer { 0% { background-position: 200% 0; } 100% { background-position: -200% 0; } }
</style>
