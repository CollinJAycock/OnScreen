<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { goto } from '$app/navigation';
  import {
    api,
    endpoints,
    type HubData,
    type HubItem,
    type HubRowPref,
    type MediaCollection,
  } from '$lib/api';
  import { Unauthorized, collectionCoverPath } from '$lib/api';
  import { focusable } from '$lib/focus/focusable';
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
  import { openItem } from '$lib/nav';
  import { ensureNavGates } from '$lib/navGates';
  import { orderRows } from '$lib/hubLayout';
  import {
    endpointMissing,
    hubTileText,
    progressRatio,
    restoreContinueWatching,
    watchWriteError,
    withoutContinueWatching,
  } from '$lib/watchState';

  let data = $state<HubData | null>(null);
  // The Collections row (GET /collections) and the saved row layout
  // (preferences hub_layout) ride along with the hub, best effort.
  let collections = $state<MediaCollection[]>([]);
  let layout = $state<HubRowPref[] | null>(null);
  let error = $state('');
  // Autofocus only on the first paint — a card re-created later (a failed
  // removal putting a tile back) must not steal focus.
  let firstPaint = $state(true);
  // Back from a card: the first card holds its autofocus while the one the
  // user opened takes focus again (lib/focus/memory).
  let restoring = $state(false);

  // Continue Watching card options (hold OK). Offered on servers that have
  // the v2.5 watch-state routes (they send next_up); hidden for good if the
  // dismiss route turns out to be missing.
  let cwMenuItem = $state<HubItem | null>(null);
  let cwMenuOrigin: HTMLElement | null = null;
  let cwRemovalMissing = $state(false);
  let notice = $state('');
  let noticeTimer: ReturnType<typeof setTimeout> | null = null;
  // The error state's "Change server" confirm (Android's escape hatch from a
  // home screen that can't load: a wrong or retired server address).
  let confirmChangeServer = $state(false);
  // Confirmed and under way. The revoke goes to the very server that isn't
  // answering and can take its request timeout (20 s), so meanwhile the
  // button says "Signing out…" and keeps focus, both buttons are disabled,
  // and every key is swallowed (Back here would leave the app mid-revoke).
  let signingOut = $state(false);

  $effect(() => {
    if (!signingOut) return;
    return focusManager.pushKeyHandler(() => true);
  });

  onMount(() => {
    // Ends the restore when the page goes away or the user moves focus
    // while the hub is still loading (lib/focus/memory).
    const guard = restoreGuard();
    void load(takeFocusMemo(), guard);

    // No Back handler here: the hub is a first screen, so Back falls
    // through to the layout's exit popup (lib/appExit).
    return () => {
      guard.end();
      if (noticeTimer) clearTimeout(noticeTimer);
    };
  });

  // The hub plus, in parallel, the collections and the saved layout. Only
  // the hub is required: without the other two the page shows no
  // Collections row and the default row order rather than an error.
  // `memo`: the card to put focus back on (Back to the hub), while `guard`
  // says the restore may still act.
  async function load(memo: FocusMemo | null = null, guard?: RestoreGuard<HTMLElement>) {
    error = '';
    data = null;
    firstPaint = true;
    restoring = !!memo?.focusedId;
    try {
      const [hub, cols, prefs] = await Promise.all([
        endpoints.hub.get(),
        endpoints.collections.list().catch(() => [] as MediaCollection[]),
        endpoints.users.preferences().catch(() => null),
      ]);
      collections = Array.isArray(cols) ? cols : [];
      layout = prefs?.hub_layout ?? null;
      data = hub;
      // The server answers now: Retry keeps the bar mounted, so a pill whose
      // answer failed at launch (server or Wi-Fi not up yet) asks again here.
      void ensureNavGates();
      await tick();
      if (memo && restoring && guard) restoreHubFocus(memo, guard);
      firstPaint = false;
    } catch (e) {
      if (e instanceof Unauthorized) goto('#/login');
      else error = (e as Error).message;
    } finally {
      restoring = false;
    }
  }

  // The card the user opened; else, when it has left its row (finished and
  // gone from Continue Watching), the first card of that row; else the first
  // card on the page. Keys are "<row key>:<id>" (see the cards below).
  function restoreHubFocus(memo: FocusMemo, guard: RestoreGuard<HTMLElement>) {
    if (!guard.active || restoreKeyed(memo, guard) || !memo.focusedId) return;
    const rowKey = memo.focusedId.slice(0, memo.focusedId.lastIndexOf(':'));
    const row = [...document.querySelectorAll<HTMLElement>('[data-row]')].find(
      (r) => r.getAttribute('data-row') === rowKey,
    );
    const first = row?.querySelector<HTMLElement>('[data-focusable]');
    if (first) restoreFocusTo(first, guard);
    else focusFirstOf('[data-row] [data-focusable]', guard);
  }

  // Confirmed: the same teardown as Settings' Forget server (revoke, clear
  // the tokens and the address), then Setup. The revoke stays best effort:
  // forgetServer clears the session whatever the server says.
  async function changeServer() {
    if (signingOut) return;
    confirmChangeServer = false;
    signingOut = true;
    await tick();
    // The dialog's gone: focus back on the button, now "Signing out…".
    focusFirstOf('.message .secondary');
    await api.forgetServer();
    goto('#/setup');
  }

  async function cancelChangeServer() {
    confirmChangeServer = false;
    await tick();
    // Back on the button that opened it.
    focusFirstOf('.message .secondary');
  }

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

  type Row =
    | { kind: 'items'; key: string; title: string; items: HubItem[]; continueWatching?: boolean }
    | { kind: 'collections'; key: string; title: string; items: MediaCollection[] };

  // Home rows in their default order — the web / Android default: Next Up
  // right under Continue Watching TV, Plan to Watch after the Continue rows,
  // the collections an admin put on the home screen, Trending, then the per-library "Recently Added to <Library>" strips (or
  // the flat aggregate on older servers that don't emit
  // recently_added_by_library), then Collections. The user's saved layout
  // (hub_layout, keys shared with the web) reorders and hides them; empty
  // rows are dropped after that.
  const rows = $derived.by<Row[]>(() => {
    if (!data) return [];
    const out: Row[] = [
      { kind: 'items', key: 'continue_tv', title: 'Continue Watching TV Shows', items: tv, continueWatching: true },
      { kind: 'items', key: 'next_up', title: 'Next Up', items: nextUp },
      { kind: 'items', key: 'continue_movies', title: 'Continue Watching Movies', items: movies, continueWatching: true },
      { kind: 'items', key: 'continue_other', title: 'Continue Watching', items: other, continueWatching: true },
      { kind: 'items', key: 'plan_to_watch', title: 'Plan to Watch', items: plan },
    ];
    for (const c of data.collection_rows ?? []) {
      out.push({ kind: 'items', key: `collection:${c.collection_id}`, title: c.name, items: c.items ?? [] });
    }
    out.push({ kind: 'items', key: 'trending', title: 'Trending', items: data.trending ?? [] });
    if (data.recently_added_by_library && data.recently_added_by_library.length > 0) {
      for (const r of data.recently_added_by_library) {
        out.push({ kind: 'items', key: `library:${r.library_id}`, title: `Recently Added to ${r.library_name}`, items: r.items });
      }
    } else {
      out.push({ kind: 'items', key: 'recently_added', title: 'Recently Added', items: data.recently_added ?? [] });
    }
    out.push({ kind: 'collections', key: 'collections', title: 'Collections', items: collections });
    return orderRows(out, layout).filter((r) => r.items.length > 0);
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
  <!-- The rows run full-bleed (their scrollers pad themselves), so the bar
       gets the page inset other pages give it with their own padding. -->
  <div class="nav-inset"><TopNav /></div>

  {#if error}
    <!-- Android's error overlay: the reason, Retry, and one Down press
         below it Change server, for a wrong or retired server address
         (confirmed first: it signs out). -->
    <div class="message">
      <p class="message-title">Couldn't load your home screen</p>
      <p class="error">{error}</p>
      <button
        use:focusable={{ autofocus: true }}
        class="btn"
        disabled={signingOut}
        onclick={() => void load()}>Retry</button>
      <button
        use:focusable
        class="btn secondary"
        disabled={signingOut}
        onclick={() => (confirmChangeServer = true)}
      >
        {signingOut ? 'Signing out…' : 'Change server'}
      </button>
    </div>
  {:else if !data}
    <Spinner />
  {:else}
    {#each rows as row, ri (row.key)}
      {#if row.kind === 'collections'}
        <HubRow title={row.title} rowKey={row.key}>
          {#each row.items as col, i (col.id)}
            <PosterCard
              title={col.name}
              posterPath={col.poster_path}
              posterSrc={collectionCoverPath(col)}
              focusKey={`${row.key}:${col.id}`}
              autofocus={firstPaint && !restoring && ri === 0 && i === 0}
              onclick={() => open(col.id, 'collection')}
            />
          {/each}
        </HubRow>
      {:else}
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
              focusKey={`${row.key}:${item.id}`}
              autofocus={firstPaint && !restoring && ri === 0 && i === 0}
              onclick={() => open(item.id, item.type)}
              onlongpress={row.continueWatching && cwOptions ? () => openCwMenu(item) : undefined}
            />
          {/each}
        </HubRow>
      {/if}
    {/each}

    {#if rows.length === 0}
      <!-- An empty server (or every row hidden): say so rather than show a
           blank page. The top nav stays reachable. -->
      <div class="message">
        <p class="message-title">Nothing to watch yet</p>
        <p class="empty">Add a library from the web app, then come back.</p>
      </div>
    {/if}
  {/if}
</div>

{#if notice}
  <div class="notice" role="status">{notice}</div>
{/if}

{#if confirmChangeServer}
  <OptionsDialog
    title="Change server"
    message="Disconnect from this server and choose another? You'll be signed out."
    options={[{ label: 'Change server', danger: true, onselect: () => void changeServer() }]}
    focusCancel
    oncancel={() => void cancelChangeServer()}
  />
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

  .nav-inset {
    padding: 0 var(--page-pad);
  }

  /* A one-column grid rather than a flex column: grid `gap` works on
     webOS 6's Chromium 79, flexbox `gap` needs Chrome 84. */
  .message {
    padding: 24px var(--page-pad) 0;
    display: grid;
    grid-template-columns: minmax(0, 1fr);
    justify-items: start;
    row-gap: 16px;
  }

  .message-title {
    margin: 0;
    font-size: var(--font-lg);
    font-weight: 600;
  }

  .error, .empty {
    margin: 0;
    font-size: var(--font-md);
  }

  .error { color: #fca5a5; }
  .empty { color: var(--text-secondary); }

  .btn {
    margin-top: 16px;
    min-width: 320px;
    font-family: inherit;
    font-size: var(--font-md);
    padding: 20px 48px;
    border-radius: 12px;
    border: 2px solid var(--accent);
    background: var(--accent);
    color: white;
    cursor: pointer;
  }

  /* After .btn so it wins. Sits right under Retry (grid row gap). */
  .btn.secondary {
    margin-top: 0;
    border-color: var(--border);
    background: var(--bg-elevated);
    color: var(--text-primary);
  }

  /* Signing out: Retry dims; the focused button keeps its look and says
     what is happening. */
  .btn:disabled {
    cursor: default;
  }
  .btn:disabled:not([data-focused='true']) {
    opacity: 0.5;
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
