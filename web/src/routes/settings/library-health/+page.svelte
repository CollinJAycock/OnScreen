<script lang="ts">
  // Admin "Library health": the problem reports users filed with "Report a
  // problem", the files the integrity probe marked damaged, and the
  // Unmatched / Missing art counts. Each report can be resolved, dismissed,
  // or resolved by asking Radarr/Sonarr to re-grab the title (optionally
  // blocklisting the release it last grabbed). Resolving or dismissing
  // notifies the reporter.
  import { onMount } from 'svelte';
  import {
    assetUrl,
    issuesApi,
    type AdminMediaIssue,
    type DamagedFile,
    type IssueStatus,
    type LibraryHealth,
  } from '$lib/api';
  import { toast } from '$lib/stores/toast';
  import { issueKindLabel, relativeAge } from '$lib/reportProblem';
  import {
    ISSUE_FILTERS,
    canRegrab,
    damagedTitle,
    issueTitle,
    regrabErrorMessage,
    regrabResolutionNote,
    regrabSummary,
  } from './library-health';

  const ISSUE_PAGE = 50;
  const DAMAGED_PAGE = 100;

  let health = $state<LibraryHealth | null>(null);
  let healthError = $state('');
  let damaged = $state<DamagedFile[]>([]);
  let damagedTotal = $state(0);
  let damagedLoadingMore = $state(false);

  let filter = $state<IssueStatus | 'all'>('open');
  let issues = $state<AdminMediaIssue[]>([]);
  let issuesTotal = $state(0);
  let issuesLoading = $state(true);
  let issuesError = $state('');
  let issuesLoadingMore = $state(false);
  let issueSeq = 0;

  // Per-row UI state, keyed by issue id or file id.
  let notes = $state<Record<string, string>>({});
  let blocklist = $state<Record<string, boolean>>({});
  let busy = $state<Record<string, boolean>>({});
  let requested = $state<Record<string, boolean>>({});
  let now = $state(Date.now());

  function errText(e: unknown, fallback: string): string {
    return e instanceof Error && e.message ? e.message : fallback;
  }

  async function loadHealth() {
    healthError = '';
    try {
      const h = await issuesApi.libraryHealth({ limit: DAMAGED_PAGE });
      health = h;
      damaged = h.damaged_files ?? [];
      damagedTotal = h.damaged_total ?? damaged.length;
    } catch (e) {
      healthError = errText(e, 'Failed to load library health');
    }
  }

  async function loadMoreDamaged() {
    damagedLoadingMore = true;
    try {
      const h = await issuesApi.libraryHealth({ limit: DAMAGED_PAGE, offset: damaged.length });
      const seen = new Set(damaged.map((d) => d.file_id));
      damaged = [...damaged, ...(h.damaged_files ?? []).filter((d) => !seen.has(d.file_id))];
      damagedTotal = h.damaged_total;
    } catch (e) {
      toast.error(errText(e, 'Failed to load more damaged files'));
    } finally {
      damagedLoadingMore = false;
    }
  }

  async function loadIssues() {
    const seq = ++issueSeq;
    issuesLoading = true;
    issuesError = '';
    try {
      const res = await issuesApi.adminList({ status: filter, limit: ISSUE_PAGE });
      if (seq !== issueSeq) return;
      issues = res.items ?? [];
      issuesTotal = res.total ?? issues.length;
      now = Date.now();
    } catch (e) {
      if (seq !== issueSeq) return;
      issues = [];
      issuesError = errText(e, 'Failed to load problem reports');
    } finally {
      if (seq === issueSeq) issuesLoading = false;
    }
  }

  async function loadMoreIssues() {
    const seq = issueSeq;
    issuesLoadingMore = true;
    try {
      const res = await issuesApi.adminList({ status: filter, limit: ISSUE_PAGE, offset: issues.length });
      if (seq !== issueSeq) return;
      const seen = new Set(issues.map((i) => i.id));
      issues = [...issues, ...(res.items ?? []).filter((i) => !seen.has(i.id))];
      issuesTotal = res.total;
    } catch (e) {
      toast.error(errText(e, 'Failed to load more reports'));
    } finally {
      issuesLoadingMore = false;
    }
  }

  function setFilter(v: IssueStatus | 'all') {
    if (filter === v) return;
    filter = v;
    void loadIssues();
  }

  // Applies a closed report to the list: drop it from the Open view, update
  // it in place elsewhere, and keep the header count honest.
  function applyClosed(row: AdminMediaIssue, status: 'resolved' | 'dismissed', note: string | undefined) {
    if (filter === 'open') {
      issues = issues.filter((i) => i.id !== row.id);
      issuesTotal = Math.max(0, issuesTotal - 1);
    } else {
      issues = issues.map((i) =>
        i.id === row.id
          ? { ...i, status, resolution_note: note, resolved_at: new Date().toISOString() }
          : i,
      );
    }
    if (health) health = { ...health, open_issues: Math.max(0, health.open_issues - 1) };
  }

  async function closeIssue(row: AdminMediaIssue, status: 'resolved' | 'dismissed') {
    if (busy[row.id]) return;
    busy[row.id] = true;
    const note = (notes[row.id] ?? '').trim() || undefined;
    try {
      await issuesApi.close(row.id, status, note);
      applyClosed(row, status, note);
      toast.success(status === 'resolved' ? 'Report resolved — the reporter was notified.' : 'Report dismissed.');
    } catch (e) {
      toast.error(errText(e, 'Failed to update the report'));
    } finally {
      busy[row.id] = false;
    }
  }

  async function regrabIssue(row: AdminMediaIssue) {
    if (busy[row.id]) return;
    busy[row.id] = true;
    try {
      const result = await issuesApi.regrab(row.item_id, !!blocklist[row.id]);
      toast.success(regrabSummary(result));
      const note = regrabResolutionNote(result, notes[row.id] ?? '');
      try {
        await issuesApi.close(row.id, 'resolved', note);
        applyClosed(row, 'resolved', note);
      } catch (e) {
        toast.error(`Re-grab requested, but the report couldn't be resolved: ${errText(e, 'request failed')}`);
      }
    } catch (e) {
      toast.error(regrabErrorMessage(e));
    } finally {
      busy[row.id] = false;
    }
  }

  async function regrabFile(file: DamagedFile) {
    const key = file.file_id;
    if (busy[key]) return;
    busy[key] = true;
    try {
      const result = await issuesApi.regrab(file.item_id, !!blocklist[key]);
      requested[key] = true;
      toast.success(regrabSummary(result));
    } catch (e) {
      toast.error(regrabErrorMessage(e));
    } finally {
      busy[key] = false;
    }
  }

  function posterSrc(path: string): string {
    return assetUrl(`/artwork/${encodeURI(path)}?w=150`);
  }

  onMount(() => {
    void loadHealth();
    void loadIssues();
  });
