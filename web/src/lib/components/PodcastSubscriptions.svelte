<script lang="ts">
  // The podcast library page's "Subscriptions" tab (admins): subscribe the
  // library to an RSS feed, and list its feeds. Episodes download into the
  // library's folder and appear in the Library tab once scanned; each feed's
  // page lists its episodes and settings.
  import { podcastApi, type PodcastFeed } from '$lib/api';
  import { toast } from '$lib/stores/toast';
  import { t, tn, tr } from '$lib/i18n/index.svelte';

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

  // 0 = keep every episode; otherwise the newest n.
  const keepOptions: number[] = [0, 1, 3, 5, 10, 25, 50];

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
      error = e instanceof Error && e.message ? e.message : t('ui.podcasts.loadFailed');
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
    if (!url) { formError = t('ui.podcasts.urlRequired'); return; }
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
          ? tn('ui.podcasts.subscribedDownloading', n, { title: feed.title })
          : t('ui.podcasts.subscribed', { title: feed.title })
      );
      await load(libraryId);
      onchange?.();
    } catch (err) {
      formError = err instanceof Error && err.message ? err.message : t('ui.podcasts.subscribeFailed');
    } finally {
      subscribing = false;
    }
  }

  function countLabel(f: PodcastFeed): string {
    const n = f.episode_count ?? 0;
    const d = f.downloaded_count ?? 0;
    return tn('ui.podcasts.counts', n, { downloaded: d });
  }
</script>

<section class="subs" aria-label={$tr('ui.podcasts.label')}>
  <form class="subscribe" onsubmit={subscribe}>
    <div class="row">
      <input type="url" bind:value={feedURL} placeholder="https://example.com/podcast/feed.xml"
             aria-label={$tr('ui.podcasts.feedUrl')} autocomplete="off" disabled={subscribing} />
      <button type="submit" class="btn-primary" disabled={subscribing}>
        {subscribing ? $tr('ui.podcasts.subscribing') : $tr('ui.podcasts.subscribe')}
      </button>
    </div>
    <div class="opts">
      <label class="check">
        <input type="checkbox" bind:checked={autoDownload} />
        {$tr('ui.podcasts.autoDownload')}
      </label>
      <label>
        {$tr('ui.podcasts.downloadNow')}
        <select bind:value={downloadNow}>
          <option value={0}>{$tr('player.subStyle.none')}</option>
          <option value={1}>{$tr('ui.podcasts.newestEpisode')}</option>
          <option value={3}>{$tr('ui.podcasts.newestN', { n: 3 })}</option>
          <option value={5}>{$tr('ui.podcasts.newestN', { n: 5 })}</option>
          <option value={10}>{$tr('ui.podcasts.newestN', { n: 10 })}</option>
        </select>
      </label>
      <label>
        {$tr('ui.podcasts.keep')}
        <select bind:value={keepLatest}>
          {#each keepOptions as n}
            <option value={n}>{n === 0 ? $tr('ui.podcasts.allEpisodes') : $tr('ui.podcasts.newestN', { n })}</option>
          {/each}
        </select>
      </label>
    </div>
    {#if formError}<p class="form-error" role="alert">{formError}</p>{/if}
    <p class="hint">{$tr('ui.podcasts.hint')}</p>
  </form>

  {#if loading}
    <p class="muted" role="status">{$tr('ui.podcasts.loading')}</p>
  {:else if error}
    <div class="error-bar" role="alert">
      {error}
      <button type="button" class="retry" onclick={() => load(libraryId)}>{$tr('ui.podcasts.retry')}</button>
    </div>
  {:else if feeds.length === 0}
    <div class="empty">
      <p class="empty-t">{$tr('ui.podcasts.emptyTitle')}</p>
      <p class="empty-s">{$tr('ui.podcasts.emptyText')}</p>
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
                {countLabel(f)}{f.auto_download ? '' : ` · ${$tr('ui.podcasts.autoOff')}`}
              </div>
              {#if f.last_error}<div class="feed-error">{$tr('ui.podcasts.lastCheckFailed', { error: f.last_error })}</div>{/if}
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
