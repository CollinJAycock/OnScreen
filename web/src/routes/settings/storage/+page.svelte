<script lang="ts">
  import { onMount } from 'svelte';
  import { settingsApi } from '$lib/api';
  import type { StorageSettings } from '$lib/api';
  import { toast } from '$lib/stores/toast';
  import { t, tr } from '$lib/i18n/index.svelte';

  let loading = true;
  let saving = false;
  let testing = false;
  let error = '';

  // Inline test-connection result (separate from toasts so it persists).
  let testResult: { ok: boolean; error?: string } | null = null;

  let storage: StorageSettings = {
    enabled: false,
    backend: 'local',
    endpoint: '',
    region: '',
    bucket: '',
    access_key: '',
    secret_key: '',
    use_ssl: true,
    media_root: '',
    path_prefix: '',
    cdn_base_url: '',
    path_mappings: {},
  };
  let secretMasked = false;
  // Multi-site path remap, edited as "from=to" lines and (de)serialised around
  // the path_mappings map on load/save.
  let pathMapText = '';

  function mapToText(m: Record<string, string>): string {
    return Object.entries(m ?? {}).map(([from, to]) => `${from}=${to}`).join('\n');
  }
  function textToMap(text: string): Record<string, string> {
    const out: Record<string, string> = {};
    for (const line of text.split('\n')) {
      const s = line.trim();
      if (!s) continue;
      const i = s.indexOf('=');
      if (i > 0) out[s.slice(0, i).trim()] = s.slice(i + 1).trim();
    }
    return out;
  }

  // The S3 fields only matter when object storage is the chosen backend.
  $: showS3 = storage.enabled && storage.backend === 's3';

  onMount(async () => {
    try {
      const s = await settingsApi.getStorage();
      storage = { ...s };
      secretMasked = storage.secret_key === '****';
      pathMapText = mapToText(storage.path_mappings);
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : t('settings.storage.loadFailed');
    } finally {
      loading = false;
    }
  });

  async function save() {
    saving = true;
    testResult = null;
    try {
      // secret_key is sent as-is: "****" preserves the stored secret server-side,
      // a typed value replaces it.
      storage.path_mappings = textToMap(pathMapText);
      await settingsApi.updateStorage(storage);
      toast.success(t('settings.storage.saved'));
      const s = await settingsApi.getStorage();
      storage = { ...s };
      secretMasked = storage.secret_key === '****';
      pathMapText = mapToText(storage.path_mappings);
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : t('settings.storage.saveFailed'));
    } finally {
      saving = false;
    }
  }

  async function test() {
    testing = true;
    testResult = null;
    try {
      // Tests the current form values without saving (server reuses the stored
      // secret when "****" is echoed back).
      testResult = await settingsApi.testStorage(storage);
      if (testResult.ok) toast.success(t('settings.storage.reachable'));
    } catch (e: unknown) {
      testResult = { ok: false, error: e instanceof Error ? e.message : t('settings.storage.testFailed') };
    } finally {
      testing = false;
    }
  }
</script>

