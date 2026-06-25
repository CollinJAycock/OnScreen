<script lang="ts">
  import { onMount } from 'svelte';
  import { toast } from '$lib/stores/toast';
  import { libraryApi, maintenanceApi, type Library, type ReprobeResult } from '$lib/api';

  let libraries: Library[] = [];
  let scope = ''; // '' = all libraries; otherwise a library id
  let running = false;
  let lastResult: ReprobeResult | null = null;

  onMount(async () => {
    try {
      libraries = await libraryApi.list();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Failed to load libraries');
    }
  });

  async function reprobe() {
    running = true;
    lastResult = null;
    try {
      const res = await maintenanceApi.reprobeMetadata(scope || undefined);
      lastResult = res;
      toast.success(
        `Re-probe queued: cleared ${res.files_cleared} file${res.files_cleared === 1 ? '' : 's'}, ` +
          `${res.libraries_queued} librar${res.libraries_queued === 1 ? 'y' : 'ies'} scanning.`
      );
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Re-probe failed');
    } finally {
      running = false;
    }
  }
</script>

<svelte:head><title>Maintenance — OnScreen</title></svelte:head>

<div class="wrap">
  <section>
    <h2>Re-probe technical metadata</h2>
    <p class="hint">
      Re-runs <code>ffprobe</code> on existing files to backfill technical
      metadata that older scans didn't persist — notably <strong>frame rate</strong>
      (which the adaptive-bitrate ladder needs to align segments) and audio
      <strong>ReplayGain</strong> tags. It clears the scanner's content hashes so the
      next scan can't fast-skip unchanged files, then queues that scan; the
      re-probe runs in the background. Safe to run anytime — it doesn't change
      your files, only re-reads them.
    </p>
    <p class="hint subtle">
      This covers probe-derived fields only. Missing posters and TMDB ratings
      come from metadata enrichment — use <a href="/settings/missing-art">Set Poster</a>
      or <a href="/settings/unmatched">Fix Match</a> for those.
    </p>

    <div class="row">
      <label>
        Scope
        <select bind:value={scope} disabled={running}>
          <option value="">All libraries</option>
          {#each libraries as lib}
            <option value={lib.id}>{lib.name}</option>
          {/each}
        </select>
      </label>
    </div>

    <button class="btn btn-primary" on:click={reprobe} disabled={running}>
      {running ? 'Queuing…' : 'Re-probe metadata'}
    </button>

    {#if lastResult}
      <div class="result ok">
        <div class="result-head">
          Cleared <code>{lastResult.files_cleared}</code> file hash{lastResult.files_cleared === 1 ? '' : 'es'};
          queued <code>{lastResult.libraries_queued}</code> library scan{lastResult.libraries_queued === 1 ? '' : 's'}.
        </div>
        <p class="hint" style="margin: 0.4rem 0 0;">
          The re-probe is running in the background. Check
          <a href="/settings/tasks">Tasks</a> or the server logs for scan progress.
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
