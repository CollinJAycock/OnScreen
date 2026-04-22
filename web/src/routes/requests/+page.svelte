<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import {
    api,
    discoverApi,
    requestsApi,
    requestsAdminApi,
    type DiscoverItem,
    type MediaRequest,
    type RequestStatus,
  } from '$lib/api';
  import { toast } from '$lib/stores/toast';

  type Tab = 'discover' | 'mine' | 'queue';

  let ready = false;
  let isAdmin = false;
  let activeTab: Tab = 'discover';

  // Discover state
  let query = '';
  let searching = false;
  let searchError = '';
  let results: DiscoverItem[] = [];
  let creatingFor = new Set<number>(); // tmdb_ids in flight
  let searchTimer: ReturnType<typeof setTimeout> | null = null;

  // My Requests state
  let mineLoading = true;
  let mineError = '';
  let mineFilter: RequestStatus | 'all' = 'all';
  let mineItems: MediaRequest[] = [];

  // Admin queue state
  let queueLoading = true;
  let queueError = '';
  let queueFilter: RequestStatus | 'all' = 'pending';
  let queueItems: MediaRequest[] = [];
  let declineFor: MediaRequest | null = null;
  let declineReason = '';
  // Per-request in-flight guard. Without this, a second click before the
  // first response lands re-fires Approve against a no-longer-pending row
  // and surfaces "request is no longer pending" as a spurious error toast.
  let processing = new Set<string>();

  onMount(async () => {
    const user = api.getUser();
    if (!user) { goto('/login'); return; }
    isAdmin = user.is_admin;
    ready = true;
    await loadMine();
    if (isAdmin) await loadQueue();
  });

  // ── Discover ──────────────────────────────────────────────────────────────

  function debounceSearch() {
    if (searchTimer) clearTimeout(searchTimer);
    if (!query.trim()) { results = []; searchError = ''; return; }
    searchTimer = setTimeout(runSearch, 300);
  }

  async function runSearch() {
    searching = true;
    searchError = '';
    try {
      results = await discoverApi.search(query.trim(), 24);
    } catch (e: unknown) {
      searchError = e instanceof Error ? e.message : 'Search failed';
      results = [];
    } finally {
      searching = false;
    }
  }

  async function requestItem(item: DiscoverItem) {
    creatingFor = new Set(creatingFor).add(item.tmdb_id);
    try {
      const created = await requestsApi.create({ type: item.type, tmdb_id: item.tmdb_id });
      toast.success(`Requested: ${item.title}`);
      // Mirror the returned state into the result row so the card flips immediately.
      results = results.map(r =>
        r.tmdb_id === item.tmdb_id && r.type === item.type
          ? { ...r, has_active_request: true, active_request_id: created.id, active_request_status: created.status }
          : r,
      );
      // Refresh My Requests in the background so the count is correct when the user switches tab.
      loadMine();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Request failed');
    } finally {
      const next = new Set(creatingFor);
      next.delete(item.tmdb_id);
      creatingFor = next;
    }
  }

  // ── My Requests ───────────────────────────────────────────────────────────

  async function loadMine() {
    mineLoading = true; mineError = '';
    try {
      const params = mineFilter === 'all' ? {} : { status: mineFilter };
      const res = await requestsApi.list(params);
      mineItems = res.items;
    } catch (e: unknown) {
      mineError = e instanceof Error ? e.message : 'Failed to load requests';
    } finally {
      mineLoading = false;
    }
  }

  async function cancelMine(req: MediaRequest) {
    if (processing.has(req.id)) return;
    markProcessing(req.id, true);
    try {
      await requestsApi.cancel(req.id);
      toast.success('Request cancelled');
      await loadMine();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Cancel failed');
    } finally {
      markProcessing(req.id, false);
    }
  }

  // ── Admin queue ───────────────────────────────────────────────────────────

  async function loadQueue() {
    queueLoading = true; queueError = '';
    try {
      const params = queueFilter === 'all' ? {} : { status: queueFilter };
      const res = await requestsAdminApi.list(params);
      queueItems = res.items;
    } catch (e: unknown) {
      queueError = e instanceof Error ? e.message : 'Failed to load queue';
    } finally {
      queueLoading = false;
    }
  }

  function markProcessing(id: string, on: boolean) {
    const next = new Set(processing);
    if (on) next.add(id); else next.delete(id);
    processing = next;
  }

  async function approve(req: MediaRequest) {
    if (processing.has(req.id)) return;
    markProcessing(req.id, true);
    try {
      await requestsAdminApi.approve(req.id);
      toast.success(`Approved: ${req.title}`);
      await loadQueue();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Approve failed');
    } finally {
      markProcessing(req.id, false);
    }
  }

  function openDecline(req: MediaRequest) {
    declineFor = req;
    declineReason = '';
  }

  async function confirmDecline() {
    if (!declineFor) return;
    const target = declineFor;
    if (processing.has(target.id)) return;
    markProcessing(target.id, true);
    try {
      await requestsAdminApi.decline(target.id, declineReason);
      toast.success(`Declined: ${target.title}`);
      declineFor = null;
      await loadQueue();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Decline failed');
    } finally {
      markProcessing(target.id, false);
    }
  }

  async function adminDelete(req: MediaRequest) {
    if (processing.has(req.id)) return;
    if (!confirm(`Delete request for "${req.title}"? This does not affect the upstream arr instance.`)) return;
    markProcessing(req.id, true);
    try {
      await requestsAdminApi.del(req.id);
      toast.success('Request deleted');
      await loadQueue();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Delete failed');
    } finally {
      markProcessing(req.id, false);
    }
  }

  function statusLabel(s: string): string {
    return s.charAt(0).toUpperCase() + s.slice(1);
  }
