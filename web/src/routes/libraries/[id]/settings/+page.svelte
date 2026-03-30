<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { libraryApi, type Library } from '$lib/api';
  import DirPicker from '$lib/DirPicker.svelte';

  let pickerOpen = false;
  let activePathIndex = 0;
  function openPicker(i: number) { activePathIndex = i; pickerOpen = true; }
  function onPickerSelect(e: CustomEvent<string>) { updatePath(activePathIndex, e.detail); }

  let library: Library | null = null;
  let loading = true;
  let saving = false;
  let deleting = false;
  let error = '';
  let saved = false;
  let showDelete = false;

  let name = '';
  let paths: string[] = [''];
  let agent = 'tmdb';
  let language = 'en';
  let scanIntervalMinutes = 60;

  let mounted = false;
  let prevId = '';
  let savedTimeout: ReturnType<typeof setTimeout>;

  $: id = $page.params.id;

  onMount(async () => {
    if (!localStorage.getItem('onscreen_user')) { goto('/login'); return; }
    prevId = id;
    await fetchLibrary();
    mounted = true;
  });

  onDestroy(() => clearTimeout(savedTimeout));

  $: if (mounted && id && id !== prevId) {
    prevId = id;
    library = null;
    loading = true;
    error = '';
    saved = false;
    name = '';
    paths = [''];
    agent = 'tmdb';
    language = 'en';
    scanIntervalMinutes = 60;
    fetchLibrary();
  }

  async function fetchLibrary() {
    try {
      library = await libraryApi.get(id);
      name = library.name;
      paths = library.scan_paths?.length ? [...library.scan_paths] : [''];
      agent = library.agent;
      language = library.language;
      scanIntervalMinutes = library.scan_interval_minutes ?? 60;
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed to load';
    } finally { loading = false; }
  }

  function addPath() { paths = [...paths, '']; }
  function removePath(i: number) { paths = paths.filter((_, j) => j !== i); }
  function updatePath(i: number, val: string) { paths = paths.map((p, j) => j === i ? val : p); }

  async function save() {
    error = ''; saved = false;
    if (!name.trim()) { error = 'Name is required'; return; }
    const scanPaths = paths.map(p => p.trim()).filter(Boolean);
    if (!scanPaths.length) { error = 'At least one scan path is required'; return; }
    saving = true;
    try {
      library = await libraryApi.update(id, { name: name.trim(), scan_paths: scanPaths, agent, language, scan_interval_minutes: scanIntervalMinutes });
      saved = true;
      savedTimeout = setTimeout(() => saved = false, 3000);
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed to save';
    } finally { saving = false; }
  }

  async function del() {
    deleting = true;
    try { await libraryApi.del(id); goto('/'); }
    catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Delete failed';
      deleting = false; showDelete = false;
    }
  }
</script>

<svelte:head><title>{library?.name ?? 'Library'} Settings — OnScreen</title></svelte:head>

