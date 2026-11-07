<script lang="ts">
  import { onMount } from 'svelte';
  import { settingsApi } from '$lib/api';
  import type { SystemSettings } from '$lib/api';
  import { toast } from '$lib/stores/toast';
  import { t, i18n } from '$lib/i18n/index.svelte';

  let loading = true;
  let saving = false;
  let error = '';

  let sys: SystemSettings = {
    server_name: '',
    retain_months: 24,
    tmdb_rate_limit: 5,
    public_asset_cache: false,
    static_abr_enabled: false,
    missing_file_grace_minutes: 15,
    scan_file_concurrency: 0,
    scan_library_concurrency: 2,
    discovery_enabled: true,
    discovery_port: 7368,
  };

  onMount(async () => {
    try {
      sys = { ...(await settingsApi.getSystem()) };
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : t('settings.system.loadFailed');
    } finally {
      loading = false;
    }
  });

  async function save() {
    saving = true;
    try {
      await settingsApi.updateSystem(sys);
      toast.success(t('settings.system.saved'));
      sys = { ...(await settingsApi.getSystem()) };
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : t('settings.system.saveFailed'));
    } finally {
      saving = false;
    }
  }
</script>

{#if loading}
  <div class="skeleton-block"></div>
{:else if error}
  <p class="error">{error}</p>
{:else}
  <div class="wrap">
    <p class="notice">
      {#each $i18n.rich('settings.system.notice', { restart: $i18n('settings.system.noticeRestart') }) as seg}{#if seg.param === 'restart'}<strong>{seg.text}</strong>{:else}{seg.text}{/if}{/each}
    </p>

    <section>
      <header><h2>{$i18n('settings.system.general')}</h2></header>
      <div class="grid">
        <label class="full">
          {$i18n('settings.system.serverName')} <span class="hint">{$i18n('settings.system.serverNameHint')}</span>
          <input type="text" bind:value={sys.server_name} placeholder="OnScreen" />
        </label>
        <label>
          {$i18n('settings.system.retention')}
          <input type="number" min="1" max="120" bind:value={sys.retain_months} />
        </label>
        <label>
          {$i18n('settings.system.tmdbRate')}
          <input type="number" min="1" max="50" bind:value={sys.tmdb_rate_limit} />
        </label>
      </div>
    </section>

    <section>
      <header>
        <h2>{$i18n('settings.system.delivery')}</h2>
        <p class="hint">{$i18n('settings.system.deliveryHint')}</p>
      </header>
      <label class="check">
        <input type="checkbox" bind:checked={sys.public_asset_cache} />
        <span>{$i18n('settings.system.publicCache')} <span class="hint">{$i18n('settings.system.publicCacheHint')}</span></span>
      </label>
      <label class="check">
        <input type="checkbox" bind:checked={sys.static_abr_enabled} />
        <span>{$i18n('settings.system.staticAbr')} <span class="hint">{$i18n('settings.system.staticAbrHint')}</span></span>
      </label>
    </section>

    <section>
      <header>
        <h2>{$i18n('settings.system.scanner')}</h2>
        <p class="hint">{$i18n('settings.system.scannerHint')}</p>
      </header>
      <div class="grid">
        <label>
          {$i18n('settings.system.grace')}
          <input type="number" min="1" max="1440" bind:value={sys.missing_file_grace_minutes} />
        </label>
        <label>
          {$i18n('settings.system.fileConcurrency')} <span class="hint">{$i18n('settings.system.fileConcurrencyHint')}</span>
          <input type="number" min="0" max="64" bind:value={sys.scan_file_concurrency} />
        </label>
        <label>
          {$i18n('settings.system.libraryConcurrency')}
          <input type="number" min="1" max="16" bind:value={sys.scan_library_concurrency} />
        </label>
      </div>
    </section>

    <section>
      <header>
        <h2>{$i18n('settings.system.discovery')}</h2>
        <p class="hint">{$i18n('settings.system.discoveryHint')}</p>
      </header>
      <label class="check">
        <input type="checkbox" bind:checked={sys.discovery_enabled} />
        <span>{$i18n('settings.system.broadcast')}</span>
      </label>
      {#if sys.discovery_enabled}
        <div class="grid">
          <label>
            {$i18n('settings.system.discoveryPort')}
            <input type="number" min="1" max="65535" bind:value={sys.discovery_port} />
          </label>
        </div>
      {/if}
    </section>

    <div class="actions">
      <button class="btn btn-primary" disabled={saving} on:click={save}>
        {saving ? $i18n('settings.system.saving') : $i18n('settings.system.save')}
      </button>
    </div>

  </div>
{/if}

<style>
  .wrap { display: flex; flex-direction: column; gap: 1.5rem; }
  .notice {
    background: var(--accent-bg);
    border: 1px solid var(--accent);
    border-radius: 10px;
    padding: 0.75rem 1rem;
    font-size: 0.82rem;
    color: var(--text-secondary);
    line-height: 1.5;
  }
  section {
    background: var(--bg-elevated);
    border: 1px solid var(--border);
    border-radius: 10px;
    padding: 1.1rem 1.25rem;
  }
  h2 { font-size: 0.95rem; margin: 0 0 0.5rem; font-weight: 600; }
  .hint { color: var(--text-muted); font-weight: 400; font-size: 0.78rem; }
  .error { color: var(--error); }

  .skeleton-block {
    height: 120px;
    border-radius: 10px;
    background: linear-gradient(90deg, var(--bg-elevated) 25%, var(--bg-hover) 50%, var(--bg-elevated) 75%);
    background-size: 200% 100%;
    animation: shimmer 1.4s infinite;
  }
  @keyframes shimmer {
    0% { background-position: 200% 0; }
    100% { background-position: -200% 0; }
  }

  .grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 0.75rem 1rem;
    margin: 1rem 0 0;
  }
  .grid .full { grid-column: 1 / -1; }
  label {
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
    font-size: 0.78rem;
    color: var(--text-secondary);
  }
  input[type="text"], input[type="number"] {
    padding: 0.48rem 0.7rem;
    border-radius: 7px;
    border: 1px solid var(--border-strong);
    background: var(--input-bg);
    color: var(--text-primary);
    font-family: inherit;
    font-size: 0.85rem;
  }
  input[type="text"]:focus, input[type="number"]:focus {
    outline: none;
    border-color: var(--accent);
    box-shadow: 0 0 0 3px var(--accent-bg);
  }
  .check {
    flex-direction: row;
    align-items: flex-start;
    gap: 0.5rem;
    margin-top: 0.6rem;
    color: var(--text-secondary);
    font-size: 0.82rem;
    cursor: pointer;
  }
  .check input {
    margin-top: 0.15rem;
    accent-color: var(--accent);
  }

  .actions { display: flex; gap: 0.5rem; }
  .btn {
    padding: 0.45rem 0.9rem;
    border-radius: 7px;
    font-size: 0.82rem;
    font-weight: 500;
    border: 1px solid var(--border-strong);
    background: transparent;
    color: var(--text-primary);
    cursor: pointer;
    transition: background 0.15s, border-color 0.15s, color 0.15s;
  }
  .btn:disabled { opacity: 0.55; cursor: not-allowed; }
  .btn-primary { background: var(--accent); color: #fff; border-color: transparent; }
  .btn-primary:hover:not(:disabled) { background: var(--accent-hover); }

  @media (max-width: 720px) {
    .grid { grid-template-columns: 1fr; }
  }
</style>
