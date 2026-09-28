<script lang="ts">
  // Per-instance health line on Settings ▸ Arr Services: reachability and
  // version, the instance's own health checks, free space on the disks
  // behind its root folders (under 10% free is highlighted), queue size.
  // Compact by default; expands to the checks and disk bars.
  import { untrack } from 'svelte';
  import { arrServicesApi, type ArrServiceHealth } from '$lib/api';
  import { formatBytes, isLowSpace, safeLink, summarizeHealth, usedPercent } from './health';

  interface Props {
    serviceId: string;
    /** A disabled instance isn't probed. */
    enabled?: boolean;
  }

  let { serviceId, enabled = true }: Props = $props();

  let health = $state<ArrServiceHealth | null>(null);
  let loading = $state(false);
  let loadError = $state('');
  let expanded = $state(false);
  // Drops a slow response that lands after a newer refresh started.
  let seq = 0;

  const summary = $derived(health ? summarizeHealth(health) : null);

  async function load() {
    const mine = ++seq;
    loading = true;
    loadError = '';
    try {
      const h = await arrServicesApi.health(serviceId);
      if (mine !== seq) return;
      health = h;
    } catch (e: unknown) {
      if (mine !== seq) return;
      health = null;
      loadError = e instanceof Error && e.message ? e.message : 'Health check failed';
    } finally {
      if (mine === seq) loading = false;
    }
  }

  $effect(() => {
    // Re-probe when the row's id or enabled flag changes (and only then —
    // untrack keeps load()'s own state reads out of this effect's deps).
    void serviceId;
    const on = enabled;
    untrack(() => {
      if (on) {
        load();
      } else {
        seq++;
        health = null;
        loadError = '';
        loading = false;
      }
    });
  });
</script>

<div class="health" data-testid="arr-health">
  <div class="line">
    {#if !enabled}
      <span class="dot muted"></span>
      <span class="label muted-text">Disabled — health not checked</span>
    {:else if loading && !health}
      <span class="dot muted"></span>
      <span class="label muted-text">Checking…</span>
    {:else if loadError}
      <span class="dot error"></span>
      <span class="label">Health check failed: {loadError}</span>
    {:else if health && summary}
      <span class="dot {summary.tone}"></span>
      <span class="label tone-{summary.tone}">{summary.label}</span>
      {#if health.reachable && health.version}<span class="meta">v{health.version}</span>{/if}
      {#if health.reachable && health.queue_count != null}
        <span class="meta">· {health.queue_count} in queue</span>
      {/if}
    {/if}
    {#if enabled}
      <span class="spacer"></span>
      {#if summary?.hasDetails}
        <button
          type="button"
          class="link-btn"
          aria-expanded={expanded}
          onclick={() => (expanded = !expanded)}
        >{expanded ? 'Hide details' : 'Details'}</button>
      {/if}
      <button type="button" class="link-btn" onclick={load} disabled={loading} aria-label="Refresh health">
        {loading ? '…' : 'Refresh'}
      </button>
    {/if}
  </div>

  {#if expanded && health?.reachable}
    <div class="details">
      {#if health.checks.length}
        <ul class="checks">
          {#each health.checks as c, i (i)}
            <li class="check">
              <span class="pill {c.type}">{c.type}</span>
              <span class="msg">{c.message}</span>
              <span class="src">{c.source}</span>
              {#if safeLink(c.wiki_url)}
                <a class="wiki" href={safeLink(c.wiki_url)} target="_blank" rel="noopener noreferrer">Learn more</a>
              {/if}
            </li>
          {/each}
        </ul>
      {:else}
        <p class="none">No health warnings.</p>
      {/if}

      {#each health.disks as d (d.path)}
        {@const low = isLowSpace(d)}
        <div class="disk" class:low data-testid="arr-disk">
          <div class="disk-head">
            <span class="disk-path">{d.path}{#if d.label && d.label !== d.path} <span class="disk-label">({d.label})</span>{/if}</span>
            <span class="disk-free">
              {#if d.total_bytes > 0}
                {formatBytes(d.free_bytes)} free of {formatBytes(d.total_bytes)}{low ? ' — low' : ''}
              {:else}
                {formatBytes(d.free_bytes)} free
              {/if}
            </span>
          </div>
          {#if d.total_bytes > 0}
            <div
              class="bar"
              role="meter"
              aria-label="{d.path} used"
              aria-valuemin={0}
              aria-valuemax={100}
              aria-valuenow={usedPercent(d)}
            >
              <div class="fill" style="width: {usedPercent(d)}%"></div>
            </div>
          {/if}
        </div>
      {/each}
    </div>
  {/if}
</div>

<style>
  .health { margin-top: 0.55rem; font-size: 0.72rem; color: var(--text-secondary); }
  .line { display: flex; align-items: center; gap: 0.4rem; flex-wrap: wrap; }
  .spacer { flex: 1; }
  .dot { width: 8px; height: 8px; border-radius: 50%; flex-shrink: 0; background: var(--text-muted); }
  .dot.ok { background: var(--success); }
  .dot.warn { background: #fbbf24; }
  .dot.error { background: var(--error); }
  .dot.muted { opacity: 0.5; }
  .label { font-weight: 600; }
  .muted-text { color: var(--text-muted); font-weight: 400; }
  .tone-ok { color: var(--success); }
  .tone-warn { color: #fbbf24; }
  .tone-error { color: var(--error); }
  .meta { color: var(--text-muted); }
  .link-btn { background: none; border: none; padding: 0; font-size: 0.72rem; color: var(--accent-text, var(--accent)); cursor: pointer; }
  .link-btn:disabled { opacity: 0.5; cursor: default; }
  .link-btn:hover:not(:disabled) { text-decoration: underline; }

  .details { margin-top: 0.6rem; padding: 0.65rem 0.75rem; border: 1px solid var(--border); border-radius: 8px; background: var(--bg-hover); }
  .checks { list-style: none; margin: 0 0 0.6rem; padding: 0; display: flex; flex-direction: column; gap: 0.4rem; }
  .check { display: flex; flex-wrap: wrap; align-items: baseline; gap: 0.4rem; }
  .pill { font-size: 0.6rem; font-weight: 700; text-transform: uppercase; letter-spacing: 0.05em; padding: 0.1rem 0.4rem; border-radius: 4px; background: var(--bg-elevated); color: var(--text-secondary); }
  .pill.warning { background: rgba(251, 191, 36, 0.12); color: #fbbf24; }
  .pill.error { background: var(--error-bg); color: var(--error); }
  .msg { color: var(--text-primary); }
  .src { color: var(--text-muted); font-family: monospace; font-size: 0.66rem; }
  .wiki { font-size: 0.68rem; color: var(--accent-text, var(--accent)); }
  .none { margin: 0 0 0.6rem; color: var(--text-muted); }

  .disk { margin-top: 0.45rem; }
  .disk-head { display: flex; justify-content: space-between; gap: 0.75rem; flex-wrap: wrap; }
  .disk-path { font-family: monospace; color: var(--text-primary); word-break: break-all; }
  .disk-label { font-family: inherit; color: var(--text-muted); }
  .disk-free { color: var(--text-muted); }
  .bar { margin-top: 0.25rem; height: 6px; border-radius: 3px; background: var(--bg-elevated); overflow: hidden; }
  .fill { height: 100%; background: var(--accent); border-radius: 3px; }
  .disk.low .disk-free { color: var(--error); font-weight: 600; }
  .disk.low .fill { background: var(--error); }
</style>
