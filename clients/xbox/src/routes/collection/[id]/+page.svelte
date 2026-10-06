<script lang="ts">
  // Collection detail — grid of items in a curated list (auto-genre,
  // smart playlist, manual playlist all render the same way; collection
  // type is informational). Mirrors the Android CollectionFragment,
  // including paging: 100 items at a time, the next page requested as
  // focus nears the end, so a big auto-genre collection isn't cut off.
  // Back from a title puts focus on its card again, refilling the pages
  // the grid had (lib/focus/memory), as the library grid does.

  import { onMount, tick } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/state';
  import {
    endpoints,
    Unauthorized,
    type CollectionItem,
    type MediaCollection
  } from '$lib/api';
  import { focusable } from '$lib/focus/focusable';
  import { focusManager } from '$lib/focus/manager';
  import {
    focusFirstOf,
    restoreGuard,
    restoreKeyed,
    takeFocusMemo,
    type FocusMemo,
    type RestoreGuard,
  } from '$lib/focus/memory';
  import PosterCard from '$lib/components/PosterCard.svelte';
  import Spinner from '$lib/components/Spinner.svelte';
  import { openItem, goBack } from '$lib/nav';
  import { Pager, nearEnd, refill } from '$lib/paging';

  // Six posters a row (see .grid).
  const COLUMNS = 6;
  const PAGE_SIZE = 100;

  const collectionId = $derived(page.params.id!);

  let collection = $state<MediaCollection | null>(null);
  let items = $state.raw<CollectionItem[] | null>(null);
  let error = $state('');
  let loadingMore = $state(false);
  // The pager stopped on a run of pages the server emptied of hidden rows,
  // with rows still owed by the total (Pager.stalled). Focus near the end
  // carries on; Load more is there for when no card is left to move to (an
  // empty grid so far, or focus already on the last card).
  let stalled = $state(false);
  // Back from a title: the first card holds its autofocus while the grid
  // refills and the card the user opened takes focus again.
  let restoring = $state(false);
  // False once the page is gone. A load still on its way then must not move
  // focus: it is the next page's by then, and a move would also end that
  // page's own restore (its RestoreGuard sees a focus it didn't place).
  let alive = true;

  // Pages on meta.total, the count of rows the caller can see: for a
  // playlist the server drops rows from libraries the caller can't access
  // AFTER paging, so a page can come back short or even empty with visible
  // rows after it (see Pager.loadMore). shortPageEnds off covers a server
  // that sends no total: only an empty page ends the list then.
  const pager = new Pager<CollectionItem>((i) => i.id, PAGE_SIZE, false);

  onMount(() => {
    const memo = takeFocusMemo();
    restoring = !!memo?.focusedId;
    // Ends the restore when the page goes away or the user moves focus first.
    const guard = restoreGuard();
    (async () => {
      try {
        // Fetch collection metadata + the first page in parallel — the
        // title header should appear at the same time as the grid.
        const [meta] = await Promise.all([
          endpoints.collections.get(collectionId).then((m) => {
            // An auto-genre collection is filtered in the same SQL that
            // pages it (internal/api/v1/collections.go), so an empty page
            // is its end even below a total that drifted mid-browse.
            pager.emptyPageEnds = m.type === 'auto_genre' && !!m.genre;
            return m;
          }),
          loadMore(true),
        ]);
        collection = meta;
        if (memo && restoring) {
          await tick();
          await restoreGrid(memo, guard);
        }
      } catch (e) {
        if (e instanceof Unauthorized) goto('#/login');
        else error = (e as Error).message;
      } finally {
        restoring = false;
      }
    })();

    const offBack = focusManager.pushBack(() => {
      goBack();
      return true;
    });
    return () => {
      alive = false;
      guard.end();
      offBack();
    };
  });

  // Back from a title: fetch again the pages the grid had and focus the
  // card as soon as its page is in; the first card when it never shows up
  // (removed from the playlist, or past refill's page cap). Stops once
  // `guard` does (the page went away, or the user moved focus first).
  async function restoreGrid(memo: FocusMemo, guard: RestoreGuard<HTMLElement>) {
    let found = restoreKeyed(memo, guard);
    const loadWhileRestoring = () => (guard.active ? loadMore() : Promise.resolve(false));
    await refill(pager, loadWhileRestoring, memo.loadedCount, async () => {
      if (found) return;
      await tick();
      found = restoreKeyed(memo, guard);
    });
    if (!found) focusFirstOf('.grid [data-focusable]', guard);
  }

  /** The next page. The first page's failure is the page's error (thrown
   *  with `first`); a failed later page keeps the grid, and the next focus
   *  near the end retries it. True when the page changed the grid. */
  async function loadMore(first = false): Promise<boolean> {
    const before = items?.length ?? 0;
    const wasStalled = stalled;
    let changed = false;
    loadingMore = true;
    try {
      changed = await pager.loadMore((offset, limit) =>
        endpoints.collections.items(collectionId, limit, offset),
      );
      if (changed) items = pager.items;
    } catch (e) {
      if (first) throw e;
      if (e instanceof Unauthorized) goto('#/login');
    } finally {
      loadingMore = pager.loading;
      stalled = pager.stalled;
    }
    if (alive && wasStalled && !stalled) await focusAfterStall(before);
    return changed;
  }

  // A load ended the stall, so the Load more button goes away, perhaps
  // from under the focus: whichever call started the load (the button, or
  // focus on the last row before the user moved down onto the button). With
  // focus on the button or on nothing, it goes to the first of the new cards
  // (else the next key would land on the grid's first card, at the top). A
  // card the user is on keeps it. Nothing once the page is gone (`alive`).
  async function focusAfterStall(before: number) {
    if (!alive) return;
    const cur = focusManager.currentElement();
    if (cur && !cur.classList.contains('more-btn')) return;
    await tick();
    if (!alive) return;
    const now = focusManager.currentElement();
    if (now && !now.classList.contains('more-btn')) return;
    const cards = document.querySelectorAll<HTMLElement>('.grid [data-focusable]');
    const next = cards[before] ?? cards[cards.length - 1];
    if (next) focusManager.focus(next);
    else focusManager.refocus();
  }

  // Load more, pressed: carries on from where the pager stopped (see
  // focusAfterStall for where focus goes once the rows turn up).
  function pressLoadMore() {
    void loadMore();
  }

  function onCardFocus(index: number) {
    if (!pager.done && nearEnd(index, items?.length ?? 0, COLUMNS)) void loadMore();
  }
