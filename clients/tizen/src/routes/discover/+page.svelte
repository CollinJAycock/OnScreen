<script lang="ts">
  // TMDB-discover search + in-app request submit. Same shape as the
  // Android TV DiscoverFragment: type-aware results from the server's
  // /discover/search endpoint (which proxies TMDB and annotates each
  // row with library + active-request state), with a Request button
  // on rows the user doesn't already have. Submitting a request
  // routes through /api/v1/requests; the admin then wires it to the
  // configured Sonarr / Radarr.
  //
  // Locally, the "Already in library" rows route through the normal
  // /item/[id] detail flow, since the user has them already.

  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { endpoints, type DiscoverItem, Unauthorized } from '$lib/api';
  import OnScreenKeyboard from '$lib/components/OnScreenKeyboard.svelte';
  import { focusable } from '$lib/focus/focusable';
  import { focusManager } from '$lib/focus/manager';
  import Spinner from '$lib/components/Spinner.svelte';

  let query = $state('');
  let results = $state<DiscoverItem[]>([]);
  let loading = $state(false);
  let error = $state('');
  // Local cache of (tmdb_id → "pending" | "approved" | etc.) so a
  // just-submitted request flips its row chip without a re-search.
  // Keyed on tmdb_id because the same TMDB title can come back from
  // a re-search with a different active_request_id.
  let requested = $state<Record<number, string>>({});
  let submitting = $state<number | null>(null);

  let searchTimer: ReturnType<typeof setTimeout> | null = null;

  onMount(() => {
    return focusManager.pushBack(() => {
      goto('/hub');
      return true;
    });
  });

  function onQueryChange(v: string) {
    query = v;
    error = '';
    if (searchTimer) clearTimeout(searchTimer);
    if (v.trim().length < 2) {
      results = [];
      loading = false;
      return;
    }
    // Debounce — the on-screen keyboard fires onchange per character;
    // a 250 ms gate keeps the discover endpoint from getting hit
    // every keystroke.
    searchTimer = setTimeout(() => void runSearch(), 250);
  }

  async function runSearch() {
    loading = true;
    try {
      const fresh = await endpoints.discover.search(query.trim(), 18);
      results = fresh;
    } catch (e) {
      if (e instanceof Unauthorized) goto('/login');
      else error = (e as Error).message ?? 'Search failed';
      results = [];
    } finally {
      loading = false;
    }
  }

  async function submitRequest(item: DiscoverItem) {
    if (item.type !== 'movie' && item.type !== 'show') return;
    submitting = item.tmdb_id;
    try {
      const created = await endpoints.discover.createRequest(item.type, item.tmdb_id);
      requested = { ...requested, [item.tmdb_id]: created.status };
    } catch (e) {
      error = (e as Error).message ?? 'Request failed';
    } finally {
      submitting = null;
    }
  }

  function rowStatus(item: DiscoverItem): string | null {
    if (item.in_library) return 'in library';
    const local = requested[item.tmdb_id];
    if (local) return local;
    if (item.has_active_request) return item.active_request_status ?? 'requested';
    return null;
  }
</script>

