<script lang="ts">
  // Watchlist: the titles the user gave a status on their pages (Status on
  // a detail page), one row per status: Plan to Watch, Watching, On Hold,
  // Completed, Dropped, each headed with its count and left out when empty.
  // A row loads 50 cards at a time, the next 50 as focus nears its end. OK
  // opens the title's page; Back from there remounts this page, which loads
  // afresh (a status changed there shows) and puts focus back on the card,
  // the card's row opening at its place (the cards before it come in as
  // focus goes left), or from the start when it has moved from there.
  // Holding OK moves a card to another status or off the list, through the
  // per-item watch-status endpoints. Mirrors the web app's /watchlist.

  import { onMount, tick } from 'svelte';
  import { goto } from '$app/navigation';
  import { endpoints, Unauthorized } from '$lib/api';
  import type { WatchlistCounts, WatchlistItem, WatchStatusValue } from '$lib/api/endpoints';
  import type { RemoteKey } from '$lib/focus/keys';
  import { focusManager } from '$lib/focus/manager';
  import {
    focusFirstOf,
    restoreFocusTo,
    restoreGuard,
    restoreKeyed,
    takeFocusMemo,
    type FocusMemo,
    type RestoreGuard,
  } from '$lib/focus/memory';
  import HubRow from '$lib/components/HubRow.svelte';
  import OptionsDialog from '$lib/components/OptionsDialog.svelte';
  import PosterCard from '$lib/components/PosterCard.svelte';
  import Spinner from '$lib/components/Spinner.svelte';
  import TopNav from '$lib/components/TopNav.svelte';
  import { i18n, t } from '$lib/i18n/index.svelte';
  import { openItem } from '$lib/nav';
  import { nearEnd, openAtRow, refill, restoreStart, storedCardRow } from '$lib/paging';
  import { endpointMissing, watchWriteError } from '$lib/watchState';
  import {
    STATUS_LABELS,
    WATCHLIST_EMPTY,
    WATCHLIST_LOOKAHEAD,
    WATCHLIST_MISSING,
    WatchlistRow,
    moveTargets,
    statusOfFocusKey,
    statusesToLoad,
    visibleRows,
    watchlistCardText,
    withStatus,
  } from '$lib/watchlist';

  // The rows' state lives in WatchlistRow instances (not reactive);
  // `rows` is what the template draws, recomputed by redraw() after each
  // change to them.
  let model: WatchlistRow[] = [];
  let rows = $state.raw<ReturnType<typeof visibleRows> | null>(null);
  let error = $state('');
  // Back from a title: the first card holds its autofocus while the card
  // the user opened takes focus again (lib/focus/memory).
  let restoring = $state(false);
  // Autofocus only on the first paint: a card re-created later (moved onto
  // another row) must not steal focus.
  let firstPaint = $state(true);
  // False once the page is gone: a page still on its way must not move focus.
  let alive = true;

  // Hold OK on a card: its options.
  let menuItem = $state<WatchlistItem | null>(null);
  let menuOrigin: HTMLElement | null = null;
  // A status change on its way: one at a time.
  let busy = false;
  let notice = $state('');
  let noticeTimer: ReturnType<typeof setTimeout> | null = null;

  function redraw() {
    rows = visibleRows(model);
  }

  function fetcher(status: WatchStatusValue) {
    return (offset: number, limit: number) => endpoints.watchlist.list({ status, limit, offset });
  }

  // The card a title was opened from ("<place>:<status>:<id>", its place in
  // its row's list; for the session): Back opens that row there.
  const CARD_KEY = 'onscreen.watchlist_card';
  function readStored(): string | null {
    try {
      return sessionStorage.getItem(CARD_KEY);
    } catch {
      return null;
    }
  }
  function writeStored(value: string) {
    try {
      if (value) sessionStorage.setItem(CARD_KEY, value);
      else sessionStorage.removeItem(CARD_KEY);
    } catch {
      /* storage unavailable: Back refills the row from its start */
    }
  }

  onMount(() => {
    const memo = takeFocusMemo();
    restoring = !!memo?.focusedId;
    // Ends the restore when the page goes away or the user moves focus
    // while the rows are still loading (lib/focus/memory).
    const guard = restoreGuard();
    const cardPlace = storedCardRow(memo ? readStored() : null, memo?.focusedId);
    writeStored('');
    void load(memo, guard, cardPlace);

    const offBack = focusManager.pushBack(() => {
      goto('#/hub');
      return true;
    });
    const offKeys = focusManager.pushKeyHandler(onKey);
    return () => {
      alive = false;
      guard.end();
      offBack();
      offKeys();
      if (noticeTimer) clearTimeout(noticeTimer);
    };
  });

  // The counts say which rows have anything; those rows' first pages load
  // in parallel. Without counts (their request failed) every row loads and
  // the empty ones drop out. Only every row failing is the page's error.
  // Back to a card far along its row (`cardPlace`, its place there): that
  // row's first page is the one around the card, or the row's start when
  // the card isn't around there any more (lib/paging openAtRow).
  async function load(memo: FocusMemo | null, guard: RestoreGuard<HTMLElement>, cardPlace: number) {
    try {
      let counts: WatchlistCounts | null = null;
      try {
        counts = await endpoints.watchlist.counts();
      } catch (e) {
        if (e instanceof Unauthorized || endpointMissing(e)) throw e;
      }
      model = statusesToLoad(counts).map((s) => new WatchlistRow(s));
      const cardStatus = cardPlace >= 0 ? statusOfFocusKey(memo?.focusedId) : null;
      const firstPage = (r: WatchlistRow) =>
        r.status === cardStatus
          ? openAtRow(
              r,
              restoreStart(cardPlace, r.limit, 1),
              () => r.loadMore(fetcher(r.status)),
              () => r.items.some((i) => `${r.status}:${i.id}` === memo?.focusedId),
            )
          : r.loadMore(fetcher(r.status));
      const results = await Promise.all(model.map((r) => firstPage(r).then(() => null, (e: unknown) => e)));
      const failures = results.filter((e) => e != null);
      const unauthorized = failures.find((e) => e instanceof Unauthorized);
      if (unauthorized) throw unauthorized;
      if (model.length > 0 && failures.length === model.length) throw failures[0];
      if (!alive) return;
      redraw();
      await tick();
      if (memo && restoring) await restoreRows(memo, guard);
      firstPaint = false;
    } catch (e) {
      if (e instanceof Unauthorized) goto('#/login');
      else error = endpointMissing(e) ? t(WATCHLIST_MISSING) : (e as Error).message;
    } finally {
      restoring = false;
    }
  }

  // Back from a title: the card the user opened, refilling its row's pages
  // until it turns up (the row it was on had them); else that row's first
  // card (the title moved to another status meanwhile); else the first
  // card on the page. Keys are "<status>:<id>" (see the cards below).
  async function restoreRows(memo: FocusMemo, guard: RestoreGuard<HTMLElement>) {
    let found = restoreKeyed(memo, guard);
    const status = statusOfFocusKey(memo.focusedId);
    const row = model.find((r) => r.status === status);
    if (!found && row) {
      const loadWhileRestoring = () => (guard.active ? loadMore(row) : Promise.resolve(false));
      await refill(row, loadWhileRestoring, memo.loadedCount, async () => {
        if (found) return true;
        await tick();
        found = restoreKeyed(memo, guard);
        return found;
      });
    }
    if (found || !guard.active) return;
    const first = status
      ? document.querySelector<HTMLElement>(`[data-row="${status}"] [data-focusable]`)
      : null;
    if (first) restoreFocusTo(first, guard);
    else focusFirstOf('[data-row] [data-focusable]', guard);
  }

  /** The row's next page. A failure keeps the row; the next focus near its
   *  end asks again. True when the page changed the row. */
  async function loadMore(row: WatchlistRow): Promise<boolean> {
    let changed = false;
    try {
      changed = await row.loadMore(fetcher(row.status));
    } catch (e) {
      if (e instanceof Unauthorized) goto('#/login');
    }
    if (changed && alive) redraw();
    return changed;
  }

  function onCardFocus(status: WatchStatusValue, index: number) {
    const row = model.find((r) => r.status === status);
    if (row && !row.done && nearEnd(index, row.items.length, WATCHLIST_LOOKAHEAD, 1)) void loadMore(row);
    // Opened part way along: the cards before come in as focus nears the
    // row's first loaded card, so Left from it usually finds them there.
    if (row && row.hasBefore && index < WATCHLIST_LOOKAHEAD) void loadEarlier(row);
  }

  // A row's load of the cards before its first one, while on its way: a
  // second ask (focus moving on, Left) waits for it rather than doing nothing.
  const earlier = new Map<WatchlistRow, Promise<boolean>>();

  /** The row's cards before its first loaded one, put in front (one load
   *  at a time per row). True once they're in. */
  function loadEarlier(row: WatchlistRow): Promise<boolean> {
    let p = earlier.get(row);
    if (!p) {
      p = prependEarlier(row).finally(() => earlier.delete(row));
      earlier.set(row, p);
    }
    return p;
  }

  async function prependEarlier(row: WatchlistRow): Promise<boolean> {
    let changed = false;
    try {
      changed = await row.loadBefore(fetcher(row.status));
    } catch (e) {
      // The next move left asks again.
      if (e instanceof Unauthorized) goto('#/login');
    }
    if (!changed || !alive) return false;
    // The cards come in before what's on screen: the row's first card (and
    // the focused one with it) stays where it is, the row scrolled along by
    // what was put before it.
    const anchor = document.querySelector<HTMLElement>(`[data-row="${row.status}"] [data-focusable]`);
    const before = anchor ? anchor.getBoundingClientRect().left : 0;
    redraw();
    await tick();
    const scroller = anchor?.closest<HTMLElement>('[data-focus-row]');
    if (anchor && scroller && document.body.contains(anchor)) {
      const moved = anchor.getBoundingClientRect().left - before;
      if (moved !== 0) scroller.scrollLeft += moved;
    }
    return true;
  }

  /** Left from the first loaded card of a row opened part way along waits
   *  for the cards before it and moves onto the one just before. */
  function onKey(k: RemoteKey): boolean {
    if (k !== 'left' || menuItem) return false;
    const el = focusManager.currentElement();
    const rowEl = el?.closest<HTMLElement>('[data-row]');
    const row = rowEl ? model.find((r) => r.status === rowEl.dataset.row) : undefined;
    if (!el || !rowEl || !row?.hasBefore || rowEl.querySelector('[data-focusable]') !== el) return false;
    void loadEarlier(row).then(async (loaded) => {
      if (!loaded || focusManager.currentElement() !== el) return;
      await tick();
      const cards = [...rowEl.querySelectorAll<HTMLElement>('[data-focusable]')];
      const prev = cards[cards.indexOf(el) - 1];
      if (prev) focusManager.focus(prev);
    });
    return true;
  }

  /** Open `item`, noting its place in its row for Back. */
  function openCard(item: WatchlistItem) {
    const row = model.find((r) => r.status === item.status);
    const at = row ? row.items.findIndex((i) => i.id === item.id) : -1;
    writeStored(row && at >= 0 ? `${row.start + at}:${item.status}:${item.id}` : '');
    openItem(item.id, item.type);
  }

  function showNotice(text: string) {
    notice = text;
    if (noticeTimer) clearTimeout(noticeTimer);
    noticeTimer = setTimeout(() => (notice = ''), 4000);
  }

  function openMenu(item: WatchlistItem) {
    menuOrigin = focusManager.currentElement();
    menuItem = item;
  }

  async function closeMenu() {
    menuItem = null;
    await tick();
    if (menuOrigin && document.body.contains(menuOrigin)) focusManager.focus(menuOrigin);
    menuOrigin = null;
  }

  function openFromMenu() {
    const it = menuItem;
    menuItem = null;
    menuOrigin = null;
    if (it) openCard(it);
  }

  // Move a card to `next` (null = off the list). The server first: the card
  // leaves its row once the change is saved, and joins the front of the
  // other row (the order the server lists them in). Focus takes the card's
  // place in its row; else (the row emptied and went away) the first card
  // on the page; else the top nav.
  async function changeStatus(next: WatchStatusValue | null) {
    const item = menuItem;
    const origin = menuOrigin;
    menuItem = null;
    menuOrigin = null;
    if (!item || busy) {
      await tick();
      if (origin && document.body.contains(origin)) focusManager.focus(origin);
      return;
    }
    busy = true;
    await tick();
    if (origin && document.body.contains(origin)) focusManager.focus(origin);
    try {
      if (next) await endpoints.watchStatus.set(item.id, next);
      else await endpoints.watchStatus.clear(item.id);
    } catch (e) {
      busy = false;
      if (e instanceof Unauthorized) {
        goto('#/login');
        return;
      }
      showNotice(watchWriteError(e, t('watchlist.updateFailed')));
      return;
    }
    busy = false;
    if (!alive) return;

    const from = model.find((r) => r.status === item.status);
    const at = from ? from.remove(item.id) : -1;
    if (next) {
      let to = model.find((r) => r.status === next);
      if (!to) {
        to = WatchlistRow.empty(next);
        model = [...model, to];
      }
      to.addFront(withStatus(item, next));
    }
    redraw();
    await tick();
    const rowEl = document.querySelector<HTMLElement>(`[data-row="${item.status}"]`);
    const left = rowEl ? [...rowEl.querySelectorAll<HTMLElement>('[data-focusable]')] : [];
    const target =
      left[Math.min(Math.max(at, 0), left.length - 1)] ??
      document.querySelector<HTMLElement>('[data-row] [data-focusable]');
    if (target) focusManager.focus(target);
    else focusManager.refocus();
    showNotice(next ? t('watchlist.moved', { status: STATUS_LABELS[next] }) : t('watchlist.removed'));
  }

  const menuOptions = $derived.by(() => {
    const it = menuItem;
    if (!it) return [];
    return [
      { label: t('browse.open'), onselect: openFromMenu },
      ...moveTargets(it.status).map((s) => ({
        label: t('watchlist.moveTo', { status: STATUS_LABELS[s] }),
        onselect: () => void changeStatus(s),
      })),
      { label: t('watchlist.remove'), danger: true, onselect: () => void changeStatus(null) },
    ];
  });
