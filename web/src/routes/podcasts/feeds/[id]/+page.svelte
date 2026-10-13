<script lang="ts">
  // An RSS podcast subscription (admins): the feed's episodes with their
  // download state, auto-download and retention, refresh and unsubscribe.
  import { onMount, onDestroy } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { podcastApi, type PodcastFeed, type PodcastEpisode } from '$lib/api';
  import { episodeStatusLabel, canDownload, formatBytes } from '$lib/podcasts';
  import { toast } from '$lib/stores/toast';
  import { confirmAction } from '$lib/native';

  const PAGE = 50;

  let feed = $state<PodcastFeed | null>(null);
  let episodes = $state<PodcastEpisode[]>([]);
  let total = $state(0);
  let loading = $state(true);
  let error = $state('');
  let loadingMore = $state(false);
  let refreshing = $state(false);
  let busy = $state<Record<string, boolean>>({});
  let confirmingUnsub = $state(false);
  let deleteFiles = $state(false);
  let unsubscribing = $state(false);
  let pollTimer: ReturnType<typeof setInterval> | null = null;

  let id = $derived($page.params.id!);
  let downloading = $derived(episodes.some((e) => e.status === 'downloading'));
  let downloadedCount = $derived(feed?.downloaded_count ?? episodes.filter((e) => e.status === 'downloaded').length);

  const keepOptions: [number, string][] = [
    [0, 'All episodes'], [1, 'Newest 1'], [3, 'Newest 3'], [5, 'Newest 5'],
    [10, 'Newest 10'], [25, 'Newest 25'], [50, 'Newest 50']
  ];

  onMount(() => {
    let admin = false;
    try { admin = !!JSON.parse(localStorage.getItem('onscreen_user') ?? '{}')?.is_admin; } catch { /* not admin */ }
    if (!admin) { goto('/'); return; }
    load();
  });

  onDestroy(() => stopPolling());

  // While an episode downloads, re-read the loaded episodes every few
  // seconds so its status (and the Play link) show up without a reload.
  $effect(() => {
    if (downloading) startPolling();
    else stopPolling();
  });

  function startPolling() {
    if (pollTimer) return;
    pollTimer = setInterval(reloadEpisodes, 3000);
  }
  function stopPolling() {
    if (pollTimer) { clearInterval(pollTimer); pollTimer = null; }
  }

  async function load() {
    loading = true;
    error = '';
    try {
      const [f, eps] = await Promise.all([podcastApi.getFeed(id), podcastApi.episodes(id, PAGE, 0)]);
      feed = f;
      episodes = eps.items;
      total = eps.total;
    } catch (e) {
      error = e instanceof Error && e.message ? e.message : 'Could not load the subscription';
    } finally {
      loading = false;
    }
  }

  async function reloadEpisodes() {
    try {
      const eps = await podcastApi.episodes(id, Math.max(episodes.length, PAGE), 0);
      episodes = eps.items;
      total = eps.total;
    } catch { /* keep what's shown; the next tick retries */ }
  }

  async function loadMore() {
    loadingMore = true;
    try {
      const eps = await podcastApi.episodes(id, PAGE, episodes.length);
      episodes = [...episodes, ...eps.items];
      total = eps.total;
    } catch (e) {
      toast.error(e instanceof Error ? e.message : 'Could not load more episodes');
    } finally {
      loadingMore = false;
    }
  }

  async function refresh() {
    if (!feed) return;
    refreshing = true;
    try {
      const res = await podcastApi.refresh(feed.id);
      if (res.not_modified) toast.info('The feed has no changes.');
      else if (res.new_episodes === 0) toast.info('No new episodes.');
      else toast.success(`${res.new_episodes} new episode${res.new_episodes === 1 ? '' : 's'}` +
        (res.downloading ? `, ${res.downloading} downloading` : '') + '.');
      feed = await podcastApi.getFeed(feed.id);
      await reloadEpisodes();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : 'Refresh failed');
      try { feed = await podcastApi.getFeed(id); } catch { /* keep */ }
    } finally {
      refreshing = false;
    }
  }

  async function saveSettings(patch: { auto_download?: boolean; keep_latest?: number }) {
    if (!feed) return;
    const before = feed;
    try {
      feed = await podcastApi.updateFeed(feed.id, patch);
      if (patch.keep_latest !== undefined) await reloadEpisodes();
      toast.success('Saved.');
    } catch (e) {
      feed = before;
      toast.error(e instanceof Error ? e.message : 'Could not save');
    }
  }

  async function onKeepChange(e: Event) {
    const select = e.currentTarget as HTMLSelectElement;
    const n = Number(select.value);
    const downloaded = feed?.downloaded_count ?? episodes.filter((x) => x.status === 'downloaded').length;
    if (n > 0 && downloaded > n &&
        !(await confirmAction(`Keep only the newest ${n}? Older downloaded episodes are deleted now.`))) {
      select.value = String(feed?.keep_latest ?? 0);
      return;
    }
    saveSettings({ keep_latest: n });
  }

  function replace(ep: PodcastEpisode) {
    episodes = episodes.map((x) => (x.id === ep.id ? ep : x));
  }

  async function download(ep: PodcastEpisode) {
    busy = { ...busy, [ep.id]: true };
    try {
      replace(await podcastApi.download(ep.id));
    } catch (e) {
      toast.error(e instanceof Error ? e.message : 'Download failed to start');
    } finally {
      busy = { ...busy, [ep.id]: false };
    }
  }

  async function deleteDownload(ep: PodcastEpisode) {
    if (!(await confirmAction(`Delete the downloaded file of "${ep.title}"? It won't be downloaded again automatically.`))) return;
    busy = { ...busy, [ep.id]: true };
    try {
      await podcastApi.deleteDownload(ep.id);
      replace({ ...ep, status: 'removed', media_item_id: undefined });
    } catch (e) {
      toast.error(e instanceof Error ? e.message : 'Delete failed');
    } finally {
      busy = { ...busy, [ep.id]: false };
    }
  }

  async function unsubscribe() {
    if (!feed) return;
    unsubscribing = true;
    try {
      await podcastApi.unsubscribe(feed.id, deleteFiles);
      toast.success(`Unsubscribed from ${feed.title}.`);
      goto(`/libraries/${feed.library_id}?tab=subscriptions`);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : 'Unsubscribe failed');
      unsubscribing = false;
    }
  }

  function date(iso?: string): string {
    if (!iso) return '';
    const d = new Date(iso);
    return isNaN(d.getTime()) ? '' : d.toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' });
  }

  function dur(ms?: number): string {
    if (!ms) return '';
    const m = Math.round(ms / 60000);
    return m >= 60 ? `${Math.floor(m / 60)}h ${m % 60}m` : `${m}m`;
  }
