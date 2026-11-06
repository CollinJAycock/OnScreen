<script lang="ts">
  // Settings ▸ Library ▸ Optimized: every optimized version on the server and
  // the jobs still producing them. New ones are queued from a title's page or
  // a library's "Optimize…".
  import type { OptimizeStatus } from '$lib/api';
  import OptimizedVersionsList from '$lib/components/OptimizedVersionsList.svelte';
  import { tr, type MessageKey } from '$lib/i18n/index.svelte';

  let status = $state<OptimizeStatus | ''>('');
  const filters: { value: OptimizeStatus | ''; label: MessageKey }[] = [
    { value: '', label: 'settings.optimized.all' },
    { value: 'running', label: 'settings.optimized.encoding' },
    { value: 'queued', label: 'settings.optimized.waiting' },
    { value: 'ready', label: 'settings.optimized.ready' },
    { value: 'failed', label: 'settings.optimized.failed' },
  ];
</script>

<svelte:head><title>{$tr('settings.optimized.pageTitle')}</title></svelte:head>

<div class="wrap">
  <p class="notice">
    {#each $tr.rich('settings.optimized.notice', { optimize: $tr('settings.optimized.optimizeAction'), env: 'DOWNLOAD_MAX_CONVERSIONS' }) as seg}{#if seg.param === 'optimize'}<strong>{seg.text}</strong>{:else}{seg.text}{/if}{/each}
  </p>

  <div class="filters" role="tablist" aria-label={$tr('settings.optimized.filterByStatus')}>
    {#each filters as f (f.value)}
      <button type="button" role="tab" aria-selected={status === f.value} class:on={status === f.value}
              onclick={() => (status = f.value)}>{$tr(f.label)}</button>
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
