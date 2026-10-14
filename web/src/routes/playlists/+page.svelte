<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { playlistApi, type M3UImportResult, type Playlist, type SmartPlaylistRules } from '$lib/api';
  import { confirmAction } from '$lib/native';
  import {
    MAX_PLAYLIST_BYTES,
    importSummary,
    isPlaylistFileName,
    playlistNameFromFile,
    unmatchedReason,
  } from '$lib/playlistM3U';

  let playlists: Playlist[] = [];
  let loading = true;
  let error = '';

  let showCreate = false;
  let newName = '';
  let newDescription = '';
  // Smart-playlist mode: when toggled on, the rules JSON gets posted
  // alongside name/description and the server creates type='smart_playlist'.
  // v2.1 Stage 1 ships JSON-as-text — visual rule builder is v2.2 polish.
  let smartMode = false;
  let smartRulesJSON = '{\n  "types": ["movie"],\n  "year_min": 2015,\n  "rating_min": 7.0,\n  "limit": 50\n}';
  let creating = false;

  // Import from an .m3u / .m3u8 file. The server matches each line to an
  // item you can see and reports what it couldn't place.
  let showImport = false;
  let importFile: File | null = null;
  let importName = '';
  let importing = false;
  let importResult: M3UImportResult | null = null;

  function onPickPlaylist(e: Event) {
    const f = (e.target as HTMLInputElement).files?.[0] ?? null;
    error = '';
    importResult = null;
    if (f && !isPlaylistFileName(f.name)) {
      error = 'Choose an .m3u or .m3u8 playlist file.';
      importFile = null;
      return;
    }
    if (f && f.size > MAX_PLAYLIST_BYTES) {
      error = 'Playlist files are limited to 1 MB.';
      importFile = null;
      return;
    }
    importFile = f;
    if (f && !importName.trim()) importName = playlistNameFromFile(f.name);
  }

  async function importPlaylist() {
    if (!importFile) return;
    importing = true;
    error = '';
    try {
      importResult = await playlistApi.importM3U(importFile, importName);
      importFile = null;
      importName = '';
      showImport = false;
      await load();
    } catch (e: unknown) { error = e instanceof Error ? e.message : 'Import failed'; }
    finally { importing = false; }
  }

  onMount(async () => {
    if (!localStorage.getItem('onscreen_user')) { goto('/login'); return; }
    await load();
  });

  async function load() {
    loading = true;
    try { playlists = await playlistApi.list(); }
    catch (e: unknown) { error = e instanceof Error ? e.message : 'Failed'; }
    finally { loading = false; }
  }

  async function createPlaylist() {
    if (!newName.trim()) return;
    creating = true;
    error = '';
    try {
      let rules: SmartPlaylistRules | undefined;
      if (smartMode) {
        try {
          rules = JSON.parse(smartRulesJSON) as SmartPlaylistRules;
        } catch {
          error = 'Smart playlist rules are not valid JSON';
          creating = false;
          return;
        }
      }
      await playlistApi.create(newName.trim(), newDescription.trim() || undefined, rules);
      newName = '';
      newDescription = '';
      smartMode = false;
      showCreate = false;
      await load();
    } catch (e: unknown) { error = e instanceof Error ? e.message : 'Failed'; }
    finally { creating = false; }
  }

  async function deletePlaylist(id: string) {
    if (!(await confirmAction('Delete this playlist? This cannot be undone.'))) return;
    try {
      await playlistApi.delete(id);
      playlists = playlists.filter(p => p.id !== id);
    } catch (e: unknown) { error = e instanceof Error ? e.message : 'Failed'; }
  }

  function fmtDate(iso: string): string {
    try { return new Date(iso).toLocaleDateString(); } catch { return ''; }
  }
</script>

<svelte:head><title>Playlists — OnScreen</title></svelte:head>