{#if loading}
  <div class="skeleton-block"></div>
{:else if error}
  <p class="error">{error}</p>
{:else}
  <div class="wrap">
    <section>
      <header>
        <h2>{$tr('settings.storage.title')}</h2>
        <p class="hint">
          {#each $tr.rich('settings.storage.hint', { local: $tr('settings.storage.hintLocal'), s3: $tr('settings.storage.hintS3') }) as seg}{#if seg.param}<strong>{seg.text}</strong>{:else}{seg.text}{/if}{/each}
        </p>
      </header>

      <label class="check">
        <input type="checkbox" bind:checked={storage.enabled} />
        <span>{$tr('settings.storage.enable')}</span>
      </label>

      {#if storage.enabled}
        <div class="grid">
          <label>
            {$tr('settings.storage.backend')}
            <select bind:value={storage.backend}>
              <option value="local">{$tr('settings.storage.localDisk')}</option>
              <option value="s3">{$tr('settings.storage.s3Compatible')}</option>
            </select>
          </label>
        </div>
      {/if}

      {#if showS3}
        <div class="grid">
          <label class="full">
            {$tr('settings.storage.endpoint')}
            <input type="text" bind:value={storage.endpoint} placeholder="s3.us-west-2.amazonaws.com" autocomplete="off" />
          </label>
          <label>
            {$tr('settings.storage.bucket')}
            <input type="text" bind:value={storage.bucket} placeholder="onscreen-media" autocomplete="off" />
          </label>
          <label>
            {$tr('settings.storage.region')}
            <input type="text" bind:value={storage.region} placeholder="us-west-2" autocomplete="off" />
          </label>
          <label>
            {$tr('settings.storage.accessKey')}
            <input type="text" bind:value={storage.access_key} autocomplete="off" />
          </label>
          <label>
            {$tr('settings.storage.secretKey')}
            <input
              type="password"
              bind:value={storage.secret_key}
              on:input={() => { secretMasked = false; }}
              placeholder={secretMasked ? $tr('settings.storage.unchanged') : ''}
              autocomplete="new-password"
            />
          </label>
          <label class="check inline">
            <input type="checkbox" bind:checked={storage.use_ssl} />
            <span>{$tr('settings.storage.useTls')}</span>
          </label>
        </div>

        <div class="grid">
          <label>
            {$tr('settings.storage.mediaRoot')}
            <input type="text" bind:value={storage.media_root} placeholder="/mnt/c/media" autocomplete="off" />
          </label>
          <label>
            {$tr('settings.storage.pathPrefix')}
            <input type="text" bind:value={storage.path_prefix} placeholder="library/" autocomplete="off" />
          </label>
          <label class="full">
            {$tr('settings.storage.cdnBaseUrl')} <span class="opt">{$tr('settings.storage.optional')}</span>
            <input type="text" bind:value={storage.cdn_base_url} placeholder="https://cdn.example.com" autocomplete="off" />
          </label>
        </div>

        <p class="hint sub">
          {#each $tr.rich('settings.storage.keyHint', { mediaRoot: $tr('settings.storage.termMediaRoot'), pathPrefix: $tr('settings.storage.termPathPrefix'), cdn: $tr('settings.storage.termCdn') }) as seg}{#if seg.param}<em>{seg.text}</em>{:else}{seg.text}{/if}{/each}
        </p>
      {/if}

      <div class="actions">
        <button class="btn btn-primary" disabled={saving} on:click={save}>
          {saving ? $tr('settings.storage.saving') : $tr('settings.storage.saveStorage')}
        </button>
        {#if showS3}
          <button class="btn" disabled={testing} on:click={test}>
            {testing ? $tr('settings.storage.testing') : $tr('settings.storage.testConnection')}
          </button>
        {/if}
      </div>

      {#if testResult}
        <p class="status">
          {#if testResult.ok}
            <span class="badge success">{$tr('settings.storage.connected')}</span>
            {$tr('settings.storage.bucketReachable')}
          {:else}
            <span class="badge danger">{$tr('settings.storage.failed')}</span>
            {testResult.error}
          {/if}
        </p>
      {/if}
    </section>

    <section>
      <header>
        <h2>{$tr('settings.storage.mappingsTitle')} <span class="opt">{$tr('settings.storage.advanced')}</span></h2>
        <p class="hint">
          {#each $tr.rich('settings.storage.mappingsHint', { format: 'from=to' }) as seg}{#if seg.param}<code>{seg.text}</code>{:else}{seg.text}{/if}{/each}
        </p>
      </header>
      <div class="grid">
        <label class="full">
          {$tr('settings.storage.mappingsLabel')}
          <textarea
            bind:value={pathMapText}
            rows="3"
            placeholder={'/mnt/primary/media=/mnt/standby/media'}
            spellcheck="false"
          ></textarea>
        </label>
      </div>
      <div class="actions">
        <button class="btn btn-primary" disabled={saving} on:click={save}>
          {saving ? $tr('settings.storage.saving') : $tr('settings.storage.save')}
        </button>
      </div>
    </section>
  </div>
{/if}

<style>
  .wrap { display: flex; flex-direction: column; gap: 1.5rem; }
  section {
    background: var(--bg-elevated);
    border: 1px solid var(--border);
    border-radius: 10px;
    padding: 1.1rem 1.25rem;
  }
  h2 { font-size: 0.95rem; margin: 0 0 0.5rem; font-weight: 600; }
  .hint { color: var(--text-secondary); font-size: 0.82rem; line-height: 1.5; margin: 0 0 1rem; }
  .hint.sub { margin: 0.75rem 0 0; }
  .hint .opt, .opt { color: var(--text-muted); font-weight: 400; }
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
    margin: 1rem 0;
  }
  .grid .full { grid-column: 1 / -1; }
  label {
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
    font-size: 0.72rem;
    color: var(--text-muted);
  }
  input[type="text"], input[type="password"], select, textarea {
    padding: 0.48rem 0.7rem;
    border-radius: 7px;
    border: 1px solid var(--border-strong);
    background: var(--input-bg);
    color: var(--text-primary);
    font-family: inherit;
    font-size: 0.85rem;
    width: 100%;
  }
  input[type="text"]:focus, input[type="password"]:focus, select:focus, textarea:focus {
    outline: none;
    border-color: var(--accent);
    box-shadow: 0 0 0 3px var(--accent-bg);
  }
  input::placeholder, textarea::placeholder { color: var(--text-muted); }
  textarea { resize: vertical; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }

  input[type="checkbox"] { accent-color: var(--accent); width: 16px; height: 16px; }

  .check {
    flex-direction: row;
    align-items: center;
    gap: 0.5rem;
    color: var(--text-secondary);
    font-size: 0.82rem;
    cursor: pointer;
  }
  .check.inline { align-self: end; padding-bottom: 0.45rem; }

  .actions { display: flex; gap: 0.5rem; margin-top: 0.5rem; }
  .btn {
    padding: 0.45rem 0.9rem;
    border-radius: 7px;
    font-size: 0.78rem;
    font-weight: 500;
    border: 1px solid var(--border-strong);
    background: transparent;
    color: var(--text-secondary);
    cursor: pointer;
    transition: background 0.15s, border-color 0.15s, color 0.15s;
  }
  .btn:disabled { opacity: 0.5; cursor: not-allowed; }
  .btn-primary {
    background: var(--accent);
    color: #fff;
    border-color: transparent;
    font-size: 0.8rem;
    font-weight: 600;
  }
  .btn-primary:hover:not(:disabled) { background: var(--accent-hover); }
  .btn:hover:not(:disabled):not(.btn-primary) { background: var(--bg-hover); border-color: var(--text-muted); }

  .status { font-size: 0.82rem; margin: 0.9rem 0 0; color: var(--text-secondary); }
  .badge {
    display: inline-block;
    font-size: 0.68rem;
    font-weight: 600;
    padding: 0.1rem 0.4rem;
    border-radius: 4px;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    margin-right: 0.4rem;
  }
  .badge.success { color: var(--success); background: var(--success-bg); }
  .badge.danger { color: var(--error); background: var(--error-bg); }

  @media (max-width: 720px) {
    .grid { grid-template-columns: 1fr; }
  }
</style>
