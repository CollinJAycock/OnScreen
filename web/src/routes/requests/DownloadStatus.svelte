<script lang="ts">
  // Live Radarr / Sonarr state for one request row: a chip ("Downloading
  // 42% · ~25 min"), a thin progress bar, and — for stalled / failed — the
  // reason the arr sync reported.
  import type { RequestDownload } from '$lib/api';
  import { downloadChip, formatBytes, timeAgo } from './requestRows';

  interface Props {
    download: RequestDownload;
    /** Reference time for the ETA, bumped by the page on every list load. */
    now?: number;
  }

  let { download, now = Date.now() }: Props = $props();

  const chip = $derived(downloadChip(download, now));
  const pct = $derived(chip.progress == null ? 0 : Math.round(chip.progress * 100));
  const size = $derived(formatBytes(download.size_bytes));
  const updated = $derived(timeAgo(download.updated_at, now));
  const title = $derived([size, updated && `updated ${updated}`].filter(Boolean).join(' · '));
</script>

<div class="dl tone-{chip.tone}">
  <div class="dl-line">
    <span class="dl-chip" title={title || undefined}>{chip.label}</span>
    {#if chip.progress != null}
      <div
        class="dl-bar"
        role="progressbar"
        aria-label="Download progress"
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={pct}
      >
        <span class="dl-fill" style:width="{pct}%"></span>
      </div>
    {/if}
  </div>
  {#if chip.message}
    <div class="dl-msg">{chip.message}</div>
  {/if}
</div>

<style>
  .dl { margin: 0.35rem 0 0.3rem; }
  .dl-line { display: flex; align-items: center; gap: 0.6rem; flex-wrap: wrap; }
  .dl-chip {
    display: inline-flex; align-items: center; gap: 0.35rem;
    font-size: 0.7rem; font-weight: 600;
    padding: 0.16rem 0.55rem; border-radius: 10px;
    white-space: nowrap;
    font-variant-numeric: tabular-nums;
  }
  .dl-chip::before {
    content: ''; width: 6px; height: 6px; border-radius: 50%; background: currentColor; flex-shrink: 0;
  }
  .dl-bar {
    flex: 1 1 120px; max-width: 260px; min-width: 80px;
    height: 4px; border-radius: 2px; overflow: hidden;
    background: var(--bg-hover);
  }
  .dl-fill { display: block; height: 100%; background: currentColor; border-radius: 2px; transition: width 0.4s ease; }
  .dl-msg { font-size: 0.72rem; margin-top: 0.25rem; overflow-wrap: anywhere; }

  .tone-info    .dl-chip { background: var(--info-bg); color: var(--info); }
  .tone-accent  .dl-chip { background: var(--accent-bg); color: var(--accent-text); }
  .tone-success .dl-chip { background: var(--success-bg); color: var(--success); }
  .tone-warn    .dl-chip { background: rgba(251,191,36,0.15); color: #fcd34d; }
  .tone-error   .dl-chip { background: var(--error-bg); color: var(--error); }

  .tone-info    .dl-bar { color: var(--info); }
  .tone-accent  .dl-bar { color: var(--accent); }
  .tone-success .dl-bar { color: var(--success); }
  .tone-warn    .dl-bar { color: #fbbf24; }
  .tone-error   .dl-bar { color: var(--error); }

  .tone-warn  .dl-msg { color: #fcd34d; }
  .tone-error .dl-msg { color: var(--error); }

  @media (prefers-reduced-motion: reduce) {
    .dl-fill { transition: none; }
  }
</style>
