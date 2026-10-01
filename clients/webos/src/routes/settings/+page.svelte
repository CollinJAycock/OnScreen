<script lang="ts">
  // Account / app settings. Two destructive actions:
  //   - Sign out: clears tokens, keeps the server URL so the user
  //     just re-enters their password.
  //   - Forget server: clears everything (origin + tokens + user)
  //     and routes back through the URL prompt — used when switching
  //     to a different OnScreen deployment.
  // Both confirm before firing because the remote's OK button is
  // easy to mash from across the room.

  import { onMount, tick } from 'svelte';
  import { goto } from '$app/navigation';
  import { api, endpoints, Unauthorized } from '$lib/api';
  import { focusable, focusScope } from '$lib/focus/focusable';
  import { focusManager } from '$lib/focus/manager';
  import { focusFirstOf } from '$lib/focus/memory';
  import TopNav from '$lib/components/TopNav.svelte';
  import { APP_VERSION } from '$lib/version';
  import { hasExtraScrobblers, scrobbleSummary } from '$lib/scrobbleLink';
  import {
    LANGUAGE_OPTIONS,
    SUBTITLE_LANGUAGE_OPTIONS,
    langLabel,
    languageSaver,
    nextLangValue,
    qualityProfileBody,
  } from '$lib/settingsPrefs';
  import type { ScrobbleStatus } from '$lib/api';
  import type { UserPreferences } from '$lib/api/endpoints';

  const username = $derived(api.getUser()?.username ?? '');
  const serverUrl = $derived(api.getOrigin() ?? '');

  type Confirm = 'signOut' | 'forgetServer';
  let confirming = $state<Confirm | null>(null);

  // A confirm closed by Cancel or Back puts focus back on the row that
  // opened it (data-confirm), as the home screen's Change server does. The
  // dialog's buttons go with it, and focus left on nothing spent the next
  // press on the focus manager finding something (the Settings pill, the
  // page scrolled to the top) and made OK only bring the ring up.
  async function closeConfirm() {
    const which = confirming;
    if (!which) return;
    confirming = null;
    await tick();
    focusFirstOf(`.action-row[data-confirm="${which}"]`);
  }

  // Sign out / Forget server confirmed and under way. The revoke can take
  // the request timeout (20 s) on a server that doesn't answer, so the
  // dialog stays up saying "Signing out…" with focus on its button (both
  // disabled), and every key is swallowed until it's done: Back would only
  // close the dialog over a sign-out still running, and a second OK would
  // start a second one.
  let signingOut = $state(false);

  $effect(() => {
    if (!signingOut) return;
    return focusManager.pushKeyHandler(() => true);
  });

  // Playback preferences — server-side per-user defaults. The language
  // choices (639-2/T codes, as Android writes them) and their matching live
  // in lib/settingsPrefs.
  let audioLang = $state('');
  let subLang = $state('');
  let forcedOnly = $state(false);
  // Admin-set parental ceiling: shown, never edited here (the preferences
  // PUT doesn't take it). null = no limit.
  let maxRating = $state<string | null>(null);
  let prefsLoaded = $state(false);
  let prefsSaving = $state(false);
  let forcedSaving = $state(false);
  // One error line per save path. Each stays up until a save on that path
  // succeeds, so a press that starts the next save doesn't wipe the reason
  // the previous one failed.
  let langError = $state('');
  let forcedError = $state('');

  // Scrobble link status (ListenBrainz, plus Last.fm + Trakt on v2.5
  // servers) — surfaced as a row that opens the dedicated
  // /settings/scrobble link flow.
  let scrobbleStatus = $state<ScrobbleStatus | null>(null);
  const scrobbleBadge = $derived(scrobbleSummary(scrobbleStatus));
  const scrobbleExtras = $derived(hasExtraScrobblers(scrobbleStatus));

  function applyPrefs(p: UserPreferences) {
    audioLang = p.preferred_audio_lang ?? '';
    subLang = p.preferred_subtitle_lang ?? '';
    forcedOnly = !!p.forced_subtitles_only;
    maxRating = p.max_content_rating ?? null;
  }

  onMount(() => {
    (async () => {
      try {
        applyPrefs(await endpoints.users.preferences());
      } catch (e) {
        if (e instanceof Unauthorized) goto('#/login');
        // Otherwise best-effort: the rows show their defaults.
      } finally {
        prefsLoaded = true;
      }
    })();

    (async () => {
      try {
        scrobbleStatus = await endpoints.scrobble.status();
      } catch {
        // Best-effort — the row falls back to "Off" if status can't load.
      }
    })();

    return focusManager.pushBack(() => {
      if (confirming) {
        void closeConfirm();
        return true;
      }
      goto('#/hub');
      return true;
    });
  });

  // Saves the two languages (the endpoint answers 204 with no body, and
  // writes both, a null clearing one). Presses while a save is in flight
  // fold into one more save of the latest values, so a quick run through
  // the options can't land out of order. A failure re-reads what the server
  // kept, unless another press is already on its way (see languageSaver).
  const persist = languageSaver({
    current: () => ({ audio: audioLang, sub: subLang }),
    put: (langs) =>
      endpoints.users.setPreferences({
        preferred_audio_lang: langs.audio || null,
        preferred_subtitle_lang: langs.sub || null,
      }),
    read: async () => {
      const p = await endpoints.users.preferences();
      return { audio: p.preferred_audio_lang ?? '', sub: p.preferred_subtitle_lang ?? '' };
    },
    show: (langs) => {
      audioLang = langs.audio;
      subLang = langs.sub;
    },
    saving: (on) => (prefsSaving = on),
    error: (text) => (langError = text),
    fatal: (e) => {
      if (!(e instanceof Unauthorized)) return false;
      goto('#/login');
      return true;
    },
  });

  function cycleAudio() {
    audioLang = nextLangValue(LANGUAGE_OPTIONS, audioLang);
    void persist();
  }
  function cycleSub() {
    subLang = nextLangValue(SUBTITLE_LANGUAGE_OPTIONS, subLang);
    void persist();
  }

  // Forced-only lives in the quality profile, not the preferences (whose PUT
  // ignores it, which is why this toggle used to snap back on reload). That
  // PUT replaces the whole profile, so the other caps are echoed from a read
  // made right before it rather than from when the page opened.
  async function toggleForced() {
    if (forcedSaving) return;
    const want = !forcedOnly;
    forcedOnly = want;
    forcedSaving = true;
    try {
      const fresh = await endpoints.users.preferences();
      maxRating = fresh.max_content_rating ?? null;
      await endpoints.users.setQualityProfile(qualityProfileBody(fresh, want));
      forcedError = '';
    } catch (e) {
      forcedOnly = !want;
      if (e instanceof Unauthorized) {
        goto('#/login');
        return;
      }
      forcedError = "Couldn't save the subtitle setting.";
    } finally {
      forcedSaving = false;
    }
  }

  // The About section, read-only below the last row (see revealAbout).
  let aboutEl: HTMLElement | undefined = $state();

  // Runs once the focus manager has started its own scroll to the row (the
  // focusable's onFocus comes after it); this later one takes over and
  // brings the page's end into view, the row still on screen above it.
  function revealAbout() {
    aboutEl?.scrollIntoView({ block: 'end', behavior: 'smooth' });
  }

  const audioLabel = $derived(langLabel(LANGUAGE_OPTIONS, audioLang));
  const subLabel = $derived(langLabel(SUBTITLE_LANGUAGE_OPTIONS, subLang));

  async function doSignOut() {
    if (signingOut) return;
    signingOut = true;
    // Best-effort — the network call invalidates the refresh token
    // server-side, but `clearTokens()` runs in `finally` so the local
    // session ends cleanly even if the request fails.
    await api.logout();
    goto('#/login');
  }

  async function doForgetServer() {
    if (signingOut) return;
    signingOut = true;
    // Signs out (tokens cleared) and removes the origin, so the next launch
    // starts at /setup again. Shared with the home screen's Change server.
    await api.forgetServer();
    goto('#/setup');
  }
