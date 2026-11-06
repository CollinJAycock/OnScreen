<script lang="ts">
  import { onMount } from 'svelte';
  import { settingsApi } from '$lib/api';
  import type { OTelSettings } from '$lib/api';
  import { toast } from '$lib/stores/toast';
  import { t, tr } from '$lib/i18n/index.svelte';

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
      error = e instanceof Error ? e.message : t('settings.observability.loadFailed');
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
      toast.success(t('settings.observability.saved'));
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : t('settings.observability.saveFailed'));
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
    <section>
      <header>
        <h2>{$tr('settings.observability.title')}</h2>
        <p class="hint">{$tr('settings.observability.hint')}</p>
        <p class="restart">
          <strong>{$tr('settings.observability.restartRequired')}</strong> {$tr('settings.observability.restartHint')}
        </p>
      </header>

      <label class="check">
        <input type="checkbox" bind:checked={otel.enabled} />
        <span>{$tr('settings.observability.enable')}</span>
      </label>

      <div class="grid">
        <label class="full">
          {$tr('settings.observability.endpoint')}
          <input
            type="text"
            bind:value={otel.endpoint}
            placeholder="http://localhost:4317"
          />
          <span class="sub">
            {#each $tr.rich('settings.observability.endpointHint', { http: 'http://', https: 'https://', scheme: 'https' }) as seg}{#if seg.param === 'http'}<code>{seg.text}</code>{:else if seg.param === 'https'}<code>{seg.text}</code>{:else if seg.param === 'scheme'}<code>{seg.text}</code>{:else}{seg.text}{/if}{/each}
          </span>
        </label>
        <label>
          {$tr('settings.observability.sampleRatio')}
          <input
            type="number"
            bind:value={otel.sample_ratio}
            min="0"
            max="1"
            step="0.05"
          />
          <span class="sub">
            {#each $tr.rich('settings.observability.sampleHint', { one: '1.0' }) as seg}{#if seg.param === 'one'}<code>{seg.text}</code>{:else}{seg.text}{/if}{/each}
          </span>
        </label>
        <label>
          {$tr('settings.observability.deploymentEnv')}
          <input
            type="text"
            bind:value={otel.deployment_env}
            placeholder="production"
          />
          <span class="sub">
            {#each $tr.rich('settings.observability.envHint', { attr: 'deployment.environment' }) as seg}{#if seg.param === 'attr'}<code>{seg.text}</code>{:else}{seg.text}{/if}{/each}
          </span>
        </label>
      </div>

      <div class="actions">
        <button class="btn btn-primary" disabled={saving} on:click={save}>
          {saving ? $tr('settings.observability.saving') : $tr('settings.observability.save')}
        </button>
      </div>
    </section>

    <section>
      <header>
        <h2>{$tr('settings.observability.jaegerTitle')}</h2>
        <p class="hint">
          {#each $tr.rich('settings.observability.jaegerHint', { profile: 'tracing' }) as seg}{#if seg.param === 'profile'}<code>{seg.text}</code>{:else}{seg.text}{/if}{/each}
        </p>
      </header>
      <pre class="snippet">docker compose --profile tracing up</pre>
      <p class="hint">
        {#each $tr.rich('settings.observability.jaegerUi', { url: 'http://localhost:16686', port: ':4317' }) as seg}{#if seg.param === 'url'}<code>{seg.text}</code>{:else if seg.param === 'port'}<code>{seg.text}</code>{:else}{seg.text}{/if}{/each}
      </p>
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
  .hint { color: var(--text-secondary); font-size: 0.82rem; line-height: 1.5; margin: 0 0 0.75rem; }
  .restart {
    color: var(--text-secondary);
    font-size: 0.78rem;
    line-height: 1.5;
    margin: 0 0 1rem;
    padding: 0.55rem 0.7rem;
    border-left: 2px solid var(--accent);
    background: var(--bg-hover);
    border-radius: 0 7px 7px 0;
  }
  .restart strong { color: var(--text-primary); }
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
    font-weight: 500;
    color: var(--text-muted);
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
    background: var(--bg-hover);
    padding: 0 0.25rem;
    border-radius: 4px;
  }
  input[type="text"], input[type="number"] {
    padding: 0.48rem 0.7rem;
    border-radius: 7px;
    border: 1px solid var(--border-strong);
    background: var(--input-bg);
    color: var(--text-primary);
    font-family: inherit;
    font-size: 0.85rem;
    width: 100%;
  }
  input[type="text"]:focus, input[type="number"]:focus {
    outline: none;
    border-color: var(--accent);
    box-shadow: 0 0 0 3px var(--accent-bg);
  }
  input::placeholder { color: var(--text-muted); }

  .check {
    flex-direction: row;
    align-items: center;
    gap: 0.5rem;
    color: var(--text-secondary);
    font-size: 0.82rem;
    font-weight: 500;
    cursor: pointer;
  }
  .check input[type="checkbox"] {
    accent-color: var(--accent);
    width: 16px;
    height: 16px;
  }

  .snippet {
    margin: 0 0 0.75rem;
    padding: 0.65rem 0.85rem;
    background: var(--bg-hover);
    border: 1px solid var(--border);
    border-radius: 7px;
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
    font-size: 0.8rem;
    color: var(--text-primary);
    overflow-x: auto;
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
  .btn:disabled { opacity: 0.5; cursor: not-allowed; }
  .btn-primary { background: var(--accent); color: #fff; border-color: transparent; font-weight: 600; }
  .btn-primary:hover:not(:disabled) { background: var(--accent-hover); }
  .btn:hover:not(:disabled):not(.btn-primary) { background: var(--bg-hover); border-color: var(--text-muted); }

  @media (max-width: 720px) {
    .grid { grid-template-columns: 1fr; }
  }
</style>
