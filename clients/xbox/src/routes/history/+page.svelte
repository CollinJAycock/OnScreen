<script lang="ts">
  // Watch history — chronological list of recent watch_events for
  // the signed-in user. Same grid layout as Favorites but cards
  // include a relative timestamp so the user can tell "yesterday"
  // from "last month" at a glance.

  import { onMount, tick } from 'svelte';
  import { goto } from '$app/navigation';
  import { endpoints, Unauthorized, type HistoryItem } from '$lib/api';
  import { focusManager } from '$lib/focus/manager';
  import { focusFirstOf, restoreGuard, restoreKeyed, takeFocusMemo } from '$lib/focus/memory';
  import PosterCard from '$lib/components/PosterCard.svelte';
  import Spinner from '$lib/components/Spinner.svelte';
  import TopNav from '$lib/components/TopNav.svelte';
  import { openItem } from '$lib/nav';
  import { uniqueBy } from '$lib/paging';

  // The last 100 plays, one card per title (Android parity): a show binged
  // over a weekend is one tile at its latest play, not a wall of repeats.
  const HISTORY_LIMIT = 100;

  let items = $state<HistoryItem[] | null>(null);
  let error = $state('');
  // Back from a title: the first card holds its autofocus and the one the
  // user opened takes focus again (lib/focus/memory). Cards are keyed by
  // title, not by play: a title just watched comes back as a newer play,
  // now at the front, and that is where focus should go.
  let restoring = $state(false);

  // Coarse-grained "5m ago" / "yesterday" formatter — TV cards have
  // limited room and the user mostly cares about ordering, not exact
  // timestamps. Same buckets the web client uses.
  function relTime(iso: string): string {
    const t = Date.parse(iso);
    if (Number.isNaN(t)) return '';
    const diffSec = Math.max(0, Math.floor((Date.now() - t) / 1000));
    if (diffSec < 60) return 'just now';
    if (diffSec < 3600) return `${Math.floor(diffSec / 60)}m ago`;
    if (diffSec < 86400) return `${Math.floor(diffSec / 3600)}h ago`;
    if (diffSec < 86400 * 7) return `${Math.floor(diffSec / 86400)}d ago`;
    return new Date(t).toLocaleDateString();
  }

  onMount(() => {
    const memo = takeFocusMemo();
    restoring = !!memo?.focusedId;
    // Ends the restore when the page goes away or the user moves focus
    // while the list is still loading (lib/focus/memory).
    const guard = restoreGuard();
    (async () => {
      try {
        // Newest first from the server, so the first entry per media_id is
        // its latest play.
        items = uniqueBy(await endpoints.history.list(HISTORY_LIMIT), (h) => h.media_id);
        if (restoring) {
          await tick();
          if (!restoreKeyed(memo, guard)) focusFirstOf('.grid [data-focusable]', guard);
        }
      } catch (e) {
        if (e instanceof Unauthorized) goto('#/login');
        else error = (e as Error).message;
      }
    })();

    const offBack = focusManager.pushBack(() => {
      goto('#/hub');
      return true;
    });
    return () => {
      guard.end();
      offBack();
    };
  });
</script>

<div class="page">
  <TopNav />
  <h1>History</h1>

  {#if error}
    <p class="empty">{error}</p>
  {:else if !items}
    <Spinner />
  {:else if items.length === 0}
    <p class="empty">Nothing watched yet.</p>
  {:else}
    <div class="grid">
      {#each items as it, i (it.id)}
        <PosterCard
          title={it.title}
          posterPath={it.thumb_path}
          subtitle={relTime(it.occurred_at)}
          focusKey={it.media_id}
          autofocus={i === 0 && !restoring}
          onclick={() => openItem(it.media_id, it.type)}
        />
      {/each}
    </div>
  {/if}
</div>

<style>
  /* A one-column grid rather than a flex column: grid `gap` works on
     webOS 6's Chromium 79, flexbox `gap` needs Chrome 84. */
  .page {
    padding: 0 var(--page-pad) var(--page-pad);
    display: grid;
    grid-template-columns: minmax(0, 1fr);
    align-content: start;
    row-gap: 24px;
  }
  h1 { font-size: var(--font-2xl); margin: 24px 0 0; }
  .empty { color: var(--text-secondary); font-size: var(--font-md); }
  .grid {
    display: grid;
    grid-template-columns: repeat(6, 1fr);
    gap: var(--card-gap);
  }
</style>
