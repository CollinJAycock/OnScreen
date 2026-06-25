<script lang="ts">
  import { toast } from '$lib/stores/toast';
  import { getApiBase, getBearerToken } from '$lib/api';

  // Build the request shape api.ts would use for an admin endpoint:
  // bearer header for native, same-origin cookie for browser. The
  // backup flows can't go through `api.ts`'s wrapper because they
  // need direct Response access (binary Blob for download, raw form
  // upload for restore). authedFetch keeps the same auth contract
  // without the parsing layer.
  function authedFetch(path: string, init: RequestInit = {}): Promise<Response> {
    const token = getBearerToken();
    const base = getApiBase();
    const headers = new Headers(init.headers ?? {});
    if (token) headers.set('Authorization', `Bearer ${token}`);
    const sameOrigin = base.startsWith('/');
    return fetch(base + path, {
      ...init,
      headers,
      credentials: sameOrigin ? 'same-origin' : 'omit',
    });
  }

  type RestoreResult = {
    exit_error?: string;
    stderr?: string;
    dump_version?: number;
    server_version?: number;
    migrated?: boolean;
    migrate_error?: string;
    forced?: boolean;
  };

  let restoring = false;
  let downloading = false;
  let confirmText = '';
  let fileInput: HTMLInputElement;
  let pickedName = '';
  let lastResult: RestoreResult | null = null;
  let lastDownloadedVersion: number | null = null;
  // Set when the server refuses a restore because the dump is from a newer
  // build. Lets the operator click "Restore anyway" to retry with ?force=true.
  let pendingForce: { dump: number; server: number; message: string } | null = null;

  // Backup is fetched (not a plain <a download>) so a JSON error body
  // from the server surfaces as a toast instead of being silently saved
  // as a corrupt .dump file.
  async function download() {
    downloading = true;
    try {
      const resp = await authedFetch('/admin/backup');
      if (!resp.ok) {
        const json = await resp.json().catch(() => null);
        throw new Error(json?.error?.message ?? `HTTP ${resp.status}`);
      }
      const v = resp.headers.get('X-OnScreen-Schema-Version');
      lastDownloadedVersion = v ? Number(v) : null;
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
    pendingForce = null;
  }

  async function doRestore(force: boolean) {
    const f = fileInput?.files?.[0];
    if (!f) { toast.error('Pick a backup file first'); return; }
    if (confirmText !== 'RESTORE') { toast.error('Type RESTORE to confirm'); return; }
    restoring = true;
    lastResult = null;
    try {
      const fd = new FormData();
      fd.append('file', f);
      const path = '/admin/restore' + (force ? '?force=true' : '');
      const resp = await authedFetch(path, {
        method: 'POST',
        body: fd,
      });
      const json = await resp.json();
      if (resp.status === 409 && json?.error?.code === 'DUMP_NEWER_THAN_SERVER') {
        // Surface the override path instead of just toasting.
        const detail = json?.error?.message ?? 'Dump is from a newer server.';
        const m = detail.match(/v(\d+).*v(\d+)/);
        pendingForce = {
          dump: m ? Number(m[1]) : 0,
          server: m ? Number(m[2]) : 0,
          message: detail,
        };
        return;
      }
      if (!resp.ok) {
        throw new Error(json?.error?.message ?? `HTTP ${resp.status}`);
      }
      lastResult = json.data;
      pendingForce = null;
      if (lastResult?.exit_error) {
        toast.error('Restore finished with errors — review the output below');
      } else if (lastResult?.migrate_error) {
        toast.error('Restore succeeded but post-restore migrations failed — review below');
      } else if (lastResult?.migrated) {
        toast.success(`Restore complete; schema migrated forward to v${lastResult.server_version}.`);
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

  const restore = () => doRestore(false);
  const restoreForced = () => doRestore(true);
</script>

<div class="wrap">
  <section>
    <h2>Backup database</h2>
    <p class="hint">
      Downloads a Postgres custom-format dump of everything OnScreen tracks —
      users, libraries, items, watch history, credits, playlists. Media files
      and artwork live on disk and are not included; they are rebuilt from
      the scan sources. The filename includes the current schema version
      (<code>-vN.dump</code>) so future restores know whether the dump is
      compatible with the running build.
    </p>
    <button class="btn btn-primary" on:click={download} disabled={downloading}>
      {downloading ? 'Preparing…' : 'Download backup'}
    </button>
    {#if lastDownloadedVersion !== null}
      <div class="row picked">Last download captured schema <code>v{lastDownloadedVersion}</code></div>
    {/if}
  </section>

  <section>
    <h2>Restore database</h2>
    <p class="hint">
      Upload a backup produced by this page. The existing database is
      <strong>wiped</strong> and replaced. If the dump is from an older
      schema, the server runs migrations after the restore so it ends up
      matching this build. Dumps from a <em>newer</em> server are refused
      unless you explicitly override.
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

    {#if pendingForce}
      <div class="result">
        <div class="result-head">
          Schema mismatch — dump is <code>v{pendingForce.dump}</code>, server expects
          <code>v{pendingForce.server}</code>.
        </div>
        <p class="hint" style="margin: 0.4rem 0 0.6rem;">
          Restoring would leave the server unable to read columns it expects
          to find. Upgrade the server build, or override only if you know
          the schema gap is benign.
        </p>
        <button
          class="btn btn-danger"
          disabled={restoring}
          on:click={restoreForced}
        >
          Restore anyway (force)
        </button>
      </div>
    {/if}

    {#if lastResult}
      <div class="result" class:ok={!lastResult.exit_error && !lastResult.migrate_error}>
        <div class="result-head">
          {#if lastResult.exit_error}
            pg_restore exited with: <code>{lastResult.exit_error}</code>
          {:else}
            pg_restore completed cleanly.
          {/if}
        </div>
        {#if lastResult.dump_version || lastResult.server_version}
          <div class="result-head">
            Dump schema: <code>v{lastResult.dump_version ?? '?'}</code> →
            server schema: <code>v{lastResult.server_version ?? '?'}</code>
            {#if lastResult.migrated} (migrated forward){/if}
            {#if lastResult.forced} (forced){/if}
          </div>
        {/if}
        {#if lastResult.migrate_error}
          <div class="result-head">
            Post-restore migrations failed: <code>{lastResult.migrate_error}</code>
          </div>
        {/if}
        {#if lastResult.stderr}
          <pre>{lastResult.stderr}</pre>
        {/if}
      </div>
    {/if}
  </section>
</div>

<style>
  .wrap { display: flex; flex-direction: column; gap: 1.5rem; }
  section {
    background: var(--bg-elevated);
    border: 1px solid var(--border);
    border-radius: 10px;
    padding: 1.1rem 1.25rem;
  }
  h2 { font-size: 0.95rem; margin: 0 0 0.5rem; font-weight: 600; }
  .hint { color: var(--text-secondary); font-size: 0.82rem; line-height: 1.5; margin: 0 0 1rem; }
  .hint strong { color: var(--error); }

  .row { margin: 0.75rem 0; }
  .row label { display: flex; flex-direction: column; gap: 0.3rem; font-size: 0.82rem; color: var(--text-secondary); }
  .row input[type="text"] {
    padding: 0.48rem 0.7rem;
    border-radius: 7px;
    border: 1px solid var(--border-strong);
    background: var(--input-bg);
    color: var(--text-primary);
    font-family: inherit;
    max-width: 200px;
  }
  .row input[type="text"]:focus {
    outline: none;
    border-color: var(--accent);
    box-shadow: 0 0 0 3px var(--accent-bg);
  }
  .row input[type="text"]::placeholder { color: var(--text-muted); }
  .picked { font-size: 0.78rem; color: var(--text-muted); }
  .picked code { color: var(--text-secondary); }

  .btn {
    display: inline-block;
    padding: 0.45rem 0.9rem;
    border-radius: 7px;
    font-size: 0.8rem;
    font-weight: 600;
    border: 1px solid transparent;
    cursor: pointer;
    text-decoration: none;
    transition: background 0.15s, border-color 0.15s, color 0.15s;
  }
  .btn:disabled { opacity: 0.5; cursor: not-allowed; }
  .btn-primary {
    background: var(--accent);
    color: #fff;
  }
  .btn-primary:hover { background: var(--accent-hover); }
  .btn-danger {
    background: var(--error-bg);
    border: 1px solid var(--error);
    color: var(--error);
  }

  code {
    font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, monospace;
    font-size: 0.85em;
    background: var(--bg-hover);
    padding: 0.05rem 0.35rem;
    border-radius: 4px;
  }

  .result {
    margin-top: 1rem;
    padding: 0.75rem 1rem;
    border-radius: 7px;
    background: var(--error-bg);
    border: 1px solid var(--error);
    font-size: 0.78rem;
  }
  .result.ok {
    background: var(--success-bg);
    border-color: var(--success);
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
