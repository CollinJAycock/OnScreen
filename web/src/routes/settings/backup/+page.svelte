<script lang="ts">
  import { toast } from '$lib/stores/toast';

  let restoring = false;
  let downloading = false;
  let confirmText = '';
  let fileInput: HTMLInputElement;
  let pickedName = '';
  let lastResult: { exit_error?: string; stderr?: string } | null = null;

  // Backup is fetched (not a plain <a download>) so a JSON error body
  // from the server surfaces as a toast instead of being silently saved
  // as a corrupt .dump file.
  async function download() {
    downloading = true;
    try {
      const resp = await fetch('/api/v1/admin/backup', { credentials: 'same-origin' });
      if (!resp.ok) {
        const json = await resp.json().catch(() => null);
        throw new Error(json?.error?.message ?? `HTTP ${resp.status}`);
      }
      const blob = await resp.blob();
      const cd = resp.headers.get('Content-Disposition') ?? '';
      const m = cd.match(/filename="([^"]+)"/);
      const filename = m ? m[1] : 'onscreen-backup.dump';
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = filename;
      document.body.appendChild(a);
      a.click();
      a.remove();
      URL.revokeObjectURL(url);
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Backup failed');
    } finally {
      downloading = false;
    }
  }

  function onPick(e: Event) {
    const f = (e.target as HTMLInputElement).files?.[0];
    pickedName = f ? f.name : '';
    lastResult = null;
  }

  async function restore() {
    const f = fileInput?.files?.[0];
    if (!f) { toast.error('Pick a backup file first'); return; }
    if (confirmText !== 'RESTORE') { toast.error('Type RESTORE to confirm'); return; }
    restoring = true;
    lastResult = null;
    try {
      const fd = new FormData();
      fd.append('file', f);
      const resp = await fetch('/api/v1/admin/restore', {
        method: 'POST',
        body: fd,
        credentials: 'same-origin'
      });
      const json = await resp.json();
      if (!resp.ok) {
        throw new Error(json?.error?.message ?? `HTTP ${resp.status}`);
      }
      lastResult = json.data;
      if (lastResult?.exit_error) {
        toast.error('Restore finished with errors — review the output below');
      } else {
        toast.success('Restore complete. You may need to sign in again.');
      }
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Restore failed');
    } finally {
      restoring = false;
      confirmText = '';
    }
  }
</script>

<div class="wrap">
  <section>
    <h2>Backup database</h2>
    <p class="hint">
      Downloads a Postgres custom-format dump of everything OnScreen tracks —
      users, libraries, items, watch history, credits, playlists. Media files
      and artwork live on disk and are not included; they are rebuilt from
      the scan sources.
    </p>
    <button class="btn btn-primary" on:click={download} disabled={downloading}>
      {downloading ? 'Preparing…' : 'Download backup'}
    </button>
  </section>

  <section>
    <h2>Restore database</h2>
    <p class="hint">
      Upload a backup produced by this page. The existing database is
      <strong>wiped</strong> and replaced. If the backup predates the running
      server, re-run migrations after the restore.
    </p>

    <div class="row">
      <input
        bind:this={fileInput}
        type="file"
        accept=".dump,application/octet-stream"
        on:change={onPick}
        disabled={restoring}
      />
    </div>
    {#if pickedName}
      <div class="row picked">Selected: <code>{pickedName}</code></div>
    {/if}

    <div class="row">
      <label>
        Type <code>RESTORE</code> to confirm
        <input
          type="text"
          bind:value={confirmText}
          placeholder="RESTORE"
          disabled={restoring}
        />
      </label>
    </div>

    <button
      class="btn btn-danger"
      disabled={restoring || !pickedName || confirmText !== 'RESTORE'}
      on:click={restore}
    >
      {restoring ? 'Restoring…' : 'Restore from backup'}
    </button>

    {#if lastResult}
      <div class="result" class:ok={!lastResult.exit_error}>
        {#if lastResult.exit_error}
          <div class="result-head">pg_restore exited with: <code>{lastResult.exit_error}</code></div>
        {:else}
          <div class="result-head">pg_restore completed cleanly.</div>
        {/if}
        {#if lastResult.stderr}
          <pre>{lastResult.stderr}</pre>
        {/if}
      </div>
    {/if}
  </section>
</div>

<style>
  .wrap { display: flex; flex-direction: column; gap: 2rem; }
  section {
    background: var(--surface);
    border: 1px solid rgba(255,255,255,0.05);
    border-radius: 8px;
    padding: 1.25rem 1.5rem;
  }
  h2 { font-size: 0.95rem; margin: 0 0 0.5rem; font-weight: 600; }
  .hint { color: var(--text-secondary); font-size: 0.82rem; line-height: 1.5; margin: 0 0 1rem; }
  .hint strong { color: var(--error); }

  .row { margin: 0.75rem 0; }
  .row label { display: flex; flex-direction: column; gap: 0.3rem; font-size: 0.82rem; color: var(--text-secondary); }
  .row input[type="text"] {
    padding: 0.45rem 0.6rem;
    border-radius: 4px;
    border: 1px solid rgba(255,255,255,0.1);
    background: var(--bg);
    color: var(--text-primary);
    font-family: inherit;
    max-width: 200px;
  }
  .picked { font-size: 0.78rem; color: var(--text-muted); }
  .picked code { color: var(--text-secondary); }

  .btn {
    display: inline-block;
    padding: 0.55rem 1.1rem;
    border-radius: 4px;
    font-size: 0.82rem;
    font-weight: 500;
    border: 1px solid transparent;
    cursor: pointer;
    text-decoration: none;
    transition: background 0.12s;
  }
  .btn-primary {
    background: var(--accent);
    color: var(--accent-text);
  }
  .btn-primary:hover { filter: brightness(1.1); }
  .btn-danger {
    background: var(--error, #c03a3a);
    color: white;
  }
  .btn-danger:disabled {
    background: rgba(255,255,255,0.05);
    color: var(--text-muted);
    cursor: not-allowed;
  }
  .btn-danger:not(:disabled):hover { filter: brightness(1.1); }

  code {
    font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, monospace;
    font-size: 0.85em;
    background: rgba(255,255,255,0.05);
    padding: 0.05rem 0.35rem;
    border-radius: 3px;
  }

  .result {
    margin-top: 1rem;
    padding: 0.75rem 1rem;
    border-radius: 4px;
    background: rgba(192,58,58,0.08);
    border: 1px solid rgba(192,58,58,0.25);
    font-size: 0.78rem;
  }
  .result.ok {
    background: rgba(56,161,105,0.08);
    border-color: rgba(56,161,105,0.25);
  }
  .result-head { color: var(--text-secondary); margin-bottom: 0.4rem; }
  .result pre {
    margin: 0;
    max-height: 16rem;
    overflow: auto;
    white-space: pre-wrap;
    font-size: 0.72rem;
    color: var(--text-muted);
  }
</style>
