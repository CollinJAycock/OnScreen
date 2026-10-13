<script lang="ts">
  // Settings ▸ Import: bring users' watch history over from a Plex,
  // Jellyfin or Emby server. Connect, map each account on the old server to
  // an OnScreen user, preview what matches, then import.
  import { onMount, onDestroy } from 'svelte';
  import {
    importApi,
    userApi,
    type ImportConnection,
    type ImportJob,
    type ImportKind,
    type ImportOptions,
    type User
  } from '$lib/api';
  import {
    importKinds,
    kindLabel,
    initialMapping,
    mappingList,
    defaultOptions,
    anySelected,
    countsSummary,
    matchSummary,
    statusLabel
  } from '$lib/importer';
  import { toast } from '$lib/stores/toast';

  let kind = $state<ImportKind>('plex');
  let url = $state('');
  let token = $state('');
  let connecting = $state(false);
  let connectError = $state('');
  let connection = $state<ImportConnection | null>(null);
  let mapping = $state<Record<string, string>>({});
  let options = $state<ImportOptions>(defaultOptions('plex'));
  let starting = $state(false);

  let users = $state<User[]>([]);
  let jobs = $state<ImportJob[]>([]);
  let loadError = $state('');
  let pollTimer: ReturnType<typeof setInterval> | null = null;

  let kindInfo = $derived(importKinds.find((k) => k.kind === kind)!);
  let mapped = $derived(mappingList(mapping));
  let running = $derived(jobs.some((j) => j.status === 'running'));
  let historyOnly = $derived(connection?.accounts.filter((a) => !a.full_state) ?? []);

  onMount(async () => {
    try {
      const [u, j] = await Promise.all([userApi.list(), importApi.list()]);
      users = u.items;
      jobs = j.items;
    } catch (e) {
      loadError = e instanceof Error ? e.message : 'Failed to load';
    }
  });

  onDestroy(stopPolling);

  // While an import runs, re-read the list every couple of seconds.
  $effect(() => {
    if (running) startPolling();
    else stopPolling();
  });

  function startPolling() {
    if (pollTimer) return;
    pollTimer = setInterval(reloadJobs, 2000);
  }
  function stopPolling() {
    if (pollTimer) { clearInterval(pollTimer); pollTimer = null; }
  }

  async function reloadJobs() {
    try {
      const before = new Map(jobs.map((j) => [j.id, j.status]));
      jobs = (await importApi.list()).items;
      for (const j of jobs) {
        if (before.get(j.id) === 'running' && j.status !== 'running') {
          if (j.status === 'succeeded') toast.success(j.dry_run ? 'Preview ready.' : 'Import finished.');
          else if (j.status === 'failed') toast.error(j.error ? `Import failed: ${j.error}` : 'Import failed.');
        }
      }
    } catch { /* keep the last list */ }
  }

  function changeKind(k: ImportKind) {
    kind = k;
    connection = null;
    connectError = '';
    options = defaultOptions(k);
  }

  async function connect(e: Event) {
    e.preventDefault();
    connecting = true;
    connectError = '';
    connection = null;
    try {
      const c = await importApi.connect({ kind, url, token });
      connection = c;
      mapping = initialMapping(c.accounts);
      options = defaultOptions(kind);
    } catch (err) {
      connectError = err instanceof Error ? err.message : 'Could not connect';
    } finally {
      connecting = false;
    }
  }

  async function start(dryRun: boolean) {
    starting = true;
    try {
      const job = await importApi.start({ kind, url, token, mapping: mapped, options, dry_run: dryRun });
      jobs = [job, ...jobs.filter((j) => j.id !== job.id)];
      toast.success(dryRun ? 'Previewing what would be imported…' : 'Import started.');
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Could not start the import');
    } finally {
      starting = false;
    }
  }

  async function cancel(job: ImportJob) {
    try {
      await importApi.cancel(job.id);
      await reloadJobs();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Could not cancel');
    }
  }

  function when(iso?: string): string {
    if (!iso) return '';
    const d = new Date(iso);
    return isNaN(d.getTime()) ? '' : d.toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' });
  }

  function userName(id: string): string {
    return users.find((u) => u.id === id)?.username ?? 'a deleted user';
  }
</script>

<svelte:head><title>Import — OnScreen</title></svelte:head>

