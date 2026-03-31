<script lang="ts">
  import { createEventDispatcher } from 'svelte';
  import { fsApi, type BrowseResult } from './api';

  export let open = false;

  const dispatch = createEventDispatcher<{ select: string; close: void }>();

  let current: BrowseResult = { path: '/', parent: '', dirs: [] };
  let loading = false;
  let error = '';

  $: if (open) navigate('/');

  async function navigate(path: string) {
    if (!path) return;
    loading = true;
    error = '';
    try {
      current = await fsApi.browse(path);
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Cannot read directory';
    } finally {
      loading = false;
    }
  }

  function select() {
    dispatch('select', current.path);
    open = false;
  }

  function close() {
    dispatch('close');
    open = false;
  }

  // Detect whether a path uses Windows-style backslash separators.
  function isWindowsPath(p: string) {
    return /^[A-Za-z]:[\\\/]/.test(p) || p === '\\' || p.includes('\\');
  }

  function segments(path: string) {
    if (path === '/' || path === '\\') return [{ label: '/', full: '/' }];
    if (isWindowsPath(path)) {
      // Windows: split on backslash (and handle trailing \)
      const parts = path.replace(/\\/g, '/').split('/').filter(Boolean);
      // parts[0] is e.g. "C:" — join with \ to form "C:\"
      const segs: { label: string; full: string }[] = [{ label: '/', full: '/' }];
      let acc = '';
      for (let i = 0; i < parts.length; i++) {
        acc = i === 0 ? parts[0] + '\\' : acc + parts[i] + '\\';
        segs.push({ label: parts[i], full: acc });
      }
      return segs;
    }
    const parts = path.split('/').filter(Boolean);
    return [
      { label: '/', full: '/' },
      ...parts.map((p, i) => ({ label: p, full: '/' + parts.slice(0, i + 1).join('/') }))
    ];
  }

  function basename(p: string) {
    return p.replace(/[/\\]+$/, '').split(/[/\\]/).filter(Boolean).at(-1) ?? p;
  }
</script>

