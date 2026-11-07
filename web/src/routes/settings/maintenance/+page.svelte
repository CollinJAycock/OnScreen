<script lang="ts">
  import { onMount } from 'svelte';
  import { toast } from '$lib/stores/toast';
  import { t, i18n, tn } from '$lib/i18n/index.svelte';
  import { libraryApi, maintenanceApi, type Library, type ReprobeResult } from '$lib/api';

  let libraries: Library[] = [];
  let scope = ''; // '' = all libraries; otherwise a library id
  let running = false;
  let lastResult: ReprobeResult | null = null;

  onMount(async () => {
    try {
      libraries = await libraryApi.list();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : t('settings.maintenance.loadFailed'));
    }
  });

  async function reprobe() {
    running = true;
    lastResult = null;
    try {
      const res = await maintenanceApi.reprobeMetadata(scope || undefined);
      lastResult = res;
      toast.success(
        t('settings.maintenance.queued', {
          files: tn('settings.maintenance.filesCleared', res.files_cleared),
          libraries: tn('settings.maintenance.librariesScanning', res.libraries_queued),
        })
      );
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : t('settings.maintenance.failed'));
    } finally {
      running = false;
    }
  }
</script>

<svelte:head><title>{$i18n('settings.maintenance.pageTitle')}</title></svelte:head>

<div class="wrap">
  <section>
    <h2>{$i18n('settings.maintenance.title')}</h2>
    <p class="hint">
      {#each $i18n.rich('settings.maintenance.hint', { tool: 'ffprobe', fps: $i18n('settings.maintenance.frameRate'), rg: 'ReplayGain' }) as seg}{#if seg.param === 'tool'}<code>{seg.text}</code>{:else if seg.param === 'fps'}<strong>{seg.text}</strong>{:else if seg.param === 'rg'}<strong>{seg.text}</strong>{:else}{seg.text}{/if}{/each}
    </p>
    <p class="hint subtle">
      {#each $i18n.rich('settings.maintenance.subtle', { setPoster: $i18n('settings.maintenance.setPoster'), fixMatch: $i18n('settings.maintenance.fixMatch') }) as seg}{#if seg.param === 'setPoster'}<a href="/settings/missing-art">{seg.text}</a>{:else if seg.param === 'fixMatch'}<a href="/settings/unmatched">{seg.text}</a>{:else}{seg.text}{/if}{/each}
    </p>

    <div class="row">
      <label>
        {$i18n('settings.maintenance.scope')}
        <select bind:value={scope} disabled={running}>
          <option value="">{$i18n('settings.maintenance.allLibraries')}</option>
          {#each libraries as lib}
            <option value={lib.id}>{lib.name}</option>
          {/each}
        </select>
      </label>
    </div>

    <button class="btn btn-primary" on:click={reprobe} disabled={running}>
      {running ? $i18n('settings.maintenance.queuing') : $i18n('settings.maintenance.run')}
    </button>

    {#if lastResult}
      <div class="result ok">
        <div class="result-head">
          {$i18n('settings.maintenance.result', { hashes: $i18n.n('settings.maintenance.hashes', lastResult.files_cleared), scans: $i18n.n('settings.maintenance.scans', lastResult.libraries_queued) })}
        </div>
        <p class="hint" style="margin: 0.4rem 0 0;">
          {#each $i18n.rich('settings.maintenance.running', { tasks: $i18n('settings.maintenance.tasks') }) as seg}{#if seg.param === 'tasks'}<a href="/settings/tasks">{seg.text}</a>{:else}{seg.text}{/if}{/each}
        </p>
      </div>
    {/if}
  </section>
</div>

<style>
  .wrap { display: flex; flex-direction: column; gap: 1.5rem; }
  section {
    background: var(--bg-elevated);
    border: 1px solid var(--border);
    border-radius: 10px;
    padding: 1.1rem 1.25rem;
  }
  h2 { font-size: 0.95rem; margin: 0 0 0.5rem; font-weight: 600; color: var(--text-primary); }
  .hint { color: var(--text-secondary); font-size: 0.82rem; line-height: 1.5; margin: 0 0 1rem; }
  .hint.subtle { color: var(--text-muted); }
  .hint strong { color: var(--text-primary); }
  .hint a { color: var(--accent-text); }

  .row { margin: 0.75rem 0; }
  .row label { display: flex; flex-direction: column; gap: 0.3rem; font-size: 0.82rem; color: var(--text-secondary); }
  .row select {
    padding: 0.48rem 0.7rem;
    border-radius: 7px;
    border: 1px solid var(--border-strong);
    background: var(--input-bg);
    color: var(--text-primary);
    font-family: inherit;
    font-size: 0.85rem;
    max-width: 280px;
  }
  .row select:focus {
    outline: none;
    border-color: var(--accent);
    box-shadow: 0 0 0 3px var(--accent-bg);
  }

  .btn {
    display: inline-block;
    padding: 0.45rem 0.9rem;
    border-radius: 7px;
    font-size: 0.8rem;
    font-weight: 600;
    border: none;
    cursor: pointer;
    transition: background 0.15s, border-color 0.15s, color 0.15s;
  }
  .btn-primary { background: var(--accent); color: #fff; }
  .btn-primary:not(:disabled):hover { background: var(--accent-hover); }
  .btn:disabled { background: var(--bg-hover); color: var(--text-muted); cursor: not-allowed; }

  code {
    font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, monospace;
    font-size: 0.85em;
    background: var(--bg-hover);
    padding: 0.05rem 0.35rem;
    border-radius: 4px;
  }

  .result {
    margin-top: 1rem;
    padding: 0.75rem 1rem;
    border-radius: 7px;
    background: var(--success-bg);
    border: 1px solid var(--success);
    font-size: 0.78rem;
  }
  .result-head { color: var(--text-secondary); }
</style>
