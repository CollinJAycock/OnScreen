<script lang="ts">
  // Account page for every signed-in user: their own sign-in settings
  // (password, two-factor, profile PIN), languages, scrobbling, playback
  // quality, devices, downloads, API keys and data. Settings is admin-only, so
  // anything a user sets for their own account belongs here; Settings ▸
  // General / Security show the same PIN, language and 2FA controls to admins.
  import { onMount } from 'svelte';
  import { getApiBase, userApi } from '$lib/api';
  import ChangePassword from '$lib/components/ChangePassword.svelte';
  import TwoFactor from '$lib/components/TwoFactor.svelte';
  import ProfilePin from '$lib/components/ProfilePin.svelte';
  import LanguagePrefs from '$lib/components/LanguagePrefs.svelte';
  import SignedInDevices from '$lib/components/SignedInDevices.svelte';
  import MyDownloads from '$lib/components/MyDownloads.svelte';
  import ApiKeys from '$lib/components/ApiKeys.svelte';
  import PlaybackQuality from '$lib/components/PlaybackQuality.svelte';
  import { capabilities, ensureCapabilities } from '$lib/stores/capabilities';
  import { opdsCatalogUrl, saveBlob } from '$lib/fileDownload';
  import { toast } from '$lib/stores/toast';

  // The OPDS catalog address for e-reader apps (KOReader, Moon+ Reader,
  // Panels, Chunky). Set once mounted: it needs window.location.
  let opdsUrl = $state('');
  let opdsCopied = $state(false);
  // Whether the account signs in with an OnScreen password. SSO / LDAP-only
  // accounts and managed profiles have none: no password to change, and
  // two-factor only guards password sign-in. null until known; an older
  // server that doesn't report it is taken as "has one".
  let hasPassword = $state<boolean | null>(null);
  onMount(() => {
    opdsUrl = opdsCatalogUrl(getApiBase(), window.location.origin);
    void ensureCapabilities();
    userApi.getPreferences().then(
      (p) => { hasPassword = p.has_password ?? true; },
      () => { hasPassword = true; },
    );
  });

  async function copyOpds() {
    try {
      await navigator.clipboard.writeText(opdsUrl);
      opdsCopied = true;
      setTimeout(() => (opdsCopied = false), 2000);
    } catch {
      toast.error('Could not copy; select the address and copy it instead.');
    }
  }

  let exporting = $state(false);
  async function downloadMyData() {
    exporting = true;
    try {
      const { blob, filename } = await userApi.exportMyData();
      saveBlob(blob, filename);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : 'Could not build your data export.');
    } finally {
      exporting = false;
    }
  }
  import DigestOptIn from '$lib/components/DigestOptIn.svelte';
</script>

<svelte:head><title>Account — OnScreen</title></svelte:head>

