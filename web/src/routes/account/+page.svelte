<script lang="ts">
  // Account page for every signed-in user: their own sign-in settings
  // (password, two-factor), languages, scrobbling, playback quality, devices,
  // downloads, API keys and data. Settings is admin-only, so anything a user
  // sets for their own account belongs here; Settings ▸ General / Security
  // show the same language and 2FA controls to admins. PINs belong to managed
  // profiles and are set on the Profiles page.
  import { onMount } from 'svelte';
  import { getApiBase, userApi } from '$lib/api';
  import ChangePassword from '$lib/components/ChangePassword.svelte';
  import TwoFactor from '$lib/components/TwoFactor.svelte';
  import LanguagePrefs from '$lib/components/LanguagePrefs.svelte';
  import LanguagePicker from '$lib/components/LanguagePicker.svelte';
  import SignedInDevices from '$lib/components/SignedInDevices.svelte';
  import MyDownloads from '$lib/components/MyDownloads.svelte';
  import ApiKeys from '$lib/components/ApiKeys.svelte';
  import PlaybackQuality from '$lib/components/PlaybackQuality.svelte';
  import { capabilities, ensureCapabilities } from '$lib/stores/capabilities';
  import { opdsCatalogUrl, saveBlob } from '$lib/fileDownload';
  import { toast } from '$lib/stores/toast';
  import { t, i18n } from '$lib/i18n/index.svelte';

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
      toast.error(t('account.copyFailed'));
    }
  }

  let exporting = $state(false);
  async function downloadMyData() {
    exporting = true;
    try {
      const { blob, filename } = await userApi.exportMyData();
      saveBlob(blob, filename);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t('account.exportFailed'));
    } finally {
      exporting = false;
    }
  }
  import DigestOptIn from '$lib/components/DigestOptIn.svelte';
</script>

<svelte:head><title>{$i18n('account.pageTitle')}</title></svelte:head>

<div class="account">
  <h1>{$i18n('account.title')}</h1>

  <section class="card">
    <h2>{$i18n('account.statsTitle')}</h2>
    <p class="sub">{$i18n('account.statsDesc')}</p>
    <a class="stats-link" href="/stats">{$i18n('account.statsLink')}</a>
  </section>

  <section class="card">
    <h2>{$i18n('account.digestTitle')}</h2>
    <p class="sub">{$i18n('account.digestDesc')}</p>
    <DigestOptIn />
  </section>

  <section class="card">
    <h2>{$i18n('account.qualityTitle')}</h2>
    <p class="sub">{$i18n('account.qualityDesc')}</p>
    <PlaybackQuality />
  </section>

  <section class="card">
    <h2>{$i18n('account.languagesTitle')}</h2>
    <div class="ui-language"><LanguagePicker saveToAccount /></div>
    <p class="sub">{$i18n('account.languagesDesc')}</p>
    <LanguagePrefs />
  </section>

  <section class="card">
    <h2>{$i18n('account.scrobblingTitle')}</h2>
    <p class="sub">{$i18n('account.scrobblingDesc')}</p>
    <a class="stats-link" href="/account/scrobble">{$i18n('account.scrobblingLink')}</a>
  </section>

  {#if hasPassword}
    <section class="card">
      <h2>{$i18n('account.passwordTitle')}</h2>
      <p class="sub">{$i18n('account.passwordDesc')}</p>
      <ChangePassword />
    </section>

    <section class="card">
      <TwoFactor />
    </section>
  {:else if hasPassword === false}
    <section class="card">
      <h2>{$i18n('account.passwordTitle')}</h2>
      <p class="sub">{$i18n('account.noPassword')}</p>
    </section>
  {/if}

  <section class="card">
    <h2>{$i18n('account.devicesTitle')}</h2>
    <p class="sub">{$i18n('account.devicesDesc')}</p>
    <SignedInDevices
      load={() => userApi.listMySessions()}
      revoke={(id) => userApi.revokeMySession(id)}
      revokeAll={() => userApi.revokeMyOtherSessions()}
      revokeAllLabel={$i18n('account.revokeAll')}
      revokeAllConfirm={$i18n('account.revokeAllConfirm')}
      keepCurrent
    />
  </section>

  <section class="card">
    <h2>{$i18n('account.downloadsTitle')}</h2>
    <p class="sub">{$i18n('account.downloadsDesc')}</p>
    <MyDownloads />
  </section>

  <section class="card">
    <h2>{$i18n('account.apiKeysTitle')}</h2>
    <p class="sub">{$i18n('account.apiKeysDesc')}</p>
    <ApiKeys />

    <div class="opds">
      <h3>{$i18n('account.opdsTitle')}</h3>
      <p class="sub">{$i18n('account.opdsDesc')}</p>
      {#if opdsUrl}
        <div class="opds-row">
          <code class="opds-url">{opdsUrl}</code>
          <button class="btn" onclick={copyOpds}>{opdsCopied ? $i18n('account.copied') : $i18n('account.copy')}</button>
        </div>
      {/if}
      {#if $capabilities && !$capabilities.features?.web_downloads}
        <p class="note">{$i18n('account.opdsOff')}</p>
      {/if}
    </div>
  </section>

  <section class="card">
    <h2>{$i18n('account.dataTitle')}</h2>
    <p class="sub">{$i18n('account.dataDesc')}</p>
    <button class="btn" onclick={downloadMyData} disabled={exporting}>
      {exporting ? $i18n('account.preparing') : $i18n('account.downloadData')}
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
  .ui-language { margin-bottom: 1rem; }
</style>
