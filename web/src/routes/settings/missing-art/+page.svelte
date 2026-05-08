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
  <header class="head">
    <h1>Set Poster</h1>
    <p class="sub">
      Items the auto-fetcher couldn't pull a poster for. Pick a TMDB
      variant or paste a URL pointing to any image (jpg / png / webp).
      The image is downloaded and written next to the item's media files.
    </p>
  </header>

  {#if loading}
    <p class="muted">Loading…</p>
  {:else if error}
    <p class="error">{error}</p>
  {:else if items.length === 0}
    <p class="muted empty">All items have posters. Nothing to do here. ✅</p>
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

              <h3 class="picker-h">Paste image URL</h3>
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
  .page { max-width: 920px; margin: 0 auto; padding: 1.5rem; }
  .head { margin-bottom: 1.5rem; }
  h1 { font-size: 1.4rem; margin: 0 0 0.5rem; }
  .sub { color: var(--text-muted); font-size: 0.85rem; line-height: 1.5; margin: 0; max-width: 720px; }
  .muted { color: var(--text-muted); font-size: 0.9rem; }
  .empty { padding: 3rem 1rem; text-align: center; }
  .error { color: var(--error, #f08080); font-size: 0.9rem; }
  .count { color: var(--text-muted); font-size: 0.85rem; margin-bottom: 0.75rem; }

  .rows { list-style: none; padding: 0; margin: 0; display: flex; flex-direction: column; gap: 0.4rem; }
  .row {
    background: var(--bg-surface, #14141e);
    border: 1px solid var(--border, rgba(255,255,255,0.08));
    border-radius: 8px;
    overflow: hidden;
  }
  .row.open { border-color: rgba(124,106,247,0.4); }

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
  .type-show { color: #4f8df0; }
  .type-movie { color: #f0c14f; }

  .title { flex: 1; font-weight: 500; color: var(--text-primary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .year { color: var(--text-muted); font-size: 0.8rem; flex-shrink: 0; }
  .lib { color: var(--text-muted); font-size: 0.75rem; flex-shrink: 0; }
  .chevron { color: var(--text-muted); flex-shrink: 0; }

  .picker { padding: 0.75rem 1rem 1rem; border-top: 1px solid var(--border); }
  .picker-h { font-size: 0.78rem; text-transform: uppercase; letter-spacing: 0.05em; color: var(--text-muted); margin: 0.5rem 0 0.5rem; font-weight: 600; }

  .poster-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(100px, 1fr)); gap: 0.5rem; margin-bottom: 1rem; }
  .poster-btn { padding: 0; background: transparent; border: 1px solid var(--border); border-radius: 4px; cursor: pointer; overflow: hidden; }
  .poster-btn:hover:not(:disabled) { border-color: rgba(124,106,247,0.4); }
  .poster-btn:disabled { opacity: 0.5; cursor: progress; }
  .poster-btn img { width: 100%; aspect-ratio: 2/3; object-fit: cover; display: block; }

  .paste-form { display: flex; gap: 0.5rem; }
  .paste-input {
    flex: 1;
    padding: 0.55rem 0.75rem;
    background: var(--bg-primary);
    border: 1px solid var(--border-strong);
    border-radius: 6px;
    color: var(--text-primary);
    font-size: 0.85rem;
    outline: none;
    box-sizing: border-box;
  }
  .paste-input:focus { border-color: rgba(124,106,247,0.5); }
  .paste-btn {
    padding: 0.55rem 1rem;
    background: var(--accent);
    color: #fff;
    border: none;
    border-radius: 6px;
    font-size: 0.85rem;
    font-weight: 500;
    cursor: pointer;
  }
  .paste-btn:hover:not(:disabled) { background: #6b5ce6; }
  .paste-btn:disabled { opacity: 0.5; cursor: not-allowed; }
</style>
