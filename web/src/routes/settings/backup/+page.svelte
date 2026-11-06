<script lang="ts">
  import { toast } from '$lib/stores/toast';
  import { t, tr } from '$lib/i18n/index.svelte';
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
  // Step-up re-authentication for restore: the server requires the admin's
  // password (and TOTP code, if 2FA is enabled) to authorize a database wipe.
  let restorePassword = '';
  let restoreTotp = '';

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
      toast.error(e instanceof Error ? e.message : t('settings.backup.backupFailed'));
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
    if (!f) { toast.error(t('settings.backup.pickFirst')); return; }
    if (confirmText !== 'RESTORE') { toast.error(t('settings.backup.typeToConfirm', { word: 'RESTORE' })); return; }
    // Password is verified server-side (step-up re-auth). It is not hard-required
    // here because a federated (SSO/LDAP) admin has no local password — the
    // server allows those through the admin + CSRF gate. A password admin who
    // leaves it blank is refused by the server with a clear error.
    restoring = true;
    lastResult = null;
    try {
      const fd = new FormData();
      fd.append('file', f);
      // Step-up re-auth — the server verifies these before wiping the database.
      fd.append('password', restorePassword);
      if (restoreTotp) fd.append('totp_code', restoreTotp);
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
        toast.error(t('settings.backup.finishedWithErrors'));
      } else if (lastResult?.migrate_error) {
        toast.error(t('settings.backup.migrationsFailedToast'));
      } else if (lastResult?.migrated) {
        toast.success(t('settings.backup.completeMigrated', { version: String(lastResult.server_version) }));
      } else {
        toast.success(t('settings.backup.complete'));
      }
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : t('settings.backup.restoreFailed'));
    } finally {
      restoring = false;
      // Preserve the typed confirmation and credentials while a force-retry is
      // pending (409 dump-newer) so "Restore anyway" can reuse them; clear only
      // on a terminal outcome.
      if (!pendingForce) {
        confirmText = '';
        restorePassword = '';
        restoreTotp = '';
      }
    }
  }

  const restore = () => doRestore(false);
  const restoreForced = () => doRestore(true);
</script>

<svelte:head><title>{$tr('settings.backup.pageTitle')}</title></svelte:head>

