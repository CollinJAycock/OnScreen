<script lang="ts">
  import { onDestroy, onMount, tick } from 'svelte';
  import { goto } from '$app/navigation';
  import {
    api,
    requestsApi,
    requestsAdminApi,
    type ApproveRequestBody,
    type MediaRequest,
    type RequestStatus,
  } from '$lib/api';
  import { confirmAction } from '$lib/native';
  import { toast } from '$lib/stores/toast';
  import { refreshPendingRequests } from '$lib/stores/pendingRequests';
  import { capabilities, ensureCapabilities } from '$lib/stores/capabilities';
  import { requestQuota, loadRequestQuota } from '$lib/stores/requestAccess';
  import { requestsUsable, upcomingTabVisible } from '$lib/featureGates';
  import { REQUESTS_DISABLED_MESSAGE } from '../search/quota';
  import Upcoming from './Upcoming.svelte';
  import ApproveDialog from './ApproveDialog.svelte';
  import DownloadStatus from './DownloadStatus.svelte';
  import { createArrOptionsLoader } from './approveOptions';
  import {
    createPoller, failureReason, hasActiveRequests, showsDownload, timeAgo,
  } from './requestRows';
  import { isPartiallyAvailable, seasonsProgressLabel } from './seasonProgress';

  // Discover used to live here as a third tab; it's now folded into /search
  // (search results show library hits + a "Request" section for TMDB
  // matches in one pass), so this page is for managing existing requests,
  // seeing what Radarr / Sonarr expect to arrive (Upcoming) and the admin
  // approval queue.
  //
  // Admins always get every tab. Everyone else gets My Requests and Upcoming
  // only when the server has requests on and their account may request, and
  // Upcoming only with an enabled Radarr or Sonarr (lib/featureGates); with
  // neither, the page just says requests aren't available — the sidebar
  // doesn't link here then, but the URL still works.
  type Tab = 'mine' | 'upcoming' | 'queue';

  let ready = false;
  let isAdmin = false;
  let activeTab: Tab = 'mine';

  $: canRequest = requestsUsable($capabilities, $requestQuota, isAdmin);
  $: showMine = isAdmin || canRequest;
  $: showUpcoming = upcomingTabVisible($capabilities, $requestQuota, isAdmin);

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

  // Approve-with-options dialog. The loader caches the arr instance list and
  // each instance's profiles / folders for the page's lifetime; the queue
  // also uses the list to name a request's preferred instance.
  const arrOptions = createArrOptionsLoader();
  let approveFor: MediaRequest | null = null;
  let approveReturnFocus: HTMLElement | null = null;
  let serviceNames = new Map<string, string>();

  // Reference time for relative dates and download ETAs; bumped on every load.
  let now = Date.now();
  // Only the latest load of each list may land (filter changes and polls race).
  let mineSeq = 0;
  let queueSeq = 0;

  // While a visible row is still searching / downloading, re-read the list
  // every 30 s — but only while the browser tab is visible.
  const poller = createPoller({
    isActive: () => hasActiveRequests(visibleItems()),
    tick: refreshVisible,
  });
  onDestroy(() => poller.stop());

  function visibleItems(): MediaRequest[] {
    if (activeTab === 'mine' && !mineLoading) return mineItems;
    if (activeTab === 'queue' && isAdmin && !queueLoading) return queueItems;
    return [];
  }

  async function refreshVisible() {
    if (activeTab === 'mine') await loadMine(true);
    else if (activeTab === 'queue' && isAdmin) await loadQueue(true);
  }

  // Re-arm (or disarm) polling whenever the visible list or tab changes.
  $: syncPolling(activeTab, mineItems, queueItems, mineLoading, queueLoading);
  function syncPolling(..._deps: unknown[]) {
    poller.sync();
  }

  onMount(async () => {
    const user = api.getUser();
    if (!user) { goto('/login'); return; }
    isAdmin = user.is_admin;
    // A non-admin's tabs depend on the server's flags and their allowance;
    // settle both before drawing anything, so no tab flashes up and goes.
    if (!isAdmin) await Promise.all([ensureCapabilities(), loadRequestQuota()]);
    ready = true;
    if (isAdmin || requestsUsable($capabilities, $requestQuota, false)) await loadMine();
    if (isAdmin) await loadQueue();
  });

  // ── My Requests ───────────────────────────────────────────────────────────

  /** silent = a background poll: no skeleton, and a failure keeps the list. */
  async function loadMine(silent = false) {
    const seq = ++mineSeq;
    if (!silent) { mineLoading = true; mineError = ''; }
    try {
      const params = mineFilter === 'all' ? {} : { status: mineFilter };
      const res = await requestsApi.list(params);
      if (seq !== mineSeq) return;
      mineItems = res.items;
      mineError = '';
      now = Date.now();
    } catch (e: unknown) {
      if (seq !== mineSeq || silent) return;
      mineError = e instanceof Error ? e.message : 'Failed to load requests';
    } finally {
      if (seq === mineSeq) mineLoading = false;
    }
  }

  async function cancelMine(req: MediaRequest) {
    if (processing.has(req.id)) return;
    markProcessing(req.id, true);
    try {
      await requestsApi.cancel(req.id);
      toast.success('Request cancelled');
      // An admin cancelling their own pending request shrinks the queue too.
      if (isAdmin) refreshPendingRequests(true);
      await loadMine();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Cancel failed');
    } finally {
      markProcessing(req.id, false);
    }
  }

  // ── Admin queue ───────────────────────────────────────────────────────────

  async function loadQueue(silent = false) {
    const seq = ++queueSeq;
    if (!silent) { queueLoading = true; queueError = ''; }
    if (!silent) void loadServiceNames();
    try {
      const params = queueFilter === 'all' ? {} : { status: queueFilter };
      const res = await requestsAdminApi.list(params);
      if (seq !== queueSeq) return;
      queueItems = res.items;
      queueError = '';
      now = Date.now();
    } catch (e: unknown) {
      if (seq !== queueSeq || silent) return;
      queueError = e instanceof Error ? e.message : 'Failed to load queue';
    } finally {
      if (seq === queueSeq) queueLoading = false;
    }
  }

  // Names for "Prefers <instance>" on queue rows. Best effort: without them
  // the row still says a specific instance was requested.
  async function loadServiceNames() {
    try {
      const list = await arrOptions.services();
      serviceNames = new Map(list.map((s) => [s.id, s.name]));
    } catch { /* keep whatever we had */ }
  }

  function markProcessing(id: string, on: boolean) {
    const next = new Set(processing);
    if (on) next.add(id); else next.delete(id);
    processing = next;
  }

  function openApprove(req: MediaRequest, e: MouseEvent) {
    if (processing.has(req.id)) return;
    approveReturnFocus = e.currentTarget as HTMLElement;
    approveFor = req;
  }

  async function closeApprove() {
    approveFor = null;
    const el = approveReturnFocus;
    approveReturnFocus = null;
    await tick();
    // After an approval the row may have left the list (Pending filter).
    if (el?.isConnected) el.focus();
  }

  async function approve(req: MediaRequest, body: ApproveRequestBody) {
    if (processing.has(req.id)) return;
    markProcessing(req.id, true);
    try {
      await requestsAdminApi.approve(req.id, body);
      toast.success(`Approved: ${req.title}`);
      refreshPendingRequests(true); // nav badge
      if (approveFor?.id === req.id) void closeApprove();
      await loadQueue();
    } catch (e: unknown) {
      // The dialog stays open so a bad profile / folder can be changed.
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
      refreshPendingRequests(true); // nav badge
      await loadQueue();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Decline failed');
    } finally {
      markProcessing(target.id, false);
    }
  }

  async function adminDelete(req: MediaRequest) {
    if (processing.has(req.id)) return;
    if (!(await confirmAction(`Delete request for "${req.title}"? This does not affect the upstream arr instance.`))) return;
    markProcessing(req.id, true);
    try {
      await requestsAdminApi.del(req.id);
      toast.success('Request deleted');
      refreshPendingRequests(true); // nav badge
      await loadQueue();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Delete failed');
    } finally {
      markProcessing(req.id, false);
    }
  }

  // Filters read the element's value on change (rather than bind:value) so
  // the handler always sees the new filter.
  function onMineFilter(e: Event) {
    mineFilter = (e.currentTarget as HTMLSelectElement).value as RequestStatus | 'all';
    loadMine();
  }

  function onQueueFilter(e: Event) {
    queueFilter = (e.currentTarget as HTMLSelectElement).value as RequestStatus | 'all';
    loadQueue();
  }

  function statusLabel(s: string): string {
    return s.charAt(0).toUpperCase() + s.slice(1);
  }
