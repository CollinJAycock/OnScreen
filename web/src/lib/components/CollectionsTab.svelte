<script lang="ts">
  // The library page's "Collections" tab (movie and show libraries):
  // franchise collections (TMDB collections the server holds at least two
  // films of) and admin-made collections, each with at least one title in
  // this library the viewer can see.
  import { collectionApi, assetUrl, type LibraryCollection } from '$lib/api';
  import { collectionCover } from '$lib/franchise';

  interface Props {
    libraryId: string;
    /** The library's type; film series only exist in movie libraries. */
    libraryType?: string;
  }

  let { libraryId, libraryType = 'movie' }: Props = $props();

  /** "3 films" for a film series, "3 titles" for a collection of anything. */
  function countLabel(c: LibraryCollection): string {
    const noun = c.type === 'franchise' ? 'film' : 'title';
    return `${c.item_count} ${noun}${c.item_count === 1 ? '' : 's'}`;
  }

  let collections = $state<LibraryCollection[]>([]);
  let loading = $state(true);
  let error = $state('');
  // Guards against a slow response for a previous library landing after
  // the user switched libraries.
  let seq = 0;

  async function load(id: string) {
    const mine = ++seq;
    loading = true;
    error = '';
    try {
      const rows = await collectionApi.forLibrary(id);
      if (mine !== seq) return;
      collections = rows ?? [];
    } catch (e) {
      if (mine !== seq) return;
      collections = [];
      error = e instanceof Error && e.message ? e.message : 'Could not load collections';
    } finally {
      if (mine === seq) loading = false;
    }
  }

  $effect(() => {
    load(libraryId);
  });
</script>

<section class="collections-tab" aria-label="Collections">
  {#if loading}
    <p class="muted" role="status">Loading collections…</p>
  {:else if error}
    <div class="error-bar" role="alert">
      {error}
      <button type="button" class="retry" onclick={() => load(libraryId)}>Retry</button>
    </div>
  {:else if collections.length === 0}
    <div class="empty">
      <p class="empty-t">No collections yet</p>
      <p class="empty-s">
        {#if libraryType === 'movie'}
          Film series such as the Toy Story or Alien movies appear here once two of their films are in your
          library, along with any collections an admin makes.
        {:else}
          Collections an admin makes with this library's titles appear here.
        {/if}
      </p>
    </div>
  {:else}
    <div class="grid">
      {#each collections as c (c.id)}
        {@const cover = collectionCover(c)}
        <a class="card" href="/collections/{c.id}" title={c.name}>
          <div class="poster">
            {#if cover.url}
              <img src={cover.url} alt={c.name} loading="lazy" referrerpolicy="no-referrer" />
            {:else if cover.artwork}
              <img src={assetUrl(`/artwork/${encodeURI(cover.artwork)}?w=300`)} alt={c.name} loading="lazy" />
            {:else}
              <div class="poster-blank">{c.name[0]?.toUpperCase() ?? '◇'}</div>
            {/if}
          </div>
          <div class="name">{c.name}</div>
          <div class="count">{countLabel(c)}</div>
        </a>
      {/each}
    </div>
  {/if}
</section>

<style>
  .collections-tab { margin-top: 0.5rem; }
  .muted { color: var(--text-muted); font-size: 0.85rem; }
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(150px, 1fr));
    gap: 1rem;
  }
  .card {
    display: flex; flex-direction: column; text-decoration: none; color: inherit;
    border-radius: 8px; overflow: hidden;
    background: var(--input-bg); border: 1px solid var(--border);
    transition: border-color 0.15s, transform 0.15s;
  }
  .card:hover { border-color: rgba(124,106,247,0.3); transform: translateY(-2px); }
  .poster { aspect-ratio: 2/3; background: rgba(255,255,255,0.02); }
  .poster img { width: 100%; height: 100%; object-fit: cover; display: block; }
  .poster-blank {
    width: 100%; height: 100%; display: flex; align-items: center; justify-content: center;
    font-size: 2rem; color: var(--text-muted);
  }
  .name {
    padding: 0.55rem 0.5rem 0.1rem; font-size: 0.8rem; font-weight: 600; color: var(--text-primary);
    white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
  }
  .count { padding: 0 0.5rem 0.6rem; font-size: 0.7rem; color: var(--text-muted); }
  .empty { text-align: center; padding: 3.5rem 1rem; color: var(--text-muted); }
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
</style>
