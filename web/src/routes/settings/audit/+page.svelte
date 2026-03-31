<script lang="ts">
  import { onMount } from 'svelte';
  import { auditApi, type AuditLogEntry } from '$lib/api';

  let loading = true;
  let error = '';
  let entries: AuditLogEntry[] = [];
  let hasMore = true;
  let loadingMore = false;

  const PAGE_SIZE = 50;

  onMount(async () => {
    await loadEntries();
  });

  async function loadEntries() {
    loading = true;
    error = '';
    try {
      entries = await auditApi.list(PAGE_SIZE, 0);
      hasMore = entries.length === PAGE_SIZE;
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed to load audit log';
    } finally {
      loading = false;
    }
  }

  async function loadMore() {
    loadingMore = true;
    try {
      const more = await auditApi.list(PAGE_SIZE, entries.length);
      entries = [...entries, ...more];
      hasMore = more.length === PAGE_SIZE;
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed to load more entries';
    } finally {
      loadingMore = false;
    }
  }

  function formatTime(iso: string): string {
    try {
      const d = new Date(iso);
      return d.toLocaleString(undefined, {
        month: 'short', day: 'numeric',
        hour: '2-digit', minute: '2-digit', second: '2-digit'
      });
    } catch {
      return iso;
    }
  }

  function formatAction(action: string): string {
    return action.replace(/\./g, ' ').replace(/\b\w/g, c => c.toUpperCase());
  }

  function actionColor(action: string): string {
    if (action.includes('delete')) return 'action-danger';
    if (action.includes('login_failed')) return 'action-danger';
    if (action.includes('create') || action.includes('login_success')) return 'action-success';
    if (action.includes('role_change') || action.includes('password_reset')) return 'action-warn';
    return 'action-neutral';
  }

  function formatDetail(detail: any): string {
    if (!detail) return '';
    if (typeof detail === 'string') {
      try { detail = JSON.parse(detail); } catch { return detail; }
    }
    return Object.entries(detail)
      .map(([k, v]) => `${k}: ${v}`)
      .join(', ');
  }
</script>

<svelte:head><title>Audit Log — OnScreen</title></svelte:head>

