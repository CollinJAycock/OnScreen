<script lang="ts">
  import { onMount } from 'svelte';
  import { settingsApi } from '$lib/api';
  import type { OTelSettings } from '$lib/api';
  import { toast } from '$lib/stores/toast';

  let loading = true;
  let saving = false;
  let error = '';

  let otel: OTelSettings = {
    enabled: false,
    endpoint: '',
    sample_ratio: 1.0,
    deployment_env: '',
  };

  onMount(async () => {
    try {
      const s = await settingsApi.get();
      if (s.otel) {
        otel = { ...s.otel };
      }
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed to load settings';
    } finally {
      loading = false;
    }
  });

  async function save() {
    saving = true;
    try {
      await settingsApi.update({
        otel: {
          enabled: otel.enabled,
          endpoint: otel.endpoint,
          sample_ratio: Number(otel.sample_ratio),
          deployment_env: otel.deployment_env,
        },
      } as never);
      toast.success('Tracing settings saved — restart the server for changes to take effect');
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
    <section>
      <header>
        <h2>OpenTelemetry Tracing</h2>
        <p class="hint">
          Exports distributed traces over OTLP/gRPC. Tracing is wired
          throughout the server (HTTP routes, DB queries, scanner, transcode);
          when disabled, instrumentation is a no-op and emits zero traces.
        </p>
        <p class="restart">
          <strong>Restart required.</strong> The tracer provider is built once
          at process start. Save here, then restart the server (and any worker
          binaries) for new settings to take effect.
        </p>
      </header>

      <label class="check">
        <input type="checkbox" bind:checked={otel.enabled} />
        <span>Enable OTLP tracing exporter</span>
      </label>

      <div class="grid">
        <label class="full">
          OTLP/gRPC endpoint
          <input
            type="text"
            bind:value={otel.endpoint}
            placeholder="http://localhost:4317"
          />
          <span class="sub">
            Must include scheme (<code>http://</code> or <code>https://</code>);
            TLS is enabled automatically for <code>https</code>.
          </span>
        </label>
        <label>
          Sample ratio
          <input
            type="number"
            bind:value={otel.sample_ratio}
            min="0"
            max="1"
            step="0.05"
          />
          <span class="sub">
            0.0–1.0. <code>1.0</code> samples every trace (dev default);
            production with a paid backend often runs 0.05–0.1.
          </span>
        </label>
        <label>
          Deployment environment
          <input
            type="text"
            bind:value={otel.deployment_env}
            placeholder="production"
          />
          <span class="sub">
            Tagged on every span as <code>deployment.environment</code>.
          </span>
        </label>
      </div>

      <div class="actions">
        <button class="btn btn-primary" disabled={saving} on:click={save}>
          {saving ? 'Saving…' : 'Save tracing settings'}
        </button>
      </div>
    </section>

    <section>
      <header>
        <h2>Local Jaeger</h2>
        <p class="hint">
          The bundled docker compose ships a Jaeger all-in-one under the
          <code>tracing</code> profile:
        </p>
      </header>
      <pre class="snippet">docker compose --profile tracing up</pre>
      <p class="hint">
        UI at <code>http://localhost:16686</code>. OTLP/gRPC listens on
        <code>:4317</code> — drop that into the endpoint above.
      </p>
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
  .hint { color: var(--text-secondary); font-size: 0.82rem; line-height: 1.5; margin: 0 0 0.75rem; }
  .restart {
    color: var(--text-secondary);
    font-size: 0.78rem;
    line-height: 1.5;
    margin: 0 0 1rem;
    padding: 0.55rem 0.7rem;
    border-left: 2px solid var(--accent);
    background: rgba(255,255,255,0.02);
    border-radius: 0 4px 4px 0;
  }
  .restart strong { color: var(--text-primary); }
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
  .sub {
    color: var(--text-muted);
    font-size: 0.72rem;
    line-height: 1.4;
  }
  code {
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
    font-size: 0.78rem;
    color: var(--text-primary);
    background: rgba(255,255,255,0.04);
    padding: 0 0.25rem;
    border-radius: 3px;
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
    align-items: center;
    gap: 0.5rem;
    color: var(--text-secondary);
    font-size: 0.82rem;
    cursor: pointer;
  }

  .snippet {
    margin: 0 0 0.75rem;
    padding: 0.65rem 0.85rem;
    background: var(--bg);
    border: 1px solid rgba(255,255,255,0.08);
    border-radius: 4px;
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
    font-size: 0.8rem;
    color: var(--text-primary);
    overflow-x: auto;
  }

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
  .btn:hover:not(:disabled):not(.btn-primary) { background: rgba(255,255,255,0.04); }

  @media (max-width: 720px) {
    .grid { grid-template-columns: 1fr; }
  }
</style>
