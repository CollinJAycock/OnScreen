<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { goto } from '$app/navigation';
  import { endpoints, type HubData, type HubItem } from '$lib/api';
  import { Unauthorized } from '$lib/api';
  import { focusManager } from '$lib/focus/manager';
  import HubRow from '$lib/components/HubRow.svelte';
  import OptionsDialog from '$lib/components/OptionsDialog.svelte';
  import PosterCard from '$lib/components/PosterCard.svelte';
  import Spinner from '$lib/components/Spinner.svelte';
  import TopNav from '$lib/components/TopNav.svelte';
  import { openItem } from '$lib/nav';
  import {
    endpointMissing,
    hubTileText,
    progressRatio,
    restoreContinueWatching,
    watchWriteError,
    withoutContinueWatching,
  } from '$lib/watchState';

  let data = $state<HubData | null>(null);
  let error = $state('');
  // Autofocus only on the first paint — a card re-created later (a failed
  // removal putting a tile back) must not steal focus.
  let firstPaint = $state(true);

  // Continue Watching card options (hold OK). Offered on servers that have
  // the v2.5 watch-state routes (they send next_up); hidden for good if the
  // dismiss route turns out to be missing.
  let cwMenuItem = $state<HubItem | null>(null);
  let cwMenuOrigin: HTMLElement | null = null;
  let cwRemovalMissing = $state(false);
  let notice = $state('');
  let noticeTimer: ReturnType<typeof setTimeout> | null = null;

  onMount(() => {
    (async () => {
      try {
        data = await endpoints.hub.get();
        await tick();
        firstPaint = false;
      } catch (e) {
        if (e instanceof Unauthorized) goto('#/login');
        else error = (e as Error).message;
      }
    })();

    // No Back handler here: the hub is the root, so Back falls through to
    // webOS's platformBack (see the focus manager).
    return () => {
      if (noticeTimer) clearTimeout(noticeTimer);
    };
  });

  function open(id: string, type: string) {
    // Type-aware routing: photos go to a full-screen viewer,
    // collections drill into their item grid, everything else lands
    // on the standard /item detail page.
    openItem(id, type);
  }

  // Resolve the three Continue Watching buckets. Newer servers
  // pre-split them; older servers send only the combined feed and
  // we filter client-side.
  const tv      = $derived(data?.continue_watching_tv ?? data?.continue_watching.filter(i => i.type === 'episode') ?? []);
  const movies  = $derived(data?.continue_watching_movies ?? data?.continue_watching.filter(i => i.type === 'movie') ?? []);
  const other   = $derived(data?.continue_watching_other ?? data?.continue_watching.filter(i => i.type !== 'episode' && i.type !== 'movie') ?? []);
  const nextUp  = $derived(data?.next_up ?? []);
  const plan    = $derived(data?.plan_to_watch ?? []);

  const cwOptions = $derived(!!data && data.next_up !== undefined && !cwRemovalMissing);

  interface Row {
    key: string;
    title: string;
    items: HubItem[];
    continueWatching?: boolean;
  }

  // Home rows in display order — the web / Android default: Next Up right
  // under Continue Watching TV, Plan to Watch after the Continue rows, then
  // the per-library "Recently Added to <Library>" strips (or the flat
  // aggregate on older servers that don't emit recently_added_by_library).
  const rows = $derived.by<Row[]>(() => {
    if (!data) return [];
    const out: Row[] = [
      { key: 'continue_tv', title: 'Continue Watching TV Shows', items: tv, continueWatching: true },
      { key: 'next_up', title: 'Next Up', items: nextUp },
      { key: 'continue_movies', title: 'Continue Watching Movies', items: movies, continueWatching: true },
      { key: 'continue_other', title: 'Continue Watching', items: other, continueWatching: true },
      { key: 'plan_to_watch', title: 'Plan to Watch', items: plan },
    ];
    if (data.recently_added_by_library && data.recently_added_by_library.length > 0) {
      for (const r of data.recently_added_by_library) {
        out.push({ key: `library:${r.library_id}`, title: `Recently Added to ${r.library_name}`, items: r.items });
      }
    } else {
      out.push({ key: 'recently_added', title: 'Recently Added', items: data.recently_added });
    }
    return out.filter((r) => r.items.length > 0);
  });

  function showNotice(text: string) {
    notice = text;
    if (noticeTimer) clearTimeout(noticeTimer);
    noticeTimer = setTimeout(() => (notice = ''), 4000);
  }

  function openCwMenu(item: HubItem) {
    cwMenuOrigin = focusManager.currentElement();
    cwMenuItem = item;
  }

  async function closeCwMenu() {
    cwMenuItem = null;
    await tick();
    if (cwMenuOrigin && document.body.contains(cwMenuOrigin)) focusManager.focus(cwMenuOrigin);
    cwMenuOrigin = null;
  }

  function openFromMenu() {
    const it = cwMenuItem;
    cwMenuItem = null;
    cwMenuOrigin = null;
    if (it) open(it.id, it.type);
  }

  // Optimistic: the tile disappears at once and comes back if the server
  // call fails. Focus moves to the neighbouring card in the same row.
  async function removeFromContinueWatching() {
    const item = cwMenuItem;
    const origin = cwMenuOrigin;
    cwMenuItem = null;
    cwMenuOrigin = null;
    if (!item || !data) return;
    const rowEl = origin?.closest('[data-row]') as HTMLElement | null;
    const cards = rowEl ? [...rowEl.querySelectorAll<HTMLElement>('[data-focusable]')] : [];
    const at = origin ? cards.indexOf(origin) : -1;
    const before = data;
    data = withoutContinueWatching(data, item.id);
    await tick();
    const left = rowEl && document.body.contains(rowEl)
      ? [...rowEl.querySelectorAll<HTMLElement>('[data-focusable]')]
      : [];
    // Same slot in the row, else (the row emptied and went away) the first
    // card on the page rather than the top nav.
    const next =
      left[Math.min(Math.max(at, 0), left.length - 1)] ??
      document.querySelector<HTMLElement>('[data-row] [data-focusable]');
    if (next) focusManager.focus(next);
    else focusManager.refocus();
    try {
      await endpoints.items.dismissContinueWatching(item.id);
    } catch (e) {
      if (e instanceof Unauthorized) {
        goto('#/login');
        return;
      }
      // Put the tile back where it was (other removals made meanwhile stay).
      if (data) data = restoreContinueWatching(data, before, item.id);
      if (endpointMissing(e)) {
        cwRemovalMissing = true;
        return;
      }
      showNotice(watchWriteError(e, "Couldn't remove from Continue Watching."));
    }
  }