</script>

<svelte:head><title>Request — OnScreen</title></svelte:head>

{#if ready}
<div class="page">
  <h1 class="page-title">Requests</h1>

  {#if !showMine}
    <div class="empty" data-testid="requests-unavailable">
      <p>
        {$capabilities?.features?.requests === true
          ? REQUESTS_DISABLED_MESSAGE
          : "Requests aren't available on this server."}
      </p>
    </div>
  {:else}
    <div class="tabs">
      <button class="tab" class:active={activeTab === 'mine'} on:click={() => { activeTab = 'mine'; loadMine(); }}>
        My Requests
      </button>
      {#if showUpcoming}
        <button class="tab" class:active={activeTab === 'upcoming'} on:click={() => { activeTab = 'upcoming'; }}>
          Upcoming
        </button>
      {/if}
      {#if isAdmin}
        <button class="tab" class:active={activeTab === 'queue'} on:click={() => { activeTab = 'queue'; loadQueue(); }}>
          Queue
        </button>
      {/if}
      {#if canRequest}
        <span class="hint">
          To request something new, use <a href="/search">Search</a>.
        </span>
      {/if}
    </div>
  {/if}

  {#if activeTab === 'mine' && showMine}
    <div class="filter-row">
      <select value={mineFilter} on:change={onMineFilter} aria-label="Filter my requests by status">
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
      <div class="empty"><p>No requests yet — head to <a href="/search">Search</a> to request something.</p></div>
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
              {#if isPartiallyAvailable(req)}<span class="partial-pill">Partially available</span>{/if}
              {#if req.auto_approved}<span class="auto-pill" title="Approved automatically when it was requested">Auto-approved</span>{/if}
            </div>
            {#if req.overview}<div class="row-overview">{req.overview}</div>{/if}
            {#if req.download && showsDownload(req)}
              <DownloadStatus download={req.download} {now} />
            {/if}
            {#if failureReason(req)}
              <div class="row-meta failure-reason">{failureReason(req)}</div>
            {/if}
            {#if req.status === 'declined' && req.decline_reason}
              <div class="row-meta decline-reason">Reason: {req.decline_reason}</div>
            {/if}
            <div class="row-meta">
              <span title={new Date(req.created_at).toLocaleString()}>Requested {timeAgo(req.created_at, now)}</span>
              {#if req.type === 'show'}<span class="sep">·</span><span>{seasonsProgressLabel(req)}</span>{/if}
            </div>
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

  {#if activeTab === 'upcoming' && showUpcoming}
    <Upcoming {isAdmin} />
  {/if}

  {#if activeTab === 'queue' && isAdmin}
    <div class="filter-row">
      <select value={queueFilter} on:change={onQueueFilter} aria-label="Filter the queue by status">
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
              {#if isPartiallyAvailable(req)}<span class="partial-pill">Partially available</span>{/if}
              {#if req.auto_approved}<span class="auto-pill" title="Approved automatically when it was requested">Auto-approved</span>{/if}
            </div>
            {#if req.overview}<div class="row-overview">{req.overview}</div>{/if}
            {#if req.download && showsDownload(req)}
              <DownloadStatus download={req.download} {now} />
            {/if}
            {#if failureReason(req)}
              <div class="row-meta failure-reason">{failureReason(req)}</div>
            {/if}
            {#if req.status === 'declined' && req.decline_reason}
              <div class="row-meta decline-reason">Reason: {req.decline_reason}</div>
            {/if}
            <div class="row-meta">
              {#if req.username}
                <span class="requester">Requested by <strong>{req.username}</strong></span>
                <span class="sep">·</span>
                <span title={new Date(req.created_at).toLocaleString()}>{timeAgo(req.created_at, now)}</span>
              {:else}
                <span title={new Date(req.created_at).toLocaleString()}>Requested {timeAgo(req.created_at, now)}</span>
              {/if}
              {#if req.type === 'show'}<span class="sep">·</span><span>{seasonsProgressLabel(req)}</span>{/if}
              {#if req.requested_service_id}
                <span class="sep">·</span>
                <span>{serviceNames.has(req.requested_service_id) ? `Prefers ${serviceNames.get(req.requested_service_id)}` : 'Prefers a specific instance'}</span>
              {/if}
            </div>
          </div>
          <div class="row-actions">
            {#if req.status === 'pending'}
              <button class="btn primary sm" on:click={(e) => openApprove(req, e)} disabled={processing.has(req.id)}>
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
        <!-- svelte-ignore a11y_click_events_have_key_events -->
        <div class="modal" on:click|stopPropagation role="dialog" aria-modal="true" tabindex="-1" aria-label="Decline request">
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

    {#if approveFor}
      {@const target = approveFor}
      {#key target.id}
        <ApproveDialog
          request={target}
          loader={arrOptions}
          busy={processing.has(target.id)}
          onapprove={(body) => approve(target, body)}
          oncancel={closeApprove}
        />
      {/key}
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

  .tabs {
    display: flex;
    gap: 0.4rem;
    align-items: center;
    border-bottom: 1px solid var(--border);
    margin-bottom: 1.5rem;
  }
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
  .hint {
    margin-left: auto;
    margin-bottom: 0.55rem;
    font-size: 0.78rem;
    color: var(--text-muted);
  }
  .hint a { color: var(--accent-text); text-decoration: none; }
  .hint a:hover { text-decoration: underline; }

  select, textarea {
    background: var(--input-bg);
    border: 1px solid var(--border-strong);
    border-radius: 7px;
    padding: 0.5rem 0.75rem;
    font-size: 0.85rem;
    color: var(--text-primary);
    width: 100%;
  }
  textarea { resize: vertical; min-height: 70px; margin-bottom: 1rem; font-family: inherit; }
  select:focus, textarea:focus {
    outline: none; border-color: var(--accent); box-shadow: 0 0 0 3px var(--accent-bg);
  }

  .filter-row { display: flex; gap: 0.6rem; align-items: center; margin-bottom: 1rem; }
  .filter-row select { width: auto; min-width: 160px; }

  .banner { padding: 0.6rem 0.9rem; border-radius: 8px; font-size: 0.8rem; margin-bottom: 1.25rem; }
  .banner.error { background: rgba(248,113,113,0.1); border: 1px solid rgba(248,113,113,0.2); color: #fca5a5; }

  .skeleton-block {
    background: linear-gradient(90deg, var(--bg-elevated) 25%, var(--bg-hover) 50%, var(--bg-elevated) 75%);
    background-size: 200% 100%;
    animation: shimmer 1.4s infinite;
    border-radius: 8px;
    height: 80px;
    margin-bottom: 0.75rem;
  }
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
    display: -webkit-box; -webkit-line-clamp: 2; line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden;
  }
  .row-meta { font-size: 0.7rem; color: var(--text-muted); display: flex; flex-wrap: wrap; column-gap: 0.35rem; row-gap: 0.1rem; }
  .row-meta + .row-meta { margin-top: 0.15rem; }
  .requester strong { color: var(--text-secondary); font-weight: 600; }
  .decline-reason { color: #fca5a5; }
  .failure-reason { color: var(--error); font-size: 0.72rem; overflow-wrap: anywhere; }
  .row-actions { display: flex; flex-direction: column; gap: 0.4rem; align-items: stretch; flex-shrink: 0; min-width: 100px; }

  .status-pill {
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
  /* Outlined rather than filled so it reads as a note on the status, not a
     second status. */
  .partial-pill {
    font-size: 0.65rem;
    font-weight: 700;
    padding: 0.12rem 0.5rem;
    border-radius: 10px;
    background: rgba(52,211,153,0.12);
    color: #6ee7b7;
    text-transform: uppercase;
    letter-spacing: 0.05em;
  }
  .auto-pill {
    font-size: 0.65rem;
    font-weight: 600;
    padding: 0.12rem 0.5rem;
    border-radius: 10px;
    border: 1px solid var(--border-strong);
    color: var(--text-secondary);
    text-transform: uppercase;
    letter-spacing: 0.05em;
  }

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
    /* Three tabs leave no room for the hint beside them — lift it above. */
    .tabs { flex-wrap: wrap; }
    .hint { order: -1; flex-basis: 100%; margin-left: 0; }
    .row { flex-direction: column; }
    .row-poster { width: 80px; }
    .row-actions { flex-direction: row; min-width: 0; }
  }
</style>
