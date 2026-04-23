<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { liveTvApi, type LiveTVRecording, type LiveTVSchedule } from '$lib/api';
  import { toast } from '$lib/stores/toast';

  let recordings: LiveTVRecording[] = [];
  let schedules: LiveTVSchedule[] = [];
  let tab: 'upcoming' | 'recording' | 'completed' | 'rules' = 'upcoming';
  let loading = true;
  let error = '';
  let ready = false;
  let busyId = '';

  // Map status → friendly label for the row.
  const statusLabel: Record<LiveTVRecording['status'], string> = {
    scheduled: 'Scheduled',
    recording: 'Recording now',
    completed: 'Done',
    failed: 'Failed',
    cancelled: 'Cancelled',
    superseded: 'Superseded',
  };

  onMount(async () => {
    if (!localStorage.getItem('onscreen_user')) { goto('/login'); return; }
    ready = true;
    await load();
  });

  async function load() {
    loading = true; error = '';
    try {
      const [rRes, sRes] = await Promise.all([
        liveTvApi.listRecordings(),
        liveTvApi.listSchedules(),
      ]);
      recordings = rRes.items;
      schedules = sRes.items;
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed to load';
    } finally { loading = false; }
  }

  // Cheap client-side filter — total recordings list is typically <500
  // rows so no pagination needed for Phase B. List endpoint accepts a
  // status query param if we need server-side filtering later.
  $: filtered = (() => {
    switch (tab) {
      case 'upcoming': return recordings.filter(r => r.status === 'scheduled');
      case 'recording': return recordings.filter(r => r.status === 'recording');
      case 'completed': return recordings.filter(r => r.status === 'completed' || r.status === 'failed');
      default: return [];
    }
  })();

  async function cancel(r: LiveTVRecording) {
    if (!confirm(`Cancel recording "${r.title}"?`)) return;
    busyId = r.id;
    try {
      await liveTvApi.cancelRecording(r.id);
      toast.success('Cancelled');
      await load();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Cancel failed');
    } finally { busyId = ''; }
  }

  async function deleteSchedule(s: LiveTVSchedule) {
    if (!confirm('Delete this schedule? In-flight recordings it already queued will continue.')) return;
    busyId = s.id;
    try {
      await liveTvApi.deleteSchedule(s.id);
      toast.success('Schedule deleted');
      await load();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Delete failed');
    } finally { busyId = ''; }
  }

  function fmtTime(iso: string): string {
    const d = new Date(iso);
    const today = new Date();
    const isToday = d.toDateString() === today.toDateString();
    const isTomorrow = d.toDateString() === new Date(today.getTime() + 86400000).toDateString();
    const time = d.toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' });
    if (isToday) return `Today ${time}`;
    if (isTomorrow) return `Tomorrow ${time}`;
    return `${d.toLocaleDateString([], { weekday: 'short', month: 'short', day: 'numeric' })} ${time}`;
  }

  function scheduleLabel(s: LiveTVSchedule): string {
    if (s.type === 'once') return 'One-time';
    if (s.type === 'series') {
      const newOnly = s.new_only ? ' (new episodes only)' : '';
      return `Series: "${s.title_match ?? ''}"${newOnly}`;
    }
    if (s.type === 'channel_block') {
      return `Channel block ${s.time_start} – ${s.time_end}`;
    }
    return s.type;
  }
</script>

<svelte:head><title>Recordings - OnScreen</title></svelte:head>