</script>

<div class="page">
  <TopNav />

  {#if error}
    <p class="error">{error}</p>
  {:else if !data}
    <Spinner />
  {:else}
    {#each rows as row, ri (row.key)}
      <HubRow
        title={row.title}
        rowKey={row.key}
        hint={row.continueWatching && cwOptions ? 'Hold OK for options' : undefined}
      >
        {#each row.items as item, i (item.id)}
          {@const text = hubTileText(item)}
          <PosterCard
            title={text.title}
            posterPath={item.poster_path}
            subtitle={text.subtitle}
            progressRatio={row.continueWatching ? progressRatio(item.view_offset_ms, item.duration_ms) : undefined}
            autofocus={firstPaint && ri === 0 && i === 0}
            onclick={() => open(item.id, item.type)}
            onlongpress={row.continueWatching && cwOptions ? () => openCwMenu(item) : undefined}
          />
        {/each}
      </HubRow>
    {/each}

    {#if rows.length === 0}
      <p class="empty">Your library is empty. Add a library and run a scan from the web UI.</p>
    {/if}
  {/if}
</div>

{#if notice}
  <div class="notice" role="status">{notice}</div>
{/if}

{#if cwMenuItem}
  <OptionsDialog
    title={hubTileText(cwMenuItem).title}
    options={[
      { label: 'Remove from Continue Watching', onselect: () => void removeFromContinueWatching() },
      { label: 'Open', onselect: openFromMenu },
    ]}
    oncancel={() => void closeCwMenu()}
  />
{/if}

<style>
  .page {
    padding: 0 0 32px;
  }

  .error, .empty {
    padding: 0 var(--page-pad);
    font-size: var(--font-md);
  }

  .error { color: #fca5a5; }
  .empty { color: var(--text-secondary); }

  .notice {
    position: fixed;
    left: 50%;
    bottom: 64px;
    transform: translateX(-50%);
    padding: 16px 32px;
    border-radius: 12px;
    background: rgba(7, 7, 13, 0.92);
    border: 2px solid var(--border-strong);
    font-size: var(--font-sm);
    color: var(--text-primary);
    z-index: 50;
  }
</style>
