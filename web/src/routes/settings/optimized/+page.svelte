<script lang="ts">
  // Settings ▸ Library ▸ Optimized: every optimized version on the server and
  // the jobs still producing them. New ones are queued from a title's page or
  // a library's "Optimize…".
  import type { OptimizeStatus } from '$lib/api';
  import OptimizedVersionsList from '$lib/components/OptimizedVersionsList.svelte';

  let status = $state<OptimizeStatus | ''>('');
  const filters: { value: OptimizeStatus | ''; label: string }[] = [
    { value: '', label: 'All' },
    { value: 'running', label: 'Encoding' },
    { value: 'queued', label: 'Waiting' },
    { value: 'ready', label: 'Ready' },
    { value: 'failed', label: 'Failed' },
  ];
</script>

<svelte:head><title>Optimized versions — Settings — OnScreen</title></svelte:head>

<div class="wrap">
  <p class="notice">
    Optimized versions are smaller MP4 copies (H.264 + AAC) made ahead of time. When a player would
    otherwise need a live transcode — a codec it can't decode, or a bitrate limit away from home — the
    server hands it a version that fits instead, and it plays directly. Queue them with
    <strong>Optimize…</strong> on a title's page or a library. They are encoded one or two at a time
    (DOWNLOAD_MAX_CONVERSIONS) alongside converted downloads, and deleted automatically when the original
    file is deleted or replaced.
  </p>

  <div class="filters" role="tablist" aria-label="Filter by status">
    {#each filters as f (f.value)}
      <button type="button" role="tab" aria-selected={status === f.value} class:on={status === f.value}
              onclick={() => (status = f.value)}>{f.label}</button>
    {/each}
  </div>

  <OptimizedVersionsList {status} showTitles />
</div>

<style>
  .wrap { display: flex; flex-direction: column; gap: 1rem; }
  .notice {
    background: var(--accent-bg);
    border: 1px solid var(--accent);
    border-radius: 10px;
    padding: 0.75rem 1rem;
    font-size: 0.82rem;
    color: var(--text-secondary);
    line-height: 1.5;
    margin: 0;
  }
  .filters { display: flex; flex-wrap: wrap; gap: 0.35rem; }
  .filters button {
    padding: 0.3rem 0.75rem; border-radius: 999px;
    border: 1px solid var(--border-strong); background: var(--input-bg);
    color: var(--text-secondary); font-size: 0.75rem; cursor: pointer;
  }
  .filters button.on { background: var(--accent-bg); border-color: var(--accent); color: var(--text-primary); }
</style>
