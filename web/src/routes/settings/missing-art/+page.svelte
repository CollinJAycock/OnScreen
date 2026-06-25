<script lang="ts">
  // Admin "Set Poster" tray. Items here have provider IDs (so they're
  // not in Fix Match) but no poster_path — typically because TMDB
  // returned no poster URL for the title or the auto-fetched download
  // failed. Each row offers two paths: pick from TMDB poster variants
  // (via the existing /items/{id}/posters endpoint, when the item has
  // a tmdb_id) and a paste-URL fallback for items where TMDB has no
  // posters at all (legitimate gaps for obscure titles).

  import { onMount } from 'svelte';
  import {
    missingArtApi,
    libraryApi,
    itemApi,
    type MissingArtItem,
    type Library,
    type PosterCandidate,
  } from '$lib/api';
  import { toast } from '$lib/stores/toast';

  let loading = true;
  let error = '';
  let items: MissingArtItem[] = [];
  let libraries: Record<string, Library> = {};

  let openId: string | null = null;
  let posters: PosterCandidate[] = [];
  let postersLoading = false;
  let postersError = '';
  let pasteUrl = '';
  let applying = false;

  onMount(async () => {
    try {
      const [m, libs] = await Promise.all([
        missingArtApi.list(),
        libraryApi.list(),
      ]);
      items = m.items;
      libraries = Object.fromEntries(libs.map((l) => [l.id, l]));
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed to load missing-art items';
    } finally {
      loading = false;
    }
  });

  function libraryName(id: string): string {
    return libraries[id]?.name ?? id.slice(0, 8);
  }

  async function openRow(item: MissingArtItem) {
    if (openId === item.id) {
      openId = null;
      posters = [];
      pasteUrl = '';
      postersError = '';
      return;
    }
    openId = item.id;
    posters = [];
    pasteUrl = '';
    postersError = '';
    if (item.tmdb_id) {
      postersLoading = true;
      try {
        posters = await itemApi.listPosters(item.id, item.tmdb_id);
      } catch (e: unknown) {
        postersError = e instanceof Error ? e.message : 'Failed to load poster variants';
      } finally {
        postersLoading = false;
      }
    }
  }

  async function applyUrl(item: MissingArtItem, url: string) {
    if (!url) return;
    applying = true;
    try {
      await itemApi.applyPoster(item.id, url);
      toast.success(`Poster set for "${item.title}"`);
      items = items.filter((i) => i.id !== item.id);
      openId = null;
      pasteUrl = '';
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Failed to apply poster');
    } finally {
      applying = false;
    }
  }
</script>

<svelte:head><title>Set Poster — OnScreen</title></svelte:head>

