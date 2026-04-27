<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { libraryApi } from '$lib/api';
  import DirPicker from '$lib/DirPicker.svelte';

  let pickerOpen = false;
  let activePathIndex = 0;

  function openPicker(i: number) { activePathIndex = i; pickerOpen = true; }
  function onPickerSelect(e: CustomEvent<string>) {
    updatePath(activePathIndex, e.detail);
  }

  let name = '';
  let type: 'movie' | 'show' | 'music' | 'photo' | 'dvr' | 'audiobook' | 'podcast' | 'home_video' | 'book' = 'movie';
  let paths: string[] = [''];
  let agent = 'tmdb';
  let language = 'en';
  let submitting = false;
  let error = '';

  // DVR recordings carry their own metadata from EPG — no point
  // hitting TMDB. Auto-flip agent when user picks DVR; reset on
  // switch back so we don't leave 'none' stuck across type changes.
  let lastType: string = type;
  $: {
    if ((type as string) !== lastType) {
      if (type === 'dvr') agent = 'none';
      else if (lastType === 'dvr') agent = 'tmdb';
      lastType = type;
    }
  }

  onMount(() => { if (!localStorage.getItem('onscreen_user')) goto('/login'); });

  function addPath() { paths = [...paths, '']; }
  function removePath(i: number) { paths = paths.filter((_, j) => j !== i); }
  function updatePath(i: number, val: string) { paths = paths.map((p, j) => j === i ? val : p); }

  async function submit() {
    error = '';
    if (!name.trim()) { error = 'Name is required'; return; }
    const scanPaths = paths.map(p => p.trim()).filter(Boolean);
    if (!scanPaths.length) { error = 'At least one scan path is required'; return; }
    submitting = true;
    try {
      const lib = await libraryApi.create({ name: name.trim(), type, scan_paths: scanPaths, agent, language });
      goto(`/libraries/${lib.id}`);
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed to create';
    } finally { submitting = false; }
  }
</script>

<svelte:head><title>New Library — OnScreen</title></svelte:head>