</script>

<div class="page">
  <!-- The rows run full-bleed (their scrollers pad themselves), so the bar
       and the heading get the page inset, as on the hub. -->
  <div class="inset">
    <TopNav />
    <h1>{$i18n('nav.watchlist')}</h1>
  </div>

  {#if error}
    <p class="inset empty">{error}</p>
  {:else if !rows}
    <Spinner />
  {:else if rows.length === 0}
    <p class="inset empty">{$i18n(WATCHLIST_EMPTY)}</p>
  {:else}
    {#each rows as row, ri (row.status)}
      <HubRow title={row.title} rowKey={row.status} hint={ri === 0 ? $i18n('browse.holdOk') : undefined}>
        {#each row.items as item, i (item.id)}
          {@const text = watchlistCardText(item)}
          <PosterCard
            title={text.title}
            subtitle={text.subtitle}
            posterPath={item.poster_path ?? item.thumb_path}
            watched={item.watch_state === 'watched'}
            focusKey={`${row.status}:${item.id}`}
            autofocus={firstPaint && !restoring && ri === 0 && i === 0}
            onclick={() => openCard(item)}
            onlongpress={() => openMenu(item)}
            onfocus={() => onCardFocus(row.status, i)}
          />
        {/each}
      </HubRow>
    {/each}
  {/if}
</div>

{#if notice}
  <div class="notice" role="status">{notice}</div>
{/if}

{#if menuItem}
  <OptionsDialog
    title={watchlistCardText(menuItem).title}
    message={STATUS_LABELS[menuItem.status]}
    options={menuOptions}
    oncancel={() => void closeMenu()}
  />
{/if}

<style>
  .page {
    padding: 0 0 32px;
  }

  .inset {
    padding: 0 var(--page-pad);
  }

  h1 {
    font-size: var(--font-2xl);
    margin: 24px 0 32px;
  }

  .empty {
    margin: 0;
    color: var(--text-secondary);
    font-size: var(--font-md);
  }

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
