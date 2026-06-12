<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { goto } from '$app/navigation';
  import { libraryApi, hubApi, userApi, assetUrl, type Library, type HubItem, type HubData, type HubLibraryRow, type HubRowPref } from '$lib/api';
  import { itemHref } from '$lib/itemHref';
  import { toast } from '$lib/stores/toast';

  let libraries: Library[] = [];
  let continueTV: HubItem[] = [];
  let continueMovies: HubItem[] = [];
  let continueOther: HubItem[] = [];
  let recentlyAddedByLibrary: HubLibraryRow[] = [];
  let trending: HubItem[] = [];
  let loading = true;
  let error = '';
  let confirmDelete: Library | null = null;
  let deleting = false;
  let pollTimer: ReturnType<typeof setInterval>;

  // ── Per-user hub layout (row order + visibility) ─────────────────────────
  // Saved server-side so it follows the user across devices. Rows absent
  // from the saved layout render enabled after the configured ones, so new
  // libraries appear without re-saving.
  let hubLayout: HubRowPref[] = [];
  let editMode = false;
  let editEntries: { key: string; title: string; enabled: boolean; empty: boolean }[] = [];
  let savingLayout = false;

  type HubSection = {
    key: string;
    title: string;
    items: HubItem[];
    // 'continue' rows show the progress bar; 'library' rows link their
    // header and may render square; 'plain' is trending.
    kind: 'continue' | 'plain' | 'library';
    libraryId?: string;
    librarySquare?: boolean;
  };

  // Canonical section list in default order, derived from hub data.
  $: availableSections = [
    { key: 'continue_tv',     title: 'Continue Watching TV Shows', items: continueTV,     kind: 'continue' },
    { key: 'continue_movies', title: 'Continue Watching Movies',   items: continueMovies, kind: 'continue' },
    { key: 'continue_other',  title: 'Continue Watching',          items: continueOther,  kind: 'continue' },
    { key: 'trending',        title: 'Trending this week',         items: trending,       kind: 'plain' },
    ...recentlyAddedByLibrary.map((row): HubSection => ({
      key: `library:${row.library_id}`,
      title: `Recently Added to ${row.library_name}`,
      items: row.items,
      kind: 'library',
      libraryId: row.library_id,
      librarySquare: isSquareLibrary(row.library_type),
    })),
  ] as HubSection[];

  // Saved order first (unknown keys skipped — e.g. a deleted library),
  // then anything not in the saved layout in default order.
  $: orderedSections = (() => {
    const byKey = new Map(availableSections.map((s) => [s.key, s]));
    const used = new Set<string>();
    const out: { section: HubSection; enabled: boolean }[] = [];
    for (const pref of hubLayout) {
      const s = byKey.get(pref.key);
      if (!s) continue;
      used.add(pref.key);
      out.push({ section: s, enabled: pref.enabled });
    }
    for (const s of availableSections) {
      if (!used.has(s.key)) out.push({ section: s, enabled: true });
    }
    return out;
  })();

  function openEdit() {
    editEntries = orderedSections.map((e) => ({
      key: e.section.key,
      title: e.section.title,
      enabled: e.enabled,
      empty: e.section.items.length === 0,
    }));
    editMode = true;
  }

  function moveEntry(i: number, delta: number) {
    const j = i + delta;
    if (j < 0 || j >= editEntries.length) return;
    const next = [...editEntries];
    [next[i], next[j]] = [next[j], next[i]];
    editEntries = next;
  }

  async function saveLayout() {
    savingLayout = true;
    const rows = editEntries.map((e) => ({ key: e.key, enabled: e.enabled }));
    try {
      await userApi.setHubLayout(rows);
      hubLayout = rows;
      editMode = false;
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Failed to save layout');
    } finally { savingLayout = false; }
  }

  async function resetLayout() {
    savingLayout = true;
    try {
      await userApi.setHubLayout([]);
      hubLayout = [];
      editMode = false;
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Failed to reset layout');
    } finally { savingLayout = false; }
  }

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
    // Honor a stashed post-login redirect. Set by /login when the
    // user arrives via ?next= and clicks an SSO button (the IdP
    // round-trip discards URL state, so /login persists the target
    // here for the / route to pick up). Same-origin guard mirrors
    // the /login validation — never bounce off-site.
    try {
      const stash = sessionStorage.getItem('onscreen_post_login_redirect');
      if (stash && stash.startsWith('/') && !stash.startsWith('//')) {
        sessionStorage.removeItem('onscreen_post_login_redirect');
        goto(stash);
        return;
      }
    } catch { /* sessionStorage disabled / private mode — ignore */ }
    await load();
    pollTimer = setInterval(refreshHub, 30_000);
  });

  onDestroy(() => { if (pollTimer) clearInterval(pollTimer); });

  // Older server builds only return `continue_watching` (the
  // combined feed); newer ones return the pre-split arrays. Fall
  // back to a client-side type filter so the UI keeps working
  // either way until every operator has restarted.
  function unpackContinue(hub: HubData) {
    if (hub.continue_watching_tv || hub.continue_watching_movies || hub.continue_watching_other) {
      continueTV = hub.continue_watching_tv ?? [];
      continueMovies = hub.continue_watching_movies ?? [];
      continueOther = hub.continue_watching_other ?? [];
      return;
    }
    const all = hub.continue_watching ?? [];
    continueTV = all.filter(i => i.type === 'episode');
    continueMovies = all.filter(i => i.type === 'movie');
    continueOther = all.filter(i => i.type !== 'episode' && i.type !== 'movie');
  }

  async function refreshHub() {
    try {
      const hub = await hubApi.get();
      unpackContinue(hub);
      recentlyAddedByLibrary = hub.recently_added_by_library ?? [];
      trending = hub.trending ?? [];
    } catch { /* silently skip — next poll will retry */ }
  }

  async function load() {
    loading = true; error = '';
    try {
      // Preferences are best-effort: a failure just renders the default
      // layout rather than blocking the hub.
      const [libs, hub, prefs] = await Promise.all([
        libraryApi.list(),
        hubApi.get(),
        userApi.getPreferences().catch(() => null),
      ]);
      libraries = libs;
      unpackContinue(hub);
      recentlyAddedByLibrary = hub.recently_added_by_library ?? [];
      trending = hub.trending ?? [];
      hubLayout = prefs?.hub_layout ?? [];
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
    return itemHref(item.type, item.id);
  }

  // Albums, photos, and audiobook-tree containers look right as
  // squares; movies/shows keep the 2:3 poster.
  function isSquare(item: HubItem): boolean {
    return item.type === 'album'
      || item.type === 'photo'
      || item.type === 'artist'
      || item.type === 'book_author';
  }


  // Whole per-library rows render as squares when the library's item
  // type is square-friendly. Skips the per-item check inside the #each,
  // which would otherwise produce a mix of shapes if a library ever
  // returns cross-typed items (shouldn't, but guards against it).
  function isSquareLibrary(type: string): boolean {
    return type === 'music' || type === 'photo';
  }

  // Per-library-type presentation. `icon` + `label` drive the tile header;
  // `colors` is the accent hue, used both for the type label and the tile's
  // subtle background tint (see .lib-tile). The tint layers over the THEMED
  // surface (var(--bg-elevated)), so tiles read correctly in both light and
  // dark — they used to be hardcoded dark gradients that became dark-on-dark
  // mush in light mode.
  // The Anime tile uses a real Goku headshot (bundled static asset) instead of an
  // emoji. Other types fall back to their emoji glyph.
  const types: Record<string, { label: string; icon: string; image?: string }> = {
    movie:      { label: 'Movies',      icon: '🎬' },
    show:       { label: 'TV Shows',    icon: '📺' },
    music:      { label: 'Music',       icon: '🎵' },
    photo:      { label: 'Photos',      icon: '🖼️' },
    anime:      { label: 'Anime',       icon: '🌸', image: '/goku.png' },
    cartoons:   { label: 'Cartoons',    icon: '🦸', image: '/cage-superman.png' },
    audiobook:  { label: 'Audiobooks',  icon: '🎧' },
    podcast:    { label: 'Podcasts',    icon: '🎙️' },
    book:       { label: 'Books',       icon: '📚' },
    home_video: { label: 'Home Videos', icon: '📹' },
    dvr:        { label: 'DVR',          icon: '📡' },
  };
  const colors: Record<string, string> = {
    movie: '#60a5fa', show: '#a78bfa', music: '#34d399', photo: '#fb923c',
    anime: '#f472b6', cartoons: '#2563eb', audiobook: '#fbbf24', podcast: '#f87171',
    book: '#818cf8', home_video: '#2dd4bf', dvr: '#fb7185',
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
    <div class="hub-controls">
      <button class="customize-btn" on:click={openEdit} title="Choose which rows show and their order">
        <svg viewBox="0 0 24 24" width="14" height="14" fill="currentColor" aria-hidden="true">
          <path d="M3 7h12v2H3V7zm0 4h18v2H3v-2zm0 4h9v2H3v-2zM19 4l-2 2 2 2 2-2-2-2z"/>
        </svg>
        Customize rows
      </button>
    </div>

    {#if editMode}
      <div class="edit-panel">
        <h2 class="edit-title">Hub rows</h2>
        <p class="edit-hint">Reorder rows and toggle which ones you see. This only affects your account.</p>
        <ul class="edit-list">
          {#each editEntries as entry, i (entry.key)}
            <li class="edit-row" class:disabled={!entry.enabled}>
              <div class="edit-move">
                <button class="edit-btn" disabled={i === 0} on:click={() => moveEntry(i, -1)} aria-label="Move {entry.title} up">↑</button>
                <button class="edit-btn" disabled={i === editEntries.length - 1} on:click={() => moveEntry(i, 1)} aria-label="Move {entry.title} down">↓</button>
              </div>
              <span class="edit-name">
                {entry.title}
                {#if entry.empty}<span class="edit-empty">(empty right now)</span>{/if}
              </span>
              <label class="edit-toggle">
                <input type="checkbox" bind:checked={entry.enabled} />
                Show
              </label>
            </li>
          {/each}
        </ul>
        <div class="edit-actions">
          <button class="edit-save" on:click={saveLayout} disabled={savingLayout}>
            {savingLayout ? 'Saving…' : 'Save'}
          </button>
          <button class="edit-cancel" on:click={() => (editMode = false)} disabled={savingLayout}>Cancel</button>
          <button class="edit-reset" on:click={resetLayout} disabled={savingLayout}>Reset to default</button>
        </div>
      </div>
    {/if}

    <!-- Hub rows in the user's configured order. Disabled rows and
         empty rows are suppressed (empty so a user with only movies in
         flight doesn't see a bare TV row). -->
    {#each orderedSections as entry (entry.section.key)}
      {@const section = entry.section}
      {#if entry.enabled && section.items.length > 0}
        <section class="hub-section">
          {#if section.kind === 'library'}
            <h2 class="hub-title">
              <a class="hub-title-link" href={`/libraries/${section.libraryId}?sort=created_at&sort_dir=desc`}>
                {section.title}
              </a>
            </h2>
          {:else}
            <h2 class="hub-title">{section.title}</h2>
          {/if}
          <div class="hub-scroll">
            {#each section.items as item (item.id)}
              {@const art = section.kind === 'library' ? item.poster_path : (item.poster_path ?? item.thumb_path)}
              {@const square = section.kind === 'library' ? section.librarySquare : (section.kind === 'continue' && isSquare(item))}
              <a class="hub-card" class:square href={hubHref(item)}>
                {#if art}
                  <img src={assetUrl(`/artwork/${encodeURI(art)}?v=${item.updated_at}&w=300`)}
                       srcset="{assetUrl(`/artwork/${encodeURI(art)}?v=${item.updated_at}&w=150`)} 150w, {assetUrl(`/artwork/${encodeURI(art)}?v=${item.updated_at}&w=300`)} 300w, {assetUrl(`/artwork/${encodeURI(art)}?v=${item.updated_at}&w=450`)} 450w"
                       sizes="(max-width: 768px) 130px, 220px"
                       alt={item.title} loading="lazy" />
                {:else}
                  <div class="hub-poster-blank" class:square>
                    <span>{item.title[0]?.toUpperCase()}</span>
                  </div>
                {/if}
                {#if section.kind === 'continue'}
                  <div class="hub-progress">
                    <div class="hub-progress-bar" style="width:{progressPct(item)}%"></div>
                  </div>
                {/if}
                {#if section.kind === 'library' && item.show_title}
                  <div class="hub-label">{item.show_title}</div>
                  <div class="hub-sublabel">{item.title}</div>
                {:else}
                  <div class="hub-label">{item.title}</div>
                  {#if item.year}<div class="hub-year">{item.year}</div>{/if}
                {/if}
              </a>
            {/each}
          </div>
        </section>
      {/if}
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
        {@const t = types[lib.type] ?? { label: lib.type, icon: '📁', image: undefined }}
        {@const color = colors[lib.type] ?? '#aaa'}
        <div
          class="lib-tile"
          role="button"
          tabindex="0"
          style="--tile-accent:{color}"
          on:click={() => goto(`/libraries/${lib.id}`)}
          on:keydown={e => (e.key === 'Enter' || e.key === ' ') && (e.preventDefault(), goto(`/libraries/${lib.id}`))}
        >
          <div class="tile-top">
            {#if t.image}
              <img class="tile-icon-img" src={t.image} alt={t.label} />
            {:else}
              <span class="tile-icon">{t.icon}</span>
            {/if}
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
            <div class="tile-type">{t.label}</div>
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
  <!-- Backdrop click dismisses; the inner dialog stops propagation
       so clicks on the dialog body don't dismiss. The svelte-ignore
       comment skips the keyboard-handler warning because the dialog
       has its own focusable buttons (Cancel/Delete) and Escape is
       handled by the modal's natural focus trap — there's nothing
       useful for a keyboard handler on the wrapper. -->
  <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
  <div class="overlay" role="presentation" on:click={() => confirmDelete = null}>
    <!-- svelte-ignore a11y_click_events_have_key_events -->
    <div class="dialog" role="dialog" aria-modal="true" tabindex="-1" on:click|stopPropagation>
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

  /* ── Hub customization ─────────────────────────────────────────────── */
  .hub-controls { display: flex; justify-content: flex-end; margin-bottom: 0.5rem; }
  .customize-btn {
    display: inline-flex; align-items: center; gap: 0.4rem;
    font-size: 0.75rem; padding: 0.35rem 0.7rem; border-radius: 6px;
    background: transparent; border: 1px solid var(--border);
    color: var(--text-muted); cursor: pointer;
  }
  .customize-btn:hover { color: var(--text-primary); border-color: var(--border-strong); }

  .edit-panel {
    background: var(--bg-elevated, var(--bg-secondary));
    border: 1px solid var(--border);
    border-radius: 10px;
    padding: 1rem 1.25rem;
    margin-bottom: 1.5rem;
  }
  .edit-title { font-size: 0.95rem; font-weight: 600; margin: 0 0 0.25rem; }
  .edit-hint { font-size: 0.78rem; color: var(--text-muted); margin: 0 0 0.8rem; }
  .edit-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 0.35rem; }
  .edit-row {
    display: flex; align-items: center; gap: 0.75rem;
    padding: 0.4rem 0.6rem; border: 1px solid var(--border); border-radius: 7px;
  }
  .edit-row.disabled .edit-name { color: var(--text-muted); text-decoration: line-through; }
  .edit-move { display: flex; gap: 0.25rem; }
  .edit-btn {
    width: 1.7rem; height: 1.7rem; border-radius: 5px;
    background: var(--bg-secondary); border: 1px solid var(--border);
    color: var(--text-secondary); cursor: pointer; line-height: 1;
  }
  .edit-btn:disabled { opacity: 0.35; cursor: default; }
  .edit-btn:hover:not(:disabled) { border-color: var(--accent); color: var(--text-primary); }
  .edit-name { flex: 1; font-size: 0.85rem; }
  .edit-empty { color: var(--text-muted); font-size: 0.72rem; margin-left: 0.4rem; }
  .edit-toggle { display: inline-flex; align-items: center; gap: 0.35rem; font-size: 0.78rem; color: var(--text-secondary); cursor: pointer; }
  .edit-actions { display: flex; gap: 0.6rem; margin-top: 0.9rem; }
  .edit-save {
    padding: 0.4rem 1rem; border-radius: 6px; border: none;
    background: var(--accent); color: #fff; font-size: 0.8rem; font-weight: 600; cursor: pointer;
  }
  .edit-cancel, .edit-reset {
    padding: 0.4rem 0.8rem; border-radius: 6px;
    background: transparent; border: 1px solid var(--border);
    color: var(--text-secondary); font-size: 0.8rem; cursor: pointer;
  }
  .edit-reset { margin-left: auto; }
  .edit-save:disabled, .edit-cancel:disabled, .edit-reset:disabled { opacity: 0.6; cursor: default; }
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
  /* Episode title under the show name on recently-added TV tiles. */
  .hub-sublabel {
    padding: 0 0.5rem 0.4rem;
    font-size: 0.65rem;
    color: var(--text-muted);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
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
    --tile-accent: #8888aa;
    padding: 1.4rem 1.5rem 1.3rem;
    cursor: pointer;
    transition: box-shadow 0.15s;
    min-height: 140px;
    display: flex;
    flex-direction: column;
    justify-content: space-between;
    /* Accent-tinted corner fading into the themed surface. Base is
       var(--bg-elevated) (not a fixed dark), so the tile follows the theme
       and the var(--text-primary) name/labels keep their contrast. */
    background:
      linear-gradient(135deg, color-mix(in srgb, var(--tile-accent) 20%, transparent), transparent 58%),
      var(--bg-elevated);
  }
  .lib-tile:hover {
    box-shadow: inset 0 0 0 2px color-mix(in srgb, var(--tile-accent) 55%, transparent);
  }

  .tile-top {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    margin-bottom: 1.25rem;
  }
  .tile-icon { font-size: 1.4rem; line-height: 1; }
  .tile-icon-img {
    width: 2rem;
    height: 2rem;
    border-radius: 50%;
    object-fit: cover;
    display: block;
    box-shadow: 0 0 0 1.5px var(--tile-accent, #f472b6);
  }
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
    background: var(--bg-hover);
    color: var(--text-secondary);
    cursor: pointer;
    transition: background 0.12s, color 0.12s;
  }
  .tile-btn:hover { background: var(--border-strong); color: var(--text-primary); }
  .tile-btn-danger:hover { background: var(--error-bg); color: var(--error); }

  .tile-type {
    font-size: 0.68rem;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    margin-bottom: 0.3rem;
    /* Accent hue nudged toward the theme's text color so bright accents
       (amber, teal, green) stay legible on the near-white light-mode tile
       while staying vivid on dark. */
    color: color-mix(in srgb, var(--tile-accent) 78%, var(--text-primary));
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
