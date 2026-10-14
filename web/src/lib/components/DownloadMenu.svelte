<script lang="ts">
  // The Download action on a title's page: a menu offering the original file
  // (GET /media/download/{file}, as before) or a converted, smaller copy
  // (POST /items/{id}/downloads). A converted copy is prepared on the server;
  // the button shows its progress, then starts the browser's download. The
  // job keeps going if the page is left — it is listed on the Account page.
  //
  // Qualities above the user's streaming limit (or, away from home, the
  // internet limit) are shown disabled, so the menu explains why 1080p is
  // missing instead of just not offering it.
  import { onDestroy } from 'svelte';
  import { assetUrl, downloadsApi, type DownloadJob, type DownloadQuality } from '$lib/api';
  import { isTauri, nativeDownload } from '$lib/native';
  import { toast } from '$lib/stores/toast';
  import {
    DOWNLOAD_POLL_MS,
    convertedFilename,
    downloadErrorMessage,
    isPreparing,
    progressPercent,
    startBrowserDownload,
  } from '$lib/downloads';

  interface Props {
    itemId: string;
    title: string;
    /** The file the original download serves (the primary version). */
    fileId: string;
    /** File-bound stream token: lets the original download work where no
     *  cookie travels (mobile download managers). */
    streamToken: string;
    container?: string;
  }

  let { itemId, title, fileId, streamToken, container }: Props = $props();

  const uid = Math.random().toString(36).slice(2, 8);

  let open = $state(false);
  let qualities = $state<DownloadQuality[] | null>(null);
  let loadError = $state('');
  let job = $state<DownloadJob | null>(null);
  let starting = $state(false);
  let pollTimer: ReturnType<typeof setTimeout> | null = null;
  let destroyed = false;

  let originalUrl = $derived(assetUrl(`/media/download/${fileId}?token=${encodeURIComponent(streamToken)}`));
  let preparing = $derived(job !== null && isPreparing(job));
  let buttonLabel = $derived(
    starting
      ? 'Starting…'
      : job && job.status === 'running'
        ? `Preparing ${progressPercent(job)}%`
        : job && job.status === 'queued'
          ? 'Waiting…'
          : 'Download',
  );

  async function loadOptions() {
    loadError = '';
    try {
      const opts = await downloadsApi.options(itemId, fileId);
      qualities = opts?.qualities ?? [];
    } catch (err) {
      qualities = [];
      loadError = downloadErrorMessage(err);
    }
  }

  function toggle(e: MouseEvent) {
    e.stopPropagation();
    open = !open;
    if (open && qualities === null) void loadOptions();
  }

  // Under Tauri the webview's <a download> doesn't open the OS save dialog,
  // so the native shim downloads it instead.
  async function saveNative(url: string, filename: string) {
    try {
      const path = await nativeDownload(url, filename);
      if (path) toast.success('Saved to ' + path);
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Download failed');
    }
  }

  function onOriginalClick(e: MouseEvent) {
    open = false;
    if (!isTauri()) return; // the anchor's download attribute does it
    e.preventDefault();
    const ext = container ? '.' + container.toLowerCase() : '';
    void saveNative(originalUrl, (title || 'download').replace(/[\\/:*?"<>|]/g, '_') + ext);
  }

  function deliver(j: DownloadJob) {
    if (!j.file_url) return;
    const url = assetUrl(j.file_url);
    if (isTauri()) {
      void saveNative(url, convertedFilename(j.title || title, j.quality_label));
    } else {
      startBrowserDownload(url);
      toast.success(`Downloading ${j.quality_label} copy`);
    }
  }

  function stopPolling() {
    if (pollTimer) clearTimeout(pollTimer);
    pollTimer = null;
  }

  function schedulePoll() {
    stopPolling();
    pollTimer = setTimeout(async () => {
      if (!job || destroyed) return;
      try {
        job = await downloadsApi.get(job.id);
      } catch {
        // Transient: try again on the next tick.
      }
      if (destroyed || !job) return;
      follow(job);
    }, DOWNLOAD_POLL_MS);
  }

  function follow(j: DownloadJob) {
    if (isPreparing(j)) {
      schedulePoll();
      return;
    }
    stopPolling();
    if (j.status === 'ready') {
      deliver(j);
    } else if (j.status === 'failed') {
      toast.error(j.error ? `Couldn't prepare the download: ${j.error}` : "Couldn't prepare the download");
    }
    job = null;
  }

  async function pick(q: DownloadQuality) {
    open = false;
    if (!q.allowed || preparing || starting) return;
    starting = true;
    try {
      const j = await downloadsApi.create(itemId, q.id, fileId);
      job = j;
      if (isPreparing(j)) {
        toast.success(`Preparing the ${q.label} copy — it will download when ready`);
      }
      follow(j);
    } catch (err) {
      toast.error(downloadErrorMessage(err));
    } finally {
      starting = false;
    }
  }

  function onWindowClick() {
    open = false;
  }

  onDestroy(() => {
    destroyed = true;
    stopPolling();
  });
</script>

<svelte:window onclick={onWindowClick} />

<div class="dl">
  <button
    type="button"
    class="dl-btn"
    class:busy={preparing || starting}
    aria-haspopup="menu"
    aria-expanded={open}
    aria-controls="dl-menu-{uid}"
    title="Download for offline viewing"
    onclick={toggle}
  >
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="14" height="14" aria-hidden="true">
      <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/>
      <polyline points="7 10 12 15 17 10"/>
      <line x1="12" y1="15" x2="12" y2="3"/>
    </svg>
    {buttonLabel}
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="10" height="10" aria-hidden="true"><polyline points="6 9 12 15 18 9"/></svg>
  </button>
  {#if preparing && job}
    <div class="dl-progress" aria-hidden="true"><div style="width:{progressPercent(job)}%"></div></div>
  {/if}

  {#if open}
    <!-- svelte-ignore a11y_click_events_have_key_events -->
    <div class="dl-menu" id="dl-menu-{uid}" role="menu" tabindex="-1" onclick={(e) => e.stopPropagation()}>
      <a class="dl-opt" role="menuitem" href={originalUrl} download onclick={onOriginalClick}>
        <span class="dl-name">Original</span>
        <span class="dl-note">the file as it is</span>
      </a>
      {#if qualities === null}
        <div class="dl-hint">Loading…</div>
      {:else}
        {#each qualities as q (q.id)}
          <button
            type="button"
            class="dl-opt"
            role="menuitem"
            disabled={!q.allowed || preparing || starting}
            title={q.allowed ? `Convert to ${q.label} and download` : 'Above your streaming limit'}
            onclick={() => pick(q)}
          >
            <span class="dl-name">{q.label}</span>
            {#if !q.allowed}<span class="dl-note">above your limit</span>{/if}
          </button>
        {/each}
        {#if loadError}
          <div class="dl-hint">{loadError}</div>
        {:else if qualities.length === 0}
          <div class="dl-hint">No smaller copies for this file</div>
        {/if}
      {/if}
    </div>
  {/if}
</div>

<style>
  .dl { position: relative; display: inline-block; margin-left: 0.6rem; }
  .dl-btn {
    display: inline-flex;
    align-items: center;
    gap: 0.4rem;
    padding: 0.5rem 0.9rem;
    border-radius: 4px;
    /* Theme tokens, like the Play on / Report a problem pills beside it:
       fixed white text vanished on the light theme. */
    border: 1px solid var(--border-strong);
    background: var(--input-bg);
    color: var(--text-secondary);
    font-size: 0.85rem;
    cursor: pointer;
    transition: background 120ms ease, border-color 120ms ease;
  }
  .dl-btn:hover { background: var(--bg-hover); color: var(--text-primary); }
  .dl-btn.busy { font-variant-numeric: tabular-nums; }
  .dl-progress {
    position: absolute; left: 2px; right: 2px; bottom: 1px; height: 2px;
    background: var(--border-strong); border-radius: 1px; overflow: hidden;
  }
  .dl-progress div { height: 100%; background: var(--accent, #7c6af7); transition: width 300ms ease; }
  .dl-menu {
    position: absolute;
    top: calc(100% + 4px);
    left: 0;
    z-index: 40;
    min-width: 210px;
    padding: 0.3rem;
    background: var(--bg-elevated, #1c1c22);
    border: 1px solid var(--border-strong, rgba(255, 255, 255, 0.16));
    border-radius: 8px;
    box-shadow: 0 8px 24px rgba(0, 0, 0, 0.45);
  }
  .dl-opt {
    display: flex;
    width: 100%;
    align-items: baseline;
    justify-content: space-between;
    gap: 0.75rem;
    padding: 0.45rem 0.6rem;
    border: none;
    border-radius: 5px;
    background: none;
    color: var(--text-primary, #eee);
    font: inherit;
    font-size: 0.82rem;
    text-align: left;
    text-decoration: none;
    cursor: pointer;
  }
  .dl-opt:hover:not(:disabled) { background: var(--bg-hover, rgba(255, 255, 255, 0.08)); }
  .dl-opt:disabled { opacity: 0.45; cursor: not-allowed; }
  .dl-note { color: var(--text-muted, #999); font-size: 0.72rem; }
  .dl-hint { padding: 0.4rem 0.6rem; color: var(--text-muted, #999); font-size: 0.75rem; }
</style>
