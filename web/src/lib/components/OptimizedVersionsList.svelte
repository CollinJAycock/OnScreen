<script lang="ts">
  // The admin's optimized versions and the jobs still producing them, with
  // progress and delete. On a title's page it lists that item's; on
  // Settings ▸ Library ▸ Optimized, every one (optionally by status). Polls
  // while anything is queued or encoding.
  import { onDestroy } from 'svelte';
  import { optimizeApi, type OptimizeJob, type OptimizeStatus } from '$lib/api';
  import { confirmAction } from '$lib/native';
  import { toast } from '$lib/stores/toast';
  import {
    OPTIMIZE_POLL_MS,
    isOptimizing,
    optimizeErrorMessage,
    optimizeJobTitle,
    optimizeProgress,
    optimizeStatusText,
  } from '$lib/optimize';

  interface Props {
    itemId?: string;
    status?: OptimizeStatus | '';
    /** Show each row's title and a link to it (the server-wide list). */
    showTitles?: boolean;
    /** Bump to reload (after a new request). */
    refreshKey?: number;
    /** Hide the whole list when there is nothing in it (a title's page). */
    hideWhenEmpty?: boolean;
  }

  let { itemId, status = '', showTitles = false, refreshKey = 0, hideWhenEmpty = false }: Props = $props();

  let jobs = $state<OptimizeJob[]>([]);
  let counts = $state<Partial<Record<OptimizeStatus, number>>>({});
  let loaded = $state(false);
  let error = $state('');
  let deleting = $state<Record<string, boolean>>({});
  let timer: ReturnType<typeof setTimeout> | null = null;
  let destroyed = false;

  $effect(() => {
    // Reload whenever the filter or the refresh key changes.
    void itemId;
    void status;
    void refreshKey;
    void load();
  });

  async function load() {
    if (timer) clearTimeout(timer);
    timer = null;
    try {
      const res = await optimizeApi.list({ item_id: itemId, status: status || undefined, limit: 200 });
      if (destroyed) return;
      jobs = res?.jobs ?? [];
      counts = res?.counts ?? {};
      error = '';
    } catch (e) {
      error = optimizeErrorMessage(e);
    } finally {
      loaded = true;
    }
    if (!destroyed && jobs.some(isOptimizing)) {
      timer = setTimeout(load, OPTIMIZE_POLL_MS);
    }
  }

  async function remove(j: OptimizeJob) {
    const what = isOptimizing(j) ? 'Cancel this optimize job?' : `Delete the ${j.profile_label} version? Players go back to the original (and a transcode where needed).`;
    if (!(await confirmAction(what))) return;
    deleting = { ...deleting, [j.id]: true };
    try {
      await optimizeApi.remove(j.id);
      jobs = jobs.filter((x) => x.id !== j.id);
      toast.success(isOptimizing(j) ? 'Optimize job cancelled' : 'Optimized version deleted');
      void load();
    } catch (e) {
      toast.error(optimizeErrorMessage(e));
    } finally {
      const next = { ...deleting };
      delete next[j.id];
      deleting = next;
    }
  }

  onDestroy(() => {
    destroyed = true;
    if (timer) clearTimeout(timer);
  });

  const summary = $derived(
    (['running', 'queued', 'ready', 'failed'] as OptimizeStatus[])
      .filter((s) => (counts[s] ?? 0) > 0)
      .map((s) => `${counts[s]} ${s === 'running' ? 'encoding' : s}`)
      .join(' · '),
  );
</script>

{#if !(hideWhenEmpty && loaded && jobs.length === 0 && !error)}
  <div class="opt-list">
    {#if showTitles && summary}<div class="summary">{summary}</div>{/if}
    {#if error}
      <p class="err" role="alert">{error}</p>
    {:else if !loaded}
      <p class="muted">Loading…</p>
    {:else if jobs.length === 0}
      <p class="muted">No optimized versions{status ? ` (${status})` : ''}.</p>
    {:else}
      <ul>
        {#each jobs as j (j.id)}
          <li class="row">
            <div class="main">
              {#if showTitles}
                <a class="title" href="/watch/{j.item_id}">{optimizeJobTitle(j)}</a>
              {/if}
              <div class="line">
                <span class="profile">{j.profile_label}</span>
                <span class="status" class:failed={j.status === 'failed'} class:ready={j.status === 'ready'}>{optimizeStatusText(j)}</span>
              </div>
              {#if isOptimizing(j)}
                <div class="bar" aria-hidden="true"><div style="width:{optimizeProgress(j)}%"></div></div>
              {/if}
            </div>
            <button class="del" disabled={deleting[j.id]} onclick={() => remove(j)}
                    title={isOptimizing(j) ? 'Cancel this job' : 'Delete this optimized version'}>
              {isOptimizing(j) ? 'Cancel' : 'Delete'}
            </button>
          </li>
        {/each}
      </ul>
    {/if}
  </div>
{/if}

<style>
  .opt-list { display: flex; flex-direction: column; gap: 0.4rem; }
  .summary { font-size: 0.75rem; color: var(--text-secondary); }
  .muted { font-size: 0.78rem; color: var(--text-muted); margin: 0; }
  .err { font-size: 0.78rem; color: var(--error, #f87171); margin: 0; }
  ul { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 0.35rem; }
  .row {
    display: flex; align-items: center; gap: 0.75rem;
    padding: 0.5rem 0.7rem; border-radius: 8px;
    background: var(--bg-elevated); border: 1px solid var(--border);
  }
  .main { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 0.2rem; }
  .title {
    font-size: 0.8rem; font-weight: 600; color: var(--text-primary); text-decoration: none;
    overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  }
  .title:hover { text-decoration: underline; }
  .line { display: flex; flex-wrap: wrap; align-items: baseline; gap: 0.5rem; font-size: 0.75rem; }
  .profile { color: var(--text-primary); }
  .status { color: var(--text-muted); font-variant-numeric: tabular-nums; }
  .status.ready { color: var(--text-secondary); }
  .status.failed { color: var(--error, #f87171); }
  .bar { height: 3px; background: var(--border-strong); border-radius: 2px; overflow: hidden; }
  .bar div { height: 100%; background: var(--accent); transition: width 0.5s ease; }
  .del {
    flex-shrink: 0; padding: 0.3rem 0.7rem;
    background: transparent; color: var(--text-muted);
    border: 1px solid var(--border-strong); border-radius: 6px;
    font-size: 0.72rem; cursor: pointer;
  }
  .del:hover:not(:disabled) { color: var(--error, #f87171); border-color: var(--error, #f87171); }
  .del:disabled { opacity: 0.55; cursor: not-allowed; }
</style>
