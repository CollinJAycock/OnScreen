<script lang="ts">
  // Scrobbling links: ListenBrainz (a user token typed on the on-screen
  // keyboard), plus Last.fm and Trakt on servers that have them (v2.5).
  // Mirrors the web client's /settings/scrobble within the TV focus model.
  // The scrobbles themselves are sent server-side from watch events, so
  // this page is purely the per-user link — link, replace, or unlink.
  //
  // Last.fm and Trakt approve OnScreen on their own sites, which a TV can't
  // open: the link dialog shows the address as a QR code (and as text) for
  // a phone, and polls the server until the approval lands.

  import { onMount, tick } from 'svelte';
  import { goto } from '$app/navigation';
  import {
    endpoints,
    Unauthorized,
    type LastFMLinkStart,
    type ScrobbleStatus,
    type TraktLinkStart
  } from '$lib/api';
  import { focusable, focusScope } from '$lib/focus/focusable';
  import { focusManager } from '$lib/focus/manager';
  import OnScreenKeyboard from '$lib/components/OnScreenKeyboard.svelte';
  import QrCode from '$lib/components/QrCode.svelte';
  import TopNav from '$lib/components/TopNav.svelte';
  import {
    LASTFM_POLL_MS,
    LASTFM_TIMEOUT_MS,
    bareURL,
    hasExtraScrobblers,
    outcomeMessage,
    pollLink,
    type LinkOutcome
  } from '$lib/scrobbleLink';

  type View = 'loading' | 'unlinked' | 'linked';
  type Service = 'lastfm' | 'trakt';

  let status = $state<ScrobbleStatus | null>(null);
  let view = $state<View>('loading');
  let enabled = $state(false);
  // ListenBrainz token entry. The token is write-only — we only ever hold
  // what the user just typed; the server never returns it. The keyboard
  // shows on demand (Enter token / Replace token) so the Last.fm and Trakt
  // cards stay a short D-pad hop away.
  let editing = $state(false);
  let token = $state('');
  let busy = $state(false);
  let error = $state('');
  let lbNote = $state('');

  // Last.fm / Trakt.
  let lastfmBusy = $state(false);
  let traktBusy = $state(false);
  let lastfmMsg = $state('');
  let traktMsg = $state('');
  let lastfmStart = $state<LastFMLinkStart | null>(null);
  let traktStart = $state<TraktLinkStart | null>(null);
  let cancelPoll: (() => void) | null = null;

  const extras = $derived(hasExtraScrobblers(status));
  const linkOpen = $derived(!!lastfmStart || !!traktStart);

  onMount(() => {
    void loadStatus();
    // Back: close the link dialog, then step out of the token keyboard,
    // otherwise return to the settings page.
    const offBack = focusManager.pushBack(() => {
      if (linkOpen) {
        void cancelLink();
        return true;
      }
      if (editing) {
        void stopEditing();
        return true;
      }
      goto('#/settings');
      return true;
    });
    return () => {
      offBack();
      cancelPoll?.();
      cancelPoll = null;
    };
  });

  async function loadStatus() {
    error = '';
    try {
      const s = await endpoints.scrobble.status();
      status = s;
      enabled = s.listenbrainz_enabled;
      view = s.listenbrainz_linked ? 'linked' : 'unlinked';
    } catch (e) {
      if (e instanceof Unauthorized) {
        goto('#/login');
        return;
      }
      error = e instanceof Error ? e.message : 'Could not load scrobble status.';
      view = 'unlinked';
    }
  }

  function errMessage(e: unknown, fallback: string): string {
    return e instanceof Error && e.message ? e.message : fallback;
  }

  async function focusIn(selector: string) {
    await tick();
    const el = document.querySelector<HTMLElement>(`${selector} [data-focusable]`);
    if (el) focusManager.focus(el);
  }

  // ── ListenBrainz ──

  async function startEditing() {
    editing = true;
    token = '';
    lbNote = '';
    await focusIn('[data-service="listenbrainz"] .osk');
  }

  async function stopEditing() {
    editing = false;
    token = '';
    await focusIn('[data-service="listenbrainz"] .actions');
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
      editing = false;
      lbNote = 'ListenBrainz account linked.';
      await loadStatus();
      await focusIn('[data-service="listenbrainz"] .actions');
    } catch (e) {
      error = errMessage(e, 'Could not link account.');
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
      editing = false;
      lbNote = 'ListenBrainz account unlinked.';
      await loadStatus();
      await focusIn('[data-service="listenbrainz"] .actions');
    } catch (e) {
      error = errMessage(e, 'Could not unlink account.');
    } finally {
      busy = false;
    }
  }

  const statusLabel = $derived(
    view === 'linked' ? (enabled ? 'Linked' : 'Paused') : 'Off'
  );

  // ── Last.fm / Trakt ──

  function serviceName(s: Service): string {
    return s === 'lastfm' ? 'Last.fm' : 'Trakt';
  }

  function setMsg(s: Service, text: string) {
    if (s === 'lastfm') lastfmMsg = text;
    else traktMsg = text;
  }

  async function finishLink(s: Service, outcome: LinkOutcome, username?: string) {
    cancelPoll = null;
    lastfmStart = null;
    traktStart = null;
    const name = serviceName(s);
    if (outcome === 'linked') {
      setMsg(s, username ? `${name} linked as ${username}.` : `${name} linked.`);
      await loadStatus();
    } else {
      setMsg(s, outcomeMessage(name, outcome));
    }
    await focusIn(`[data-service="${s}"] .actions`);
  }

  async function failLink(s: Service, e: unknown) {
    cancelPoll = null;
    lastfmStart = null;
    traktStart = null;
    setMsg(s, errMessage(e, `Could not link ${serviceName(s)}.`));
    await focusIn(`[data-service="${s}"] .actions`);
  }

  async function connectLastFM() {
    lastfmBusy = true;
    lastfmMsg = '';
    try {
      const start = await endpoints.scrobble.startLastFM();
      lastfmStart = start;
      cancelPoll = pollLink({
        complete: () => endpoints.scrobble.completeLastFM(start.pending),
        intervalMs: LASTFM_POLL_MS,
        timeoutMs: LASTFM_TIMEOUT_MS,
        onDone: (outcome, username) => void finishLink('lastfm', outcome, username),
        onError: (e) => void failLink('lastfm', e),
      });
      await focusIn('.link-dialog');
    } catch (e) {
      lastfmMsg = errMessage(e, 'Could not reach Last.fm.');
    } finally {
      lastfmBusy = false;
    }
  }

  async function connectTrakt() {
    traktBusy = true;
    traktMsg = '';
    try {
      const start = await endpoints.scrobble.startTrakt();
      traktStart = start;
      cancelPoll = pollLink({
        complete: () => endpoints.scrobble.completeTrakt(start.pending),
        intervalMs: Math.max(1, start.interval) * 1000,
        timeoutMs: Math.max(1, start.expires_in) * 1000,
        onDone: (outcome, username) => void finishLink('trakt', outcome, username),
        onError: (e) => void failLink('trakt', e),
      });
      await focusIn('.link-dialog');
    } catch (e) {
      traktMsg = errMessage(e, 'Could not reach Trakt.');
    } finally {
      traktBusy = false;
    }
  }

  async function cancelLink() {
    const s: Service = lastfmStart ? 'lastfm' : 'trakt';
    cancelPoll?.();
    cancelPoll = null;
    lastfmStart = null;
    traktStart = null;
    await focusIn(`[data-service="${s}"] .actions`);
  }

  async function disconnect(s: Service) {
    if (s === 'lastfm') lastfmBusy = true;
    else traktBusy = true;
    setMsg(s, '');
    try {
      if (s === 'lastfm') await endpoints.scrobble.unlinkLastFM();
      else await endpoints.scrobble.unlinkTrakt();
      setMsg(s, `${serviceName(s)} unlinked.`);
      await loadStatus();
      await focusIn(`[data-service="${s}"] .actions`);
    } catch (e) {
      setMsg(s, errMessage(e, `Could not unlink ${serviceName(s)}.`));
    } finally {
      if (s === 'lastfm') lastfmBusy = false;
      else traktBusy = false;
    }
  }
