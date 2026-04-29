<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { goto } from '$app/navigation';
  import { libraryApi, hubApi, assetUrl, type Library, type HubItem, type HubLibraryRow } from '$lib/api';
  import { toast } from '$lib/stores/toast';

  let libraries: Library[] = [];
  let continueWatching: HubItem[] = [];
  let recentlyAddedByLibrary: HubLibraryRow[] = [];
  let trending: HubItem[] = [];
  let loading = true;
  let error = '';
  let confirmDelete: Library | null = null;
  let deleting = false;
  let pollTimer: ReturnType<typeof setInterval>;

  onMount(async () => {
    // SSO/SAML/OIDC callback redirects land here with a marker query
    // param. The layout's onMount races this gate to bootstrap the
    // user from /api/v1/auth/refresh — wait briefly so we don't bounce
    // a freshly signed-in user back to /login. Other pages don't need
    // this because every SSO callback redirects to / (this file).
    if (!localStorage.getItem('onscreen_user')) {
      const hasAuthMarker = /(google|oidc|saml)_auth=1/.test(window.location.search);
      if (hasAuthMarker) {
        for (let i = 0; i < 30; i++) {
          await new Promise((r) => setTimeout(r, 100));
          if (localStorage.getItem('onscreen_user')) break;
        }
      }
      if (!localStorage.getItem('onscreen_user')) { goto('/login'); return; }
    }
    await load();
    pollTimer = setInterval(refreshHub, 30_000);
  });

  onDestroy(() => { if (pollTimer) clearInterval(pollTimer); });

  async function refreshHub() {
    try {
      const hub = await hubApi.get();
      continueWatching = hub.continue_watching;
      recentlyAddedByLibrary = hub.recently_added_by_library ?? [];
      trending = hub.trending ?? [];
    } catch { /* silently skip — next poll will retry */ }
  }

  async function load() {
    loading = true; error = '';
    try {
      const [libs, hub] = await Promise.all([libraryApi.list(), hubApi.get()]);
      libraries = libs;
      continueWatching = hub.continue_watching;
      recentlyAddedByLibrary = hub.recently_added_by_library ?? [];
      trending = hub.trending ?? [];
    }
    catch (e: unknown) { error = e instanceof Error ? e.message : 'Failed to load'; }
    finally { loading = false; }
  }

  async function scan(id: string, e: MouseEvent) {
    e.stopPropagation();
    try {
      await libraryApi.scan(id);
      toast.success('Library scan started');
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Failed to start scan');
    }
  }

  async function doDelete() {
    if (!confirmDelete) return;
    deleting = true;
    try {
      const name = confirmDelete.name;
      await libraryApi.del(confirmDelete.id);
      libraries = libraries.filter(l => l.id !== confirmDelete!.id);
      confirmDelete = null;
      toast.success(`Library "${name}" deleted`);
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Delete failed');
    } finally { deleting = false; }
  }

  function progressPct(item: HubItem): number {
    if (!item.view_offset_ms || !item.duration_ms || item.duration_ms === 0) return 0;
    return Math.min(100, (item.view_offset_ms / item.duration_ms) * 100);
  }

  // Hub items mix media types; route to the page that knows how to render each.
  function hubHref(item: HubItem): string {
    switch (item.type) {
      case 'album':  return `/albums/${item.id}`;
      case 'artist': return `/artists/${item.id}`;
      case 'photo':  return `/photos/${item.id}`;
      default:       return `/watch/${item.id}`;
    }
  }

  // Albums and photos look right as squares; movies/shows keep the 2:3 poster.
  function isSquare(item: HubItem): boolean {
    return item.type === 'album' || item.type === 'photo';
  }

  // Whole per-library rows render as squares when the library's item
  // type is square-friendly. Skips the per-item check inside the #each,
  // which would otherwise produce a mix of shapes if a library ever
  // returns cross-typed items (shouldn't, but guards against it).
  function isSquareLibrary(type: string): boolean {
    return type === 'music' || type === 'photo';
  }

  const types: Record<string, { label: string; gradient: string; icon: string }> = {
    movie: { label: 'Movies',   gradient: 'linear-gradient(135deg,#1a2744 0%,#0f1520 100%)', icon: '🎬' },
    show:  { label: 'TV Shows', gradient: 'linear-gradient(135deg,#25173a 0%,#0f1520 100%)', icon: '📺' },
    music: { label: 'Music',    gradient: 'linear-gradient(135deg,#0d2e28 0%,#0f1520 100%)', icon: '🎵' },
    photo: { label: 'Photos',   gradient: 'linear-gradient(135deg,#2e1f0d 0%,#0f1520 100%)', icon: '🖼️' },
  };
  const colors: Record<string, string> = {
    movie: '#60a5fa', show: '#a78bfa', music: '#34d399', photo: '#fb923c'
  };