</script>

<div class="page">
  {#if error}
    <p class="error">{error}</p>
  {:else if !collection || !items}
    <Spinner />
  {:else}
    <h1>{collection.name}</h1>
    {#if collection.description}<p class="desc">{collection.description}</p>{/if}

    {#if items.length === 0 && !stalled}
      <p class="empty">This collection is empty.</p>
    {:else}
      {#if items.length === 0}
        <p class="empty">Nothing you can see here so far.</p>
      {:else}
        <div class="grid">
          {#each items as it, i (it.id)}
            <PosterCard
              title={it.title}
              posterPath={it.poster_path}
              subtitle={it.year ? String(it.year) : undefined}
              focusKey={it.id}
              autofocus={i === 0 && !restoring}
              onclick={() => openItem(it.id, it.type)}
              onfocus={() => onCardFocus(i)}
            />
          {/each}
        </div>
      {/if}
      {#if stalled}
        <button
          use:focusable={{ autofocus: items.length === 0 }}
          class="more-btn"
          onclick={pressLoadMore}
        >
          {loadingMore ? 'Loading…' : 'Load more'}
        </button>
      {:else if loadingMore}
        <p class="more" role="status">Loading more…</p>
      {/if}
    {/if}
  {/if}
</div>

<style>
  /* A one-column grid rather than a flex column: grid `gap` works on
     webOS 6's Chromium 79, flexbox `gap` needs Chrome 84. */
  .page {
    padding: var(--page-pad);
    display: grid;
    grid-template-columns: minmax(0, 1fr);
    align-content: start;
    row-gap: 24px;
  }
  h1 {
    font-size: var(--font-2xl);
    margin: 0;
  }
  .desc {
    color: var(--text-secondary);
    font-size: var(--font-md);
    margin: 0;
  }
  .empty, .error, .more {
    color: var(--text-secondary);
    font-size: var(--font-md);
  }
  .more {
    margin: 8px 0 0;
    font-size: var(--font-sm);
  }
  .grid {
    display: grid;
    grid-template-columns: repeat(6, 1fr);
    gap: var(--card-gap);
  }
  .more-btn {
    justify-self: start;
    font-family: inherit;
    font-size: var(--font-md);
    padding: 16px 40px;
    border-radius: 12px;
    border: 2px solid var(--border);
    background: var(--bg-elevated);
    color: var(--text-primary);
    cursor: pointer;
  }
</style>
