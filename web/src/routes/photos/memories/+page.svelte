<script lang="ts">
  // Memories: photos from this day in earlier years ("3 years ago today"),
  // or from this week when a year has none that day, across every photo
  // library this account can open. Each memory plays as a slideshow in the
  // photo viewer. ?date=YYYY-MM-DD picks another day (the home row links
  // here with the viewer's own date).
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { memoriesApi, type MemoryGroup } from '$lib/api';
  import { localISODate, photoImageUrl, saveMemorySelection } from '$lib/photos';
  import { memoryKey, photoViewerHref } from '$lib/photoViewerContext';

  const DATE_RE = /^\d{4}-\d{2}-\d{2}$/;

  let date = $state('');
  let groups = $state.raw<MemoryGroup[]>([]);
  let loading = $state(true);
  let error = $state('');
  let seq = 0;

  function errText(e: unknown, fallback: string): string {
    return e instanceof Error && e.message ? e.message : fallback;
  }

  async function load(d: string) {
    const mine = ++seq;
    loading = true;
    error = '';
    try {
      const r = await memoriesApi.get(d);
      if (mine !== seq) return;
      groups = r?.groups ?? [];
    } catch (e) {
      if (mine !== seq) return;
      error = errText(e, 'Could not load memories');
    } finally {
      if (mine === seq) loading = false;
    }
  }

  $effect(() => {
    if (!localStorage.getItem('onscreen_user')) { goto('/login'); return; }
    const wanted = $page.url.searchParams.get('date') ?? '';
    date = DATE_RE.test(wanted) ? wanted : localISODate();
    load(date);
  });

  function dayLabel(d: string): string {
    const [y, m, dd] = d.split('-').map(Number);
    if (!y) return '';
    return new Date(y, m - 1, dd).toLocaleDateString(undefined, { month: 'long', day: 'numeric' });
  }

  // Remember the memory's photos for the viewer's Previous / Next, then open
  // one (or play from the start).
  function open(i: number, photoId: string, play: boolean) {
    const key = memoryKey(date, i);
    saveMemorySelection(key, groups[i].items.map((p) => p.id));
    const href = photoViewerHref(photoId, { kind: 'memory', key });
    goto(play ? `${href}&slideshow=1` : href);
  }

  function takenLabel(iso: string): string {
    try {
      return new Date(iso).toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' });
    } catch {
      return '';
    }
  }
</script>

<svelte:head><title>Memories — OnScreen</title></svelte:head>

<div class="page">
  <div class="head">
    <h1>Memories</h1>
    {#if date}<div class="sub">{dayLabel(date)}</div>{/if}
  </div>

  {#if loading}
    <p class="muted" role="status">Loading memories…</p>
  {:else if error}
    <div class="error-bar" role="alert">
      {error}
      <button type="button" class="retry" onclick={() => load(date)}>Retry</button>
    </div>
  {:else if groups.length === 0}
    <div class="empty">
      <p class="empty-t">No memories for this day</p>
      <p class="empty-s">Photos taken on this day in earlier years show up here.</p>
    </div>
  {:else}
    {#each groups as g, i (`${g.kind}-${g.year}`)}
      <section class="memory" id="m{i}">
        <div class="memory-head">
          <div>
            <h2>{g.title}</h2>
            <div class="meta">{takenLabel(g.cover.taken_at)} · {g.total.toLocaleString()} {g.total === 1 ? 'photo' : 'photos'}</div>
          </div>
          <button type="button" class="play" onclick={() => open(i, g.items[0].id, true)} disabled={g.items.length === 0}>
            <svg viewBox="0 0 16 16" width="12" height="12" fill="currentColor" aria-hidden="true"><path d="M4 2.5v11l9-5.5-9-5.5z" /></svg>
            Play
          </button>
        </div>
        <div class="grid">
          {#each g.items as p (p.id)}
            <a
              class="tile"
              href={photoViewerHref(p.id, { kind: 'memory', key: memoryKey(date, i) })}
              title={p.title}
              onclick={(e) => { e.preventDefault(); open(i, p.id, false); }}
            >
              <img src={photoImageUrl(p, { w: 300 })} alt="" loading="lazy" />
            </a>
          {/each}
        </div>
      </section>
    {/each}
  {/if}
</div>

<style>
  .page { padding: 2.5rem 2.5rem 6rem; }
  .head { margin-bottom: 1.5rem; padding-bottom: 1.25rem; border-bottom: 1px solid var(--border); }
  h1 { font-size: 1.4rem; font-weight: 800; color: var(--text-primary); letter-spacing: -0.025em; }
  .sub { font-size: 0.8rem; color: var(--text-muted); margin-top: 0.25rem; }
  .memory { margin-bottom: 2rem; }
  .memory-head {
    display: flex; align-items: flex-end; justify-content: space-between; gap: 1rem; margin-bottom: 0.75rem;
  }
  h2 { font-size: 1rem; font-weight: 700; color: var(--text-primary); }
  .meta { font-size: 0.72rem; color: var(--text-muted); margin-top: 0.2rem; }
  .play {
    display: inline-flex; align-items: center; gap: 0.35rem;
    padding: 0.42rem 0.85rem; background: var(--accent); border: none; border-radius: 7px;
    color: #fff; font-size: 0.78rem; font-weight: 600; cursor: pointer;
  }
  .play:disabled { opacity: 0.5; cursor: not-allowed; }
  .grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(160px, 1fr)); gap: 0.5rem; }
  .tile {
    position: relative; display: block; aspect-ratio: 4 / 3;
    border-radius: 8px; overflow: hidden; background: var(--bg-elevated);
  }
  .tile img { width: 100%; height: 100%; object-fit: cover; display: block; transition: transform 0.3s; }
  .tile:hover img { transform: scale(1.04); }
  .tile:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
  .muted { color: var(--text-muted); font-size: 0.85rem; }
  .error-bar {
    background: var(--error-bg); color: var(--error); padding: 0.6rem 0.9rem;
    border-radius: 8px; font-size: 0.8rem; display: flex; gap: 0.75rem; align-items: center;
  }
  .retry {
    background: none; border: 1px solid currentColor; border-radius: 6px; color: inherit;
    font-size: 0.72rem; padding: 0.15rem 0.5rem; cursor: pointer;
  }
  .empty { text-align: center; padding: 3.5rem 1rem; color: var(--text-muted); }
  .empty-t { font-weight: 700; color: var(--text-secondary); }
  .empty-s { font-size: 0.8rem; margin-top: 0.4rem; }
  @media (max-width: 768px) {
    .page { padding: 1.25rem 1rem 8rem; }
    .grid { grid-template-columns: repeat(auto-fill, minmax(110px, 1fr)); gap: 0.35rem; }
  }
</style>