{#if open}
  <!-- svelte-ignore a11y-click-events-have-key-events -->
  <div class="overlay" role="presentation" on:click={close}>
    <div class="picker" role="dialog" aria-label="Select directory" tabindex="-1" on:click|stopPropagation>
      <div class="picker-head">
        <span class="picker-title">Select a folder</span>
        <button class="close-btn" aria-label="Close" on:click={close}>
          <svg viewBox="0 0 16 16" fill="currentColor" width="14" height="14">
            <path d="M3.72 3.72a.75.75 0 011.06 0L8 6.94l3.22-3.22a.749.749 0 111.06 1.06L9.06 8l3.22 3.22a.749.749 0 11-1.06 1.06L8 9.06l-3.22 3.22a.749.749 0 01-1.06-1.06L6.94 8 3.72 4.78a.75.75 0 010-1.06z"/>
          </svg>
        </button>
      </div>

      <div class="breadcrumb">
        {#each segments(current.path) as seg, i}
          {#if i > 0}<span class="bc-sep">/</span>{/if}
          <button class="bc-seg" on:click={() => navigate(seg.full)}>{seg.label}</button>
        {/each}
      </div>

      <div class="dir-list">
        {#if current.parent}
          <button class="dir-item dir-up" on:click={() => navigate(current.parent)}>
            <svg viewBox="0 0 16 16" fill="currentColor" width="14" height="14">
              <path d="M7.78 12.53a.75.75 0 01-1.06 0L2.47 8.28a.75.75 0 010-1.06l4.25-4.25a.75.75 0 011.06 1.06L4.81 7h7.44a.75.75 0 010 1.5H4.81l2.97 2.97a.75.75 0 010 1.06z"/>
            </svg>
            <span>Go up</span>
          </button>
        {/if}

        {#if loading}
          <div class="loading-state">
            <span class="spin">⟳</span> Loading…
          </div>
        {:else if error}
          <div class="error-state">{error}</div>
        {:else if current.dirs.length === 0}
          <div class="empty-state">No subdirectories</div>
        {:else}
          {#each current.dirs as dir}
            <button class="dir-item" on:click={() => navigate(dir)}>
              <svg viewBox="0 0 16 16" fill="currentColor" width="14" height="14" class="folder-ico">
                <path d="M1.75 1A1.75 1.75 0 000 2.75v10.5C0 14.216.784 15 1.75 15h12.5A1.75 1.75 0 0016 13.25v-8.5A1.75 1.75 0 0014.25 3H7.5a.25.25 0 01-.2-.1l-.9-1.2C6.07 1.26 5.55 1 5 1H1.75z"/>
              </svg>
              <span>{basename(dir)}</span>
              <svg viewBox="0 0 16 16" fill="currentColor" width="12" height="12" class="chevron">
                <path d="M6.22 3.22a.75.75 0 011.06 0l4.25 4.25a.75.75 0 010 1.06l-4.25 4.25a.75.75 0 01-1.06-1.06L9.94 8 6.22 4.28a.75.75 0 010-1.06z"/>
              </svg>
            </button>
          {/each}
        {/if}
      </div>

      <div class="picker-foot">
        <div class="selected-path">{current.path}</div>
        <div class="foot-actions">
          <button class="btn-cancel" on:click={close}>Cancel</button>
          <button class="btn-select" on:click={select}>Use this folder</button>
        </div>
      </div>
    </div>
  </div>
{/if}

<style>
  .overlay {
    position: fixed; inset: 0;
    background: rgba(0,0,0,0.75);
    backdrop-filter: blur(6px);
    display: flex; align-items: center; justify-content: center;
    z-index: 200; padding: 1rem;
  }

  .picker {
    background: var(--bg-secondary);
    border: 1px solid var(--border-strong);
    border-radius: 12px;
    width: 480px;
    max-width: 100%;
    max-height: 80vh;
    display: flex;
    flex-direction: column;
    box-shadow: 0 24px 60px rgba(0,0,0,0.7);
    overflow: hidden;
  }

  .picker-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0.9rem 1rem 0.75rem;
    border-bottom: 1px solid var(--border);
  }
  .picker-title { font-size: 0.82rem; font-weight: 700; color: var(--text-primary); }
  .close-btn {
    width: 24px; height: 24px;
    display: flex; align-items: center; justify-content: center;
    background: none; border: none; border-radius: 5px;
    color: var(--text-muted); cursor: pointer; transition: background 0.12s, color 0.12s;
  }
  .close-btn:hover { background: var(--border); color: var(--text-secondary); }

  .breadcrumb {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 0;
    padding: 0.55rem 1rem;
    background: rgba(255,255,255,0.02);
    border-bottom: 1px solid var(--border);
    min-height: 36px;
  }
  .bc-seg {
    background: none; border: none;
    font-size: 0.75rem; font-family: 'SF Mono', 'Consolas', monospace;
    color: var(--accent); cursor: pointer; padding: 0.1rem 0.2rem;
    border-radius: 4px; transition: background 0.1s;
  }
  .bc-seg:hover { background: rgba(124,106,247,0.1); }
  .bc-sep { font-size: 0.72rem; color: var(--text-muted); padding: 0 0.05rem; }

  .dir-list {
    flex: 1;
    overflow-y: auto;
    padding: 0.35rem 0;
    min-height: 180px;
    max-height: 320px;
  }
  .dir-list::-webkit-scrollbar { width: 4px; }
  .dir-list::-webkit-scrollbar-track { background: transparent; }
  .dir-list::-webkit-scrollbar-thumb { background: var(--border); border-radius: 2px; }

  .dir-item {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    width: 100%;
    padding: 0.5rem 1rem;
    background: none;
    border: none;
    text-align: left;
    font-size: 0.82rem;
    color: var(--text-secondary);
    cursor: pointer;
    transition: background 0.1s, color 0.1s;
  }
  .dir-item:hover { background: var(--input-bg); color: var(--text-primary); }
  .dir-item span { flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .folder-ico { color: var(--text-muted); flex-shrink: 0; }
  .dir-item:hover .folder-ico { color: var(--accent); }
  .chevron { color: var(--text-muted); flex-shrink: 0; }
  .dir-item:hover .chevron { color: var(--text-muted); }

  .dir-up { color: var(--text-muted); }
  .dir-up:hover { color: var(--text-secondary); }

  .loading-state, .error-state, .empty-state {
    display: flex; align-items: center; justify-content: center;
    padding: 2.5rem; font-size: 0.8rem; color: var(--text-muted);
  }
  .error-state { color: var(--error); }
  .spin { display: inline-block; animation: spin 0.7s linear infinite; margin-right: 0.4rem; }
  @keyframes spin { to { transform: rotate(360deg); } }

  .picker-foot {
    padding: 0.75rem 1rem;
    border-top: 1px solid var(--border);
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.75rem;
    background: rgba(255,255,255,0.02);
  }
  .selected-path {
    font-size: 0.72rem;
    font-family: 'SF Mono', 'Consolas', monospace;
    color: var(--text-muted);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    flex: 1;
    min-width: 0;
  }
  .foot-actions { display: flex; gap: 0.4rem; flex-shrink: 0; }
  .btn-cancel {
    padding: 0.38rem 0.8rem;
    background: none;
    border: 1px solid var(--border-strong);
    border-radius: 7px;
    color: var(--text-muted); font-size: 0.78rem;
    cursor: pointer; transition: all 0.12s;
  }
  .btn-cancel:hover { border-color: var(--border-strong); color: var(--text-secondary); }
  .btn-select {
    padding: 0.38rem 0.85rem;
    background: var(--accent);
    border: none;
    border-radius: 7px;
    color: #fff; font-size: 0.78rem; font-weight: 600;
    cursor: pointer; transition: background 0.15s;
  }
  .btn-select:hover { background: var(--accent-hover); }
</style>