<div class="page">
  <nav class="crumb">
    <a href="/">Libraries</a>
    <span>/</span>
    <a href="/libraries/{id}">{library?.name ?? '…'}</a>
    <span>/</span>
    <span>Settings</span>
  </nav>

  <h1>Settings</h1>

  {#if error}
    <div class="banner error">{error}</div>
  {/if}
  {#if saved}
    <div class="banner ok">Changes saved</div>
  {/if}

  {#if loading}
    <div class="skeleton-block"></div>
  {:else}
    <form on:submit|preventDefault={save}>
      <section>
        <div class="sec-label">Identity</div>
        <div class="field">
          <label for="name">Name</label>
          <input id="name" bind:value={name} autocomplete="off" />
        </div>
        <div class="field">
          <label>Type</label>
          <div class="readonly">{library?.type}</div>
        </div>
      </section>

      <section>
        <div class="sec-label">Scan Paths</div>
        <div class="path-list">
          {#each paths as path, i}
            <div class="path-row">
              <span class="pfx">/</span>
              <input
                class="path-in"
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
                <button type="button" class="path-rm" on:click={() => removePath(i)}>×</button>
              {/if}
            </div>
          {/each}
        </div>
        <button type="button" class="add-path" on:click={addPath}>+ Add path</button>
      </section>

      <section>
        <div class="sec-label">Metadata</div>
        <div class="row2">
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

      <section>
        <div class="sec-label">Scanning</div>
        <div class="field">
          <label for="scan-interval">Scan interval</label>
          <select id="scan-interval" bind:value={scanIntervalMinutes}>
            <option value={5}>Every 5 minutes</option>
            <option value={15}>Every 15 minutes</option>
            <option value={30}>Every 30 minutes</option>
            <option value={60}>Every hour</option>
            <option value={360}>Every 6 hours</option>
            <option value={720}>Every 12 hours</option>
            <option value={1440}>Every day</option>
          </select>
        </div>
      </section>

      <div class="form-foot">
        <a href="/libraries/{id}" class="btn-ghost">Cancel</a>
        <button type="submit" class="btn-save" disabled={saving}>
          {saving ? 'Saving…' : 'Save Changes'}
        </button>
      </div>
    </form>

    <!-- Danger -->
    <div class="danger-zone">
      <div class="dz-label">Danger Zone</div>
      <div class="dz-row">
        <div>
          <div class="dz-title">Delete library</div>
          <div class="dz-sub">Removes all metadata. Files on disk are not affected.</div>
        </div>
        <button class="btn-del-outline" on:click={() => showDelete = true}>Delete</button>
      </div>
    </div>
  {/if}
</div>

<DirPicker bind:open={pickerOpen} on:select={onPickerSelect} />

{#if showDelete}
  <div class="overlay" role="presentation" on:click={() => showDelete = false}>
    <div class="dialog" role="dialog" on:click|stopPropagation>
      <p class="dlg-title">Delete "{library?.name}"?</p>
      <p class="dlg-body">All metadata for this library will be permanently removed. Files on disk will not be deleted. This cannot be undone.</p>
      <div class="dlg-foot">
        <button class="btn-ghost" on:click={() => showDelete = false}>Cancel</button>
        <button class="btn-del-solid" disabled={deleting} on:click={del}>
          {deleting ? 'Deleting…' : 'Delete Library'}
        </button>
      </div>
    </div>
  </div>
{/if}

<style>
  .page { padding: 2.5rem; max-width: 560px; }

  .crumb {
    display: flex; align-items: center; gap: 0.4rem;
    font-size: 0.75rem; color: #33333d; margin-bottom: 1.25rem;
  }
  .crumb a { color: #55556a; text-decoration: none; }
  .crumb a:hover { color: #aaaacc; }

  h1 { font-size: 1.25rem; font-weight: 700; color: #eeeef8; letter-spacing: -0.02em; margin-bottom: 1.75rem; }

  .banner {
    padding: 0.6rem 0.9rem;
    border-radius: 8px;
    font-size: 0.8rem;
    margin-bottom: 1.25rem;
  }
  .banner.error { background: rgba(248,113,113,0.1); border: 1px solid rgba(248,113,113,0.2); color: #fca5a5; }
  .banner.ok { background: rgba(52,211,153,0.1); border: 1px solid rgba(52,211,153,0.2); color: #6ee7b7; }

  .skeleton-block {
    height: 380px; border-radius: 10px;
    background: linear-gradient(90deg, #111118 25%, #16161f 50%, #111118 75%);
    background-size: 200% 100%;
    animation: shimmer 1.4s infinite;
  }
  @keyframes shimmer { 0% { background-position: 200% 0; } 100% { background-position: -200% 0; } }

  form { display: flex; flex-direction: column; }

  section {
    padding: 1.25rem 0;
    border-bottom: 1px solid rgba(255,255,255,0.055);
  }

  .sec-label {
    font-size: 0.68rem; font-weight: 700;
    text-transform: uppercase; letter-spacing: 0.09em;
    color: #33333d; margin-bottom: 1rem;
  }

  .field { display: flex; flex-direction: column; gap: 0.3rem; margin-bottom: 0.65rem; }
  .field:last-child { margin-bottom: 0; }
  .row2 { display: grid; grid-template-columns: 1fr 1fr; gap: 0.75rem; }
  label { font-size: 0.75rem; font-weight: 500; color: #44445a; }

  input, select {
    background: rgba(255,255,255,0.04);
    border: 1px solid rgba(255,255,255,0.09);
    border-radius: 7px;
    padding: 0.48rem 0.7rem;
    font-size: 0.85rem;
    color: #eeeef8;
    transition: border-color 0.15s;
    width: 100%;
  }
  input:focus, select:focus { outline: none; border-color: #7c6af7; box-shadow: 0 0 0 3px rgba(124,106,247,0.12); }
  select option { background: #13131e; }
  ::placeholder { color: #2a2a3d; }

  .readonly {
    padding: 0.48rem 0.7rem;
    border: 1px solid rgba(255,255,255,0.055);
    border-radius: 7px;
    font-size: 0.85rem;
    color: #33333d;
    text-transform: capitalize;
  }

  .path-list { display: flex; flex-direction: column; gap: 0.4rem; margin-bottom: 0.6rem; }
  .path-row {
    display: flex; align-items: center;
    background: rgba(255,255,255,0.04);
    border: 1px solid rgba(255,255,255,0.09);
    border-radius: 7px; overflow: hidden;
    transition: border-color 0.15s;
  }
  .path-row:focus-within { border-color: #7c6af7; box-shadow: 0 0 0 3px rgba(124,106,247,0.12); }
  .pfx { padding: 0 0.1rem 0 0.7rem; font-size: 0.82rem; color: #33333d; font-family: monospace; }
  .path-in {
    flex: 1; background: none !important; border: none !important;
    box-shadow: none !important; padding: 0.48rem 0.4rem 0.48rem 0 !important;
    font-family: monospace; font-size: 0.8rem;
  }
  .path-in:focus { outline: none; box-shadow: none !important; }
  .browse-btn {
    padding: 0 0.55rem;
    background: none;
    border-left: 1px solid rgba(255,255,255,0.07);
    color: #44445a;
    cursor: pointer;
    align-self: stretch;
    display: flex;
    align-items: center;
    transition: color 0.12s, background 0.12s;
    flex-shrink: 0;
  }
  .browse-btn:hover { background: rgba(124,106,247,0.08); color: #a89ffa; }

  .path-rm {
    padding: 0 0.6rem; background: none; border: none;
    font-size: 1rem; color: #2e2e3d; cursor: pointer; align-self: stretch;
    display: flex; align-items: center; transition: color 0.12s;
  }
  .path-rm:hover { color: #fca5a5; }
  .add-path {
    background: none; border: none; color: #44445a;
    font-size: 0.75rem; cursor: pointer; padding: 0;
    transition: color 0.12s;
  }
  .add-path:hover { color: #a89ffa; }

  .form-foot {
    display: flex; justify-content: flex-end; align-items: center;
    gap: 0.5rem; padding-top: 1.5rem;
  }
  .btn-ghost {
    padding: 0.42rem 0.85rem; background: none;
    border: 1px solid rgba(255,255,255,0.09);
    border-radius: 7px; color: #55556a; font-size: 0.8rem;
    text-decoration: none; cursor: pointer; transition: all 0.12s;
  }
  .btn-ghost:hover { border-color: rgba(255,255,255,0.18); color: #aaaacc; }
  .btn-save {
    padding: 0.42rem 0.9rem; background: #7c6af7;
    border: none; border-radius: 7px; color: #fff;
    font-size: 0.8rem; font-weight: 600; cursor: pointer; transition: background 0.15s;
  }
  .btn-save:hover { background: #8f7ef9; }
  .btn-save:disabled { opacity: 0.5; cursor: not-allowed; }

  /* Danger zone */
  .danger-zone {
    margin-top: 2.5rem;
    border: 1px solid rgba(248,113,113,0.15);
    border-radius: 10px;
    overflow: hidden;
  }
  .dz-label {
    padding: 0.6rem 1rem;
    background: rgba(248,113,113,0.06);
    border-bottom: 1px solid rgba(248,113,113,0.12);
    font-size: 0.68rem;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: rgba(248,113,113,0.5);
  }
  .dz-row {
    display: flex; align-items: center; justify-content: space-between;
    gap: 1.5rem; padding: 1rem;
  }
  .dz-title { font-size: 0.82rem; font-weight: 600; color: #6a2222; margin-bottom: 0.2rem; }
  .dz-sub { font-size: 0.75rem; color: #3a2020; }
  .btn-del-outline {
    flex-shrink: 0; padding: 0.42rem 0.9rem;
    background: none; border: 1px solid rgba(248,113,113,0.25);
    border-radius: 7px; color: rgba(248,113,113,0.6);
    font-size: 0.78rem; font-weight: 600; cursor: pointer; transition: all 0.12s;
    white-space: nowrap;
  }
  .btn-del-outline:hover { background: rgba(248,113,113,0.08); color: #fca5a5; border-color: rgba(248,113,113,0.4); }

  /* Modal */
  .overlay {
    position: fixed; inset: 0; background: rgba(0,0,0,0.75);
    backdrop-filter: blur(6px);
    display: flex; align-items: center; justify-content: center;
    z-index: 100; padding: 1rem;
  }
  .dialog {
    background: #13131e; border: 1px solid rgba(255,255,255,0.09);
    border-radius: 12px; padding: 1.5rem; max-width: 380px; width: 100%;
    box-shadow: 0 24px 48px rgba(0,0,0,0.6);
  }
  .dlg-title { font-size: 0.9rem; font-weight: 700; color: #eeeef8; margin-bottom: 0.5rem; }
  .dlg-body { font-size: 0.8rem; color: #55556a; line-height: 1.55; margin-bottom: 1.25rem; }
  .dlg-foot { display: flex; justify-content: flex-end; gap: 0.5rem; }
  .btn-del-solid {
    padding: 0.42rem 0.9rem; background: rgba(248,113,113,0.15);
    border: 1px solid rgba(248,113,113,0.3);
    border-radius: 7px; color: #fca5a5;
    font-size: 0.8rem; font-weight: 600; cursor: pointer; transition: background 0.12s;
  }
  .btn-del-solid:hover { background: rgba(248,113,113,0.25); }
  .btn-del-solid:disabled { opacity: 0.45; cursor: not-allowed; }
</style>
