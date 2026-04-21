<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { libraryApi, mediaApi, type Library, type YearCount } from '$lib/api';

  let library: Library | null = null;
  let years: YearCount[] = [];
  let loading = true;
  let error = '';

  $: id = $page.params.id!;

  // Group years into decades for easier scanning. Decade key is the floor of
  // the year to ten (1990s → 1990). Years within a decade keep their natural
  // descending order so newest is first inside each section.
  $: decades = (() => {
    const map = new Map<number, YearCount[]>();
    for (const y of years) {
      const d = Math.floor(y.year / 10) * 10;
      if (!map.has(d)) map.set(d, []);
      map.get(d)!.push(y);
    }
    return Array.from(map.entries()).sort((a, b) => b[0] - a[0]);
  })();

  onMount(async () => {
    if (!localStorage.getItem('onscreen_user')) { goto('/login'); return; }
    try {
      [library, years] = await Promise.all([libraryApi.get(id), mediaApi.years(id)]);
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed';
    } finally {
      loading = false;
    }
  });

  function pickYear(y: YearCount) {
    goto(`/libraries/${id}?year_min=${y.year}&year_max=${y.year}`);
  }

  function pickDecade(d: number) {
    goto(`/libraries/${id}?year_min=${d}&year_max=${d + 9}`);
  }
</script>

<div class="page">
  <header>
    <a class="back" href="/libraries/{id}">← {library?.name ?? 'Library'}</a>
    <h1>Browse by Year</h1>
    {#if !loading}<span class="subtle">{years.length} years</span>{/if}
  </header>

  {#if loading}
    <p class="loading">Loading…</p>
  {:else if error}
    <p class="err">{error}</p>
  {:else if years.length === 0}
    <p class="empty">No release years on file. Run a scan with metadata enrichment enabled.</p>
  {:else}
    {#each decades as [d, ys]}
      <section>
        <div class="decade-head">
          <button class="decade-btn" on:click={() => pickDecade(d)}>{d}s</button>
          <span class="subtle">{ys.reduce((s, y) => s + y.count, 0).toLocaleString()} items</span>
        </div>
        <div class="grid">
          {#each ys as y}
            <button class="tile" on:click={() => pickYear(y)}>
              <span class="name">{y.year}</span>
              <span class="count">{y.count.toLocaleString()}</span>
            </button>
          {/each}
        </div>
      </section>
    {/each}
  {/if}
</div>

<style>
  .page { padding: 1.5rem 2rem; max-width: 1200px; margin: 0 auto; }
  header { display: flex; align-items: baseline; gap: 1rem; margin-bottom: 1.5rem; }
  .back { color: var(--text-secondary); text-decoration: none; font-size: 0.85rem; }
  .back:hover { color: var(--text-primary); }
  h1 { margin: 0; font-size: 1.5rem; }
  .subtle { color: var(--text-secondary); font-size: 0.85rem; }
  section { margin-bottom: 1.75rem; }
  .decade-head { display: flex; align-items: baseline; gap: 0.75rem; margin-bottom: 0.5rem; }
  .decade-btn {
    background: none; border: none; color: var(--text-primary);
    font-size: 1.1rem; font-weight: 600; cursor: pointer; padding: 0;
  }
  .decade-btn:hover { color: var(--accent); text-decoration: underline; }
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(120px, 1fr));
    gap: 0.5rem;
  }
  .tile {
    display: flex; justify-content: space-between; align-items: center;
    padding: 0.6rem 0.8rem;
    background: var(--surface); border: 1px solid var(--border); border-radius: 8px;
    color: var(--text-primary); cursor: pointer; transition: background 0.15s, border-color 0.15s;
    font-size: 0.9rem;
  }
  .tile:hover { background: var(--surface-hover); border-color: var(--accent); }
  .name { font-weight: 500; }
  .count { color: var(--text-secondary); font-size: 0.75rem; }
  .loading, .err, .empty { color: var(--text-secondary); padding: 2rem 0; text-align: center; }
  .err { color: var(--error); }
</style>
