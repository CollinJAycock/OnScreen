<script lang="ts">
  // The podcast library page's "Subscriptions" tab (admins): subscribe the
  // library to an RSS feed, and list its feeds. Episodes download into the
  // library's folder and appear in the Library tab once scanned; each feed's
  // page lists its episodes and settings.
  import { podcastApi, type PodcastFeed } from '$lib/api';
  import { toast } from '$lib/stores/toast';

  interface Props {
    libraryId: string;
    /** Called after a subscription, so the library grid can reload. */
    onchange?: () => void;
  }

  let { libraryId, onchange }: Props = $props();

  let feeds = $state<PodcastFeed[]>([]);
  let loading = $state(true);
  let error = $state('');
  let seq = 0;

  let feedURL = $state('');
  let autoDownload = $state(true);
  let downloadNow = $state(1);
  let keepLatest = $state(0);
  let subscribing = $state(false);
  let formError = $state('');

  const keepOptions: [number, string][] = [
    [0, 'All episodes'], [1, 'Newest 1'], [3, 'Newest 3'], [5, 'Newest 5'],
    [10, 'Newest 10'], [25, 'Newest 25'], [50, 'Newest 50']
  ];

  async function load(id: string) {
    const mine = ++seq;
    loading = true;
    error = '';
    try {
      const res = await podcastApi.listFeeds(id);
      if (mine !== seq) return;
      feeds = res.items ?? [];
    } catch (e) {
      if (mine !== seq) return;
      feeds = [];
      error = e instanceof Error && e.message ? e.message : 'Could not load subscriptions';
    } finally {
      if (mine === seq) loading = false;
    }
  }

  $effect(() => {
    load(libraryId);
  });

  async function subscribe(e: SubmitEvent) {
    e.preventDefault();
    formError = '';
    const url = feedURL.trim();
    if (!url) { formError = 'Paste the podcast\'s RSS feed URL'; return; }
    subscribing = true;
    try {
      const feed = await podcastApi.subscribe({
        library_id: libraryId,
        feed_url: url,
        auto_download: autoDownload,
        keep_latest: keepLatest,
        download_latest: downloadNow
      });
      feedURL = '';
      // The server downloads no more than the episodes it keeps.
      const n = keepLatest > 0 ? Math.min(downloadNow, keepLatest) : downloadNow;
      toast.success(
        n > 0
          ? `Subscribed to ${feed.title}. Downloading the newest ${n === 1 ? 'episode' : `${n} episodes`}…`
          : `Subscribed to ${feed.title}.`
      );
      await load(libraryId);
      onchange?.();
    } catch (err) {
      formError = err instanceof Error && err.message ? err.message : 'Subscribe failed';
    } finally {
      subscribing = false;
    }
  }

  function countLabel(f: PodcastFeed): string {
    const n = f.episode_count ?? 0;
    const d = f.downloaded_count ?? 0;
    return `${n} episode${n === 1 ? '' : 's'} · ${d} downloaded`;
  }
</script>

