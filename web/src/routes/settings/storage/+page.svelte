<script lang="ts">
  import { onMount } from 'svelte';
  import { settingsApi } from '$lib/api';
  import type { StorageSettings } from '$lib/api';
  import { toast } from '$lib/stores/toast';

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
  };
  let secretMasked = false;

  // The S3 fields only matter when object storage is the chosen backend.
  $: showS3 = storage.enabled && storage.backend === 's3';

  onMount(async () => {
    try {
      const s = await settingsApi.getStorage();
      storage = { ...s };
      secretMasked = storage.secret_key === '****';
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed to load storage settings';
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
      await settingsApi.updateStorage(storage);
      toast.success('Storage settings saved');
      const s = await settingsApi.getStorage();
      storage = { ...s };
      secretMasked = storage.secret_key === '****';
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Save failed');
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
      if (testResult.ok) toast.success('Storage backend reachable');
    } catch (e: unknown) {
      testResult = { ok: false, error: e instanceof Error ? e.message : 'Test failed' };
    } finally {
      testing = false;
    }
  }
</script>

{#if loading}
  <p class="muted">Loading…</p>
{:else if error}
  <p class="error">{error}</p>
{:else}
  <div class="wrap">
    <section>
      <header>
        <h2>Media storage</h2>
        <p class="hint">
          Where OnScreen reads media bytes from. <strong>Local disk</strong> is the
          default and serves files straight from the filesystem. <strong>S3-compatible
          object storage</strong> (AWS S3, MinIO, Backblaze B2, Wasabi, Cloudflare R2)
          lets workers and clients read source and segments directly from the bucket
          or a CDN — taking byte delivery off this server. Leaving this disabled keeps
          the existing local-disk behaviour unchanged.
        </p>
      </header>

      <label class="check">
        <input type="checkbox" bind:checked={storage.enabled} />
        <span>Enable object storage</span>
      </label>

      {#if storage.enabled}
        <div class="grid">
          <label>
            Backend
            <select bind:value={storage.backend}>
              <option value="local">Local disk</option>
              <option value="s3">S3-compatible</option>
            </select>
          </label>
        </div>
      {/if}

      {#if showS3}
        <div class="grid">
          <label class="full">
            Endpoint
            <input type="text" bind:value={storage.endpoint} placeholder="s3.us-west-2.amazonaws.com" autocomplete="off" />
          </label>
          <label>
            Bucket
            <input type="text" bind:value={storage.bucket} placeholder="onscreen-media" autocomplete="off" />
          </label>
          <label>
            Region
            <input type="text" bind:value={storage.region} placeholder="us-west-2" autocomplete="off" />
          </label>
          <label>
            Access key
            <input type="text" bind:value={storage.access_key} autocomplete="off" />
          </label>
          <label>
            Secret key
            <input
              type="password"
              bind:value={storage.secret_key}
              on:input={() => { secretMasked = false; }}
              placeholder={secretMasked ? 'unchanged' : ''}
              autocomplete="new-password"
            />
          </label>
          <label class="check inline">
            <input type="checkbox" bind:checked={storage.use_ssl} />
            <span>Use TLS (https) to the endpoint</span>
          </label>
        </div>

        <div class="grid">
          <label>
            Media root
            <input type="text" bind:value={storage.media_root} placeholder="/mnt/c/media" autocomplete="off" />
          </label>
          <label>
            Path prefix
            <input type="text" bind:value={storage.path_prefix} placeholder="library/" autocomplete="off" />
          </label>
          <label class="full">
            CDN base URL <span class="opt">(optional)</span>
            <input type="text" bind:value={storage.cdn_base_url} placeholder="https://cdn.example.com" autocomplete="off" />
          </label>
        </div>

        <p class="hint sub">
          The object key is a file's path with <em>media root</em> stripped and
          <em>path prefix</em> prepended. With a <em>CDN base URL</em>, signed URLs
          point at the CDN (front a public-read bucket, or give the CDN origin
          credentials); otherwise OnScreen issues time-limited presigned bucket URLs.
        </p>
      {/if}

      <div class="actions">
        <button class="btn btn-primary" disabled={saving} on:click={save}>
          {saving ? 'Saving…' : 'Save storage settings'}
        </button>
        {#if showS3}
          <button class="btn" disabled={testing} on:click={test}>
            {testing ? 'Testing…' : 'Test connection'}
          </button>
        {/if}
      </div>

      {#if testResult}
        <p class="status" class:ok={testResult.ok} class:bad={!testResult.ok}>
          {#if testResult.ok}
            ● Connected — bucket reachable with these credentials.
          {:else}
            ● Failed — {testResult.error}
          {/if}
        </p>
      {/if}
    </section>
  </div>
{/if}

<style>
  .wrap { display: flex; flex-direction: column; gap: 2rem; }
  section {
    background: var(--surface);
    border: 1px solid rgba(255,255,255,0.05);
    border-radius: 8px;
    padding: 1.25rem 1.5rem;
  }
  h2 { font-size: 0.95rem; margin: 0 0 0.5rem; font-weight: 600; }
  .hint { color: var(--text-secondary); font-size: 0.82rem; line-height: 1.5; margin: 0 0 1rem; }
  .hint.sub { margin: 0.75rem 0 0; }
  .hint .opt, .opt { color: var(--text-muted); font-weight: 400; }
  .muted { color: var(--text-muted); }
  .error { color: var(--error); }

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
    font-size: 0.78rem;
    color: var(--text-secondary);
  }
  input[type="text"], input[type="password"], select {
    padding: 0.45rem 0.6rem;
    border-radius: 4px;
    border: 1px solid rgba(255,255,255,0.1);
    background: var(--bg);
    color: var(--text-primary);
    font-family: inherit;
    font-size: 0.85rem;
  }

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
    padding: 0.55rem 1.1rem;
    border-radius: 4px;
    font-size: 0.82rem;
    font-weight: 500;
    border: 1px solid rgba(255,255,255,0.1);
    background: transparent;
    color: var(--text-primary);
    cursor: pointer;
    transition: background 0.12s, filter 0.12s;
  }
  .btn:disabled { opacity: 0.55; cursor: not-allowed; }
  .btn-primary { background: var(--accent); color: var(--accent-text); border-color: transparent; }
  .btn-primary:hover:not(:disabled) { filter: brightness(1.1); }
  .btn:hover:not(:disabled):not(.btn-primary) { background: rgba(255,255,255,0.04); }

  .status { font-size: 0.82rem; margin: 0.9rem 0 0; }
  .status.ok { color: var(--success, #4ade80); }
  .status.bad { color: var(--error, #f87171); }

  @media (max-width: 720px) {
    .grid { grid-template-columns: 1fr; }
  }
</style>