</script>

<svelte:head><title>Request — OnScreen</title></svelte:head>

{#if ready}
<div class="page">
  <h1 class="page-title">Request</h1>

  <div class="tabs">
    <button class="tab" class:active={activeTab === 'discover'} on:click={() => (activeTab = 'discover')}>
      Discover
    </button>
    <button class="tab" class:active={activeTab === 'mine'} on:click={() => { activeTab = 'mine'; loadMine(); }}>
      My Requests
    </button>
    {#if isAdmin}
      <button class="tab" class:active={activeTab === 'queue'} on:click={() => { activeTab = 'queue'; loadQueue(); }}>
        Queue
      </button>
    {/if}
  </div>

  {#if activeTab === 'discover'}
    <div class="search-row">
      <input
        type="search"
        bind:value={query}
        on:input={debounceSearch}
        placeholder="Search TMDB for a movie or show…"
        autocomplete="off"
        spellcheck="false"
      />
    </div>

    {#if searchError}
      <div class="banner error">{searchError}</div>
    {/if}

    {#if searching}
      <div class="grid">
        {#each [1,2,3,4,5,6,7,8] as _}
          <div class="skeleton-tile"></div>
        {/each}
      </div>
    {:else if results.length === 0 && query.trim()}
      <div class="empty">
        <p>No matches for "{query.trim()}"</p>
      </div>
    {:else if results.length === 0}
      <div class="empty">
        <p>Search TMDB to find something to request.</p>
      </div>
    {:else}
      <div class="grid">
        {#each results as it (it.type + ':' + it.tmdb_id)}
          <div class="tile">
            <div class="poster">
              {#if it.poster_url}
                <img src={it.poster_url} alt={it.title} loading="lazy" />
              {:else}
                <div class="poster-blank">{it.title[0]?.toUpperCase() ?? '?'}</div>
              {/if}
              <span class="type-pill type-{it.type}">{it.type === 'show' ? 'Show' : 'Movie'}</span>
              {#if it.in_library}
                <span class="library-check" title="In your library" aria-label="In your library">
                  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round">
                    <polyline points="20 6 9 17 4 12"></polyline>
                  </svg>
                </span>
              {/if}
            </div>
            <div class="tile-title" title={it.title}>{it.title}</div>
            {#if it.year}<div class="tile-year">{it.year}</div>{/if}

            {#if it.in_library && it.library_item_id}
              <a class="btn pill in-library" href="/watch/{it.library_item_id}">In library</a>
            {:else if it.has_active_request}
              <span class="btn pill status-{it.active_request_status ?? 'pending'}">
                {statusLabel(it.active_request_status ?? 'pending')}
              </span>
            {:else}
              <button
                class="btn primary"
                disabled={creatingFor.has(it.tmdb_id)}
                on:click={() => requestItem(it)}
              >
                {creatingFor.has(it.tmdb_id) ? 'Requesting…' : 'Request'}
              </button>
            {/if}
          </div>
        {/each}
      </div>
    {/if}
  {/if}

  {#if activeTab === 'mine'}
    <div class="filter-row">
      <select bind:value={mineFilter} on:change={loadMine}>
        <option value="all">All</option>
        <option value="pending">Pending</option>
        <option value="approved">Approved</option>
        <option value="downloading">Downloading</option>
        <option value="available">Available</option>
        <option value="declined">Declined</option>
        <option value="failed">Failed</option>
      </select>
    </div>

    {#if mineError}
      <div class="banner error">{mineError}</div>
    {/if}

    {#if mineLoading}
      <div class="skeleton-block"></div>
    {:else if mineItems.length === 0}
      <div class="empty"><p>No requests yet — head to Discover to request something.</p></div>
    {:else}
      {#each mineItems as req (req.id)}
        <div class="row">
          <div class="row-poster">
            {#if req.poster_url}<img src={req.poster_url} alt={req.title} loading="lazy" />{/if}
          </div>
          <div class="row-body">
            <div class="row-title">
              {req.title}
              {#if req.year}<span class="row-year">({req.year})</span>{/if}
              <span class="status-pill status-{req.status}">{statusLabel(req.status)}</span>
            </div>
            {#if req.overview}<div class="row-overview">{req.overview}</div>{/if}
            {#if req.status === 'declined' && req.decline_reason}
              <div class="row-meta decline-reason">Reason: {req.decline_reason}</div>
            {/if}
            <div class="row-meta">Requested {new Date(req.created_at).toLocaleString()}</div>
          </div>
          <div class="row-actions">
            {#if req.status === 'available' && req.fulfilled_item_id}
              <a class="btn primary sm" href="/watch/{req.fulfilled_item_id}">Watch</a>
            {/if}
            {#if req.status === 'pending'}
              <button class="btn ghost sm" on:click={() => cancelMine(req)} disabled={processing.has(req.id)}>
                {processing.has(req.id) ? 'Cancelling…' : 'Cancel'}
              </button>
            {/if}
          </div>
        </div>
      {/each}
    {/if}
  {/if}

  {#if activeTab === 'queue' && isAdmin}
    <div class="filter-row">
      <select bind:value={queueFilter} on:change={loadQueue}>
        <option value="pending">Pending</option>
        <option value="approved">Approved</option>
        <option value="downloading">Downloading</option>
        <option value="available">Available</option>
        <option value="declined">Declined</option>
        <option value="failed">Failed</option>
        <option value="all">All</option>
      </select>
      <a class="btn ghost sm" href="/settings/arr-services">Manage arr services</a>
    </div>

    {#if queueError}
      <div class="banner error">{queueError}</div>
    {/if}

    {#if queueLoading}
      <div class="skeleton-block"></div>
    {:else if queueItems.length === 0}
      <div class="empty"><p>Nothing in the queue.</p></div>
    {:else}
      {#each queueItems as req (req.id)}
        <div class="row">
          <div class="row-poster">
            {#if req.poster_url}<img src={req.poster_url} alt={req.title} loading="lazy" />{/if}
          </div>
          <div class="row-body">
            <div class="row-title">
              {req.title}
              {#if req.year}<span class="row-year">({req.year})</span>{/if}
              <span class="status-pill status-{req.status}">{statusLabel(req.status)}</span>
            </div>
            {#if req.overview}<div class="row-overview">{req.overview}</div>{/if}
            <div class="row-meta">
              Requested {new Date(req.created_at).toLocaleString()}
              {#if req.requested_service_id}· requested service preference{/if}
            </div>
          </div>
          <div class="row-actions">
            {#if req.status === 'pending'}
              <button class="btn primary sm" on:click={() => approve(req)} disabled={processing.has(req.id)}>
                {processing.has(req.id) ? 'Approving…' : 'Approve'}
              </button>
              <button class="btn ghost sm" on:click={() => openDecline(req)} disabled={processing.has(req.id)}>
                Decline
              </button>
            {/if}
            <button class="btn danger sm" on:click={() => adminDelete(req)} disabled={processing.has(req.id)}>
              Delete
            </button>
          </div>
        </div>
      {/each}
    {/if}

    {#if declineFor}
      <div class="modal-overlay" on:click={() => (declineFor = null)} on:keydown={e => e.key === 'Escape' && (declineFor = null)} role="button" tabindex="-1">
        <div class="modal" on:click|stopPropagation role="dialog" aria-label="Decline request">
          <p class="modal-text">Decline "{declineFor.title}"?</p>
          <textarea bind:value={declineReason} placeholder="Optional reason — shown to the requester" rows="3"></textarea>
          <div class="modal-actions">
            <button class="btn ghost sm" on:click={() => (declineFor = null)}>Cancel</button>
            <button class="btn danger sm" on:click={confirmDecline} disabled={processing.has(declineFor.id)}>
              {processing.has(declineFor.id) ? 'Declining…' : 'Decline'}
            </button>
          </div>
        </div>
      </div>
    {/if}
  {/if}
</div>
{/if}

<style>
  .page { padding: 2.5rem 2.5rem 4rem; max-width: 1200px; }
  .page-title {
    font-size: 1.1rem;
    font-weight: 700;
    color: var(--text-primary);
    letter-spacing: -0.02em;
    margin-bottom: 1.25rem;
  }

  .tabs { display: flex; gap: 0.4rem; border-bottom: 1px solid var(--border); margin-bottom: 1.5rem; }
  .tab {
    background: transparent;
    border: none;
    color: var(--text-muted);
    padding: 0.55rem 1rem;
    font-size: 0.85rem;
    cursor: pointer;
    border-bottom: 2px solid transparent;
    margin-bottom: -1px;
  }
  .tab:hover { color: var(--text-secondary); }
  .tab.active { color: var(--text-primary); border-bottom-color: var(--accent); }

  .search-row { margin-bottom: 1.25rem; }
  input[type="search"], select, textarea {
    background: var(--input-bg);
    border: 1px solid var(--border-strong);
    border-radius: 7px;
    padding: 0.5rem 0.75rem;
    font-size: 0.85rem;
    color: var(--text-primary);
    width: 100%;
  }
  textarea { resize: vertical; min-height: 70px; margin-bottom: 1rem; font-family: inherit; }
  input[type="search"]:focus, select:focus, textarea:focus {
    outline: none; border-color: var(--accent); box-shadow: 0 0 0 3px var(--accent-bg);
  }

  .filter-row { display: flex; gap: 0.6rem; align-items: center; margin-bottom: 1rem; }
  .filter-row select { width: auto; min-width: 160px; }

  .banner { padding: 0.6rem 0.9rem; border-radius: 8px; font-size: 0.8rem; margin-bottom: 1.25rem; }
  .banner.error { background: rgba(248,113,113,0.1); border: 1px solid rgba(248,113,113,0.2); color: #fca5a5; }

  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(170px, 1fr));
    gap: 1.1rem;
  }
  .tile { display: flex; flex-direction: column; gap: 0.35rem; }
  .poster {
    aspect-ratio: 2 / 3;
    border-radius: 8px;
    overflow: hidden;
    background: var(--bg-elevated);
    position: relative;
  }
  .poster img { width: 100%; height: 100%; object-fit: cover; display: block; }
  .poster-blank {
    width: 100%; height: 100%;
    display: flex; align-items: center; justify-content: center;
    font-size: 2rem; color: var(--text-muted);
  }
  .type-pill {
    position: absolute;
    top: 0.4rem; left: 0.4rem;
    font-size: 0.6rem;
    padding: 0.15rem 0.45rem;
    border-radius: 10px;
    background: rgba(0,0,0,0.65);
    color: #fff;
    text-transform: uppercase;
    letter-spacing: 0.05em;
  }
  .library-check {
    position: absolute;
    top: 0.4rem; right: 0.4rem;
    width: 1.4rem; height: 1.4rem;
    display: flex; align-items: center; justify-content: center;
    border-radius: 50%;
    background: #16a34a;
    color: #fff;
    box-shadow: 0 0 0 2px rgba(0,0,0,0.55);
  }
  .library-check svg { width: 0.85rem; height: 0.85rem; }
  .tile-title {
    font-size: 0.85rem; font-weight: 500; color: var(--text-primary);
    overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  }
  .tile-year { font-size: 0.72rem; color: var(--text-muted); }

  .skeleton-tile, .skeleton-block {
    background: linear-gradient(90deg, var(--bg-elevated) 25%, var(--bg-hover) 50%, var(--bg-elevated) 75%);
    background-size: 200% 100%;
    animation: shimmer 1.4s infinite;
    border-radius: 8px;
  }
  .skeleton-tile { aspect-ratio: 2 / 3; }
  .skeleton-block { height: 80px; margin-bottom: 0.75rem; }
  @keyframes shimmer { 0% { background-position: 200% 0; } 100% { background-position: -200% 0; } }

  .btn {
    display: inline-flex; align-items: center; justify-content: center;
    padding: 0.45rem 0.8rem;
    font-size: 0.78rem;
    font-weight: 600;
    border-radius: 7px;
    cursor: pointer;
    border: 1px solid transparent;
    text-decoration: none;
    transition: background 0.12s, color 0.12s, border-color 0.12s;
    text-align: center;
    line-height: 1.1;
  }
  .btn.sm { padding: 0.35rem 0.65rem; font-size: 0.74rem; }
  .btn.primary { background: var(--accent); color: #fff; }
  .btn.primary:hover:not(:disabled) { background: var(--accent-hover); }
  .btn.ghost { background: transparent; border-color: var(--border-strong); color: var(--text-secondary); }
  .btn.ghost:hover { background: var(--bg-hover); }
  .btn.danger { background: rgba(248,113,113,0.12); color: #fca5a5; border-color: rgba(248,113,113,0.25); }
  .btn.danger:hover { background: rgba(248,113,113,0.22); }
  .btn:disabled { opacity: 0.55; cursor: not-allowed; }

  .pill { width: 100%; }
  .in-library { background: rgba(52,211,153,0.12); color: #6ee7b7; border-color: rgba(52,211,153,0.2); }

  .row {
    display: flex;
    gap: 0.9rem;
    padding: 0.85rem;
    border: 1px solid var(--border);
    border-radius: 10px;
    margin-bottom: 0.7rem;
    background: rgba(255,255,255,0.025);
  }
  .row-poster { width: 60px; flex-shrink: 0; aspect-ratio: 2/3; border-radius: 6px; overflow: hidden; background: var(--bg-elevated); }
  .row-poster img { width: 100%; height: 100%; object-fit: cover; display: block; }
  .row-body { flex: 1; min-width: 0; }
  .row-title { font-size: 0.92rem; font-weight: 600; color: var(--text-primary); display: flex; gap: 0.5rem; align-items: center; flex-wrap: wrap; }
  .row-year { color: var(--text-muted); font-weight: 400; }
  .row-overview {
    font-size: 0.78rem; color: var(--text-secondary);
    margin: 0.25rem 0;
    display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden;
  }
  .row-meta { font-size: 0.7rem; color: var(--text-muted); }
  .decline-reason { color: #fca5a5; }
  .row-actions { display: flex; flex-direction: column; gap: 0.4rem; align-items: stretch; flex-shrink: 0; min-width: 100px; }

  .status-pill, .pill {
    font-size: 0.65rem;
    font-weight: 600;
    padding: 0.18rem 0.55rem;
    border-radius: 10px;
    text-transform: uppercase;
    letter-spacing: 0.05em;
  }
  .status-pending      { background: rgba(251,191,36,0.15); color: #fcd34d; }
  .status-approved     { background: rgba(124,106,247,0.15); color: var(--accent-text); }
  .status-downloading  { background: rgba(96,165,250,0.15); color: #93c5fd; }
  .status-available    { background: rgba(52,211,153,0.15); color: #6ee7b7; }
  .status-declined     { background: rgba(248,113,113,0.15); color: #fca5a5; }
  .status-failed       { background: rgba(248,113,113,0.15); color: #fca5a5; }

  .empty { text-align: center; padding: 3rem 1rem; color: var(--text-muted); font-size: 0.85rem; }

  .modal-overlay {
    position: fixed; inset: 0; background: var(--shadow);
    display: flex; align-items: center; justify-content: center; z-index: 1000;
  }
  .modal {
    background: var(--bg-elevated);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 1.5rem;
    max-width: 420px; width: 90%;
  }
  .modal-text { font-size: 0.92rem; font-weight: 600; color: var(--text-primary); margin-bottom: 0.85rem; }
  .modal-actions { display: flex; justify-content: flex-end; gap: 0.6rem; }

  @media (max-width: 600px) {
    .page { padding: 1.25rem 1rem 3rem; }
    .grid { grid-template-columns: repeat(auto-fill, minmax(130px, 1fr)); gap: 0.75rem; }
    .row { flex-direction: column; }
    .row-poster { width: 80px; }
    .row-actions { flex-direction: row; min-width: 0; }
  }
</style>