<div class="page">
  <header>
    <h1>Discover</h1>
    <nav class="links">
      <a href="/hub/" data-sveltekit-preload-data="false">home</a>
    </nav>
  </header>

  <div class="search-row">
    <OnScreenKeyboard value={query} onchange={onQueryChange} />
  </div>

  {#if error}
    <p class="error">{error}</p>
  {/if}

  {#if loading}
    <Spinner />
  {:else if results.length === 0 && query.trim().length >= 2}
    <p class="empty">No results.</p>
  {:else if results.length === 0}
    <p class="empty">Type a title to search TMDB.</p>
  {:else}
    <div class="grid">
      {#each results as r, i (`${r.type}-${r.tmdb_id}`)}
        {@const status = rowStatus(r)}
        <div class="card">
          {#if r.poster_url}
            <img src={r.poster_url} alt="" class="poster" />
          {:else}
            <div class="poster placeholder"></div>
          {/if}
          <div class="card-body">
            <div class="card-title">
              {r.title}{#if r.year}{' · '}{r.year}{/if}
            </div>
            <div class="card-meta">
              <span class="type-tag">{r.type}</span>
              {#if r.rating}<span>★ {r.rating.toFixed(1)}</span>{/if}
              {#if status}<span class="status status-{status}">{status}</span>{/if}
            </div>
            {#if r.overview}
              <p class="overview">{r.overview}</p>
            {/if}
            <div class="card-actions">
              {#if r.in_library && r.library_item_id}
                <button
                  use:focusable={{ autofocus: i === 0 }}
                  class="btn-primary"
                  onclick={() => goto(`/item/${r.library_item_id}`)}
                >
                  Open
                </button>
              {:else if status}
                <button
                  use:focusable={{ autofocus: i === 0 }}
                  class="btn-secondary"
                  disabled
                >
                  {status === 'pending' ? 'Requested' : status}
                </button>
              {:else}
                <button
                  use:focusable={{ autofocus: i === 0 }}
                  class="btn-primary"
                  disabled={submitting === r.tmdb_id}
                  onclick={() => submitRequest(r)}
                >
                  {submitting === r.tmdb_id ? 'Requesting…' : 'Request'}
                </button>
              {/if}
            </div>
          </div>
        </div>
      {/each}
    </div>
  {/if}
</div>

<style>
  .page {
    padding: 32px var(--page-pad) 0;
  }
  header {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    margin-bottom: 32px;
  }
  h1 {
    font-size: var(--font-xl);
    margin: 0;
    color: var(--accent);
  }
  .links {
    display: flex;
    gap: 32px;
    font-size: var(--font-md);
    color: var(--text-secondary);
  }
  .links a { color: inherit; text-decoration: none; }

  .search-row { margin-bottom: 24px; }
  .error { color: #fca5a5; }
  .empty { color: var(--text-secondary); }

  .grid {
    display: grid;
    grid-template-columns: repeat(2, 1fr);
    gap: 24px;
  }
  .card {
    display: flex;
    gap: 20px;
    background: rgba(255, 255, 255, 0.03);
    border-radius: 8px;
    padding: 20px;
  }
  .poster {
    width: 160px;
    height: 240px;
    object-fit: cover;
    border-radius: 6px;
    background: rgba(255, 255, 255, 0.05);
  }
  .poster.placeholder { display: block; }
  .card-body {
    flex: 1;
    display: flex;
    flex-direction: column;
    gap: 8px;
    min-width: 0;
  }
  .card-title {
    font-size: var(--font-md);
    font-weight: 600;
  }
  .card-meta {
    display: flex;
    gap: 16px;
    font-size: var(--font-sm);
    color: var(--text-secondary);
  }
  .type-tag {
    text-transform: uppercase;
    letter-spacing: 0.1em;
    color: var(--accent);
  }
  .status {
    text-transform: uppercase;
    letter-spacing: 0.05em;
    font-size: var(--font-xs, 14px);
  }
  .status-pending { color: #fbbf24; }
  .status-approved, .status-downloading { color: #60a5fa; }
  .status-available { color: #34d399; }
  .status-declined, .status-failed { color: #f87171; }
  .overview {
    margin: 0;
    font-size: var(--font-sm);
    color: var(--text-secondary);
    display: -webkit-box;
    -webkit-line-clamp: 3;
    line-clamp: 3;
    -webkit-box-orient: vertical;
    overflow: hidden;
  }
  .card-actions { margin-top: auto; }
  .card-actions button {
    padding: 10px 24px;
    font-size: var(--font-sm);
    border-radius: 6px;
    cursor: pointer;
    background: var(--bg-secondary, #1f1f24);
    border: 2px solid transparent;
    color: var(--text-primary);
    font-family: inherit;
  }
  .card-actions button:focus,
  .card-actions button:focus-visible {
    border-color: var(--accent);
    outline: none;
  }
  .card-actions .btn-primary:focus,
  .card-actions .btn-primary:focus-visible {
    background: var(--accent);
    color: white;
  }
  .card-actions button[disabled] {
    opacity: 0.6;
    cursor: not-allowed;
  }
</style>