</script>

<div class="page">
  <TopNav />
  <h1>Settings</h1>

  <section>
    <div class="section-title">Account</div>
    {#if username || serverUrl}
      <div class="identity">
        {#if username}
          <div class="identity-line">Signed in as <strong>{username}</strong></div>
        {/if}
        {#if serverUrl}
          <div class="identity-server">{serverUrl}</div>
          {#if serverUrl.toLowerCase().startsWith('http://')}
            <!-- Persistent reminder that this session (password, refresh
                 token) travels in cleartext; fine on a trusted LAN only. -->
            <div class="identity-insecure">Not encrypted (http://) — use https:// outside your home network</div>
          {/if}
        {/if}
      </div>
    {/if}

    <button
      use:focusable={{ autofocus: true }}
      class="action-row"
      data-confirm="signOut"
      onclick={() => (confirming = 'signOut')}
    >
      <div class="action-title">Sign out</div>
      <div class="action-desc">
        Clear your session on this TV. The server URL is kept so you can sign in again with the same account.
      </div>
    </button>

    <button
      use:focusable
      class="action-row"
      data-confirm="forgetServer"
      onclick={() => (confirming = 'forgetServer')}
    >
      <div class="action-title">Forget server</div>
      <div class="action-desc">
        Remove the server URL and all session state. Use when switching to a different OnScreen deployment.
      </div>
    </button>
  </section>

  <section>
    <div class="section-title">Playback</div>
    {#if !prefsLoaded}
      <div class="action-desc">Loading preferences…</div>
    {:else}
      <button use:focusable class="action-row" onclick={cycleAudio}>
        <div class="action-title">Audio language</div>
        <div class="action-desc">
          {audioLabel}{prefsSaving ? ' · saving…' : ''}
        </div>
      </button>
      <button use:focusable class="action-row" onclick={cycleSub}>
        <div class="action-title">Subtitle language</div>
        <div class="action-desc">{subLabel}</div>
      </button>
      <button use:focusable class="action-row" onclick={() => void toggleForced()}>
        <div class="action-title">Forced subtitles only</div>
        <div class="action-desc">
          {forcedOnly ? 'On — only show subtitles flagged as forced' : 'Off — show full subtitle tracks'}{forcedSaving ? ' · saving…' : ''}
        </div>
      </button>
      {#if langError}<div class="prefs-error" role="status">{langError}</div>{/if}
      {#if forcedError}<div class="prefs-error" role="status">{forcedError}</div>{/if}
      <!-- Read-only: the ceiling is the administrator's parental control
           (Android shows it the same way). -->
      <div class="info-row indented">
        <div class="info-label">Max content rating</div>
        <div class="info-value">
          {maxRating ?? 'No limit'}<span class="info-note">Managed by the server administrator</span>
        </div>
      </div>
    {/if}
  </section>

  <section>
    <div class="section-title">Scrobbling</div>
    <!-- The last row: its focus scrolls the page to its end, or About (the
         app version, the account, the server) would never come into view;
         the page only scrolls as focus moves, and nothing below is
         focusable. -->
    <button
      use:focusable={{ onFocus: revealAbout }}
      class="action-row"
      onclick={() => goto('#/settings/scrobble')}
    >
      <div class="action-title">
        {scrobbleExtras ? 'ListenBrainz, Last.fm and Trakt' : 'ListenBrainz'}
        <span class="badge {scrobbleBadge.on ? 'on' : 'off'}">{scrobbleBadge.label}</span>
      </div>
      <div class="action-desc">
        {#if scrobbleExtras}
          Send the music you play to ListenBrainz or Last.fm, and the movies and episodes you watch to Trakt.
        {:else}
          Submit a listen to ListenBrainz when you finish a music track.
        {/if}
      </div>
    </button>
  </section>

  <section bind:this={aboutEl}>
    <div class="section-title">About</div>
    <div class="info-row">
      <div class="info-label">Version</div>
      <div class="info-value">{APP_VERSION}</div>
    </div>
    {#if username}
      <div class="info-row">
        <div class="info-label">Signed in as</div>
        <div class="info-value">{username}</div>
      </div>
    {/if}
    {#if serverUrl}
      <div class="info-row">
        <div class="info-label">Server</div>
        <div class="info-value mono">{serverUrl}</div>
      </div>
    {/if}
  </section>
</div>

<!-- use:focusScope confines D-pad navigation to the modal — without it
     the focus manager's spatial search still considers the action rows
     behind the scrim, so a stray press could fire a hidden destructive
     control. -->
{#if confirming === 'signOut'}
  <div class="modal-backdrop" role="dialog" aria-modal="true" aria-labelledby="confirm-title" use:focusScope>
    <div class="modal">
      <div class="modal-title" id="confirm-title">Sign out?</div>
      <div class="modal-body">
        You'll need to sign in again to continue using OnScreen on this TV.
      </div>
      <div class="modal-actions">
        <button
          use:focusable
          class="btn-confirm"
          disabled={signingOut}
          onclick={doSignOut}
        >
          {signingOut ? 'Signing out…' : 'Sign out'}
        </button>
        <button
          use:focusable={{ autofocus: true }}
          class="btn-cancel"
          disabled={signingOut}
          onclick={() => void closeConfirm()}
        >
          Cancel
        </button>
      </div>
    </div>
  </div>
{:else if confirming === 'forgetServer'}
  <div class="modal-backdrop" role="dialog" aria-modal="true" aria-labelledby="confirm-title" use:focusScope>
    <div class="modal">
      <div class="modal-title" id="confirm-title">Forget server?</div>
      <div class="modal-body">
        This removes the server URL and all session state. You'll start over from the server-URL prompt.
      </div>
      <div class="modal-actions">
        <button
          use:focusable
          class="btn-confirm"
          disabled={signingOut}
          onclick={doForgetServer}
        >
          {signingOut ? 'Signing out…' : 'Forget'}
        </button>
        <button
          use:focusable={{ autofocus: true }}
          class="btn-cancel"
          disabled={signingOut}
          onclick={() => void closeConfirm()}
        >
          Cancel
        </button>
      </div>
    </div>
  </div>
{/if}

<style>
  /* Bottom padding: room under About for the scroll box's bottom
     scroll-padding (app.css), which a scroll to the page's end aims for. */
  .page {
    padding: 0 var(--page-pad) var(--page-pad);
  }
  h1 {
    font-size: var(--font-2xl);
    margin: 24px 0 32px;
  }

  section {
    max-width: 1200px;
    margin: 0 0 48px;
  }
  .section-title {
    font-size: var(--font-sm);
    color: var(--accent);
    text-transform: uppercase;
    letter-spacing: 0.15em;
    margin-bottom: 16px;
  }

  .identity {
    margin-bottom: 16px;
  }
  .identity-line {
    font-size: var(--font-md);
    color: var(--text-primary);
  }
  .identity-insecure {
    margin-top: 4px;
    font-size: var(--font-sm);
    color: #fcd34d;
  }
  .identity-server {
    margin-top: 4px;
    font-family: monospace;
    font-size: var(--font-sm);
    color: var(--text-secondary);
  }

  .action-row {
    display: block;
    width: 100%;
    text-align: left;
    background: transparent;
    border: 2px solid transparent;
    color: inherit;
    padding: 16px 20px;
    margin-bottom: 8px;
    border-radius: 8px;
    cursor: pointer;
    font-family: inherit;
  }
  .action-row:focus,
  .action-row:focus-visible {
    border-color: var(--accent);
    outline: none;
  }
  .action-title {
    font-size: var(--font-md);
    margin-bottom: 4px;
  }
  .action-desc {
    font-size: var(--font-sm);
    color: var(--text-secondary);
  }

  .badge {
    display: inline-block;
    margin-left: 10px;
    font-size: var(--font-sm);
    font-weight: 700;
    padding: 2px 10px;
    border-radius: 999px;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    vertical-align: middle;
  }
  .badge.on { background: rgba(52, 211, 153, 0.15); color: #34d399; }
  .badge.off { background: var(--bg-elevated, #2a2a32); color: var(--text-secondary); }

  /* Spacing here uses margins and spelled-out offsets, not flexbox `gap`
     (Chrome 84) or `inset` (Chrome 87): webOS 6 runs Chromium 79. */
  .info-row {
    display: flex;
    padding: 8px 0;
    font-size: var(--font-md);
  }
  .info-label {
    color: var(--text-secondary);
    min-width: 200px;
  }
  .info-value {
    margin-left: 32px;
  }
  .info-note {
    margin-left: 16px;
    font-size: var(--font-sm);
    color: var(--text-secondary);
  }
  /* The Playback rows are buttons with 20px of inner padding: line up. */
  .info-row.indented {
    padding: 8px 22px;
  }
  .prefs-error {
    margin: 0 0 8px;
    padding: 0 22px;
    font-size: var(--font-sm);
    color: #fca5a5;
  }
  .info-value.mono {
    font-family: monospace;
  }

  .modal-backdrop {
    position: fixed;
    top: 0;
    right: 0;
    bottom: 0;
    left: 0;
    background: rgba(0, 0, 0, 0.7);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 100;
  }
  .modal {
    background: var(--bg-secondary, #1f1f24);
    border-radius: 12px;
    padding: 32px;
    max-width: 600px;
    box-shadow: 0 8px 32px rgba(0, 0, 0, 0.5);
  }
  .modal-title {
    font-size: var(--font-lg);
    margin-bottom: 16px;
  }
  .modal-body {
    font-size: var(--font-md);
    color: var(--text-secondary);
    margin-bottom: 24px;
    line-height: 1.4;
  }
  /* Combinators below put the inner part in :global(): Svelte 5 scopes
     every compound after the first as :where(.svelte-xyz), and :where()
     needs Chrome 88, so on webOS 6 (Chromium 79) the whole rule would be
     dropped. :global() leaves that part unscoped, and the rule stays valid
     (the outer class still keeps it to this component). */
  .modal-actions {
    display: flex;
    justify-content: flex-end;
  }
  .modal-actions > :global(button + button) {
    margin-left: 12px;
  }
  .modal-actions > :global(button) {
    padding: 12px 28px;
    font-size: var(--font-sm);
    border-radius: 6px;
    cursor: pointer;
    background: var(--bg-primary, #0a0a0e);
    border: 2px solid transparent;
    color: var(--text-primary);
    font-family: inherit;
  }
  .modal-actions > :global(button:focus),
  .modal-actions > :global(button:focus-visible) {
    border-color: var(--accent);
    outline: none;
  }
  /* Signing out: Cancel dims; the focused button keeps its look and says
     what is happening. */
  .modal-actions > :global(button:disabled) {
    cursor: default;
  }
  .modal-actions > :global(button:disabled:not([data-focused='true'])) {
    opacity: 0.5;
  }
  .btn-confirm:focus,
  .btn-confirm:focus-visible {
    background: var(--accent);
    color: white;
  }
</style>
