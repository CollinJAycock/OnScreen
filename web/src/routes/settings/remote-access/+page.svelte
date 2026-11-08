<script lang="ts">
  // Settings ▸ General ▸ Remote access: "Map a port on my router
  // automatically" (UPnP IGD / PCP / NAT-PMP) and its live status. Off by
  // default — a mapping makes the server reachable from the whole internet,
  // so the page says so plainly and points at HTTPS and two-factor sign-in.
  import { onDestroy, onMount } from 'svelte';
  import { authApi, settingsApi, type RemoteAccess } from '$lib/api';
  import { toast } from '$lib/stores/toast';
  import { t, i18n } from '$lib/i18n/index.svelte';
  import {
    externalPortError,
    publicAddress,
    remoteAccessMethodLabel,
    remoteAccessStateLabel,
    remoteAccessTone,
  } from '$lib/remoteAccess';

  let loading = $state(true);
  let error = $state('');
  let saving = $state(false);
  let ra = $state<RemoteAccess | null>(null);
  let mapPort = $state(false);
  let externalPort = $state<number | null>(null);
  let myTwoFactor = $state<boolean | null>(null);
  let timer: ReturnType<typeof setInterval> | null = null;

  const portErr = $derived(externalPortError(externalPort ?? 0, $i18n));
  const dirty = $derived(
    ra !== null && (mapPort !== ra.map_port || (externalPort ?? 0) !== (ra.external_port ?? 0)),
  );
  const st = $derived(ra?.status);
  const addr = $derived(st ? publicAddress(st) : '');

  function apply(r: RemoteAccess) {
    ra = r;
    mapPort = r.map_port;
    externalPort = r.external_port || null;
  }

  async function load() {
    try {
      apply(await settingsApi.getRemoteAccess());
      error = '';
    } catch (e) {
      error = e instanceof Error ? e.message : t('settings.remote.loadFailed');
    } finally {
      loading = false;
    }
  }

  // Status only: keep the admin's unsaved edits.
  async function refreshStatus() {
    if (saving) return;
    try {
      const r = await settingsApi.getRemoteAccess();
      if (ra) ra = { ...ra, status: r.status, reachability_hint: r.reachability_hint };
    } catch {
      // Transient — the next tick retries.
    }
  }

  onMount(() => {
    void load();
    void authApi.totp.status().then((s) => (myTwoFactor = s.enabled)).catch(() => (myTwoFactor = null));
    timer = setInterval(refreshStatus, 10_000);
  });
  onDestroy(() => {
    if (timer) clearInterval(timer);
  });

  async function save() {
    if (portErr) return;
    saving = true;
    try {
      apply(await settingsApi.updateRemoteAccess({ map_port: mapPort, external_port: externalPort ?? 0, protocol: 'TCP' }));
      toast.success(mapPort ? t('settings.remote.savedOn') : t('settings.remote.savedOff'));
      // The mapping node picks the change up shortly; show it when it does.
      setTimeout(refreshStatus, 4000);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t('settings.remote.saveFailed'));
    } finally {
      saving = false;
    }
  }

  function when(iso?: string): string {
    return iso ? new Date(iso).toLocaleString() : '';
  }
</script>

<svelte:head><title>{$i18n('settings.remote.pageTitle')}</title></svelte:head>

