<script lang="ts">
  // The Account page's Downloads list: the caller's converted downloads
  // (GET /users/me/downloads) with their progress, a download link once a
  // file is ready, and a remove button (which also stops a conversion nobody
  // else is waiting for). Refreshes itself while anything is being prepared.
  import { onDestroy, onMount } from 'svelte';
  import { assetUrl, downloadsApi, type DownloadJob } from '$lib/api';
  import { toast } from '$lib/stores/toast';
  import {
    DOWNLOAD_POLL_MS,
    downloadErrorMessage,
    downloadStatusText,
    formatBytes,
    isPreparing,
    progressPercent,
  } from '$lib/downloads';

  let jobs = $state<DownloadJob[]>([]);
  let loading = $state(true);
  let error = $state('');
  let removing = $state<Record<string, boolean>>({});
  let timer: ReturnType<typeof setTimeout> | null = null;
  let destroyed = false;

  async function load() {
    try {
      jobs = (await downloadsApi.listMine()) ?? [];
      error = '';
    } catch (err) {
      error = downloadErrorMessage(err);
    } finally {
      loading = false;
    }
    schedule();
  }

  function schedule() {
    if (timer) clearTimeout(timer);
    timer = null;
    if (destroyed || !jobs.some(isPreparing)) return;
    timer = setTimeout(load, DOWNLOAD_POLL_MS);
  }

  async function remove(j: DownloadJob) {
    removing = { ...removing, [j.id]: true };
    try {
      await downloadsApi.remove(j.id);
      jobs = jobs.filter((x) => x.id !== j.id);
    } catch (err) {
      toast.error(downloadErrorMessage(err));
    } finally {
      const { [j.id]: _, ...rest } = removing;
      removing = rest;
    }
  }

  onMount(load);
  onDestroy(() => {
    destroyed = true;
    if (timer) clearTimeout(timer);
  });
</script>

{#if loading}
  <p class="muted">Loading…</p>
{:else if error}
  <p class="err">{error}</p>
{:else if jobs.length === 0}
  <p class="muted">
    No downloads yet. On a movie or episode, open the Download menu and pick a
    smaller copy.
  </p>
{:else}
  <ul class="dl-list">
    {#each jobs as j (j.id)}
      <li class="dl-row">
        <div class="dl-info">
          <a class="dl-title" href="/watch/{j.item_id}">{j.title}</a>
          <span class="dl-meta">
            {j.quality_label}
            {#if j.status === 'ready' && j.size_bytes} · {formatBytes(j.size_bytes)}{/if}
            · <span class="dl-status dl-status-{j.status}">{downloadStatusText(j)}</span>
          </span>
          {#if isPreparing(j)}
            <div class="bar" role="progressbar" aria-label="Preparing {j.title}" aria-valuemin="0" aria-valuemax="100" aria-valuenow={progressPercent(j)}>
              <div style="width:{progressPercent(j)}%"></div>
            </div>
          {/if}
        </div>
        <div class="dl-actions">
          {#if j.status === 'ready' && j.file_url}
            <a class="btn" href={assetUrl(j.file_url)} download>Download</a>
          {/if}
          <button
            type="button"
            class="btn btn-quiet"
            disabled={removing[j.id]}
            aria-label="Remove {j.title} ({j.quality_label})"
            onclick={() => remove(j)}
          >
            {isPreparing(j) ? 'Cancel' : 'Remove'}
          </button>
        </div>
      </li>
    {/each}
  </ul>
{/if}

<style>
  .muted { color: var(--text-muted); font-size: 0.82rem; margin: 0; }
  .err { color: var(--error, #f87171); font-size: 0.82rem; margin: 0; }
  .dl-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 0.6rem; }
  .dl-row {
    display: flex; align-items: center; justify-content: space-between; gap: 0.75rem;
    padding: 0.55rem 0.7rem;
    border: 1px solid var(--border);
    border-radius: 8px;
  }
  .dl-info { display: flex; flex-direction: column; gap: 0.2rem; min-width: 0; flex: 1; }
  .dl-title {
    color: var(--text-primary); font-size: 0.86rem; font-weight: 600; text-decoration: none;
    overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  }
  .dl-title:hover { text-decoration: underline; }
  .dl-meta { color: var(--text-muted); font-size: 0.75rem; }
  .dl-status-failed { color: var(--error, #f87171); }
  .dl-status-ready { color: var(--success, #4ade80); }
  .bar { height: 3px; background: var(--border); border-radius: 2px; overflow: hidden; max-width: 260px; }
  .bar div { height: 100%; background: var(--accent); transition: width 300ms ease; }
  .dl-actions { display: flex; gap: 0.4rem; flex-shrink: 0; }
  .btn {
    padding: 0.3rem 0.7rem;
    border-radius: 6px;
    border: 1px solid var(--border-strong);
    background: var(--input-bg);
    color: var(--text-primary);
    font: inherit; font-size: 0.78rem;
    text-decoration: none;
    cursor: pointer;
  }
  .btn:hover:not(:disabled) { background: var(--bg-hover); }
  .btn:disabled { opacity: 0.5; cursor: not-allowed; }
  .btn-quiet { color: var(--text-muted); }
</style>