<section class="subs" aria-label="Podcast subscriptions">
  <form class="subscribe" onsubmit={subscribe}>
    <div class="row">
      <input type="url" bind:value={feedURL} placeholder="https://example.com/podcast/feed.xml"
             aria-label="RSS feed URL" autocomplete="off" disabled={subscribing} />
      <button type="submit" class="btn-primary" disabled={subscribing}>
        {subscribing ? 'Subscribing…' : 'Subscribe'}
      </button>
    </div>
    <div class="opts">
      <label class="check">
        <input type="checkbox" bind:checked={autoDownload} />
        Download new episodes automatically
      </label>
      <label>
        Download now
        <select bind:value={downloadNow}>
          <option value={0}>None</option>
          <option value={1}>Newest episode</option>
          <option value={3}>Newest 3</option>
          <option value={5}>Newest 5</option>
          <option value={10}>Newest 10</option>
        </select>
      </label>
      <label>
        Keep
        <select bind:value={keepLatest}>
          {#each keepOptions as [n, label]}
            <option value={n}>{label}</option>
          {/each}
        </select>
      </label>
    </div>
    {#if formError}<p class="form-error" role="alert">{formError}</p>{/if}
    <p class="hint">Episodes download into this library's folder, one folder per podcast, and the feed is
      checked for new ones every hour.</p>
  </form>

  {#if loading}
    <p class="muted" role="status">Loading subscriptions…</p>
  {:else if error}
    <div class="error-bar" role="alert">
      {error}
      <button type="button" class="retry" onclick={() => load(libraryId)}>Retry</button>
    </div>
  {:else if feeds.length === 0}
    <div class="empty">
      <p class="empty-t">No subscriptions yet</p>
      <p class="empty-s">Paste a podcast's RSS feed URL above to subscribe.</p>
    </div>
  {:else}
    <ul class="list">
      {#each feeds as f (f.id)}
        <li>
          <a class="feed" href="/podcasts/feeds/{f.id}">
            <div class="art">
              {#if f.image_url}
                <img src={f.image_url} alt="" loading="lazy" referrerpolicy="no-referrer" />
              {:else}
                <span>🎙️</span>
              {/if}
            </div>
            <div class="meta">
              <div class="title">{f.title}</div>
              {#if f.author}<div class="author">{f.author}</div>{/if}
              <div class="counts">
                {countLabel(f)}{f.auto_download ? '' : ' · auto-download off'}
              </div>
              {#if f.last_error}<div class="feed-error">Last check failed: {f.last_error}</div>{/if}
            </div>
          </a>
        </li>
      {/each}
    </ul>
  {/if}
</section>

<style>
  .subs { margin-top: 0.5rem; display: flex; flex-direction: column; gap: 1.25rem; }
  .subscribe {
    background: var(--input-bg); border: 1px solid var(--border); border-radius: 10px;
    padding: 0.9rem; display: flex; flex-direction: column; gap: 0.6rem;
  }
  .row { display: flex; gap: 0.5rem; }
  .row input {
    flex: 1; min-width: 0; background: var(--bg-primary); color: var(--text-primary);
    border: 1px solid var(--border); border-radius: 7px; padding: 0.5rem 0.65rem; font-size: 0.85rem;
  }
  .btn-primary {
    background: var(--accent); color: #fff; border: none; border-radius: 7px;
    padding: 0.5rem 1rem; font-size: 0.82rem; font-weight: 600; cursor: pointer; white-space: nowrap;
  }
  .btn-primary:disabled { opacity: 0.6; cursor: default; }
  .opts { display: flex; flex-wrap: wrap; gap: 0.5rem 1.25rem; font-size: 0.78rem; color: var(--text-secondary); }
  .opts label { display: flex; align-items: center; gap: 0.4rem; }
  .opts select {
    background: var(--input-bg); color: var(--text-primary); border: 1px solid var(--border);
    border-radius: 6px; padding: 0.2rem 0.35rem; font-size: 0.78rem;
  }
  .hint { font-size: 0.72rem; color: var(--text-muted); margin: 0; }
  .form-error { color: var(--error); font-size: 0.78rem; margin: 0; }
  .muted { color: var(--text-muted); font-size: 0.85rem; }
  .list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 0.5rem; }
  .feed {
    display: flex; gap: 0.85rem; align-items: center; text-decoration: none; color: inherit;
    background: var(--input-bg); border: 1px solid var(--border); border-radius: 10px; padding: 0.6rem;
    transition: border-color 0.15s;
  }
  .feed:hover { border-color: rgba(124,106,247,0.35); }
  .art {
    width: 64px; height: 64px; flex-shrink: 0; border-radius: 6px; overflow: hidden;
    background: rgba(255,255,255,0.03); display: flex; align-items: center; justify-content: center; font-size: 1.6rem;
  }
  .art img { width: 100%; height: 100%; object-fit: cover; display: block; }
  .meta { min-width: 0; display: flex; flex-direction: column; gap: 0.15rem; }
  .title { font-weight: 600; font-size: 0.9rem; color: var(--text-primary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .author, .counts { font-size: 0.75rem; color: var(--text-muted); }
  .feed-error { font-size: 0.72rem; color: var(--error); }
  .empty { text-align: center; padding: 2.5rem 1rem; color: var(--text-muted); }
  .empty-t { font-weight: 700; color: var(--text-secondary); }
  .empty-s { font-size: 0.8rem; margin-top: 0.4rem; }
  .error-bar {
    background: var(--error-bg); color: var(--error); padding: 0.6rem 0.9rem;
    border-radius: 8px; font-size: 0.8rem; display: flex; gap: 0.75rem; align-items: center;
  }
  .retry {
    background: none; border: 1px solid currentColor; border-radius: 6px; color: inherit;
    font-size: 0.72rem; padding: 0.15rem 0.5rem; cursor: pointer;
  }
  @media (max-width: 560px) {
    .row { flex-direction: column; }
  }
</style>