<div class="page">
  <nav class="crumb">
    <a href="/">Libraries</a>
    <span>/</span>
    <span>New</span>
  </nav>

  <h1>New Library</h1>

  {#if error}
    <div class="error-bar">{error}</div>
  {/if}

  <form on:submit|preventDefault={submit}>
    <section class="section">
      <div class="section-label">Name & Type</div>
      <div class="field">
        <label for="name">Library name</label>
        <input id="name" bind:value={name} placeholder="Movies" autocomplete="off" />
      </div>
      <div class="type-picker">
        {#each [['movie','🎬','Movies'],['show','📺','TV Shows'],['music','🎵','Music'],['audiobook','🎧','Audiobooks'],['podcast','🎙️','Podcasts'],['photo','🖼️','Photos'],['home_video','📹','Home Videos'],['book','📚','Books'],['dvr','📼','DVR Recordings']] as [val, icon, label]}
          <label class="type-opt" class:selected={type === val}>
            <input type="radio" bind:group={type} value={val} />
            <span class="type-icon">{icon}</span>
            <span>{label}</span>
          </label>
        {/each}
      </div>
    </section>

    <section class="section">
      <div class="section-label">Scan Paths</div>
      <p class="hint">Directories to scan for media files.</p>
      <div class="path-list">
        {#each paths as path, i}
          <div class="path-row">
            <span class="path-prefix">/</span>
            <input
              class="path-input"
              value={path}
              on:input={e => updatePath(i, (e.target as HTMLInputElement).value)}
              placeholder="media/movies"
            />
            <button type="button" class="browse-btn" title="Browse" on:click={() => openPicker(i)}>
              <svg viewBox="0 0 16 16" fill="currentColor" width="13" height="13">
                <path d="M1.75 1A1.75 1.75 0 000 2.75v10.5C0 14.216.784 15 1.75 15h12.5A1.75 1.75 0 0016 13.25v-8.5A1.75 1.75 0 0014.25 3H7.5a.25.25 0 01-.2-.1l-.9-1.2C6.07 1.26 5.55 1 5 1H1.75z"/>
              </svg>
            </button>
            {#if paths.length > 1}
              <button type="button" class="path-remove" on:click={() => removePath(i)}>×</button>
            {/if}
          </div>
        {/each}
      </div>
      <button type="button" class="add-path" on:click={addPath}>+ Add path</button>
    </section>

    <section class="section">
      <div class="section-label">Metadata</div>
      <div class="field-row">
        <div class="field">
          <label for="agent">Agent</label>
          <select id="agent" bind:value={agent}>
            <option value="tmdb">TMDB</option>
            <option value="none">None</option>
          </select>
        </div>
        <div class="field">
          <label for="lang">Language</label>
          <select id="lang" bind:value={language}>
            <option value="en">English</option>
            <option value="fr">French</option>
            <option value="de">German</option>
            <option value="es">Spanish</option>
            <option value="ja">Japanese</option>
            <option value="ko">Korean</option>
            <option value="zh">Chinese</option>
          </select>
        </div>
      </div>
    </section>

    <div class="form-foot">
      <a href="/" class="btn-ghost">Cancel</a>
      <button type="submit" class="btn-submit" disabled={submitting}>
        {submitting ? 'Creating…' : 'Create Library'}
      </button>
    </div>
  </form>
</div>

<DirPicker bind:open={pickerOpen} on:select={onPickerSelect} />

<style>
  .page { padding: 2.5rem; max-width: 560px; }

  .crumb {
    display: flex; align-items: center; gap: 0.4rem;
    font-size: 0.75rem; color: var(--text-muted); margin-bottom: 1.25rem;
  }
  .crumb a { color: var(--text-muted); text-decoration: none; }
  .crumb a:hover { color: var(--text-secondary); }

  h1 { font-size: 1.25rem; font-weight: 700; color: var(--text-primary); letter-spacing: -0.02em; margin-bottom: 1.75rem; }

  .error-bar {
    background: rgba(248,113,113,0.1);
    border: 1px solid rgba(248,113,113,0.2);
    color: #fca5a5;
    padding: 0.6rem 0.9rem;
    border-radius: 8px;
    font-size: 0.8rem;
    margin-bottom: 1.25rem;
  }

  form { display: flex; flex-direction: column; gap: 0; }

  .section {
    padding: 1.25rem 0;
    border-bottom: 1px solid var(--border);
  }
  .section:last-of-type { border-bottom: none; }

  .section-label {
    font-size: 0.7rem;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: var(--text-muted);
    margin-bottom: 1rem;
  }
  .hint { font-size: 0.78rem; color: var(--text-muted); margin-top: -0.5rem; margin-bottom: 0.75rem; }

  .field { display: flex; flex-direction: column; gap: 0.35rem; margin-bottom: 0.75rem; }
  .field:last-child { margin-bottom: 0; }
  .field-row { display: grid; grid-template-columns: 1fr 1fr; gap: 0.75rem; }
  label { font-size: 0.78rem; font-weight: 500; color: var(--text-muted); }

  input:not([type="radio"]), select {
    background: var(--input-bg);
    border: 1px solid var(--border-strong);
    border-radius: 7px;
    padding: 0.5rem 0.75rem;
    font-size: 0.85rem;
    color: var(--text-primary);
    transition: border-color 0.15s, box-shadow 0.15s;
    width: 100%;
  }
  input:focus, select:focus {
    outline: none;
    border-color: var(--accent);
    box-shadow: 0 0 0 3px rgba(124,106,247,0.15);
  }
  select option { background: var(--bg-elevated); }
  ::placeholder { color: var(--text-muted); }

  .type-picker {
    display: grid;
    grid-template-columns: repeat(4, 1fr);
    gap: 0.5rem;
  }
  .type-opt {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 0.35rem;
    padding: 0.75rem 0.5rem;
    border: 1px solid var(--border);
    border-radius: 9px;
    cursor: pointer;
    font-size: 0.72rem;
    color: var(--text-muted);
    transition: border-color 0.12s, background 0.12s, color 0.12s;
  }
  .type-opt input { display: none; }
  .type-icon { font-size: 1.4rem; line-height: 1; }
  .type-opt:hover { border-color: rgba(255,255,255,0.14); color: var(--text-secondary); }
  .type-opt.selected {
    border-color: var(--accent);
    background: rgba(124,106,247,0.1);
    color: var(--accent-text);
  }

  .path-list { display: flex; flex-direction: column; gap: 0.4rem; margin-bottom: 0.6rem; }
  .path-row {
    display: flex;
    align-items: center;
    background: var(--input-bg);
    border: 1px solid var(--border-strong);
    border-radius: 7px;
    overflow: hidden;
    transition: border-color 0.15s;
  }
  .path-row:focus-within {
    border-color: var(--accent);
    box-shadow: 0 0 0 3px rgba(124,106,247,0.15);
  }
  .path-prefix {
    padding: 0 0.1rem 0 0.75rem;
    font-size: 0.85rem;
    color: var(--text-muted);
    font-family: monospace;
    user-select: none;
  }
  .path-input {
    flex: 1;
    background: none !important;
    border: none !important;
    box-shadow: none !important;
    padding: 0.5rem 0.5rem 0.5rem 0 !important;
    font-family: 'SF Mono', 'Consolas', monospace;
    font-size: 0.82rem;
  }
  .path-input:focus { outline: none; box-shadow: none !important; }
  .browse-btn {
    padding: 0 0.55rem;
    background: none;
    border-left: 1px solid var(--border);
    color: var(--text-muted);
    cursor: pointer;
    align-self: stretch;
    display: flex;
    align-items: center;
    transition: color 0.12s, background 0.12s;
    flex-shrink: 0;
  }
  .browse-btn:hover { background: rgba(124,106,247,0.08); color: var(--accent-text); }

  .path-remove {
    padding: 0 0.65rem;
    background: none;
    border: none;
    font-size: 1rem;
    color: var(--text-muted);
    cursor: pointer;
    transition: color 0.12s;
    line-height: 1;
    align-self: stretch;
    display: flex;
    align-items: center;
  }
  .path-remove:hover { color: #fca5a5; }

  .add-path {
    background: none;
    border: none;
    color: var(--text-muted);
    font-size: 0.78rem;
    cursor: pointer;
    padding: 0;
    transition: color 0.12s;
  }
  .add-path:hover { color: var(--accent-text); }

  .form-foot {
    display: flex;
    align-items: center;
    justify-content: flex-end;
    gap: 0.5rem;
    padding-top: 1.5rem;
  }
  .btn-ghost {
    padding: 0.45rem 0.9rem;
    background: none;
    border: 1px solid var(--border-strong);
    border-radius: 7px;
    color: var(--text-muted);
    font-size: 0.8rem;
    text-decoration: none;
    cursor: pointer;
    transition: border-color 0.12s, color 0.12s;
  }
  .btn-ghost:hover { border-color: var(--border-strong); color: var(--text-secondary); }
  .btn-submit {
    padding: 0.45rem 1rem;
    background: var(--accent);
    border: none;
    border-radius: 7px;
    color: #fff;
    font-size: 0.8rem;
    font-weight: 600;
    cursor: pointer;
    transition: background 0.15s;
  }
  .btn-submit:hover { background: var(--accent-hover); }
  .btn-submit:disabled { opacity: 0.5; cursor: not-allowed; }
</style>
