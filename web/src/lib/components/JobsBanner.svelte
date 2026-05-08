<script lang="ts">
  // Compact status banner — renders only when there's actually
  // something the user might want to know about (a scan running or
  // matched-but-artless items). Reads the polling store; the store
  // owns the fetch cadence so multiple mounts don't multiply traffic.
  import { jobsStatus } from '$lib/stores/jobs';

  $: status = $jobsStatus;
  $: scans = status?.scans ?? [];
  $: missingArt = status?.missing_art_count ?? 0;
  $: unmatched = status?.unmatched_count ?? 0;
  $: visible = scans.length > 0 || missingArt > 0 || unmatched > 0;
  $: scanLabel = scans
    .map((s) => s.library_name || 'a library')
    .join(', ');
</script>

{#if visible}
  <div class="jobs-banner" role="status">
    {#if scans.length > 0}
      <span class="dot scan" aria-hidden="true"></span>
      <span class="msg">
        Scanning <strong>{scanLabel}</strong>{scans.length === 1 ? '' : ` (${scans.length})`}…
      </span>
    {:else if missingArt > 0}
      <span class="dot art" aria-hidden="true"></span>
      <span class="msg">
        Filling in artwork — <strong>{missingArt}</strong> {missingArt === 1 ? 'item' : 'items'} pending. Auto-retry every 2&nbsp;hours.
      </span>
    {/if}
    {#if unmatched > 0 && scans.length === 0 && missingArt === 0}
      <span class="dot warn" aria-hidden="true"></span>
      <span class="msg">
        <strong>{unmatched}</strong> {unmatched === 1 ? 'item needs' : 'items need'} a manual match. <a href="/settings/unmatched">Review →</a>
      </span>
    {/if}
  </div>
{/if}

<style>
  .jobs-banner {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.4rem 0.85rem;
    background: var(--bg-hover, rgba(255, 255, 255, 0.04));
    border-bottom: 1px solid var(--border, rgba(255, 255, 255, 0.08));
    color: var(--text-muted, #aaaab8);
    font-size: 0.78rem;
    line-height: 1.4;
  }
  .msg strong { color: var(--text-primary, #fff); font-weight: 600; }
  .dot {
    width: 8px; height: 8px; border-radius: 50%; flex-shrink: 0;
    box-shadow: 0 0 0 0 currentColor;
  }
  .dot.scan { background: #4f8df0; animation: pulse 1.6s ease-in-out infinite; }
  .dot.art  { background: #f0c14f; }
  .dot.warn { background: #f08080; }
  @keyframes pulse {
    0%, 100% { box-shadow: 0 0 0 0 rgba(79, 141, 240, 0.5); }
    50%      { box-shadow: 0 0 0 6px rgba(79, 141, 240, 0); }
  }
  a { color: var(--accent-text, #9aa9ff); text-decoration: none; }
  a:hover { text-decoration: underline; }
</style>