<div class="account">
  <h1>Account</h1>

  <section class="card">
    <h2>My stats</h2>
    <p class="sub">
      Your own viewing and listening numbers: hours, top titles, genres, when
      you play, and your year in review. Only you can see them.
    </p>
    <a class="stats-link" href="/stats">Open my stats →</a>
  </section>

  <section class="card">
    <h2>Email me a digest</h2>
    <p class="sub">
      A weekly or monthly email of what was added to your libraries, with what
      you're in the middle of and your numbers for the period.
    </p>
    <DigestOptIn />
  </section>

  <section class="card">
    <h2>Playback quality</h2>
    <p class="sub">
      Limits for your account on every device, at home and away; the server converts anything
      above them. The audio limit applies whenever audio is converted, and to music in apps that
      check with the server before playing. The web music player plays the original files.
    </p>
    <PlaybackQuality />
  </section>

  <section class="card">
    <h2>Languages</h2>
    <p class="sub">
      Audio and subtitle languages the player picks when a title has them,
      on every device you sign in to.
    </p>
    <LanguagePrefs />
  </section>

  <section class="card">
    <h2>Scrobbling</h2>
    <p class="sub">
      Send what you play to ListenBrainz, Last.fm or Trakt.
    </p>
    <a class="stats-link" href="/account/scrobble">Link your accounts →</a>
  </section>

  {#if hasPassword}
    <section class="card">
      <h2>Password</h2>
      <p class="sub">
        Changing it signs you out on your other devices; this browser stays
        signed in.
      </p>
      <ChangePassword />
    </section>

    <section class="card">
      <TwoFactor />
    </section>
  {:else if hasPassword === false}
    <section class="card">
      <h2>Password</h2>
      <p class="sub">
        You sign in through your organisation's sign-in or a profile, so there's
        no OnScreen password to change here.
      </p>
    </section>
  {/if}

  <section class="card">
    <h2>Profile PIN</h2>
    <p class="sub">
      A 4-digit PIN lets other users on this server switch to your profile
      without needing your password.
    </p>
    <ProfilePin />
  </section>

  <section class="card">
    <h2>Signed-in devices</h2>
    <p class="sub">
      Browsers and apps signed in to your account. Signing one out ends its
      session at once; your other devices stay signed in.
    </p>
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
    <h2>Downloads</h2>
    <p class="sub">
      Smaller copies you asked for from a title's Download menu. Each is kept
      for a while after you last downloaded it, then removed to save space.
    </p>
    <MyDownloads />
  </section>

  <section class="card">
    <h2>API keys</h2>
    <p class="sub">
      Long-lived keys for scripts and integrations such as Home Assistant or
      Kometa. A key can do what you can, except sign in or change your account's
      security. Revoking one stops it at once.
    </p>
    <ApiKeys />

    <div class="opds">
      <h3>Read your books in an e-reader app</h3>
      <p class="sub">
        Apps such as KOReader, Moon+ Reader, Panels and Chunky can browse and download your
        book libraries over OPDS. Add this catalog in the app, create a read-only key above, and
        enter the key as the password (any user name).
      </p>
      {#if opdsUrl}
        <div class="opds-row">
          <code class="opds-url">{opdsUrl}</code>
          <button class="btn" onclick={copyOpds}>{opdsCopied ? 'Copied' : 'Copy'}</button>
        </div>
      {/if}
      {#if $capabilities && !$capabilities.features?.web_downloads}
        <p class="note">Downloads are turned off on this server, so the catalog is unavailable until an admin turns them on.</p>
      {/if}
    </div>
  </section>

  <section class="card">
    <h2>Your data</h2>
    <p class="sub">
      Download a ZIP of what this server keeps about your account: profile and preferences,
      watch history and progress, ratings, favorites, playlists, reading positions, requests
      and signed-in devices. Passwords, two-factor secrets and keys are never included.
    </p>
    <button class="btn" onclick={downloadMyData} disabled={exporting}>
      {exporting ? 'Preparing…' : 'Download my data'}
    </button>
  </section>
</div>

<style>
  .account { max-width: 640px; }
  h1 { font-size: 1.25rem; font-weight: 700; color: var(--text-primary); margin: 0 0 1.25rem; }
  .card + .card { margin-top: 1rem; }
  .card {
    background: var(--bg-elevated);
    border: 1px solid var(--border);
    border-radius: 10px;
    padding: 1.1rem 1.25rem;
  }
  h2 { font-size: 1rem; font-weight: 600; color: var(--text-primary); margin: 0 0 0.3rem; }
  .sub { color: var(--text-muted); font-size: 0.82rem; margin: 0 0 1rem; line-height: 1.45; max-width: 46ch; }
  .opds { margin-top: 1.25rem; padding-top: 1rem; border-top: 1px solid var(--border); }
  h3 { font-size: 0.88rem; font-weight: 600; color: var(--text-primary); margin: 0 0 0.3rem; }
  .opds .sub { margin-bottom: 0.6rem; }
  .opds-row { display: flex; align-items: center; gap: 0.5rem; flex-wrap: wrap; }
  .opds-url {
    flex: 1; min-width: 0; overflow-wrap: anywhere;
    padding: 0.4rem 0.6rem;
    background: var(--input-bg);
    border: 1px solid var(--border-strong);
    border-radius: 6px;
    color: var(--text-primary);
    font-size: 0.8rem;
  }
  .note { color: var(--text-secondary); font-size: 0.78rem; margin: 0.6rem 0 0; }
  .btn {
    padding: 0.42rem 0.85rem;
    background: var(--bg-hover);
    border: 1px solid var(--border-strong);
    border-radius: 7px;
    color: var(--text-primary);
    font-size: 0.8rem;
    font-weight: 600;
    cursor: pointer;
  }
  .btn:hover:not(:disabled) { border-color: var(--accent); }
  .btn:disabled { opacity: 0.6; cursor: default; }
  .stats-link { color: var(--accent-text); font-size: 0.85rem; font-weight: 600; text-decoration: none; }
  .stats-link:hover { text-decoration: underline; }
</style>
