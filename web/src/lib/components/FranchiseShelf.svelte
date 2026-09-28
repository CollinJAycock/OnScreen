<script lang="ts">
  // "Part of the <Name>" shelf on a movie page: the franchise's other films,
  // owned first. Owned films link to their page; missing ones are greyed and
  // point at the collection page, where they can be requested.
  import { collectionApi, type FranchisePart, type ItemCollectionRef } from '$lib/api';
  import { partOfHeading, partState, requestStatusLabel, shelfParts } from '$lib/franchise';

  interface Props {
    collection: ItemCollectionRef;
    currentItemId: string;
  }

  let { collection, currentItemId }: Props = $props();

  let parts = $state<FranchisePart[]>([]);
  let loaded = $state(false);
  let seq = 0;

  async function load(id: string, current: string) {
    const mine = ++seq;
    loaded = false;
    try {
      const col = await collectionApi.get(id);
      if (mine !== seq) return;
      parts = shelfParts(col.parts ?? [], current);
    } catch {
      if (mine !== seq) return;
      parts = []; // non-critical: the shelf just doesn't render
    } finally {
      if (mine === seq) loaded = true;
    }
  }

  $effect(() => {
    load(collection.id, currentItemId);
  });
</script>

{#if loaded && parts.length > 0}
  <section class="franchise-shelf" aria-label={partOfHeading(collection.name)}>
    <div class="head">
      <h3>{partOfHeading(collection.name)}</h3>
      <a class="see-all" href="/collections/{collection.id}">See all</a>
    </div>
    <div class="row">
      {#each parts as p (p.item_id ?? `tmdb-${p.tmdb_id}`)}
        {@const ps = partState(p)}
        <a
          class="part"
          class:missing={ps !== 'owned'}
          href={p.item_id ? `/watch/${p.item_id}` : `/collections/${collection.id}`}
          title={ps === 'owned' ? p.title : `${p.title} — not in your library`}
        >
          <div class="poster">
            {#if p.poster_url}
              <img src={p.poster_url} alt={p.title} loading="lazy" referrerpolicy="no-referrer" />
            {:else}
              <div class="poster-blank">{p.title[0]?.toUpperCase() ?? '?'}</div>
            {/if}
            {#if ps === 'requested'}
              <span class="badge">{requestStatusLabel(p.request_status)}</span>
            {:else if ps === 'missing'}
              <span class="badge">Not in library</span>
            {/if}
          </div>
          <div class="title">{p.title}</div>
          {#if p.year}<div class="year">{p.year}</div>{/if}
        </a>
      {/each}
    </div>
  </section>
{/if}

<style>
  .franchise-shelf { margin-bottom: 2rem; }
  .head { display: flex; align-items: baseline; gap: 0.75rem; margin-bottom: 0.75rem; }
  h3 { font-size: 0.95rem; font-weight: 700; color: var(--text-primary); margin: 0; }
  .see-all { font-size: 0.75rem; color: var(--accent-text, var(--accent)); text-decoration: none; }
  .see-all:hover { text-decoration: underline; }
  .row { display: flex; gap: 0.85rem; overflow-x: auto; padding-bottom: 0.4rem; }
  /* min-width: 0 — otherwise the flex item's minimum width is its nowrap
     title's full length and a long title widens the slot past the poster. */
  .part { flex: 0 0 120px; min-width: 0; text-decoration: none; color: inherit; }
  .poster {
    position: relative; aspect-ratio: 2/3; border-radius: 6px; overflow: hidden;
    background: rgba(255,255,255,0.03); border: 1px solid var(--border);
  }
  .poster img { width: 100%; height: 100%; object-fit: cover; display: block; }
  .poster-blank {
    width: 100%; height: 100%; display: flex; align-items: center; justify-content: center;
    font-size: 1.6rem; color: var(--text-muted);
  }
  .part.missing .poster img, .part.missing .poster-blank { filter: grayscale(1); opacity: 0.45; }
  .badge {
    position: absolute; left: 0.3rem; bottom: 0.3rem; right: 0.3rem; text-align: center;
    background: rgba(0,0,0,0.7); color: #ddd; font-size: 0.62rem; font-weight: 600;
    border-radius: 4px; padding: 0.15rem 0.2rem;
  }
  .title {
    margin-top: 0.35rem; font-size: 0.74rem; font-weight: 600; color: var(--text-primary);
    white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
  }
  .part.missing .title { color: var(--text-muted); }
  .year { font-size: 0.68rem; color: var(--text-muted); }
  .part:hover .poster { border-color: rgba(124,106,247,0.35); }
</style>
