<script lang="ts">
  import { onMount } from 'svelte';
  import { settingsApi } from '$lib/api';
  import type { SystemSettings } from '$lib/api';
  import { toast } from '$lib/stores/toast';

  let loading = true;
  let saving = false;
  let error = '';

  let sys: SystemSettings = {
    server_name: '',
    retain_months: 24,
    tmdb_rate_limit: 5,
    transcode_abr: false,
    transcode_abr_max_height: 0,
    transcode_abr_auto_max_height: 1080,
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
      error = e instanceof Error ? e.message : 'Failed to load system settings';
    } finally {
      loading = false;
    }
  });

  async function save() {
    saving = true;
    try {
      await settingsApi.updateSystem(sys);
      toast.success('System settings saved — restart the server to apply');
      sys = { ...(await settingsApi.getSystem()) };
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Save failed');
    } finally {
      saving = false;
    }
  }
</script>

{#if loading}
  <p class="muted">Loading…</p>
{:else if error}
  <p class="error">{error}</p>
{:else}
  <div class="wrap">
    <p class="notice">
      These were previously environment variables and are read at startup —
      <strong>restart the server to apply changes</strong>. Node- and site-specific
      settings (connection strings, secret key, bind addresses, file paths, site
      ID, per-worker hardware) stay in the environment by design.
    </p>

    <section>
      <header><h2>General</h2></header>
      <div class="grid">
        <label class="full">
          Server name <span class="hint">— advertised over LAN discovery + capabilities</span>
          <input type="text" bind:value={sys.server_name} placeholder="OnScreen" />
        </label>
        <label>
          Watch-history retention (months)
          <input type="number" min="1" max="120" bind:value={sys.retain_months} />
        </label>
        <label>
          TMDB rate limit (req/s)
          <input type="number" min="1" max="50" bind:value={sys.tmdb_rate_limit} />
        </label>
      </div>
    </section>

    <section>
      <header>
        <h2>Adaptive bitrate (ABR)</h2>
        <p class="hint">Multi-rendition HLS — the player switches rungs on real-time bandwidth.</p>
      </header>
      <label class="check">
        <input type="checkbox" bind:checked={sys.transcode_abr} />
        <span>Enable adaptive-bitrate ladder</span>
      </label>
      {#if sys.transcode_abr}
        <div class="grid">
          <label>
            Hard max rung height <span class="hint">(0 = source)</span>
            <input type="number" min="0" max="4320" bind:value={sys.transcode_abr_max_height} />
          </label>
          <label>
            Auto max rung height <span class="hint">(0 = source)</span>
            <input type="number" min="0" max="4320" bind:value={sys.transcode_abr_auto_max_height} />
          </label>
        </div>
      {/if}
    </section>

    <section>
      <header>
        <h2>Delivery & scale</h2>
        <p class="hint">Object storage itself is configured under Integrations ▸ Storage.</p>
      </header>
      <label class="check">
        <input type="checkbox" bind:checked={sys.public_asset_cache} />
        <span>Public cache headers on artwork <span class="hint">— for a CDN fronting the app (local-disk deployments)</span></span>
      </label>
      <label class="check">
        <input type="checkbox" bind:checked={sys.static_abr_enabled} />
        <span>Static-ABR pre-encode <span class="hint">— pre-encode popular titles' ladders to the store (object storage + CDN recommended)</span></span>
      </label>
    </section>

    <section>
      <header>
        <h2>Scanner</h2>
        <p class="hint">Library scan parallelism and how long a vanished file waits before it's marked gone.</p>
      </header>
      <div class="grid">
        <label>
          Missing-file grace period (minutes)
          <input type="number" min="1" max="1440" bind:value={sys.missing_file_grace_minutes} />
        </label>
        <label>
          Scan file concurrency <span class="hint">(0 = auto, CPU×2)</span>
          <input type="number" min="0" max="64" bind:value={sys.scan_file_concurrency} />
        </label>
        <label>
          Scan library concurrency
          <input type="number" min="1" max="16" bind:value={sys.scan_library_concurrency} />
        </label>
      </div>
    </section>

    <section>
      <header>
        <h2>LAN discovery</h2>
        <p class="hint">Whether the server advertises itself over UDP broadcast so first-party clients can auto-find it.</p>
      </header>
      <label class="check">
        <input type="checkbox" bind:checked={sys.discovery_enabled} />
        <span>Broadcast over LAN discovery</span>
      </label>
      {#if sys.discovery_enabled}
        <div class="grid">
          <label>
            Discovery UDP port
            <input type="number" min="1" max="65535" bind:value={sys.discovery_port} />
          </label>
        </div>
      {/if}
    </section>

    <div class="actions">
      <button class="btn btn-primary" disabled={saving} on:click={save}>
        {saving ? 'Saving…' : 'Save system settings'}
      </button>
    </div>

  </div>
{/if}

<style>
  .wrap { display: flex; flex-direction: column; gap: 1.5rem; }
  .notice {
    background: rgba(124,106,247,0.12);
    border: 1px solid rgba(124,106,247,0.35);
    border-radius: 8px;
    padding: 0.75rem 1rem;
    font-size: 0.82rem;
    color: var(--text-secondary);
    line-height: 1.5;
  }
  section {
    background: var(--surface);
    border: 1px solid rgba(255,255,255,0.05);
    border-radius: 8px;
    padding: 1.25rem 1.5rem;
  }
  h2 { font-size: 0.95rem; margin: 0 0 0.5rem; font-weight: 600; }
  .hint { color: var(--text-muted); font-weight: 400; font-size: 0.78rem; }
  .muted { color: var(--text-muted); }
  .error { color: var(--error); }

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
    align-items: flex-start;
    gap: 0.5rem;
    margin-top: 0.6rem;
    color: var(--text-secondary);
    font-size: 0.82rem;
    cursor: pointer;
  }
  .check input { margin-top: 0.15rem; }

  .actions { display: flex; gap: 0.5rem; }
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

  @media (max-width: 720px) {
    .grid { grid-template-columns: 1fr; }
  }
</style>