<div class="page">
  <p class="sub">
    Items the auto-fetcher couldn't pull a poster for. Pick a TMDB
    variant or paste a URL pointing to any image (jpg / png / webp).
    The image is downloaded and written next to the item's media files.
  </p>

  {#if loading}
    <div class="skeleton-block"></div>
  {:else if error}
    <p class="error">{error}</p>
  {:else if items.length === 0}
    <div class="empty">
      <svg width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <rect x="3" y="3" width="18" height="18" rx="2" />
        <circle cx="8.5" cy="8.5" r="1.5" />
        <path d="m21 15-5-5L5 21" />
      </svg>
      <span>All items have posters. Nothing to do here.</span>
    </div>
  {:else}
    <p class="count">{items.length} {items.length === 1 ? 'item needs' : 'items need'} a poster</p>

    <ul class="rows">
      {#each items as item (item.id)}
        <li class="row" class:open={openId === item.id}>
          <button type="button" class="row-head" on:click={() => openRow(item)}>
            <span class="type-pill type-{item.type}">{item.type}</span>
            <span class="title">{item.title}</span>
            {#if item.year}<span class="year">({item.year})</span>{/if}
            <span class="lib">{libraryName(item.library_id)}</span>
            <span class="chevron">{openId === item.id ? '▾' : '▸'}</span>
          </button>

          {#if openId === item.id}
            <div class="picker">
              {#if item.tmdb_id}
                <h3 class="picker-h">TMDB poster variants</h3>
                {#if postersLoading}
                  <p class="muted">Loading variants…</p>
                {:else if postersError}
                  <p class="error">{postersError}</p>
                {:else if posters.length === 0}
                  <p class="muted">TMDB has no posters for this title.</p>
                {:else}
                  <div class="poster-grid">
                    {#each posters as p (p.url)}
                      <button
                        type="button"
                        class="poster-btn"
                        disabled={applying}
                        on:click={() => applyUrl(item, p.url)}
                        title={p.language ? `lang=${p.language} vote=${p.vote.toFixed(1)}` : `vote=${p.vote.toFixed(1)}`}
                      >
                        <img src={p.url} alt="" loading="lazy" />
                      </button>
                    {/each}
                  </div>
                {/if}
              {:else}
                <p class="muted">No TMDB ID attached — paste a URL below.</p>
              {/if}

              <h3 class="picker-h">Paste direct image URL</h3>
              <p class="paste-hint">
                Must be a direct link to the image file (right-click → Copy
                image address). Page URLs from IMDB, Wikipedia, Google Images
                won't work.
              </p>
              <form class="paste-form" on:submit|preventDefault={() => applyUrl(item, pasteUrl.trim())}>
                <input
                  type="url"
                  class="paste-input"
                  placeholder="https://example.com/poster.jpg"
                  bind:value={pasteUrl}
                  required
                />
                <button type="submit" class="paste-btn" disabled={applying || !pasteUrl.trim()}>
                  {applying ? 'Applying…' : 'Apply'}
                </button>
              </form>
            </div>
          {/if}
        </li>
      {/each}
    </ul>
  {/if}
</div>

<style>
  .page { max-width: 720px; }
  .sub { color: var(--text-secondary); font-size: 0.85rem; line-height: 1.5; margin: 0 0 1.5rem; max-width: 60ch; }
  .muted { color: var(--text-muted); font-size: 0.9rem; }
  .empty {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 0.6rem;
    padding: 3rem 1rem;
    color: var(--text-muted);
    font-size: 0.85rem;
    text-align: center;
  }
  .error { color: var(--error); font-size: 0.9rem; }
  .count { color: var(--text-muted); font-size: 0.85rem; margin-bottom: 0.75rem; }

  .skeleton-block {
    height: 120px;
    border-radius: 10px;
    background: linear-gradient(90deg, var(--bg-elevated) 25%, var(--bg-hover) 50%, var(--bg-elevated) 75%);
    background-size: 200% 100%;
    animation: shimmer 1.4s infinite;
  }
  @keyframes shimmer {
    0% { background-position: 200% 0; }
    100% { background-position: -200% 0; }
  }

  .rows { list-style: none; padding: 0; margin: 0; display: flex; flex-direction: column; gap: 0.4rem; }
  .row {
    background: var(--bg-elevated);
    border: 1px solid var(--border);
    border-radius: 10px;
    overflow: hidden;
  }
  .row.open { border-color: var(--accent); }

  .row-head {
    width: 100%;
    display: flex;
    align-items: center;
    gap: 0.75rem;
    padding: 0.75rem 1rem;
    background: transparent;
    border: none;
    color: inherit;
    text-align: left;
    cursor: pointer;
    font-size: 0.9rem;
  }
  .row-head:hover { background: var(--bg-hover, rgba(255,255,255,0.04)); }

  .type-pill {
    font-size: 0.7rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    padding: 0.15rem 0.5rem;
    border-radius: 4px;
    background: var(--bg-hover);
    color: var(--text-muted);
    flex-shrink: 0;
  }
  .type-show { color: var(--info); }
  .type-movie { color: #fcd34d; }

  .title { flex: 1; font-weight: 500; color: var(--text-primary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .year { color: var(--text-muted); font-size: 0.8rem; flex-shrink: 0; }
  .lib { color: var(--text-muted); font-size: 0.75rem; flex-shrink: 0; }
  .chevron { color: var(--text-muted); flex-shrink: 0; }

  .picker { padding: 0.75rem 1rem 1rem; border-top: 1px solid var(--border); }
  .picker-h { font-size: 0.78rem; text-transform: uppercase; letter-spacing: 0.05em; color: var(--text-muted); margin: 0.5rem 0 0.5rem; font-weight: 600; }

  .poster-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(100px, 1fr)); gap: 0.5rem; margin-bottom: 1rem; }
  .poster-btn { padding: 0; background: transparent; border: 1px solid var(--border); border-radius: 4px; cursor: pointer; overflow: hidden; }
  .poster-btn:hover:not(:disabled) { border-color: var(--accent); }
  .poster-btn:disabled { opacity: 0.5; cursor: progress; }
  .poster-btn img { width: 100%; aspect-ratio: 2/3; object-fit: cover; display: block; }

  .paste-hint {
    color: var(--text-muted);
    font-size: 0.75rem;
    line-height: 1.5;
    margin: 0 0 0.6rem;
  }
  .paste-form { display: flex; gap: 0.5rem; }
  .paste-input {
    flex: 1;
    padding: 0.48rem 0.7rem;
    background: var(--input-bg);
    border: 1px solid var(--border-strong);
    border-radius: 7px;
    color: var(--text-primary);
    font-size: 0.85rem;
    font-family: inherit;
    outline: none;
    box-sizing: border-box;
  }
  .paste-input::placeholder { color: var(--text-muted); }
  .paste-input:focus { border-color: var(--accent); box-shadow: 0 0 0 3px var(--accent-bg); }
  .paste-btn {
    padding: 0.45rem 0.9rem;
    background: var(--accent);
    color: #fff;
    border: none;
    border-radius: 7px;
    font-size: 0.8rem;
    font-weight: 600;
    cursor: pointer;
  }
  .paste-btn:hover:not(:disabled) { background: var(--accent-hover); }
  .paste-btn:disabled { opacity: 0.5; cursor: not-allowed; }
</style>