<div class="page">
  <p class="intro">
    Bring each user's watched state, resume points, ratings, favorites, playlists and play history over
    from a Plex, Jellyfin or Emby server. Items are matched by file path, then by TMDB, TVDB or IMDb ID,
    so add your libraries here and scan them first. Preview to see what matches before anything is written;
    running an import again adds only what's new, and anything users already did on OnScreen is kept.
  </p>

  {#if loadError}<div class="banner error">{loadError}</div>{/if}

  <form class="card" onsubmit={connect}>
    <div class="card-title">Connect</div>
    <div class="kinds" role="radiogroup" aria-label="Server type">
      {#each importKinds as k (k.kind)}
        <button type="button" role="radio" aria-checked={kind === k.kind} class="kind" class:on={kind === k.kind}
                onclick={() => changeKind(k.kind)}>{k.label}</button>
      {/each}
    </div>
    <div class="field">
      <label for="imp-url">Server URL</label>
      <input id="imp-url" type="url" required placeholder={kindInfo.placeholder} bind:value={url} autocomplete="off" />
    </div>
    <div class="field">
      <label for="imp-token">{kindInfo.tokenLabel}</label>
      <input id="imp-token" type="password" required bind:value={token} autocomplete="off" />
      <span class="hint">{kindInfo.tokenHelp} It's used for this import only and isn't stored.</span>
    </div>
    {#if connectError}<div class="banner error">{connectError}</div>{/if}
    <div class="actions">
      <button class="btn primary" type="submit" disabled={connecting || !url || !token}>
        {connecting ? 'Connecting…' : 'Connect'}
      </button>
    </div>
  </form>

  {#if connection}
    <section class="card" aria-label="Accounts">
      <div class="card-title">Accounts on {connection.server_name || kindLabel(connection.kind)}</div>
      <p class="sub">
        {kindLabel(connection.kind)} {connection.version} · {connection.accounts.length}
        {connection.accounts.length === 1 ? 'account' : 'accounts'}. Choose the OnScreen user each one's history goes to.
      </p>
      <table class="accounts">
        <tbody>
          {#each connection.accounts as a (a.id)}
            <tr>
              <td class="acct">
                {a.name}
                {#if !a.full_state}<span class="pill" title="Plex shows this account's play history, not its resume points, ratings or playlists">history only</span>{/if}
              </td>
              <td>
                <select bind:value={mapping[a.id]} aria-label={`OnScreen user for ${a.name}`}>
                  <option value="">Don't import</option>
                  {#each users as u (u.id)}<option value={u.id}>{u.username}</option>{/each}
                </select>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
      {#if historyOnly.length > 0}
        <p class="note">
          Plex shows resume points, ratings and playlists only for the server owner's account (the token's).
          The other accounts bring their play history and what it shows they watched.
        </p>
      {/if}

      <div class="card-title second">What to import</div>
      <div class="opts">
        <label class="check"><input type="checkbox" bind:checked={options.watched} /> Watched state and resume points</label>
        <label class="check"><input type="checkbox" bind:checked={options.ratings} /> Ratings</label>
        <label class="check" class:muted={connection.kind === 'plex'}>
          <input type="checkbox" bind:checked={options.favorites} disabled={connection.kind === 'plex'} />
          Favorites{#if connection.kind === 'plex'}<span class="hint"> (Plex has none)</span>{/if}
        </label>
        <label class="check"><input type="checkbox" bind:checked={options.playlists} /> Playlists</label>
        <label class="check"><input type="checkbox" bind:checked={options.history} /> Play history</label>
      </div>
      <p class="note">
        Play history counts toward play counts, Trending and analytics. Plays older than the server's watch-history
        retention are left out, as they'd be removed anyway (the items still count as watched).
        {#if connection.kind !== 'plex'}
          {kindLabel(connection.kind)} keeps no log of individual plays, so each watched item brings one play, on the
          date it was last played. A playlist shared with everyone comes over for each user who could see it.
        {/if}
      </p>
      <div class="actions">
        <button class="btn ghost" type="button" disabled={starting || running || mapped.length === 0 || !anySelected(options)}
                onclick={() => start(true)}>Preview</button>
        <button class="btn primary" type="button" disabled={starting || running || mapped.length === 0 || !anySelected(options)}
                onclick={() => start(false)}>Import</button>
      </div>
      {#if running}<p class="hint right">Another import is running.</p>{/if}
    </section>
  {/if}

  <section aria-label="Imports">
    <div class="list-title">Imports</div>
    {#if jobs.length === 0}
      <p class="empty">No imports yet.</p>
    {:else}
      {#each jobs as job (job.id)}
        <div class="card job" class:running={job.status === 'running'}>
          <div class="job-head">
            <span class="kind-pill">{kindLabel(job.source_kind)}</span>
            <span class="job-name">{job.server_name || job.source_url}</span>
            <span class="status s-{job.status}">{statusLabel(job)}</span>
            <span class="job-when">{when(job.created_at)}</span>
            {#if job.status === 'running'}
              <button class="btn ghost sm" type="button" onclick={() => cancel(job)}>Cancel</button>
            {/if}
          </div>
          {#if job.status === 'running'}
            <div class="phase">{job.phase}</div>
            {#if job.stats?.write_progress}
              <div class="bar" role="progressbar" aria-valuemin="0" aria-valuemax="100"
                   aria-valuenow={Math.round(job.stats.write_progress * 100)}>
                <div style="width: {Math.round(job.stats.write_progress * 100)}%"></div>
              </div>
            {/if}
          {/if}
          {#if job.error}<div class="banner error">{job.error}</div>{/if}
          {#if matchSummary(job)}
            <div class="line">{matchSummary(job)}{job.mapping?.length ? ` for ${job.mapping.map((m) => `${m.account_name ?? m.account_id} → ${userName(m.user_id)}`).join(', ')}` : ''}</div>
          {/if}
          {#if job.dry_run && countsSummary(job.stats?.planned)}
            <div class="line">Found to import: {countsSummary(job.stats.planned)}</div>
          {:else if !job.dry_run && job.status !== 'running' && job.stats?.planned}
            <div class="line">Imported: {countsSummary(job.stats.written) || 'nothing new'}</div>
          {/if}
          {#if job.stats?.plays_outside_retention}
            <div class="line muted">{job.stats.plays_outside_retention.toLocaleString()} plays were older than the history retention.</div>
          {/if}
          {#if job.unmatched?.length}
            <details>
              <summary>{(job.stats?.unmatched ?? job.unmatched.length).toLocaleString()} items didn't match{(job.stats?.unmatched ?? 0) > job.unmatched.length ? ` (first ${job.unmatched.length} shown)` : ''}</summary>
              <ul class="unmatched">{#each job.unmatched as u, i (i)}<li>{u}</li>{/each}</ul>
            </details>
          {/if}
        </div>
      {/each}
    {/if}
  </section>
</div>

<style>
  .page { max-width: 760px; }
  .intro { font-size: 0.8rem; color: var(--text-muted); line-height: 1.55; margin: 0 0 1.25rem; }
  .banner { padding: 0.55rem 0.85rem; border-radius: 7px; font-size: 0.8rem; margin: 0.5rem 0; }
  .banner.error { background: var(--error-bg); border: 1px solid var(--error); color: var(--error); }

  .card { background: var(--bg-elevated); border: 1px solid var(--border); border-radius: 10px; padding: 1.1rem 1.25rem; margin-bottom: 0.9rem; }
  .card-title { font-size: 0.78rem; font-weight: 700; color: var(--accent-text); margin-bottom: 0.9rem; text-transform: uppercase; letter-spacing: 0.06em; }
  .card-title.second { margin-top: 1.25rem; }
  .sub { font-size: 0.8rem; color: var(--text-secondary); margin: -0.4rem 0 0.8rem; }
  .note { font-size: 0.75rem; color: var(--text-muted); line-height: 1.5; margin: 0.6rem 0 0; }
  .hint { font-size: 0.72rem; color: var(--text-muted); }
  .hint.right { text-align: right; margin: 0.4rem 0 0; }

  .kinds { display: flex; gap: 0.4rem; margin-bottom: 1rem; }
  .kind { padding: 0.4rem 0.9rem; border-radius: 7px; border: 1px solid var(--border-strong); background: transparent; color: var(--text-secondary); font-size: 0.8rem; font-weight: 600; cursor: pointer; }
  .kind.on { background: var(--accent); border-color: var(--accent); color: #fff; }

  .field { display: flex; flex-direction: column; gap: 0.3rem; margin-bottom: 0.9rem; }
  label { font-size: 0.75rem; font-weight: 500; color: var(--text-muted); }
  input:not([type='checkbox']), select { background: var(--input-bg); border: 1px solid var(--border-strong); border-radius: 7px; padding: 0.48rem 0.7rem; font-size: 0.85rem; color: var(--text-primary); width: 100%; box-sizing: border-box; }
  input:focus, select:focus { outline: none; border-color: var(--accent); box-shadow: 0 0 0 3px var(--accent-bg); }

  .accounts { width: 100%; border-collapse: collapse; }
  .accounts td { padding: 0.35rem 0; font-size: 0.85rem; color: var(--text-primary); vertical-align: middle; }
  .accounts td.acct { width: 45%; padding-right: 0.75rem; }
  .pill { font-size: 0.62rem; padding: 0.12rem 0.45rem; border-radius: 4px; background: var(--bg-hover); color: var(--text-muted); margin-left: 0.4rem; text-transform: uppercase; letter-spacing: 0.04em; }

  .opts { display: grid; grid-template-columns: 1fr 1fr; gap: 0.45rem 1rem; }
  .check { display: flex; align-items: center; gap: 0.45rem; font-size: 0.82rem; color: var(--text-secondary); cursor: pointer; }
  .check.muted { opacity: 0.6; }

  .actions { display: flex; justify-content: flex-end; gap: 0.6rem; margin-top: 1rem; }
  .btn { display: inline-flex; align-items: center; justify-content: center; padding: 0.45rem 0.9rem; font-size: 0.8rem; font-weight: 600; border-radius: 7px; cursor: pointer; border: 1px solid transparent; }
  .btn.sm { padding: 0.3rem 0.65rem; font-size: 0.74rem; }
  .btn.primary { background: var(--accent); color: #fff; }
  .btn.primary:hover:not(:disabled) { background: var(--accent-hover); }
  .btn.ghost { background: transparent; border-color: var(--border-strong); color: var(--text-secondary); }
  .btn.ghost:hover:not(:disabled) { background: var(--bg-hover); }
  .btn:disabled { opacity: 0.55; cursor: not-allowed; }

  .list-title { font-size: 0.78rem; font-weight: 700; color: var(--text-muted); text-transform: uppercase; letter-spacing: 0.06em; margin: 1.5rem 0 0.6rem; }
  .empty { font-size: 0.82rem; color: var(--text-muted); }
  .job.running { border-color: var(--accent); }
  .job-head { display: flex; align-items: center; gap: 0.6rem; flex-wrap: wrap; }
  .job-name { font-size: 0.9rem; font-weight: 600; color: var(--text-primary); min-width: 0; overflow-wrap: anywhere; }
  .job-when { font-size: 0.72rem; color: var(--text-muted); margin-left: auto; }
  .kind-pill { font-size: 0.62rem; font-weight: 600; padding: 0.15rem 0.5rem; border-radius: 4px; text-transform: uppercase; letter-spacing: 0.05em; background: var(--info-bg); color: var(--info); }
  .status { font-size: 0.68rem; font-weight: 600; padding: 0.12rem 0.45rem; border-radius: 4px; background: var(--bg-hover); color: var(--text-secondary); }
  .status.s-succeeded { background: var(--success-bg); color: var(--success); }
  .status.s-failed { background: var(--error-bg); color: var(--error); }
  .phase { font-size: 0.78rem; color: var(--text-secondary); margin-top: 0.5rem; }
  .bar { height: 6px; border-radius: 3px; background: var(--bg-hover); margin-top: 0.4rem; overflow: hidden; }
  .bar > div { height: 100%; background: var(--accent); transition: width 0.4s; }
  .line { font-size: 0.8rem; color: var(--text-secondary); margin-top: 0.45rem; line-height: 1.45; }
  .line.muted { color: var(--text-muted); font-size: 0.75rem; }
  details { margin-top: 0.5rem; font-size: 0.78rem; color: var(--text-secondary); }
  summary { cursor: pointer; }
  .unmatched { margin: 0.4rem 0 0; padding-left: 1.1rem; max-height: 220px; overflow-y: auto; color: var(--text-muted); }

  @media (max-width: 560px) {
    .opts { grid-template-columns: 1fr; }
    .accounts td.acct { width: 40%; }
  }
</style>