</script>

<svelte:head><title>OnScreen</title></svelte:head>

<div class="page">
  {#if error}
    <div class="banner-error">{error}</div>
  {/if}

  {#if loading}
    <div class="hub-row">
      <h2 class="hub-title">Continue Watching</h2>
      <div class="hub-scroll">
        {#each [1,2,3,4] as _}
          <div class="hub-card skeleton"></div>
        {/each}
      </div>
    </div>
  {:else}
    <!-- Continue Watching -->
    {#if continueWatching.length > 0}
      <section class="hub-section">
        <h2 class="hub-title">Continue Watching</h2>
        <div class="hub-scroll">
          {#each continueWatching as item (item.id)}
            {@const art = item.poster_path ?? item.thumb_path}
            <a class="hub-card" class:square={isSquare(item)} href={hubHref(item)}>
              {#if art}
                <img src={assetUrl(`/artwork/${encodeURI(art)}?v=${item.updated_at}&w=300`)}
                     srcset="{assetUrl(`/artwork/${encodeURI(art)}?v=${item.updated_at}&w=150`)} 150w, {assetUrl(`/artwork/${encodeURI(art)}?v=${item.updated_at}&w=300`)} 300w, {assetUrl(`/artwork/${encodeURI(art)}?v=${item.updated_at}&w=450`)} 450w"
                     sizes="(max-width: 768px) 130px, 220px"
                     alt={item.title} loading="lazy" />
              {:else}
                <div class="hub-poster-blank">
                  <span>{item.title[0]?.toUpperCase()}</span>
                </div>
              {/if}
              <div class="hub-progress">
                <div class="hub-progress-bar" style="width:{progressPct(item)}%"></div>
              </div>
              <div class="hub-label">{item.title}</div>
              {#if item.year}<div class="hub-year">{item.year}</div>{/if}
            </a>
          {/each}
        </div>
      </section>
    {/if}

    <!-- Trending — what others are watching across the library this week. -->
    {#if trending.length > 0}
      <section class="hub-section">
        <h2 class="hub-title">Trending this week</h2>
        <div class="hub-scroll">
          {#each trending as item (item.id)}
            {@const art = item.poster_path ?? item.thumb_path}
            <a class="hub-card" href={hubHref(item)}>
              {#if art}
                <img src={assetUrl(`/artwork/${encodeURI(art)}?v=${item.updated_at}&w=300`)}
                     srcset="{assetUrl(`/artwork/${encodeURI(art)}?v=${item.updated_at}&w=150`)} 150w, {assetUrl(`/artwork/${encodeURI(art)}?v=${item.updated_at}&w=300`)} 300w, {assetUrl(`/artwork/${encodeURI(art)}?v=${item.updated_at}&w=450`)} 450w"
                     sizes="(max-width: 768px) 130px, 220px"
                     alt={item.title} loading="lazy" />
              {:else}
                <div class="hub-poster-blank">
                  <span>{item.title[0]?.toUpperCase()}</span>
                </div>
              {/if}
              <div class="hub-label">{item.title}</div>
              {#if item.year}<div class="hub-year">{item.year}</div>{/if}
            </a>
          {/each}
        </div>
      </section>
    {/if}

    <!-- Per-library recently added — one strip per library. The header
         link carries the sort state so clicking lands on the library
         page already sorted by created_at descending — same view the
         shelf is showing, just with the full pagination + filters. -->
    {#each recentlyAddedByLibrary as row (row.library_id)}
      {@const square = isSquareLibrary(row.library_type)}
      <section class="hub-section">
        <h2 class="hub-title">
          <a class="hub-title-link" href={`/libraries/${row.library_id}?sort=created_at&sort_dir=desc`}>
            Recently Added to {row.library_name}
          </a>
        </h2>
        <div class="hub-scroll">
          {#each row.items as item (item.id)}
            <a class="hub-card" class:square href={hubHref(item)}>
              {#if item.poster_path}
                <img src={assetUrl(`/artwork/${encodeURI(item.poster_path)}?v=${item.updated_at}&w=300`)}
                     srcset="{assetUrl(`/artwork/${encodeURI(item.poster_path)}?v=${item.updated_at}&w=150`)} 150w, {assetUrl(`/artwork/${encodeURI(item.poster_path)}?v=${item.updated_at}&w=300`)} 300w, {assetUrl(`/artwork/${encodeURI(item.poster_path)}?v=${item.updated_at}&w=450`)} 450w"
                     sizes="(max-width: 768px) 130px, 220px"
                     alt={item.title} loading="lazy" />
              {:else}
                <div class="hub-poster-blank" class:square>
                  <span>{item.title[0]?.toUpperCase()}</span>
                </div>
              {/if}
              <div class="hub-label">{item.title}</div>
              {#if item.year}<div class="hub-year">{item.year}</div>{/if}
            </a>
          {/each}
        </div>
      </section>
    {/each}
  {/if}

  <!-- Libraries -->
  <div class="topbar">
    <h1>Libraries</h1>
    <a href="/libraries/new" class="btn-new">
      <svg viewBox="0 0 16 16" fill="currentColor" width="13" height="13">
        <path d="M8.75 3.75a.75.75 0 00-1.5 0v3.5h-3.5a.75.75 0 000 1.5h3.5v3.5a.75.75 0 001.5 0v-3.5h3.5a.75.75 0 000-1.5h-3.5v-3.5z"/>
      </svg>
      New Library
    </a>
  </div>

  {#if !loading && libraries.length === 0}
    <div class="empty">
      <div class="empty-glyph">⬡</div>
      <p class="empty-title">No libraries</p>
      <p class="empty-sub">Add a library to start managing your media.</p>
      <a href="/libraries/new" class="btn-new">New Library</a>
    </div>
  {:else if !loading}
    <div class="grid">
      {#each libraries as lib (lib.id)}
        {@const t = types[lib.type] ?? { label: lib.type, gradient: 'linear-gradient(135deg,#1a1a2a,#0f0f18)', icon: '📁' }}
        {@const color = colors[lib.type] ?? '#aaa'}
        <div
          class="lib-tile"
          role="button"
          tabindex="0"
          style="background:{t.gradient}"
          on:click={() => goto(`/libraries/${lib.id}`)}
          on:keydown={e => (e.key === 'Enter' || e.key === ' ') && (e.preventDefault(), goto(`/libraries/${lib.id}`))}
        >
          <div class="tile-top">
            <span class="tile-icon">{t.icon}</span>
            <div class="tile-actions">
              <button class="tile-btn" title="Scan" on:click={e => scan(lib.id, e)}>
                <svg viewBox="0 0 16 16" fill="currentColor" width="13" height="13">
                  <path fill-rule="evenodd" d="M12.416 3.376a.75.75 0 01.208 1.04l-5 7.5a.75.75 0 01-1.154.114l-3-3a.75.75 0 011.06-1.06l2.353 2.353 4.493-6.74a.75.75 0 011.04-.207z" clip-rule="evenodd"/>
                </svg>
              </button>
              <button class="tile-btn" title="Settings" on:click={e => { e.stopPropagation(); goto(`/libraries/${lib.id}/settings`); }}>
                <svg viewBox="0 0 16 16" fill="currentColor" width="13" height="13">
                  <path d="M8 9.5a1.5 1.5 0 100-3 1.5 1.5 0 000 3z"/>
                  <path fill-rule="evenodd" d="M8 0a.75.75 0 01.716.527l.502 1.607a5.987 5.987 0 011.29.745l1.648-.567a.75.75 0 01.879.344l1 1.732a.75.75 0 01-.14 1.022l-1.345 1.053a6.02 6.02 0 010 1.476l1.345 1.053a.75.75 0 01.14 1.022l-1 1.732a.75.75 0 01-.879.344l-1.648-.567a5.99 5.99 0 01-1.29.745l-.502 1.607a.75.75 0 01-1.432 0l-.502-1.607a5.989 5.989 0 01-1.29-.745l-1.648.567a.75.75 0 01-.879-.344l-1-1.732a.75.75 0 01.14-1.022l1.345-1.053a6.026 6.026 0 010-1.476L.75 7.511a.75.75 0 01-.14-1.022l1-1.732a.75.75 0 01.879-.344l1.648.567a5.989 5.989 0 011.29-.745L5.928.527A.75.75 0 018 0zm0 5.5a2.5 2.5 0 100 5 2.5 2.5 0 000-5z" clip-rule="evenodd"/>
                </svg>
              </button>
              <button class="tile-btn tile-btn-danger" title="Delete" on:click={e => { e.stopPropagation(); confirmDelete = lib; }}>
                <svg viewBox="0 0 16 16" fill="currentColor" width="13" height="13">
                  <path d="M11 1.75V3h2.25a.75.75 0 010 1.5H2.75a.75.75 0 010-1.5H5V1.75C5 .784 5.784 0 6.75 0h2.5C10.216 0 11 .784 11 1.75zM4.496 6.675l.66 6.6a.25.25 0 00.249.225h5.19a.25.25 0 00.249-.225l.66-6.6a.75.75 0 011.492.149l-.66 6.6A1.748 1.748 0 0110.595 15h-5.19a1.75 1.75 0 01-1.741-1.575l-.66-6.6a.75.75 0 111.492-.15z"/>
                </svg>
              </button>
            </div>
          </div>

          <div class="tile-body">
            <div class="tile-type" style="color:{color}">{t.label}</div>
            <div class="tile-name">{lib.name}</div>
            {#if (lib.scan_paths ?? []).length > 0}
              <div class="tile-path">{lib.scan_paths[0]}{lib.scan_paths.length > 1 ? ` +${lib.scan_paths.length - 1}` : ''}</div>
            {/if}
          </div>
        </div>
      {/each}
    </div>
  {/if}
</div>

{#if confirmDelete}
  <div class="overlay" role="presentation" on:click={() => confirmDelete = null}>
    <div class="dialog" role="dialog" on:click|stopPropagation>
      <p class="dialog-title">Delete "{confirmDelete.name}"?</p>
      <p class="dialog-body">Metadata will be permanently removed. Files on disk are not affected.</p>
      <div class="dialog-actions">
        <button class="dbtn-cancel" on:click={() => confirmDelete = null}>Cancel</button>
        <button class="dbtn-confirm" disabled={deleting} on:click={doDelete}>
          {deleting ? 'Deleting…' : 'Delete'}
        </button>
      </div>
    </div>
  </div>
{/if}

<style>
  .page { padding: 2.5rem 2.5rem 4rem; }

  .banner-error {
    background: var(--error-bg);
    border: 1px solid var(--error);
    color: var(--error);
    padding: 0.65rem 1rem;
    border-radius: 8px;
    font-size: 0.8rem;
    margin-bottom: 1.5rem;
  }

  /* ── Hub rows ─────────────────────────────────────────────────────────────── */
  .hub-section { margin-bottom: 2.5rem; }
  .hub-title {
    font-size: 1.1rem;
    font-weight: 700;
    color: var(--text-primary);
    letter-spacing: -0.02em;
    margin-bottom: 0.85rem;
  }
  .hub-title-link {
    color: inherit;
    text-decoration: none;
  }
  .hub-title-link:hover {
    color: var(--accent);
  }
  .hub-scroll {
    display: flex;
    gap: 0.75rem;
    overflow-x: auto;
    padding-bottom: 0.5rem;
    scrollbar-width: thin;
    scrollbar-color: var(--border-strong) transparent;
  }
  .hub-scroll::-webkit-scrollbar { height: 4px; }
  .hub-scroll::-webkit-scrollbar-thumb { background: var(--border-strong); border-radius: 2px; }

  .hub-card {
    --card-w: clamp(120px, 10vw, 220px);
    flex: 0 0 var(--card-w);
    text-decoration: none;
    color: inherit;
    transition: transform 0.15s, box-shadow 0.15s;
    border-radius: 8px;
    overflow: hidden;
    background: var(--bg-elevated);
  }
  .hub-card:hover { transform: translateY(-3px); box-shadow: 0 8px 24px var(--shadow); }
  .hub-card img {
    width: var(--card-w);
    height: calc(var(--card-w) * 1.5);
    object-fit: cover;
    display: block;
  }
  .hub-card.square img,
  .hub-card.square .hub-poster-blank,
  .hub-poster-blank.square {
    height: var(--card-w);
  }
  .hub-poster-blank {
    width: var(--card-w);
    height: calc(var(--card-w) * 1.5);
    display: flex;
    align-items: center;
    justify-content: center;
    background: linear-gradient(135deg, var(--bg-secondary), var(--bg-primary));
    font-size: 2rem;
    font-weight: 700;
    color: var(--border-strong);
  }
  .hub-progress {
    height: 3px;
    background: var(--border-strong);
  }
  .hub-progress-bar {
    height: 100%;
    background: var(--accent);
    border-radius: 0 1.5px 1.5px 0;
  }
  .hub-label {
    padding: 0.4rem 0.5rem 0.15rem;
    font-size: 0.72rem;
    font-weight: 600;
    color: var(--text-primary);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .hub-year {
    padding: 0 0.5rem 0.4rem;
    font-size: 0.65rem;
    color: var(--text-muted);
  }

  .hub-card.skeleton {
    width: var(--card-w);
    height: calc(var(--card-w) * 1.5 + 40px);
    background: linear-gradient(90deg, var(--bg-elevated) 25%, #16161f 50%, var(--bg-elevated) 75%);
    background-size: 200% 100%;
    animation: shimmer 1.4s infinite;
  }

  /* ── Library grid ─────────────────────────────────────────────────────────── */
  .topbar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 1.25rem;
  }
  h1 { font-size: 1.1rem; font-weight: 700; color: var(--text-primary); letter-spacing: -0.02em; }

  .btn-new {
    display: inline-flex;
    align-items: center;
    gap: 0.35rem;
    background: var(--accent);
    color: #fff;
    border: none;
    padding: 0.45rem 0.9rem;
    border-radius: 7px;
    font-size: 0.8rem;
    font-weight: 600;
    text-decoration: none;
    cursor: pointer;
    transition: background 0.15s;
    letter-spacing: 0.01em;
  }
  .btn-new:hover { background: var(--accent-hover); }

  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(260px, 1fr));
    gap: 1px;
    background: var(--border);
    border: 1px solid var(--border);
    border-radius: 12px;
    overflow: hidden;
  }

  .lib-tile {
    padding: 1.4rem 1.5rem 1.3rem;
    cursor: pointer;
    transition: filter 0.15s;
    min-height: 140px;
    display: flex;
    flex-direction: column;
    justify-content: space-between;
  }
  .lib-tile:hover { filter: brightness(1.12); }

  .tile-top {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    margin-bottom: 1.25rem;
  }
  .tile-icon { font-size: 1.4rem; line-height: 1; }
  .tile-actions {
    display: flex;
    gap: 2px;
    opacity: 0;
    transition: opacity 0.15s;
  }
  .lib-tile:hover .tile-actions { opacity: 1; }

  .tile-btn {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 26px;
    height: 26px;
    border: none;
    border-radius: 5px;
    background: var(--border-strong);
    color: rgba(255,255,255,0.55);
    cursor: pointer;
    transition: background 0.12s, color 0.12s;
  }
  .tile-btn:hover { background: rgba(255,255,255,0.15); color: #fff; }
  .tile-btn-danger:hover { background: var(--error); color: var(--error); }

  .tile-type {
    font-size: 0.68rem;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    margin-bottom: 0.3rem;
    opacity: 0.85;
  }
  .tile-name {
    font-size: 1rem;
    font-weight: 700;
    color: var(--text-primary);
    letter-spacing: -0.01em;
    margin-bottom: 0.3rem;
    line-height: 1.3;
  }
  .tile-path {
    font-size: 0.72rem;
    color: var(--text-muted);
    font-family: 'SF Mono', 'Consolas', monospace;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .skeleton {
    background: linear-gradient(90deg, var(--bg-elevated) 25%, #16161f 50%, var(--bg-elevated) 75%);
    background-size: 200% 100%;
    animation: shimmer 1.4s infinite;
    min-height: 140px;
  }
  @keyframes shimmer { 0% { background-position: 200% 0; } 100% { background-position: -200% 0; } }

  .empty {
    display: flex;
    flex-direction: column;
    align-items: center;
    text-align: center;
    padding: 6rem 2rem;
    gap: 0.5rem;
  }
  .empty-glyph { font-size: 2rem; color: var(--text-muted); margin-bottom: 0.75rem; }
  .empty-title { font-size: 1rem; font-weight: 600; color: var(--text-muted); }
  .empty-sub { font-size: 0.82rem; color: var(--text-muted); margin-bottom: 1.25rem; }

  /* Dialog */
  .overlay {
    position: fixed; inset: 0;
    background: rgba(0,0,0,0.7);
    backdrop-filter: blur(4px);
    display: flex; align-items: center; justify-content: center;
    z-index: 100; padding: 1rem;
  }
  .dialog {
    background: var(--bg-elevated);
    border: 1px solid var(--border-strong);
    border-radius: 12px;
    padding: 1.5rem;
    max-width: 380px;
    width: 100%;
    box-shadow: 0 24px 48px var(--shadow);
  }
  .dialog-title { font-size: 0.9rem; font-weight: 700; color: var(--text-primary); margin-bottom: 0.5rem; }
  .dialog-body { font-size: 0.8rem; color: var(--text-muted); line-height: 1.5; margin-bottom: 1.25rem; }
  .dialog-actions { display: flex; gap: 0.5rem; justify-content: flex-end; }
  .dbtn-cancel {
    padding: 0.45rem 0.9rem;
    background: var(--bg-hover);
    border: 1px solid var(--border-strong);
    border-radius: 7px;
    color: var(--text-secondary);
    font-size: 0.8rem;
    cursor: pointer;
    transition: background 0.12s;
  }
  .dbtn-cancel:hover { background: var(--border-strong); }
  .dbtn-confirm {
    padding: 0.45rem 0.9rem;
    background: rgba(248,113,113,0.15);
    border: 1px solid rgba(248,113,113,0.3);
    border-radius: 7px;
    color: var(--error);
    font-size: 0.8rem;
    font-weight: 600;
    cursor: pointer;
    transition: background 0.12s;
  }
  .dbtn-confirm:hover { background: rgba(248,113,113,0.25); }
  .dbtn-confirm:disabled { opacity: 0.5; cursor: not-allowed; }

  /* ── Mobile ────────────────────────────────────────────────────────────── */
  @media (max-width: 768px) {
    .page { padding: 1.25rem 1rem 5rem; }
    .hub-card { --card-w: clamp(90px, 26vw, 130px); }

    .grid { grid-template-columns: 1fr; }
    .lib-tile { min-height: 120px; padding: 1rem 1.1rem; }
    .tile-actions { opacity: 1; }
  }
</style>
