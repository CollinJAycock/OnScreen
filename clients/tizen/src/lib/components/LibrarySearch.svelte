<script lang="ts">
  // "Search this library": a panel over the library page with the search
  // page's on-screen keyboard and result tiles, the search narrowed to this
  // library (library_id). use:focusScope keeps the D-pad in it; Back closes
  // it (pushed on the back stack while it's up), so Back from the grid still
  // leaves the page as before, and Back here only takes the panel down. The
  // page owns focus after close (the Search chip that opened it).
  //
  // Results open as the search page's do (openItem by type). Back from one
  // brings the page up again; the page then reopens the panel on the same
  // text (`query`) and passes the focus note on (`memo`), so the result
  // that was opened takes focus again.
  import { onMount, tick, untrack } from 'svelte';
  import { goto } from '$app/navigation';
  import { endpoints, Unauthorized } from '$lib/api';
  import { focusScope } from '$lib/focus/focusable';
  import { focusManager } from '$lib/focus/manager';
  import { focusFirstOf, restoreGuard, restoreKeyed, type FocusMemo } from '$lib/focus/memory';
  import { i18n } from '$lib/i18n/index.svelte';
  import { LibrarySearch, MIN_QUERY, resultTypeLabel, searchResultKey, type SearchState } from '$lib/librarySearch';
  import { openItem } from '$lib/nav';
  import OnScreenKeyboard from './OnScreenKeyboard.svelte';
  import PosterCard from './PosterCard.svelte';

  interface Props {
    libraryId: string;
    libraryName: string;
    /** The text to open on (Back from a result); empty for a fresh search. */
    query?: string;
    /** Back from a result: the focus note naming it. */
    memo?: FocusMemo | null;
    /** The text changed (the page keeps it for Back from a result). */
    onquery?: (q: string) => void;
    onclose: () => void;
  }
  let { libraryId, libraryName, query: initialQuery = '', memo = null, onquery, onclose }: Props = $props();

  // The panel is mounted per opening: the text it opens on is a start, not
  // a binding.
  let query = $state(untrack(() => initialQuery));
  let view = $state<SearchState>({ kind: 'idle' });

  const search = new LibrarySearch({
    libraryId: () => libraryId,
    search: (q, limit, id) => endpoints.search.query(q, limit, id),
    onstate: (s) => {
      view = s;
      if (s.kind === 'failed' && s.error instanceof Unauthorized) goto('#/login');
    },
  });

  function onQueryChange(v: string) {
    query = v;
    onquery?.(v);
    search.type(v);
  }

  onMount(() => {
    // The keyboard takes focus at once (nothing behind the panel may keep
    // it). Back from a result: the same search, then focus moves on to that
    // result, unless the user moved it first (lib/focus/memory).
    const guard = restoreGuard();
    queueMicrotask(() => focusFirstOf('.library-search .osk [data-focusable]', guard));
    if (query.trim().length >= MIN_QUERY) {
      (async () => {
        const applied = await search.now(query);
        await tick();
        if (applied && memo?.focusedId) restoreKeyed(memo, guard);
      })();
    }
    const offBack = focusManager.pushBack(() => {
      onclose();
      return true;
    });
    return () => {
      guard.end();
      offBack();
      search.dispose();
    };
  });
</script>

<div class="library-search" role="dialog" aria-modal="true" aria-labelledby="library-search-title" use:focusScope>
  <h1 id="library-search-title">{$i18n('library.searchTitle', { library: libraryName })}</h1>

  <!-- Exitable: Down from its last row reaches the results, as on the
       search page. Left / Right stay in the keyboard. -->
  <OnScreenKeyboard value={query} onchange={onQueryChange} exitable />

  <section class="results">
    {#if view.kind === 'searching'}
      <p class="status" role="status">{$i18n('browse.searching')}</p>
    {:else if view.kind === 'idle' || query.trim().length < MIN_QUERY}
      <p class="status">{$i18n.n('browse.typeMore', MIN_QUERY)}</p>
    {:else if view.kind === 'failed'}
      <p class="status">{$i18n('browse.searchFailed')}</p>
    {:else if view.results.length === 0}
      <p class="status">{$i18n('library.searchNoMatch', { library: libraryName, query: query.trim() })}</p>
    {:else}
      <div class="grid">
        {#each view.results as r (r.id)}
          <PosterCard
            title={r.title}
            posterPath={r.poster_path ?? r.thumb_path}
            subtitle={r.year ? String(r.year) : resultTypeLabel(r.type)}
            focusKey={searchResultKey(r.id)}
            onclick={() => openItem(r.id, r.type)}
          />
        {/each}
      </div>
    {/if}
  </section>
</div>

<style>
  /* Over the whole page, the grid left as it is under it. Spelled-out
     offsets instead of `inset` (Chrome 87), and a one-column grid rather
     than a flex column: grid `gap` works on webOS 6's Chromium 79, flexbox
     `gap` needs Chrome 84. It scrolls itself: the focus manager's
     scrollIntoView brings a result row in, scroll-padding leaving room for
     the focus ring's scale and glow. */
  .library-search {
    position: fixed;
    top: 0;
    right: 0;
    bottom: 0;
    left: 0;
    z-index: 50;
    overflow-x: hidden;
    overflow-y: auto;
    scroll-padding: 64px 0;
    background: var(--bg-primary);
    padding: 0 var(--page-pad) var(--page-pad);
    display: grid;
    grid-template-columns: minmax(0, 1fr);
    align-content: start;
    row-gap: 24px;
  }
  .library-search::-webkit-scrollbar {
    display: none;
  }
  h1 {
    font-size: var(--font-2xl);
    margin: 48px 0 0;
  }
  .results {
    min-height: 400px;
  }
  .status {
    font-size: var(--font-md);
    color: var(--text-secondary);
  }
  .grid {
    display: grid;
    grid-template-columns: repeat(6, 1fr);
    gap: var(--card-gap);
    row-gap: calc(var(--card-gap) + 24px);
  }
</style>
