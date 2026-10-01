<script lang="ts">
  // DVR recordings list. /tv/recordings returns scheduled +
  // in-progress + completed + failed + cancelled in one call;
  // we group by status so the user can see "what's recording right
  // now" + "what landed" + "what's queued" without filtering chips.
  //
  // Completed recordings with a non-null `item_id` route through
  // the standard /item/[id] flow so playback resume + scrobbling
  // work like any other library item. Scheduled / failed rows are
  // informational — there's no admin action surface on the TV
  // (cancel / reschedule lives in the web settings UI).
  //
  // Back from a recording puts focus on its row again (lib/focus/memory),
  // as Android's RecordingsFragment keeps its grid position.

  import { onMount, tick } from 'svelte';
  import { goto } from '$app/navigation';
  import { endpoints, Unauthorized, type Recording } from '$lib/api';
  import { focusable } from '$lib/focus/focusable';
  import { focusManager } from '$lib/focus/manager';
  import { focusFirstOf, restoreGuard, restoreKeyed, takeFocusMemo } from '$lib/focus/memory';
  import Spinner from '$lib/components/Spinner.svelte';
  import TopNav from '$lib/components/TopNav.svelte';
  import { pushTo } from '$lib/nav';

  let recordings = $state<Recording[]>([]);
  let loading = $state(true);
  let error = $state('');
  // Back from a recording: the first row holds its autofocus and the one
  // the user opened takes focus again.
  let restoring = $state(false);

  // Group order matters — "recording now" is the most actionable
  // surface (in-progress shows you might want to start watching
  // immediately), followed by "ready to watch", then the rest.
  const order: { key: string; label: string }[] = [
    { key: 'recording', label: 'Recording now' },
    { key: 'completed', label: 'Ready to watch' },
    { key: 'scheduled', label: 'Scheduled' },
    { key: 'failed', label: 'Failed' },
    { key: 'cancelled', label: 'Cancelled' },
  ];

  const grouped = $derived.by(() => {
    const byStatus: Record<string, Recording[]> = {};
    for (const r of recordings) {
      const k = r.status ?? 'scheduled';
      (byStatus[k] ??= []).push(r);
    }
    // Within each group, sort by starts_at descending (most
    // recent first) — same convention the web client uses.
    for (const k of Object.keys(byStatus)) {
      byStatus[k].sort((a, b) => (b.starts_at ?? '').localeCompare(a.starts_at ?? ''));
    }
    return byStatus;
  });

  onMount(() => {
    const memo = takeFocusMemo();
    restoring = !!memo?.focusedId;
    // Ends the restore when the page goes away or the user moves focus
    // while the list is still loading (lib/focus/memory).
    const guard = restoreGuard();
    (async () => {
      const loaded = await load();
      // Signed out: on the way to the sign-in page.
      if (!loaded && !error) return;
      await tick();
      // Found in whichever group it is in now (a recording that finished
      // moves from "Recording now" to "Ready to watch").
      if (restoring && restoreKeyed(memo, guard)) return;
      // Gone from the list meanwhile: the first row instead, whose autofocus
      // the restore held back. With no row at all (no recordings, or the
      // list failed) there's none, and focusFirstOf hands focus to the focus
      // manager, which places it on the top nav, rather than leave it on
      // nothing with the first press spent finding it.
      if (restoring || recordings.length === 0) focusFirstOf('.rows [data-focusable]', guard);
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

  /** True when the list came in. */
  async function load(): Promise<boolean> {
    loading = true;
    error = '';
    try {
      recordings = await endpoints.livetv.recordings();
      return true;
    } catch (e) {
      if (e instanceof Unauthorized) goto('#/login');
      else error = (e as Error).message ?? 'Could not load recordings';
      return false;
    } finally {
      loading = false;
    }
  }

  function open(r: Recording) {
    if (r.item_id) pushTo(`#/item/${r.item_id}`);
  }

  function fmtRange(r: Recording): string {
    try {
      const s = new Date(r.starts_at);
      const e = new Date(r.ends_at);
      const date = s.toLocaleDateString(undefined, { weekday: 'short', month: 'short', day: 'numeric' });
      const fmt = (d: Date) => d.toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' });
      return `${date} · ${fmt(s)} – ${fmt(e)}`;
    } catch {
      return '';
    }
  }

  // The first focusable card grabs focus on mount (unless a restore
  // holds it back) — track which group runs first to assign autofocus
  // only on its lead card.
  const firstNonEmptyGroup = $derived(
    order.find((g) => grouped[g.key]?.length > 0)?.key ?? null,
  );
</script>

<div class="page">
  <TopNav />
  <h1>Recordings</h1>

  {#if error}<p class="error">{error}</p>{/if}

  {#if loading}
    <Spinner />
  {:else if recordings.length === 0}
    <p class="empty">
      No recordings yet. Schedule one from the web settings UI or
      the Live TV grid.
    </p>
  {:else}
    {#each order as group (group.key)}
      {@const items = grouped[group.key] ?? []}
      {#if items.length > 0}
        <section>
          <div class="section-title">{group.label}</div>
          <div class="rows">
            {#each items as r, i (r.id)}
              <button
                use:focusable={{
                  autofocus: group.key === firstNonEmptyGroup && i === 0 && !restoring,
                }}
                class="row row-{r.status}"
                data-focus-key={r.id}
                disabled={!r.item_id}
                onclick={() => open(r)}
              >
                {#if r.channel_logo}
                  <img src={r.channel_logo} alt="" class="logo" />
                {:else}
                  <div class="logo placeholder"></div>
                {/if}
                <div class="row-body">
                  <div class="row-title">
                    {r.title}{#if r.subtitle}{' · '}{r.subtitle}{/if}
                  </div>
                  <div class="row-meta">
                    <span class="row-channel">{r.channel_number} {r.channel_name}</span>
                    <span class="row-time">{fmtRange(r)}</span>
                    {#if r.season_num != null && r.episode_num != null}
                      <span>S{r.season_num}E{r.episode_num}</span>
                    {/if}
                  </div>
                  {#if r.error}
                    <div class="row-error">{r.error}</div>
                  {/if}
                </div>
                <div class="row-action">
                  {#if r.status === 'completed' && r.item_id}
                    <span class="action-play">▶ Watch</span>
                  {:else if r.status === 'recording'}
                    <span class="action-rec">● Recording</span>
                  {:else if r.status === 'scheduled'}
                    <span class="action-sched">Scheduled</span>
                  {:else if r.status === 'failed'}
                    <span class="action-fail">Failed</span>
                  {:else if r.status === 'cancelled'}
                    <span class="action-fail">Cancelled</span>
                  {/if}
                </div>
              </button>
            {/each}
          </div>
        </section>
      {/if}
    {/each}
  {/if}
</div>

<style>
  .page {
    padding: 0 var(--page-pad);
  }
  h1 {
    font-size: var(--font-2xl);
    margin: 24px 0 32px;
  }

  .error { color: #fca5a5; }
  .empty { color: var(--text-secondary); }

  section { margin-bottom: 36px; max-width: 1600px; }
  .section-title {
    font-size: var(--font-sm);
    color: var(--accent);
    text-transform: uppercase;
    letter-spacing: 0.15em;
    margin-bottom: 12px;
  }
  /* Rows and meta items are spaced with margins, not flexbox `gap`
     (Chrome 84): webOS 6 runs Chromium 79. Flex items' margins don't
     collapse, so the spacing is the same. A row is flex as well, not the
     80px | 1fr | 200px grid it reads as: Chromium 79 can't make a <button>
     a grid container (it computes display: grid, then stacks the children
     as blocks), so the columns are fixed and growing flex items and margins
     stand in for the 24px gaps. */
  .rows {
    display: flex;
    flex-direction: column;
  }
  .row + .row {
    margin-top: 8px;
  }
  .row {
    display: flex;
    align-items: center;
    padding: 14px 20px;
    background: rgba(255, 255, 255, 0.03);
    border: 2px solid transparent;
    border-radius: 8px;
    color: inherit;
    text-align: left;
    cursor: pointer;
    font-family: inherit;
  }
  .row:focus,
  .row:focus-visible {
    border-color: var(--accent);
    outline: none;
    background: rgba(124, 106, 247, 0.12);
  }
  .row[disabled] {
    cursor: not-allowed;
    opacity: 0.85;
  }
  .row[disabled]:focus,
  .row[disabled]:focus-visible {
    /* still receive visual focus so D-pad navigation works, just
       not a "click ready" affordance */
    border-color: var(--accent);
    background: rgba(124, 106, 247, 0.06);
  }
  .logo {
    /* The 60px logo sat at the start of an 80px column: the 20px left over
       plus the 24px gap keep the text where it was. */
    flex: 0 0 auto;
    margin-right: 44px;
    width: 60px;
    height: 45px;
    object-fit: contain;
    background: rgba(255, 255, 255, 0.05);
    border-radius: 4px;
  }
  .logo.placeholder { display: block; }
  .row-body {
    flex: 1 1 0%;
    min-width: 0;
    margin-right: 24px;
  }
  .row-title {
    font-size: var(--font-md);
    font-weight: 600;
    margin-bottom: 4px;
  }
  .row-meta {
    display: flex;
    font-size: var(--font-sm);
    color: var(--text-secondary);
  }
  .row-meta > span + span {
    margin-left: 16px;
  }
  .row-error {
    margin-top: 4px;
    font-size: var(--font-sm);
    color: #fca5a5;
  }
  .row-action {
    flex: 0 0 200px;
    text-align: right;
    font-size: var(--font-md);
  }
  .action-play { color: var(--accent); font-weight: 600; }
  .action-rec { color: #f87171; font-weight: 600; }
  .action-sched { color: var(--text-secondary); }
  .action-fail { color: #fca5a5; }
</style>