<div class="page">

  {#if error}
    <div class="banner error">{error}</div>
  {/if}

  {#if loading}
    <div class="skeleton-block"></div>
  {:else if entries.length === 0}
    <div class="empty">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" width="40" height="40">
        <path stroke-linecap="round" stroke-linejoin="round" d="M9 12h3.75M9 15h3.75M9 18h3.75m3 .75H18a2.25 2.25 0 002.25-2.25V6.108c0-1.135-.845-2.098-1.976-2.192a48.424 48.424 0 00-1.123-.08m-5.801 0c-.065.21-.1.433-.1.664 0 .414.336.75.75.75h4.5a.75.75 0 00.75-.75 2.25 2.25 0 00-.1-.664m-5.8 0A2.251 2.251 0 0113.5 2.25H15a2.25 2.25 0 012.15 1.586m-5.8 0c-.376.023-.75.05-1.124.08C9.095 4.01 8.25 4.973 8.25 6.108V8.25m0 0H4.875c-.621 0-1.125.504-1.125 1.125v11.25c0 .621.504 1.125 1.125 1.125h9.75c.621 0 1.125-.504 1.125-1.125V9.375c0-.621-.504-1.125-1.125-1.125H8.25z"/>
      </svg>
      <p>No audit log entries yet</p>
      <p class="empty-sub">Security-relevant actions will appear here as they occur.</p>
    </div>
  {:else}
    <div class="table-wrap">
      <table>
        <thead>
          <tr>
            <th>Time</th>
            <th>Action</th>
            <th>Target</th>
            <th>Detail</th>
            <th>IP</th>
          </tr>
        </thead>
        <tbody>
          {#each entries as entry (entry.id)}
            <tr>
              <td class="col-time">{formatTime(entry.created_at)}</td>
              <td>
                <span class="action-badge {actionColor(entry.action)}">
                  {formatAction(entry.action)}
                </span>
              </td>
              <td class="col-target">{entry.target ?? ''}</td>
              <td class="col-detail">{formatDetail(entry.detail)}</td>
              <td class="col-ip">{entry.ip_addr ?? ''}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>

    {#if hasMore}
      <div class="load-more-wrap">
        <button class="btn-load-more" on:click={loadMore} disabled={loadingMore}>
          {loadingMore ? 'Loading...' : 'Load More'}
        </button>
      </div>
    {/if}
  {/if}
</div>

<style>
  .page { max-width: 900px; }

  .banner {
    padding: 0.6rem 0.9rem;
    border-radius: 8px;
    font-size: 0.8rem;
    margin-bottom: 1.25rem;
  }
  .banner.error { background: rgba(248,113,113,0.1); border: 1px solid rgba(248,113,113,0.2); color: #fca5a5; }

  .skeleton-block {
    height: 200px; border-radius: 10px;
    background: linear-gradient(90deg, var(--bg-elevated) 25%, var(--bg-hover) 50%, var(--bg-elevated) 75%);
    background-size: 200% 100%;
    animation: shimmer 1.4s infinite;
  }
  @keyframes shimmer { 0% { background-position: 200% 0; } 100% { background-position: -200% 0; } }

  .empty {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 0.6rem;
    padding: 3rem 1rem;
    color: var(--text-muted);
    text-align: center;
  }
  .empty p { font-size: 0.88rem; color: var(--text-muted); }
  .empty .empty-sub { font-size: 0.75rem; color: var(--text-muted); max-width: 320px; }

  .table-wrap {
    overflow-x: auto;
    border: 1px solid var(--border);
    border-radius: 10px;
    background: rgba(255,255,255,0.02);
  }

  table {
    width: 100%;
    border-collapse: collapse;
    font-size: 0.78rem;
  }

  thead {
    background: rgba(255,255,255,0.03);
  }

  th {
    text-align: left;
    padding: 0.65rem 0.85rem;
    font-size: 0.68rem;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.09em;
    color: var(--text-muted);
    border-bottom: 1px solid var(--border);
    white-space: nowrap;
  }

  td {
    padding: 0.6rem 0.85rem;
    color: var(--text-secondary);
    border-bottom: 1px solid rgba(255,255,255,0.03);
    vertical-align: top;
  }

  tr:last-child td { border-bottom: none; }

  .col-time {
    white-space: nowrap;
    color: var(--text-muted);
    font-size: 0.72rem;
  }

  .col-target {
    font-family: monospace;
    font-size: 0.72rem;
    color: #8888a0;
    word-break: break-all;
    max-width: 180px;
  }

  .col-detail {
    font-size: 0.72rem;
    color: var(--text-muted);
    max-width: 200px;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .col-ip {
    font-family: monospace;
    font-size: 0.72rem;
    color: var(--text-muted);
    white-space: nowrap;
  }

  .action-badge {
    display: inline-block;
    padding: 0.15rem 0.5rem;
    border-radius: 4px;
    font-size: 0.68rem;
    font-weight: 600;
    white-space: nowrap;
  }
  .action-success { background: rgba(52,211,153,0.1); color: #6ee7b7; }
  .action-danger  { background: rgba(248,113,113,0.1); color: #fca5a5; }
  .action-warn    { background: rgba(251,191,36,0.1);  color: #fcd34d; }
  .action-neutral { background: rgba(124,106,247,0.1); color: var(--accent-text); }

  .load-more-wrap {
    display: flex;
    justify-content: center;
    padding: 1.25rem 0;
  }

  .btn-load-more {
    padding: 0.42rem 1.2rem;
    background: transparent;
    border: 1px solid var(--border-strong);
    border-radius: 7px;
    color: var(--text-secondary);
    font-size: 0.8rem;
    font-weight: 500;
    cursor: pointer;
    transition: background 0.12s, border-color 0.12s;
  }
  .btn-load-more:hover { background: var(--bg-hover); border-color: var(--border-strong); }
  .btn-load-more:disabled { opacity: 0.5; cursor: not-allowed; }

  /* ── Mobile ────────────────────────────────────────────────────────────── */
  @media (max-width: 768px) {
    .page { max-width: 100%; }
  }
</style>