{#if loading}
  <div class="skeleton-block"></div>
{:else if error || !ra}
  <p class="error">{error || $i18n('settings.remote.unavailable')}</p>
{:else}
  <div class="wrap">
    <div class="danger" role="note">
      <strong>{$i18n('settings.remote.dangerTitle')}</strong>
      {$i18n('settings.remote.dangerIntro')}
      <ul>
        <li>
          {#each $i18n.rich('settings.remote.dangerHttps', { link: $i18n('settings.remote.securityLink') }) as seg}{#if seg.param === 'link'}<a href="/settings/security">{seg.text}</a>{:else}{seg.text}{/if}{/each} {#if !ra.https}<em>{$i18n('settings.remote.plainHttp')}</em>{/if}
        </li>
        <li>
          {#each $i18n.rich('settings.remote.danger2fa', { link: $i18n('settings.remote.securityLink') }) as seg}{#if seg.param === 'link'}<a href="/settings/security">{seg.text}</a>{:else}{seg.text}{/if}{/each}
          {#if myTwoFactor === false}<em>{$i18n('settings.remote.no2fa')}</em>{/if}
        </li>
        <li>{$i18n('settings.remote.dangerPasswords')}</li>
      </ul>
      {$i18n('settings.remote.dangerVpn')}
    </div>

    <section>
      <header>
        <h2>{$i18n('settings.remote.mappingTitle')}</h2>
        <p class="hint">
          {$i18n('settings.remote.mappingHint', { port: String(ra.listen_port ?? '') })}
        </p>
      </header>

      {#if ra.loopback_only}
        <p class="warn">
          {#each $i18n.rich('settings.remote.loopback', { addr: ra.listen_addr ?? '', link: $i18n('settings.remote.nodesLink') }) as seg}{#if seg.param === 'link'}<a href="/settings/nodes">{seg.text}</a>{:else}{seg.text}{/if}{/each}
        </p>
      {/if}

      <label class="check">
        <input type="checkbox" bind:checked={mapPort} disabled={saving} />
        <span>{$i18n('settings.remote.mapPort')}</span>
      </label>

      <div class="grid">
        <label>
          {$i18n('settings.remote.externalPort')} <span class="hint">{$i18n('settings.remote.externalPortHint', { port: String(ra.listen_port ?? '?') })}</span>
          <input type="number" min="1" max="65535" placeholder={String(ra.listen_port ?? '')}
                 bind:value={externalPort} disabled={saving} />
          {#if portErr}<span class="field-error">{portErr}</span>{/if}
        </label>
        <label>
          {$i18n('settings.remote.protocol')}
          <select disabled>
            <option>TCP</option>
          </select>
        </label>
      </div>

      <div class="actions">
        <button class="btn btn-primary" disabled={saving || !dirty || !!portErr} onclick={save}>
          {saving ? $i18n('settings.remote.saving') : $i18n('settings.remote.save')}
        </button>
      </div>
    </section>

    {#if st}
      <section>
        <header>
          <h2>{$i18n('settings.remote.status')}</h2>
        </header>
        <div class="state">
          <span class="chip {remoteAccessTone(st)}">{remoteAccessStateLabel(st, $i18n)}</span>
          {#if st.node}<span class="hint">{$i18n('settings.remote.onNode', { node: st.node })}</span>{/if}
        </div>
        <dl class="facts">
          {#if st.gateway_found}
            <dt>{$i18n('settings.remote.router')}</dt>
            <dd>{st.gateway_addr}{#if st.method} · {remoteAccessMethodLabel(st.method)}{/if}</dd>
          {/if}
          {#if addr}
            <dt>{$i18n('settings.remote.publicAddress')}</dt>
            <dd class="mono">{addr}</dd>
          {:else if st.external_port}
            <dt>{$i18n('settings.remote.publicPort')}</dt>
            <dd class="mono">{st.external_port}</dd>
          {/if}
          {#if st.local_ip}
            <dt>{$i18n('settings.remote.forwardsTo')}</dt>
            <dd class="mono">{st.local_ip}:{st.internal_port}</dd>
          {/if}
          {#if st.state === 'mapped'}
            <dt>{$i18n('settings.remote.lease')}</dt>
            <dd>
              {#if st.permanent}{$i18n('settings.remote.permanent', { minutes: 30 })}
              {:else if st.expires_at}{$i18n.n('settings.remote.leaseRenewed', Math.round((st.lease_seconds ?? 0) / 60), { when: when(st.expires_at) })}{:else}{$i18n.n('settings.remote.leaseMinutes', Math.round((st.lease_seconds ?? 0) / 60))}{/if}
            </dd>
          {/if}
          {#if st.last_renewal}
            <dt>{$i18n('settings.remote.lastRenewal')}</dt>
            <dd>{when(st.last_renewal)}</dd>
          {/if}
          {#if st.last_error}
            <dt>{$i18n('settings.remote.lastError')}</dt>
            <dd class="err">{st.last_error}{#if st.last_error_at} <span class="hint">({when(st.last_error_at)})</span>{/if}</dd>
          {/if}
        </dl>
        {#if ra.reachability_hint}
          <p class="reach">{ra.reachability_hint}</p>
        {/if}
      </section>
    {/if}
  </div>
{/if}

<style>
  .wrap { display: flex; flex-direction: column; gap: 1.5rem; }
  .danger {
    background: rgba(248, 113, 113, 0.08);
    border: 1px solid var(--error, #f87171);
    border-radius: 10px;
    padding: 0.8rem 1rem;
    font-size: 0.82rem;
    color: var(--text-secondary);
    line-height: 1.5;
  }
  .danger strong { color: var(--text-primary); display: block; margin-bottom: 0.25rem; }
  .danger ul { margin: 0.4rem 0; padding-left: 1.2rem; }
  .danger em { color: var(--error, #f87171); font-style: normal; }
  .danger a, .warn a { color: var(--accent-text, var(--accent)); }
  section {
    background: var(--bg-elevated);
    border: 1px solid var(--border);
    border-radius: 10px;
    padding: 1.1rem 1.25rem;
  }
  h2 { font-size: 0.95rem; margin: 0 0 0.5rem; font-weight: 600; color: var(--text-primary); }
  .hint { color: var(--text-muted); font-weight: 400; font-size: 0.78rem; line-height: 1.45; }
  header .hint { margin: 0 0 0.6rem; }
  .warn {
    font-size: 0.8rem; color: var(--text-secondary);
    border-left: 3px solid #f7c948; padding: 0.3rem 0 0.3rem 0.7rem; margin: 0 0 0.6rem;
  }
  .error { color: var(--error); }
  .grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 0.75rem 1rem;
    margin: 1rem 0 0;
  }
  label {
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
    font-size: 0.78rem;
    color: var(--text-secondary);
  }
  input[type="number"], select {
    padding: 0.48rem 0.7rem;
    border-radius: 7px;
    border: 1px solid var(--border-strong);
    background: var(--input-bg);
    color: var(--text-primary);
    font-family: inherit;
    font-size: 0.85rem;
  }
  select:disabled { opacity: 0.7; }
  input[type="number"]:focus {
    outline: none;
    border-color: var(--accent);
    box-shadow: 0 0 0 3px var(--accent-bg);
  }
  .field-error { color: var(--error, #f87171); font-size: 0.72rem; }
  .check {
    flex-direction: row;
    align-items: flex-start;
    gap: 0.5rem;
    margin-top: 0.4rem;
    font-size: 0.85rem;
    color: var(--text-primary);
    cursor: pointer;
  }
  /* A checkbox keeps its own size whatever form-wide input rules say. */
  .check input { width: auto; margin-top: 0.2rem; accent-color: var(--accent); }
  .actions { display: flex; gap: 0.5rem; margin-top: 1rem; }
  .btn {
    padding: 0.45rem 0.9rem;
    border-radius: 7px;
    font-size: 0.82rem;
    font-weight: 500;
    border: 1px solid var(--border-strong);
    background: transparent;
    color: var(--text-primary);
    cursor: pointer;
  }
  .btn:disabled { opacity: 0.55; cursor: not-allowed; }
  .btn-primary { background: var(--accent); color: #fff; border-color: transparent; }
  .btn-primary:hover:not(:disabled) { background: var(--accent-hover); }
  .state { display: flex; align-items: center; gap: 0.5rem; margin-bottom: 0.6rem; }
  .chip {
    display: inline-block; padding: 2px 8px; border-radius: 999px;
    font-size: 0.72rem; font-weight: 600; border: 1px solid currentColor;
  }
  .chip.ok { color: #3af7a0; }
  .chip.warn { color: #f7c948; }
  .chip.off { color: var(--text-muted); }
  .facts {
    display: grid; grid-template-columns: max-content 1fr; gap: 0.3rem 1rem;
    margin: 0; font-size: 0.8rem;
  }
  dt { color: var(--text-muted); }
  dd { margin: 0; color: var(--text-primary); min-width: 0; overflow-wrap: anywhere; }
  dd.err { color: var(--error, #f87171); }
  .mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 0.78rem; }
  .reach {
    margin: 0.9rem 0 0; font-size: 0.8rem; color: var(--text-secondary); line-height: 1.5;
    border-top: 1px solid var(--border); padding-top: 0.75rem;
  }
  .skeleton-block {
    height: 120px;
    border-radius: 10px;
    background: linear-gradient(90deg, var(--bg-elevated) 25%, var(--bg-hover) 50%, var(--bg-elevated) 75%);
    background-size: 200% 100%;
    animation: shimmer 1.4s infinite;
  }
  @keyframes shimmer {
    0% { background-position: 200% 0; }
    100% { background-position: -200% 0; }
  }
  @media (max-width: 720px) {
    .grid { grid-template-columns: 1fr; }
  }
</style>