</script>

<div class="page">
  <TopNav />
  <h1>Scrobbling</h1>

  <div class="card" data-service="listenbrainz">
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

    {:else if editing}
      <ol class="steps">
        <li>
          On a phone or computer, open
          <span class="mono">listenbrainz.org/settings</span>
          and copy your <strong>user token</strong>.
        </li>
        <li>Enter it below and link.</li>
      </ol>

      <!-- Masked: the token is a credential for the user's ListenBrainz
           account (Android masks it too). -->
      <OnScreenKeyboard bind:value={token} onchange={(v) => (token = v)} onsubmit={link} masked />

      <div class="actions">
        <button use:focusable class="btn primary" onclick={link} disabled={busy || !token.trim()}>
          {busy ? 'Linking…' : 'Link account'}
        </button>
        <button use:focusable class="btn" onclick={() => void stopEditing()} disabled={busy}>
          Cancel
        </button>
      </div>

    {:else if view === 'unlinked'}
      <div class="actions">
        <button use:focusable={{ autofocus: true }} class="btn primary" onclick={() => void startEditing()}>
          Enter token
        </button>
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
        <button use:focusable={{ autofocus: true }} class="btn" onclick={() => void startEditing()} disabled={busy}>
          Replace token
        </button>
        <button use:focusable class="btn danger" onclick={unlink} disabled={busy}>
          {busy ? 'Unlinking…' : 'Unlink account'}
        </button>
      </div>

      <p class="hint">Your token is stored encrypted and is never shown again. Unlinking removes it.</p>
    {/if}
    {#if lbNote && !editing}<p class="note" role="status">{lbNote}</p>{/if}
  </div>

  {#if status && extras}
    <div class="card" data-service="lastfm">
      <div class="card-head">
        <div class="card-title">Last.fm</div>
        <span class="badge {status.lastfm_linked ? 'on' : 'off'}">{status.lastfm_linked ? 'Linked' : 'Off'}</span>
      </div>
      <p class="sub">
        Finished tracks are scrobbled to your Last.fm profile — same rule as
        above, half the track or four minutes — and the track you're playing
        shows as scrobbling now.
      </p>
      {#if status.lastfm_linked}
        <p class="muted">
          Linked{#if status.lastfm_username}&nbsp;as <strong>{status.lastfm_username}</strong>{/if}.
        </p>
        <div class="actions">
          <button use:focusable class="btn danger" onclick={() => void disconnect('lastfm')} disabled={lastfmBusy}>
            {lastfmBusy ? 'Disconnecting…' : 'Disconnect Last.fm'}
          </button>
        </div>
        <p class="hint">
          Disconnecting forgets the link here. To revoke OnScreen's access on
          Last.fm too, remove it under Settings › Applications on last.fm.
        </p>
      {:else if !status.lastfm_available}
        <p class="muted">Last.fm isn't set up on this server. An admin adds a Last.fm API account in the web app's Settings.</p>
      {:else}
        <div class="actions">
          <button use:focusable class="btn primary" onclick={() => void connectLastFM()} disabled={lastfmBusy}>
            {lastfmBusy ? 'Connecting…' : 'Connect Last.fm'}
          </button>
        </div>
      {/if}
      {#if lastfmMsg}<p class="note" role="status">{lastfmMsg}</p>{/if}
    </div>

    <div class="card" data-service="trakt">
      <div class="card-head">
        <div class="card-title">Trakt</div>
        <span class="badge {status.trakt_linked ? 'on' : 'off'}">{status.trakt_linked ? 'Linked' : 'Off'}</span>
      </div>
      <p class="sub">
        Movies and episodes you watch are scrobbled to your Trakt history: a
        play that reaches 80% counts as watched, and a paused one keeps its
        progress there. While something plays, your profile shows it.
      </p>
      {#if status.trakt_linked}
        <p class="muted">
          Linked{#if status.trakt_username}&nbsp;as <strong>{status.trakt_username}</strong>{/if}.
        </p>
        <div class="actions">
          <button use:focusable class="btn danger" onclick={() => void disconnect('trakt')} disabled={traktBusy}>
            {traktBusy ? 'Disconnecting…' : 'Disconnect Trakt'}
          </button>
        </div>
        <p class="hint">Disconnecting also revokes OnScreen's access on Trakt.</p>
      {:else if !status.trakt_available}
        <p class="muted">Trakt isn't set up on this server. An admin adds a Trakt API app in the web app's Settings.</p>
      {:else}
        <div class="actions">
          <button use:focusable class="btn primary" onclick={() => void connectTrakt()} disabled={traktBusy}>
            {traktBusy ? 'Connecting…' : 'Connect Trakt'}
          </button>
        </div>
      {/if}
      {#if traktMsg}<p class="note" role="status">{traktMsg}</p>{/if}
    </div>
  {/if}
</div>

{#if traktStart || lastfmStart}
  <!-- use:focusScope keeps the D-pad on Cancel while the page polls. -->
  <div class="modal-backdrop link-dialog" role="dialog" aria-modal="true" aria-labelledby="link-title" use:focusScope>
    <div class="modal">
      {#if traktStart}
        <div class="modal-title" id="link-title">Connect Trakt</div>
        <div class="link-body">
          <QrCode text={traktStart.verification_url} size={300} label="Trakt activation page" />
          <div class="link-text">
            <p>Scan the code, or on any phone or computer go to</p>
            <p class="url">{bareURL(traktStart.verification_url)}</p>
            <p>and enter this code:</p>
            <div class="user-code">{traktStart.user_code}</div>
          </div>
        </div>
        <p class="waiting" role="status">Waiting for Trakt…</p>
      {:else if lastfmStart}
        <div class="modal-title" id="link-title">Connect Last.fm</div>
        <div class="link-body">
          <QrCode text={lastfmStart.auth_url} size={340} label="Last.fm approval page" />
          <div class="link-text">
            <p>Scan the code with your phone to open Last.fm, then allow OnScreen access to your account.</p>
            <p class="small">Or open this address on a phone or computer:</p>
            <p class="url long">{lastfmStart.auth_url}</p>
          </div>
        </div>
        <p class="waiting" role="status">Waiting for Last.fm… This finishes on its own once you allow access.</p>
      {/if}
      <div class="modal-actions">
        <button use:focusable={{ autofocus: true }} class="btn" onclick={() => void cancelLink()}>Cancel</button>
      </div>
    </div>
  </div>
{/if}

<style>
  .page {
    padding: 0 var(--page-pad) var(--page-pad);
  }
  h1 {
    font-size: var(--font-2xl);
    margin: 24px 0 32px;
  }

  .card {
    max-width: 1100px;
    background: var(--bg-secondary, #1f1f24);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 24px 32px;
    margin-bottom: 24px;
  }
  /* Spacing here uses margins and spelled-out offsets, not flexbox `gap`
     (Chrome 84) or `inset` (Chrome 87): webOS 6 runs Chromium 79. */
  .card-head {
    display: flex;
    align-items: center;
  }
  /* :global() on inner parts throughout: Svelte 5 would scope them as
     :where(), which Chromium 79 doesn't know, and drop the rule there. */
  .card-head > :global(.badge) {
    margin-left: 16px;
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
    font-size: var(--font-sm);
    line-height: 1.5;
    max-width: 80ch;
    margin: 12px 0 0;
  }
  .muted {
    color: var(--text-secondary);
    font-size: var(--font-md);
    margin: 16px 0 0;
  }
  .hint {
    color: var(--text-secondary);
    font-size: var(--font-xs);
    margin: 12px 0 0;
  }
  .note {
    color: var(--text-primary);
    font-size: var(--font-sm);
    margin: 12px 0 0;
  }

  .steps {
    margin: 20px 0;
    padding-left: 24px;
    color: var(--text-primary);
    font-size: var(--font-md);
    line-height: 1.6;
  }
  .steps > :global(li) {
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

  /* Wrapping row: each button carries a top + right margin and the row
     pulls itself up and right by the same, like a flex `gap: 12px`. */
  .actions {
    display: flex;
    flex-wrap: wrap;
    margin: 8px -12px 0 0;
  }
  .actions > :global(*) {
    margin: 12px 12px 0 0;
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
  .btn:focus {
    border-color: var(--accent);
    outline: none;
  }
  .btn.primary {
    background: var(--accent);
    color: #fff;
  }
  .btn.danger {
    color: #fca5a5;
  }
  .btn.danger:focus {
    border-color: #fca5a5;
  }
  .btn:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  .modal-backdrop {
    position: fixed;
    top: 0;
    right: 0;
    bottom: 0;
    left: 0;
    background: rgba(0, 0, 0, 0.78);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 100;
  }
  .modal {
    background: var(--bg-secondary, #1f1f24);
    border: 2px solid var(--border-strong);
    border-radius: 16px;
    padding: 40px 48px;
    width: 1240px;
    max-width: 94vw;
    box-shadow: 0 12px 48px rgba(0, 0, 0, 0.6);
  }
  .modal-title {
    font-size: var(--font-xl);
    margin-bottom: 28px;
  }
  .link-body {
    display: flex;
    align-items: center;
  }
  .link-text {
    margin-left: 48px;
    flex: 1;
    min-width: 0;
    font-size: var(--font-md);
    line-height: 1.45;
  }
  .link-text > :global(p) {
    margin: 0 0 12px;
  }
  .link-text > :global(.small) {
    font-size: var(--font-sm);
    color: var(--text-secondary);
    margin-top: 20px;
  }
  .url {
    font-family: monospace;
    color: var(--accent-hover);
    font-size: var(--font-lg);
  }
  .url.long {
    font-size: var(--font-xs);
    word-break: break-all;
    line-height: 1.4;
  }
  .user-code {
    display: inline-block;
    margin-top: 8px;
    padding: 16px 32px;
    border-radius: 12px;
    background: var(--bg-primary);
    border: 2px solid var(--border-strong);
    font-family: monospace;
    font-size: var(--font-2xl);
    letter-spacing: 0.15em;
    color: var(--text-primary);
  }
  .waiting {
    margin: 28px 0 0;
    font-size: var(--font-sm);
    color: var(--text-secondary);
  }
  .modal-actions {
    display: flex;
    justify-content: flex-end;
    margin-top: 20px;
  }
</style>