{#if ready}
<div class="page">
  <div class="header">
    <h1>Recordings</h1>
    <a class="back-link" href="/tv/guide">← Guide</a>
  </div>

  <nav class="tabs" role="tablist">
    <button class="tab" class:active={tab === 'upcoming'} on:click={() => tab = 'upcoming'}>
      Upcoming ({recordings.filter(r => r.status === 'scheduled').length})
    </button>
    <button class="tab" class:active={tab === 'recording'} on:click={() => tab = 'recording'}>
      Recording now ({recordings.filter(r => r.status === 'recording').length})
    </button>
    <button class="tab" class:active={tab === 'completed'} on:click={() => tab = 'completed'}>
      Completed ({recordings.filter(r => r.status === 'completed' || r.status === 'failed').length})
    </button>
    <button class="tab" class:active={tab === 'rules'} on:click={() => tab = 'rules'}>
      Rules ({schedules.length})
    </button>
  </nav>

  {#if error}
    <div class="banner-error">{error}</div>
  {/if}

  {#if loading}
    <p class="muted">Loading…</p>
  {:else if tab === 'rules'}
    {#if schedules.length === 0}
      <p class="muted">No recording rules yet. Click Record on any program in the Guide to create a rule.</p>
    {:else}
      <ul class="rule-list">
        {#each schedules as s (s.id)}
          <li class="rule">
            <div>
              <div class="rule-label">{scheduleLabel(s)}</div>
              <div class="rule-meta">Priority {s.priority} · pre {s.padding_pre_sec}s / post {s.padding_post_sec}s {#if s.retention_days}· keep {s.retention_days}d{/if}</div>
            </div>
            <button class="btn btn-danger" disabled={busyId === s.id} on:click={() => deleteSchedule(s)}>Delete</button>
          </li>
        {/each}
      </ul>
    {/if}
  {:else if filtered.length === 0}
    <p class="muted">Nothing here yet.</p>
  {:else}
    <ul class="rec-list">
      {#each filtered as r (r.id)}
        <li class="rec" class:status-recording={r.status === 'recording'} class:status-failed={r.status === 'failed'}>
          <div class="rec-info">
            {#if r.channel_logo}
              <img class="logo" src={r.channel_logo} alt="" loading="lazy" />
            {:else}
              <div class="logo-blank">{r.channel_number}</div>
            {/if}
            <div class="rec-text">
              <div class="rec-title">
                {r.title}
                {#if r.season_num && r.episode_num}
                  <span class="ep">S{r.season_num}·E{r.episode_num}</span>
                {/if}
              </div>
              {#if r.subtitle}<div class="rec-sub">{r.subtitle}</div>{/if}
              <div class="rec-meta">
                <span class="status-chip">{statusLabel[r.status]}</span>
                {r.channel_name} · {fmtTime(r.starts_at)}
                {#if r.error}<div class="rec-err">Error: {r.error}</div>{/if}
              </div>
            </div>
          </div>
          <div class="rec-actions">
            {#if r.status === 'completed' && r.item_id}
              <a class="btn btn-primary" href="/watch/{r.item_id}">Watch</a>
            {/if}
            {#if r.status === 'scheduled' || r.status === 'recording'}
              <button class="btn btn-danger" disabled={busyId === r.id} on:click={() => cancel(r)}>Cancel</button>
            {/if}
          </div>
        </li>
      {/each}
    </ul>
  {/if}
</div>
{/if}

<style>
  .page { padding: 2rem 2.5rem; max-width: 1100px; margin: 0 auto; }
  .header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 1.25rem; }
  h1 { font-size: 1.15rem; font-weight: 700; margin: 0; color: var(--text-primary); }
  .back-link { color: var(--accent); text-decoration: none; font-size: 0.85rem; }

  .tabs { display: flex; gap: 0; border-bottom: 1px solid var(--border); margin-bottom: 1.5rem; }
  .tab {
    background: none; border: none; padding: 0.5rem 1rem;
    font-size: 0.8rem; color: var(--text-muted); cursor: pointer;
    border-bottom: 2px solid transparent;
  }
  .tab:hover { color: var(--text-secondary); }
  .tab.active { color: var(--accent-text); border-bottom-color: var(--accent); }

  .muted { color: var(--text-muted); padding: 2rem; text-align: center; }
  .banner-error { background: var(--error-bg); border: 1px solid var(--error); color: var(--error); padding: 0.65rem 1rem; border-radius: 6px; margin-bottom: 1rem; }

  .rec-list, .rule-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 0.5rem; }
  .rec, .rule {
    display: flex; justify-content: space-between; align-items: center; gap: 1rem;
    padding: 0.85rem 1rem;
    background: var(--bg-elevated);
    border: 1px solid var(--border);
    border-radius: 8px;
  }
  .rec.status-recording { border-color: #e74c3c; }
  .rec.status-failed { opacity: 0.7; }

  .rec-info { display: flex; align-items: center; gap: 0.75rem; min-width: 0; }
  .logo { width: 50px; height: 32px; object-fit: contain; background: #000; border-radius: 4px; flex-shrink: 0; }
  .logo-blank {
    width: 50px; height: 32px; display: flex; align-items: center; justify-content: center;
    font-weight: 700; font-size: 0.75rem; background: var(--bg); border-radius: 4px; flex-shrink: 0;
  }
  .rec-text { min-width: 0; }
  .rec-title { font-size: 0.92rem; color: var(--text-primary); font-weight: 600; display: flex; align-items: center; gap: 0.5rem; }
  .ep { font-size: 0.75rem; color: var(--text-muted); font-weight: 400; }
  .rec-sub { font-size: 0.78rem; color: var(--text-secondary); margin-top: 0.15rem; }
  .rec-meta { font-size: 0.75rem; color: var(--text-muted); margin-top: 0.25rem; }
  .rec-err { color: var(--error); margin-top: 0.2rem; }
  .status-chip {
    display: inline-block; padding: 0.05rem 0.4rem; margin-right: 0.4rem;
    background: rgba(255,255,255,0.06); border-radius: 3px;
    font-size: 0.68rem; text-transform: uppercase; font-weight: 600; letter-spacing: 0.03em;
  }
  .status-recording .status-chip { background: #e74c3c; color: white; }

  .rec-actions { display: flex; gap: 0.4rem; flex-shrink: 0; }

  .rule-label { font-size: 0.9rem; color: var(--text-primary); }
  .rule-meta { font-size: 0.72rem; color: var(--text-muted); margin-top: 0.2rem; }

  .btn {
    padding: 0.4rem 0.75rem; background: var(--bg);
    color: var(--text-primary); border: 1px solid var(--border);
    border-radius: 4px; font-size: 0.78rem; cursor: pointer; text-decoration: none;
  }
  .btn:hover:not(:disabled) { background: var(--bg-hover); }
  .btn:disabled { opacity: 0.5; cursor: not-allowed; }
  .btn-primary { background: var(--accent); color: white; border-color: var(--accent); }
  .btn-primary:hover:not(:disabled) { filter: brightness(1.1); }
  .btn-danger { color: var(--error); }
  .btn-danger:hover:not(:disabled) { background: var(--error-bg); }
</style>