</script>

<svelte:head><title>{feed?.title ?? 'Podcast subscription'} — OnScreen</title></svelte:head>

<div class="page">
  {#if loading}
    <p class="muted">Loading…</p>
  {:else if error}
    <p class="err">{error}</p>
  {:else if feed}
    <nav class="crumb">
      <a href="/libraries/{feed.library_id}?tab=subscriptions">Subscriptions</a>
      <span>/</span>
      <span>{feed.title}</span>
    </nav>

    <header class="hero">
      <div class="art">
        {#if feed.image_url}
          <img src={feed.image_url} alt="" referrerpolicy="no-referrer" />
        {:else}
          <span>🎙️</span>
        {/if}
      </div>
      <div class="hero-meta">
        <div class="kind">Podcast subscription</div>
        <h1>{feed.title}</h1>
        {#if feed.author}<div class="author">{feed.author}</div>{/if}
        <div class="facts">
          <span>{total} episode{total === 1 ? '' : 's'} in the feed</span>
          {#if feed.last_checked_at}<span>Checked {date(feed.last_checked_at)}</span>{/if}
          {#if feed.show_item_id}<a href="/podcasts/{feed.show_item_id}">Open in library</a>{/if}
          {#if feed.site_url}<a href={feed.site_url} target="_blank" rel="noopener noreferrer">Website</a>{/if}
        </div>
        {#if feed.last_error}<div class="feed-error">Last check failed: {feed.last_error}</div>{/if}
        <div class="feed-url" title="Folder: {feed.folder}">{feed.feed_url}</div>
      </div>
    </header>

    <section class="settings" aria-label="Subscription settings">
      <label class="check">
        <input type="checkbox" checked={feed.auto_download}
               onchange={(e) => saveSettings({ auto_download: e.currentTarget.checked })} />
        Download new episodes automatically
      </label>
      <label>
        Keep
        <select value={feed.keep_latest} onchange={onKeepChange}>
          {#each keepOptions as [n, label]}
            <option value={n}>{label}</option>
          {/each}
        </select>
      </label>
      <div class="actions">
        <button type="button" class="btn" onclick={refresh} disabled={refreshing}>
          {refreshing ? 'Checking…' : 'Check for new episodes'}
        </button>
        <button type="button" class="btn danger" onclick={() => (confirmingUnsub = !confirmingUnsub)}>
          Unsubscribe
        </button>
      </div>
      {#if confirmingUnsub}
        <div class="unsub" role="group" aria-label="Unsubscribe">
          <p>Stop following {feed.title}? The feed stops being checked.</p>
          <label class="check">
            <input type="checkbox" bind:checked={deleteFiles} />
            Also delete the {downloadedCount === 1 ? 'downloaded episode' : `${downloadedCount} downloaded episodes`}
            (otherwise they stay in the library)
          </label>
          <div class="actions">
            <button type="button" class="btn danger" onclick={unsubscribe} disabled={unsubscribing}>
              {unsubscribing ? 'Unsubscribing…' : 'Unsubscribe'}
            </button>
            <button type="button" class="btn" onclick={() => (confirmingUnsub = false)}>Cancel</button>
          </div>
        </div>
      {/if}
    </section>

    <section aria-label="Episodes">
      <h2 class="section-title">Episodes</h2>
      <ul class="eps">
        {#each episodes as ep (ep.id)}
          <li class="ep" class:dim={ep.status === 'removed'}>
            <div class="ep-body">
              <div class="ep-title">{ep.title}</div>
              <div class="ep-meta">
                {[date(ep.published_at), dur(ep.duration_ms), formatBytes(ep.enclosure_length)].filter(Boolean).join(' · ')}
              </div>
              {#if ep.description}<div class="ep-desc">{ep.description}</div>{/if}
              {#if ep.status === 'failed' && ep.error}<div class="ep-error">{ep.error}</div>{/if}
            </div>
            <div class="ep-side">
              <span class="status status-{ep.status}">{episodeStatusLabel(ep)}</span>
              {#if ep.status === 'downloaded' && ep.media_item_id}
                <a class="btn small" href="/watch/{ep.media_item_id}">Play</a>
              {/if}
              {#if canDownload(ep)}
                <button type="button" class="btn small" onclick={() => download(ep)} disabled={busy[ep.id]}>
                  {ep.status === 'failed' ? 'Retry' : 'Download'}
                </button>
              {:else if ep.status === 'downloaded'}
                <button type="button" class="btn small subtle" onclick={() => deleteDownload(ep)} disabled={busy[ep.id]}>
                  Delete file
                </button>
              {/if}
            </div>
          </li>
        {/each}
      </ul>
      {#if episodes.length < total}
        <button type="button" class="btn more" onclick={loadMore} disabled={loadingMore}>
          {loadingMore ? 'Loading…' : `Show more (${total - episodes.length} older)`}
        </button>
      {/if}
    </section>
  {/if}
</div>

<style>
  .page { padding: 1.25rem 1.5rem 3rem; max-width: 1000px; margin: 0 auto; }
  .muted { color: var(--text-muted); font-size: 0.9rem; }
  .err { color: var(--error); font-size: 0.9rem; }
  .crumb { font-size: 0.78rem; color: var(--text-muted); margin-bottom: 1rem; }
  .crumb a { color: var(--text-muted); text-decoration: none; }
  .crumb a:hover { color: var(--text-secondary); }
  .crumb span { margin: 0 0.4rem; }

  .hero { display: flex; gap: 1.25rem; align-items: flex-start; margin-bottom: 1.25rem; }
  .art {
    width: 140px; height: 140px; flex-shrink: 0; border-radius: 8px; overflow: hidden;
    background: var(--bg-elevated); display: flex; align-items: center; justify-content: center; font-size: 3rem;
  }
  .art img { width: 100%; height: 100%; object-fit: cover; display: block; }
  .hero-meta { min-width: 0; flex: 1; display: flex; flex-direction: column; gap: 0.3rem; }
  .kind { font-size: 0.7rem; color: var(--accent); text-transform: uppercase; letter-spacing: 0.08em; font-weight: 600; }
  h1 { font-size: 1.6rem; font-weight: 700; color: var(--text-primary); margin: 0; line-height: 1.15; }
  .author { font-size: 0.85rem; color: var(--text-secondary); }
  .facts { display: flex; flex-wrap: wrap; gap: 0.35rem 0.9rem; font-size: 0.78rem; color: var(--text-muted); }
  .facts a { color: var(--accent); text-decoration: none; }
  .facts a:hover { text-decoration: underline; }
  .feed-url {
    font-size: 0.72rem; color: var(--text-muted); overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
    font-family: ui-monospace, monospace;
  }
  .feed-error { font-size: 0.78rem; color: var(--error); }

  .settings {
    display: flex; flex-wrap: wrap; align-items: center; gap: 0.75rem 1.25rem;
    background: var(--input-bg); border: 1px solid var(--border); border-radius: 10px;
    padding: 0.8rem 0.9rem; margin-bottom: 1.5rem; font-size: 0.82rem; color: var(--text-secondary);
  }
  .settings label { display: flex; align-items: center; gap: 0.4rem; }
  .settings select {
    background: var(--input-bg); color: var(--text-primary); border: 1px solid var(--border);
    border-radius: 6px; padding: 0.2rem 0.35rem; font-size: 0.8rem;
  }
  .actions { display: flex; gap: 0.5rem; margin-left: auto; flex-wrap: wrap; }
  .unsub {
    flex-basis: 100%; border-top: 1px solid var(--border); padding-top: 0.75rem;
    display: flex; flex-direction: column; gap: 0.5rem;
  }
  .unsub p { margin: 0; color: var(--text-primary); }
  .unsub .actions { margin-left: 0; }

  .btn {
    background: var(--bg-elevated); color: var(--text-primary); border: 1px solid var(--border);
    border-radius: 7px; padding: 0.4rem 0.8rem; font-size: 0.78rem; cursor: pointer;
    text-decoration: none; white-space: nowrap; display: inline-block;
  }
  .btn:hover:not(:disabled) { border-color: var(--border-strong); }
  .btn:disabled { opacity: 0.55; cursor: default; }
  .btn.danger { color: var(--error); }
  .btn.small { padding: 0.25rem 0.6rem; font-size: 0.72rem; }
  .btn.subtle { color: var(--text-muted); }
  .btn.more { margin-top: 0.75rem; }

  .section-title {
    font-size: 0.95rem; font-weight: 600; color: var(--text-secondary);
    text-transform: uppercase; letter-spacing: 0.05em; margin: 0 0 0.75rem;
  }
  .eps { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 0.45rem; }
  .ep {
    display: flex; gap: 1rem; justify-content: space-between; align-items: flex-start;
    background: var(--input-bg); border: 1px solid var(--border); border-radius: 8px; padding: 0.7rem 0.8rem;
  }
  .ep.dim .ep-title { color: var(--text-muted); }
  .ep-body { min-width: 0; flex: 1; }
  .ep-title { font-size: 0.88rem; font-weight: 600; color: var(--text-primary); }
  .ep-meta { font-size: 0.72rem; color: var(--text-muted); margin-top: 0.15rem; }
  .ep-desc {
    font-size: 0.75rem; color: var(--text-muted); line-height: 1.4; margin-top: 0.3rem;
    overflow: hidden; display: -webkit-box; -webkit-line-clamp: 2; line-clamp: 2; -webkit-box-orient: vertical;
    white-space: pre-line;
  }
  .ep-error { font-size: 0.72rem; color: var(--error); margin-top: 0.25rem; }
  .ep-side { display: flex; flex-direction: column; align-items: flex-end; gap: 0.35rem; flex-shrink: 0; }
  .status { font-size: 0.68rem; color: var(--text-muted); }
  .status-downloaded { color: var(--accent); }
  .status-downloading { color: var(--text-secondary); }
  .status-failed { color: var(--error); }

  @media (max-width: 600px) {
    .hero { flex-direction: column; }
    .art { width: 110px; height: 110px; }
    .actions { margin-left: 0; }
    .ep { flex-direction: column; }
    .ep-side { flex-direction: row; align-items: center; }
  }
</style>