<div class="page">
  <div class="header">
    <h1>My Playlists</h1>
    <div class="header-actions">
      <button class="btn-create" on:click={() => { showImport = !showImport; showCreate = false; }}>Import playlist</button>
      <button class="btn-create" on:click={() => { showCreate = !showCreate; showImport = false; }}>+ New Playlist</button>
    </div>
  </div>

  {#if error}<div class="error-bar">{error}</div>{/if}

  {#if showCreate}
    <form class="create-form" on:submit|preventDefault={createPlaylist}>
      <input bind:value={newName} placeholder="Playlist name" autofocus />
      <input bind:value={newDescription} placeholder="Description (optional)" />
      <label class="smart-toggle">
        <input type="checkbox" bind:checked={smartMode} />
        <span>Smart playlist (rule-based)</span>
      </label>
      {#if smartMode}
        <textarea
          class="rules-input"
          bind:value={smartRulesJSON}
          rows="8"
          spellcheck="false"
          placeholder="JSON rules: types, genres, year_min, year_max, rating_min, limit"
        ></textarea>
        <p class="smart-hint">
          Items are resolved at query time, so newly-imported matches show up automatically.
          Visual rule builder is v2.2; for now the JSON above is the source of truth.
        </p>
      {/if}
      <div class="form-row">
        <button type="submit" class="btn-save" disabled={creating || !newName.trim()}>Create</button>
        <button type="button" class="btn-cancel" on:click={() => (showCreate = false)}>Cancel</button>
      </div>
    </form>
  {/if}

  {#if showImport}
    <form class="create-form" on:submit|preventDefault={importPlaylist}>
      <p class="smart-hint">
        Pick an .m3u or .m3u8 playlist exported from another player. Each line is matched to
        something in your libraries by its path, its folder and file name, or its artist and title.
        Web addresses in the file are never opened.
      </p>
      <input class="file-input" type="file" accept=".m3u,.m3u8,audio/x-mpegurl,audio/mpegurl" on:change={onPickPlaylist} />
      <input bind:value={importName} placeholder="Playlist name (defaults to the file name)" />
      <div class="form-row">
        <button type="submit" class="btn-save" disabled={importing || !importFile}>
          {importing ? 'Importing…' : 'Import'}
        </button>
        <button type="button" class="btn-cancel" on:click={() => (showImport = false)}>Cancel</button>
      </div>
    </form>
  {/if}

  {#if importResult}
    <div class="import-result" role="status">
      <div class="import-head">
        <span>
          Created <a href="/playlists/{importResult.playlist.id}">{importResult.playlist.name}</a>.
          {importSummary(importResult)}
        </span>
        <button class="btn-cancel" on:click={() => (importResult = null)} aria-label="Dismiss">×</button>
      </div>
      {#if importResult.unmatched > 0}
        <details>
          <summary>{importResult.unmatched} {importResult.unmatched === 1 ? 'line' : 'lines'} not added</summary>
          <ul class="unmatched">
            {#each importResult.unmatched_entries as u (u.line)}
              <li><span class="line-no">Line {u.line}</span> <code>{u.entry}</code> — {unmatchedReason(u.reason)}</li>
            {/each}
          </ul>
          {#if importResult.unmatched_truncated}<p class="smart-hint">Only the first 500 are listed.</p>{/if}
        </details>
      {/if}
    </div>
  {/if}

  {#if loading}
    <div class="loading">Loading…</div>
  {:else if playlists.length === 0}
    <div class="empty">
      <p>No playlists yet.</p>
      <p class="empty-sub">Create one, then add items from any movie or episode page.</p>
    </div>
  {:else}
    <div class="grid">
      {#each playlists as pl (pl.id)}
        <a class="card" href="/playlists/{pl.id}">
          <div class="card-icon">{pl.type === 'smart_playlist' ? '⚡' : '♫'}</div>
          <div class="card-name">
            {pl.name}
            {#if pl.type === 'smart_playlist'}<span class="smart-badge">Smart</span>{/if}
          </div>
          {#if pl.description}<div class="card-desc">{pl.description}</div>{/if}
          <div class="card-meta">Updated {fmtDate(pl.updated_at)}</div>
          <button
            class="card-delete"
            title="Delete playlist"
            aria-label="Delete playlist"
            on:click|preventDefault|stopPropagation={() => deletePlaylist(pl.id)}
          >×</button>
        </a>
      {/each}
    </div>
  {/if}
</div>

<style>
  .page { padding: 2.5rem; }
  .header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 2rem; }
  h1 { font-size: 1.4rem; font-weight: 800; color: var(--text-primary); }

  .btn-create {
    padding: 0.42rem 0.85rem;
    background: var(--accent-bg);
    border: 1px solid rgba(124,106,247,0.25);
    border-radius: 7px;
    color: var(--accent-text);
    font-size: 0.78rem;
    font-weight: 600;
    cursor: pointer;
  }
  .btn-create:hover { background: rgba(124,106,247,0.2); }
  .header-actions { display: flex; gap: 0.5rem; }

  .create-form input.file-input {
    flex: none;
    background: none;
    border: none;
    padding: 0.2rem 0;
    color: var(--text-secondary);
  }
  .import-result {
    max-width: 600px;
    margin-bottom: 1.5rem;
    padding: 0.8rem 1rem;
    background: var(--bg-elevated);
    border: 1px solid var(--border);
    border-radius: 8px;
    font-size: 0.82rem;
    color: var(--text-secondary);
  }
  .import-head { display: flex; justify-content: space-between; align-items: flex-start; gap: 0.75rem; }
  .import-head a { color: var(--accent-text); font-weight: 600; }
  .import-result details { margin-top: 0.6rem; }
  .import-result summary { cursor: pointer; color: var(--text-primary); }
  .unmatched { margin: 0.5rem 0 0; padding-left: 1rem; max-height: 240px; overflow-y: auto; }
  .unmatched li { margin: 0.2rem 0; word-break: break-all; color: var(--text-muted); }
  .unmatched code { color: var(--text-secondary); font-size: 0.75rem; }
  .line-no { color: var(--text-muted); font-variant-numeric: tabular-nums; }

  .create-form {
    display: flex; flex-direction: column; gap: 0.5rem;
    margin-bottom: 1.5rem; padding: 1rem;
    background: var(--surface); border-radius: 8px;
    max-width: 600px;
  }
  .form-row { display: flex; gap: 0.5rem; }
  .smart-toggle {
    display: flex; align-items: center; gap: 0.5rem;
    font-size: 0.85rem; color: var(--text-secondary);
    margin-top: 0.25rem;
    cursor: pointer;
  }
  .rules-input {
    font-family: 'SF Mono', Consolas, monospace;
    font-size: 0.8rem;
    padding: 0.6rem 0.8rem;
    background: var(--bg-elevated);
    color: var(--text-primary);
    border: 1px solid var(--border);
    border-radius: 4px;
    resize: vertical;
  }
  .rules-input:focus { outline: none; border-color: var(--accent); }
  .smart-hint {
    font-size: 0.75rem; color: var(--text-muted);
    margin: 0; line-height: 1.4;
  }
  .smart-badge {
    display: inline-block;
    margin-left: 0.4rem;
    padding: 0.1rem 0.4rem;
    background: var(--accent);
    color: white;
    font-size: 0.65rem;
    font-weight: 700;
    border-radius: 999px;
    vertical-align: middle;
    text-transform: uppercase;
    letter-spacing: 0.05em;
  }
  /* Text fields only: the smart-playlist checkbox and the file picker keep
     their own sizing. */
  .create-form input:not([type='checkbox']):not([type='file']) {
    background: var(--bg-hover);
    border: 1px solid var(--border-strong);
    border-radius: 7px;
    padding: 0.42rem 0.75rem;
    color: var(--text-primary);
    font-size: 0.85rem;
    flex: 1;
    min-width: 160px;
  }
  .create-form input:focus { outline: none; border-color: var(--accent); }
  .btn-save {
    padding: 0.42rem 0.85rem;
    background: var(--accent); border: none; border-radius: 7px;
    color: #fff; font-size: 0.78rem; font-weight: 600; cursor: pointer;
  }
  .btn-save:disabled { opacity: 0.5; cursor: not-allowed; }
  .btn-cancel {
    padding: 0.42rem 0.85rem;
    background: var(--bg-hover); border: 1px solid var(--border-strong); border-radius: 7px;
    color: var(--text-muted); font-size: 0.78rem; cursor: pointer;
  }

  .grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(200px, 1fr)); gap: 0.9rem; }

  .card {
    display: flex; flex-direction: column; align-items: center; justify-content: center;
    padding: 1.5rem 1rem;
    background: var(--input-bg);
    border: 1px solid var(--border);
    border-radius: 10px;
    text-decoration: none; color: inherit;
    position: relative;
    transition: border-color 0.15s, background 0.15s;
    min-height: 140px;
  }
  .card:hover { border-color: rgba(124,106,247,0.3); background: rgba(124,106,247,0.05); }
  .card-icon { font-size: 1.6rem; margin-bottom: 0.5rem; color: var(--accent); }
  .card-name { font-size: 0.9rem; font-weight: 700; color: var(--text-primary); text-align: center; }
  .card-desc { font-size: 0.75rem; color: var(--text-muted); margin-top: 0.3rem; text-align: center; }
  .card-meta { font-size: 0.7rem; color: var(--text-muted); margin-top: 0.6rem; }

  .card-delete {
    position: absolute; top: 0.4rem; right: 0.4rem;
    background: none; border: none; color: var(--text-muted); font-size: 1rem;
    cursor: pointer; opacity: 0; transition: opacity 0.15s, color 0.15s;
  }
  .card:hover .card-delete { opacity: 1; }
  .card-delete:hover { color: #f87171; }

  .error-bar {
    background: var(--error-bg); border: 1px solid var(--error-bg); color: var(--error);
    padding: 0.6rem 0.9rem; border-radius: 8px; font-size: 0.8rem; margin-bottom: 1.5rem;
  }
  .loading { color: var(--text-muted); font-size: 0.85rem; }
  .empty { text-align: center; padding: 4rem 2rem; color: var(--text-muted); }
  .empty-sub { font-size: 0.8rem; color: var(--text-muted); margin-top: 0.5rem; }
</style>
