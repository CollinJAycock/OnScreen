<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/state';
  import { endpoints, Unauthorized, type MediaItem, type Library, type WatchFilter } from '$lib/api';
  import { focusable } from '$lib/focus/focusable';
  import { focusManager } from '$lib/focus/manager';
  import PosterCard from '$lib/components/PosterCard.svelte';
  import Spinner from '$lib/components/Spinner.svelte';
  import TopNav from '$lib/components/TopNav.svelte';
  import { openItem, goBack } from '$lib/nav';
  import {
    WATCH_FILTER_OPTIONS,
    cardWatchBadge,
    endpointMissing,
    errorStatus,
    hasWatchFields,
    parseWatchFilter,
    supportsWatchState,
  } from '$lib/watchState';

  let items = $state<MediaItem[] | null>(null);
  let library = $state<Library | null>(null);
  let error = $state('');

  const libraryID = $derived(page.params.id!);

  // Watch filter + Surprise me (v2.5). Offered for video libraries on a
  // server whose listing carries watch state; older servers ignore ?watch=
  // and have no /random, so they keep the plain grid.
  let watch = $state<WatchFilter | ''>('');
  let watchAware = $state(false);
  let surpriseMissing = $state(false);
  let surprising = $state(false);
  let notice = $state('');
  // Cards autofocus on the first load only; a filter change keeps focus on
  // the filter chip the user just pressed.
  let firstLoad = $state(true);

  const showWatchControls = $derived(watchAware && supportsWatchState(library?.type));

  // The filter survives a trip into an item and Back (the page remounts).
  const filterKey = $derived(`onscreen.watch_filter.${libraryID}`);
  function loadStoredFilter(): WatchFilter | '' {
    try {
      return parseWatchFilter(sessionStorage.getItem(filterKey));
    } catch {
      return '';
    }
  }
  function storeFilter(f: WatchFilter | '') {
    try {
      if (f) sessionStorage.setItem(filterKey, f);
      else sessionStorage.removeItem(filterKey);
    } catch {
      /* storage unavailable — the filter just resets on return */
    }
  }

  onMount(() => {
    watch = loadStoredFilter();
    (async () => {
      try {
        // Fire item-list + library-list in parallel — list-by-id
        // isn't exposed yet, so we filter the small library list
        // client-side for the header label.
        const [libs, libItems] = await Promise.all([
          endpoints.libraries.list(),
          endpoints.libraries.listItems(libraryID, 'title', 'asc', watch),
        ]);
        library = libs.find((l) => l.id === libraryID) ?? null;
        // An empty answer to a stored filter is a v2.5 server with no
        // match (an older one ignores ?watch= and lists everything).
        watchAware = hasWatchFields(libItems) || (watch !== '' && libItems.length === 0);
        if (!watchAware && watch) {
          // Stored filter on a server that doesn't know it — show everything.
          watch = '';
          storeFilter('');
        }
        items = libItems;
        await tick();
      } catch (e) {
        if (e instanceof Unauthorized) goto('#/login');
        else error = (e as Error).message;
      } finally {
        firstLoad = false;
      }
    })();

    return focusManager.pushBack(() => {
      goBack();
      return true;
    });
  });

  async function setWatch(next: WatchFilter | '') {
    if (next === watch && items) return;
    watch = next;
    storeFilter(next);
    notice = '';
    items = null;
    error = '';
    try {
      items = await endpoints.libraries.listItems(libraryID, 'title', 'asc', next);
    } catch (e) {
      if (e instanceof Unauthorized) goto('#/login');
      else error = (e as Error).message;
    }
  }

  async function surprise() {
    if (surprising) return;
    surprising = true;
    notice = '';
    try {
      const pick = await endpoints.libraries.random(libraryID, watch);
      openItem(pick.id, pick.type);
    } catch (e) {
      if (e instanceof Unauthorized) goto('#/login');
      else if (errorStatus(e) === 404 && (e as { code?: string }).code === 'NOT_FOUND') {
        notice = 'Nothing matches the current filter.';
      } else if (endpointMissing(e)) {
        surpriseMissing = true;
      } else {
        notice = "Couldn't pick a title.";
      }
    } finally {
      surprising = false;
    }
  }
</script>

<div class="page">
  <TopNav />
  <h1>{library?.name ?? 'Library'}</h1>

  {#if showWatchControls}
    <div class="filters">
      {#each WATCH_FILTER_OPTIONS as opt (opt.value)}
        <!-- A stored filter with no matches leaves no card to land on:
             start on the active chip instead. -->
        <button
          use:focusable={{ autofocus: firstLoad && items?.length === 0 && watch === opt.value }}
          class="chip"
          class:active={watch === opt.value}
          onclick={() => void setWatch(opt.value)}
        >
          {opt.label}
        </button>
      {/each}
      {#if !surpriseMissing}
        <button use:focusable class="chip surprise" onclick={() => void surprise()} disabled={surprising}>
          {surprising ? 'Picking…' : 'Surprise me'}
        </button>
      {/if}
      {#if notice}<span class="notice" role="status">{notice}</span>{/if}
    </div>
  {/if}

  {#if error}
    <p class="error">{error}</p>
  {:else if !items}
    <Spinner />
  {:else if items.length === 0}
    <p class="empty">{watch ? 'Nothing matches this filter.' : 'This library is empty.'}</p>
  {:else}
    <div class="grid">
      {#each items as item, i (item.id)}
        {@const badge = cardWatchBadge(item)}
        <PosterCard
          title={item.title}
          posterPath={item.poster_path}
          subtitle={item.year ? String(item.year) : undefined}
          progressRatio={badge?.progress ?? undefined}
          watched={badge?.watched ?? false}
          unwatchedCount={badge?.unwatchedCount ?? null}
          autofocus={firstLoad && i === 0}
          onclick={() => openItem(item.id, item.type)}
        />
      {/each}
    </div>
  {/if}
</div>

<style>
  .page {
    padding: 0 var(--page-pad) var(--page-pad);
  }
  h1 {
    font-size: var(--font-2xl);
    margin: 24px 0 32px;
  }

  .filters {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 16px;
    margin: -8px 0 40px;
  }

  .chip {
    font-family: inherit;
    font-size: var(--font-sm);
    padding: 12px 28px;
    border-radius: 999px;
    border: 2px solid var(--border);
    background: var(--bg-elevated);
    color: var(--text-primary);
    cursor: pointer;
  }

  .chip.active {
    background: var(--accent);
    border-color: var(--accent);
    color: white;
  }

  .chip.surprise {
    margin-left: 24px;
  }

  .notice {
    font-size: var(--font-sm);
    color: var(--text-secondary);
  }

  .grid {
    display: grid;
    grid-template-columns: repeat(6, 1fr);
    gap: var(--card-gap) var(--card-gap);
    row-gap: calc(var(--card-gap) + 24px);
  }

  .empty {
    font-size: var(--font-md);
    color: var(--text-secondary);
  }

  .error {
    font-size: var(--font-md);
    color: #fca5a5;
  }
</style>