</script>

<svelte:head><title>Library health — OnScreen</title></svelte:head>

<div class="page">
  <p class="sub">
    Problems users reported with "Report a problem", files the integrity check found damaged,
    and titles that still need a match or a poster. Resolving or dismissing a report tells the
    person who filed it.
  </p>

  {#if healthError}
    <p class="error" role="alert">{healthError}</p>
  {:else if health}
    <div class="stats">
      <div class="stat"><span class="n">{health.open_issues}</span><span class="l">open reports</span></div>
      <div class="stat"><span class="n">{health.damaged_total}</span><span class="l">damaged files</span></div>
      <a class="stat link" href="/settings/unmatched"><span class="n">{health.unmatched_count}</span><span class="l">unmatched →</span></a>
      <a class="stat link" href="/settings/missing-art"><span class="n">{health.missing_art_count}</span><span class="l">missing art →</span></a>
    </div>
  {/if}

  <section aria-labelledby="lh-reports">
    <div class="section-head">
      <h2 id="lh-reports">Problem reports</h2>
      <div class="filters" role="tablist" aria-label="Report status">
        {#each ISSUE_FILTERS as f (f.value)}
          <button
            type="button"
            role="tab"
            class="filter"
            class:active={filter === f.value}
            aria-selected={filter === f.value}
            onclick={() => setFilter(f.value)}
          >{f.label}</button>
        {/each}
      </div>
    </div>

    {#if issuesLoading}
      <div class="skeleton-block"></div>
    {:else if issuesError}
      <p class="error" role="alert">{issuesError}</p>
    {:else if issues.length === 0}
      <p class="empty">{filter === 'open' ? 'No open reports. Nothing to do here.' : 'No reports.'}</p>
    {:else}
      <ul class="rows" aria-label="Problem reports">
        {#each issues as row (row.id)}
          <li class="row" data-issue={row.id}>
            <div class="row-main">
              {#if row.poster_path}
                <img class="thumb" src={posterSrc(row.poster_path)} alt="" loading="lazy" />
              {:else}
                <div class="thumb thumb-empty" aria-hidden="true"></div>
              {/if}
              <div class="info">
                <a class="title" href="/watch/{row.item_id}">{issueTitle(row)}</a>
                <div class="meta">
                  <span class="kind kind-{row.kind}">{issueKindLabel(row.kind)}</span>
                  <span>by {row.reporter_username}</span>
                  <span title={row.created_at}>{relativeAge(row.created_at, now)}</span>
                  {#if row.file_name}<span class="file" title={row.file_name}>{row.file_name}</span>{/if}
                </div>
                {#if row.note}<p class="note">“{row.note}”</p>{/if}
                {#if row.status !== 'open'}
                  <p class="closed">
                    {row.status === 'resolved' ? 'Resolved' : 'Dismissed'}
                    {#if row.resolved_by_username}by {row.resolved_by_username}{/if}
                    {relativeAge(row.resolved_at, now)}{#if row.resolution_note}: {row.resolution_note}{/if}
                  </p>
                {/if}
              </div>
            </div>

            {#if row.status === 'open'}
              <div class="actions">
                <input
                  class="note-input"
                  type="text"
                  maxlength="1000"
                  placeholder="Note to the reporter (optional)"
                  aria-label="Note to the reporter"
                  bind:value={notes[row.id]}
                />
                <div class="buttons">
                  <button type="button" class="btn primary" disabled={busy[row.id]} onclick={() => closeIssue(row, 'resolved')}>Resolve</button>
                  <button type="button" class="btn" disabled={busy[row.id]} onclick={() => closeIssue(row, 'dismissed')}>Dismiss</button>
                  {#if canRegrab(row.item_type)}
                    <span class="regrab">
                      <button
                        type="button"
                        class="btn"
                        disabled={busy[row.id]}
                        title="Ask Radarr/Sonarr to search again, then resolve this report"
                        onclick={() => regrabIssue(row)}
                      >Re-grab</button>
                      <label class="check">
                        <input type="checkbox" bind:checked={blocklist[row.id]} />
                        Blocklist this release
                      </label>
                    </span>
                  {/if}
                </div>
              </div>
            {/if}
          </li>
        {/each}
      </ul>
      {#if issues.length < issuesTotal}
        <button type="button" class="more" disabled={issuesLoadingMore} onclick={loadMoreIssues}>
          {issuesLoadingMore ? 'Loading…' : `Show more (${issuesTotal - issues.length} left)`}
        </button>
      {/if}
    {/if}
  </section>

  <section aria-labelledby="lh-damaged">
    <div class="section-head">
      <h2 id="lh-damaged">Damaged files</h2>
    </div>
    <p class="hint">
      Found by the integrity check task (Settings ▸ Tasks). These files fail to decode past a point
      and won't play. Re-grab asks Radarr/Sonarr for a new copy.
    </p>
    {#if health && damaged.length === 0}
      <p class="empty">No damaged files found.</p>
    {:else if damaged.length}
      <ul class="rows" aria-label="Damaged files">
        {#each damaged as f (f.file_id)}
          <li class="row">
            <div class="row-main">
              <div class="info">
                <a class="title" href="/watch/{f.item_id}">{damagedTitle(f)}</a>
                <div class="meta">
                  <span class="file" title={f.path_basename}>{f.path_basename}</span>
                  {#if f.checked_at}<span>checked {relativeAge(f.checked_at, now)}</span>{/if}
                </div>
                {#if f.integrity_detail}<p class="note detail">{f.integrity_detail}</p>{/if}
              </div>
            </div>
            {#if canRegrab(f.item_type)}
              <div class="actions">
                <div class="buttons">
                  {#if requested[f.file_id]}
                    <span class="done">Re-grab requested</span>
                  {:else}
                    <button type="button" class="btn" disabled={busy[f.file_id]} onclick={() => regrabFile(f)}>Re-grab</button>
                    <label class="check">
                      <input type="checkbox" bind:checked={blocklist[f.file_id]} />
                      Blocklist this release
                    </label>
                  {/if}
                </div>
              </div>
            {/if}
          </li>
        {/each}
      </ul>
      {#if damaged.length < damagedTotal}
        <button type="button" class="more" disabled={damagedLoadingMore} onclick={loadMoreDamaged}>
          {damagedLoadingMore ? 'Loading…' : `Show more (${damagedTotal - damaged.length} left)`}
        </button>
      {/if}
    {/if}
  </section>
</div>

<style>
  .page { max-width: 820px; }
  .sub { color: var(--text-secondary); font-size: 0.85rem; line-height: 1.5; margin: 0 0 1.25rem; max-width: 65ch; }
  .hint { color: var(--text-muted); font-size: 0.78rem; line-height: 1.5; margin: 0 0 0.75rem; max-width: 65ch; }
  .error { color: var(--error); font-size: 0.9rem; }
  .empty { color: var(--text-muted); font-size: 0.85rem; padding: 1rem 0; }

  .stats { display: grid; grid-template-columns: repeat(auto-fit, minmax(140px, 1fr)); gap: 0.5rem; margin-bottom: 1.75rem; }
  .stat {
    display: flex; flex-direction: column; gap: 0.15rem;
    padding: 0.75rem 1rem; border-radius: 10px;
    background: var(--bg-elevated); border: 1px solid var(--border);
    color: inherit; text-decoration: none;
  }
  .stat.link:hover { border-color: var(--accent); }
  .stat .n { font-size: 1.4rem; font-weight: 600; color: var(--text-primary); }
  .stat .l { font-size: 0.75rem; color: var(--text-muted); }

  section { margin-bottom: 2rem; }
  .section-head { display: flex; align-items: center; justify-content: space-between; gap: 1rem; flex-wrap: wrap; margin-bottom: 0.6rem; }
  h2 { font-size: 1rem; font-weight: 600; margin: 0; }
  .filters { display: flex; gap: 0.25rem; }
  .filter {
    background: none; border: 1px solid var(--border); border-radius: 999px;
    color: var(--text-muted); font-size: 0.75rem; padding: 0.25rem 0.7rem; cursor: pointer;
  }
  .filter.active { color: var(--text-primary); border-color: var(--accent); background: var(--accent-bg); }

  .skeleton-block {
    height: 120px; border-radius: 10px;
    background: linear-gradient(90deg, var(--bg-elevated) 25%, var(--bg-hover) 50%, var(--bg-elevated) 75%);
    background-size: 200% 100%; animation: shimmer 1.4s infinite;
  }
  @keyframes shimmer { 0% { background-position: 200% 0; } 100% { background-position: -200% 0; } }

  .rows { list-style: none; padding: 0; margin: 0; display: flex; flex-direction: column; gap: 0.4rem; }
  .row {
    background: var(--bg-elevated); border: 1px solid var(--border); border-radius: 10px;
    padding: 0.7rem 0.9rem; display: flex; flex-direction: column; gap: 0.55rem;
  }
  .row-main { display: flex; gap: 0.8rem; align-items: flex-start; min-width: 0; }
  .thumb { width: 42px; aspect-ratio: 2/3; object-fit: cover; border-radius: 4px; flex-shrink: 0; background: var(--bg-hover); }
  .thumb-empty { display: block; }
  .info { display: flex; flex-direction: column; gap: 0.25rem; min-width: 0; flex: 1; }
  .title { color: var(--text-primary); font-weight: 500; font-size: 0.9rem; text-decoration: none; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .title:hover { text-decoration: underline; }
  .meta { display: flex; flex-wrap: wrap; gap: 0.35rem 0.75rem; font-size: 0.75rem; color: var(--text-muted); }
  .kind { color: var(--text-secondary); font-weight: 500; }
  .file { font-family: var(--font-mono, ui-monospace, monospace); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; max-width: 36ch; }
  .note { margin: 0; font-size: 0.82rem; color: var(--text-secondary); white-space: pre-wrap; overflow-wrap: anywhere; }
  .note.detail { font-family: var(--font-mono, ui-monospace, monospace); font-size: 0.75rem; color: var(--text-muted); }
  .closed { margin: 0; font-size: 0.75rem; color: var(--text-muted); }

  .actions { display: flex; flex-direction: column; gap: 0.45rem; padding-left: calc(42px + 0.8rem); }
  .note-input {
    width: 100%; box-sizing: border-box; padding: 0.4rem 0.6rem;
    background: var(--input-bg); border: 1px solid var(--border-strong); border-radius: 6px;
    color: var(--text-primary); font-size: 0.8rem; font-family: inherit;
  }
  .note-input:focus { outline: none; border-color: var(--accent); }
  .buttons { display: flex; flex-wrap: wrap; align-items: center; gap: 0.4rem; }
  .btn {
    padding: 0.32rem 0.8rem; border-radius: 6px; font-size: 0.78rem; font-weight: 500; cursor: pointer;
    background: var(--input-bg); border: 1px solid var(--border-strong); color: var(--text-secondary);
  }
  .btn:hover:not(:disabled) { background: var(--bg-hover); color: var(--text-primary); }
  .btn.primary { background: var(--accent); border-color: var(--accent); color: #fff; }
  .btn.primary:hover:not(:disabled) { background: var(--accent-hover); }
  .btn:disabled { opacity: 0.5; cursor: progress; }
  .regrab { display: inline-flex; align-items: center; gap: 0.5rem; margin-left: 0.4rem; }
  .check { display: inline-flex; align-items: center; gap: 0.3rem; font-size: 0.75rem; color: var(--text-muted); cursor: pointer; }
  .done { font-size: 0.78rem; color: var(--success); }
  .more {
    margin-top: 0.6rem; background: none; border: 1px solid var(--border); border-radius: 6px;
    color: var(--text-secondary); font-size: 0.8rem; padding: 0.35rem 0.9rem; cursor: pointer;
  }
  @media (max-width: 600px) {
    .actions { padding-left: 0; }
  }
</style>