<div class="wrap">
  <section>
    <h2>{$tr('settings.backup.backupTitle')}</h2>
    <p class="hint">
      {#each $tr.rich('settings.backup.backupHint', { suffix: '-vN.dump' }) as seg}{#if seg.param === 'suffix'}<code>{seg.text}</code>{:else}{seg.text}{/if}{/each}
    </p>
    <button class="btn btn-primary" on:click={download} disabled={downloading}>
      {downloading ? $tr('settings.backup.preparing') : $tr('settings.backup.download')}
    </button>
    {#if lastDownloadedVersion !== null}
      <div class="row picked">{#each $tr.rich('settings.backup.lastDownload', { version: `v${lastDownloadedVersion}` }) as seg}{#if seg.param === 'version'}<code>{seg.text}</code>{:else}{seg.text}{/if}{/each}</div>
    {/if}
  </section>

  <section>
    <h2>{$tr('settings.backup.restoreTitle')}</h2>
    <p class="hint">
      {#each $tr.rich('settings.backup.restoreHint', { wiped: $tr('settings.backup.wiped'), newer: $tr('settings.backup.newer') }) as seg}{#if seg.param === 'wiped'}<strong>{seg.text}</strong>{:else if seg.param === 'newer'}<em>{seg.text}</em>{:else}{seg.text}{/if}{/each}
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
      <div class="row picked">{#each $tr.rich('settings.backup.selected', { name: pickedName }) as seg}{#if seg.param === 'name'}<code>{seg.text}</code>{:else}{seg.text}{/if}{/each}</div>
    {/if}

    <div class="row">
      <label>
        {#each $tr.rich('settings.backup.typeToConfirm', { word: 'RESTORE' }) as seg}{#if seg.param === 'word'}<code>{seg.text}</code>{:else}{seg.text}{/if}{/each}
        <input
          type="text"
          bind:value={confirmText}
          placeholder="RESTORE"
          disabled={restoring}
        />
      </label>
    </div>

    <div class="row">
      <label>
        {$tr('settings.backup.confirmPassword')} <span class="muted">{$tr('settings.backup.passwordSso')}</span>
        <input
          type="password"
          bind:value={restorePassword}
          placeholder={$tr('settings.backup.passwordPlaceholder')}
          autocomplete="current-password"
          disabled={restoring}
        />
      </label>
    </div>

    <div class="row">
      <label>
        {$tr('settings.backup.totp')} <span class="muted">{$tr('settings.backup.totpOnlyIf')}</span>
        <input
          type="text"
          bind:value={restoreTotp}
          placeholder="123456"
          inputmode="numeric"
          autocomplete="one-time-code"
          disabled={restoring}
        />
      </label>
    </div>

    <button
      class="btn btn-danger"
      disabled={restoring || !pickedName || confirmText !== 'RESTORE'}
      on:click={restore}
    >
      {restoring ? $tr('settings.backup.restoring') : $tr('settings.backup.restore')}
    </button>

    {#if pendingForce}
      <div class="result">
        <div class="result-head">
          {#each $tr.rich('settings.backup.mismatch', { dump: `v${pendingForce.dump}`, server: `v${pendingForce.server}` }) as seg}{#if seg.param === 'dump'}<code>{seg.text}</code>{:else if seg.param === 'server'}<code>{seg.text}</code>{:else}{seg.text}{/if}{/each}
        </div>
        <p class="hint" style="margin: 0.4rem 0 0.6rem;">
          {$tr('settings.backup.mismatchHint')}
        </p>
        <button
          class="btn btn-danger"
          disabled={restoring}
          on:click={restoreForced}
        >
          {$tr('settings.backup.restoreAnyway')}
        </button>
      </div>
    {/if}

    {#if lastResult}
      <div class="result" class:ok={!lastResult.exit_error && !lastResult.migrate_error}>
        <div class="result-head">
          {#if lastResult.exit_error}
            {#each $tr.rich('settings.backup.exitedWith', { tool: 'pg_restore', error: lastResult.exit_error }) as seg}{#if seg.param === 'error'}<code>{seg.text}</code>{:else}{seg.text}{/if}{/each}
          {:else}
            {$tr('settings.backup.completedCleanly', { tool: 'pg_restore' })}
          {/if}
        </div>
        {#if lastResult.dump_version || lastResult.server_version}
          <div class="result-head">
            {#each $tr.rich('settings.backup.schemas', { dump: `v${lastResult.dump_version ?? '?'}`, server: `v${lastResult.server_version ?? '?'}` }) as seg}{#if seg.param === 'dump'}<code>{seg.text}</code>{:else if seg.param === 'server'}<code>{seg.text}</code>{:else}{seg.text}{/if}{/each}
            {#if lastResult.migrated} {$tr('settings.backup.migratedForward')}{/if}
            {#if lastResult.forced} {$tr('settings.backup.forced')}{/if}
          </div>
        {/if}
        {#if lastResult.migrate_error}
          <div class="result-head">
            {#each $tr.rich('settings.backup.migrationsFailed', { error: lastResult.migrate_error }) as seg}{#if seg.param === 'error'}<code>{seg.text}</code>{:else}{seg.text}{/if}{/each}
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
  .muted { color: var(--text-muted); font-weight: 400; }

  .row { margin: 0.75rem 0; }
  .row label { display: flex; flex-direction: column; gap: 0.3rem; font-size: 0.82rem; color: var(--text-secondary); }
  .row input[type="text"],
  .row input[type="password"] {
    padding: 0.48rem 0.7rem;
    border-radius: 7px;
    border: 1px solid var(--border-strong);
    background: var(--input-bg);
    color: var(--text-primary);
    font-family: inherit;
    max-width: 200px;
  }
  .row input[type="text"]:focus,
  .row input[type="password"]:focus {
    outline: none;
    border-color: var(--accent);
    box-shadow: 0 0 0 3px var(--accent-bg);
  }
  .row input[type="text"]::placeholder,
  .row input[type="password"]::placeholder { color: var(--text-muted); }
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
