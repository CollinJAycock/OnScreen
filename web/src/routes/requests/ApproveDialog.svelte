<script lang="ts">
  // Approve-with-options: pick the Radarr / Sonarr instance, quality profile
  // and root folder a pending request is sent with. Opens pre-filled with
  // what approving without the dialog would use (see approveOptions.ts), so
  // the common case is Approve → Enter. The page owns the API call; this
  // component only gathers the selection.
  import { onMount, tick } from 'svelte';
  import type { ApproveRequestBody, ArrService, MediaRequest } from '$lib/api';
  import {
    arrKindFor, arrKindLabel, buildApproveBody, candidateInstances, initialInstance,
    instanceLabel, pickProfileId, pickRootFolder, preferredFor,
    type ArrInstanceOptions, type ArrOptionsLoader,
  } from './approveOptions';
  import { formatBytes, seasonsLabel } from './requestRows';

  interface Props {
    request: MediaRequest;
    loader: ArrOptionsLoader;
    /** The approve call is in flight — buttons disabled, Esc ignored. */
    busy?: boolean;
    onapprove: (body: ApproveRequestBody) => void;
    oncancel: () => void;
  }

  let { request, loader, busy = false, onapprove, oncancel }: Props = $props();

  let servicesLoading = $state(true);
  let servicesError = $state('');
  let services = $state<ArrService[]>([]);
  let serviceId = $state('');
  let initialServiceId = $state('');
  let note = $state('');

  let options = $state<ArrInstanceOptions | null>(null);
  let optionsLoading = $state(false);
  let optionsError = $state('');
  let profileId = $state<number | null>(null);
  let rootFolder = $state<string | null>(null);

  let dialogEl = $state<HTMLDivElement>();
  // Instance switches can outrun the network; only the latest load may land.
  let optSeq = 0;

  const kindLabel = $derived(arrKindLabel(arrKindFor(request.type)));
  const candidates = $derived(candidateInstances(request, services));
  const selected = $derived(candidates.find((s) => s.id === serviceId) ?? null);
  const requester = $derived(request.username || 'the requester');
  const noInstances = $derived(!servicesLoading && !servicesError && candidates.length === 0);
  const canApprove = $derived(
    !busy && !!selected && !optionsLoading && profileId != null && !!rootFolder,
  );
  // The escape hatch: let the server resolve everything. Useless only when we
  // know there's nothing to send it to.
  const canApproveDefaults = $derived(!busy && !noInstances);

  function errText(e: unknown): string {
    return e instanceof Error && e.message ? e.message : 'request failed';
  }

  onMount(() => {
    void loadServices(false);
    void tick().then(() => dialogEl?.focus());
  });

  async function loadServices(force: boolean) {
    servicesLoading = true;
    servicesError = '';
    try {
      services = await loader.services(force);
    } catch (e) {
      servicesError = errText(e);
      servicesLoading = false;
      return;
    }
    servicesLoading = false;
    const pick = initialInstance(request, services);
    note = pick.note;
    serviceId = pick.service?.id ?? '';
    initialServiceId = serviceId;
    if (pick.service) void loadOptions(pick.service, false);
  }

  async function loadOptions(svc: ArrService, force: boolean) {
    const seq = ++optSeq;
    const pref = preferredFor(request, svc, svc.id === initialServiceId);
    optionsLoading = true;
    optionsError = '';
    options = null;
    profileId = null;
    rootFolder = null;
    try {
      const res = await loader.options(svc.id, force);
      if (seq !== optSeq) return;
      options = res;
      profileId = pickProfileId(res.quality_profiles, pref.profileIds);
      rootFolder = pickRootFolder(res.root_folders, pref.rootFolders);
    } catch (e) {
      if (seq !== optSeq) return;
      optionsError = errText(e);
      // Can't check them against the instance, but the saved defaults are
      // what the server would use anyway.
      profileId = pref.profileIds[0] ?? null;
      rootFolder = pref.rootFolders[0] ?? null;
    } finally {
      if (seq === optSeq) optionsLoading = false;
    }
  }

  function onInstanceChange(e: Event) {
    const id = (e.currentTarget as HTMLSelectElement).value;
    serviceId = id;
    const svc = candidates.find((s) => s.id === id);
    if (svc) void loadOptions(svc, false);
  }

  // One-way value + change handlers rather than bind:value: the selection is
  // read from the element's value, which every DOM implementation agrees on.
  function onProfileChange(e: Event) {
    const v = (e.currentTarget as HTMLSelectElement).value;
    const id = Number(v);
    profileId = v !== '' && Number.isFinite(id) ? id : null;
  }

  function onFolderChange(e: Event) {
    rootFolder = (e.currentTarget as HTMLSelectElement).value || null;
  }

  function folderLabel(f: ArrInstanceOptions['root_folders'][number]): string {
    const free = formatBytes(f.free_space);
    return free ? `${f.path} — ${free} free` : f.path;
  }

  function retryOptions() {
    if (selected) void loadOptions(selected, true);
  }

  function submit() {
    if (!canApprove) return;
    onapprove(buildApproveBody({ serviceId: selected?.id ?? null, profileId, rootFolder }));
  }

  function approveDefaults() {
    if (!canApproveDefaults) return;
    onapprove({});
  }

  function cancel() {
    if (busy) return;
    oncancel();
  }

  function onOverlayClick(e: MouseEvent) {
    if (e.target === e.currentTarget) cancel();
  }

  // Esc cancels, Enter approves (buttons and links keep their own Enter),
  // Tab stays inside the dialog.
  function onKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      e.preventDefault();
      cancel();
      return;
    }
    if (e.key === 'Enter') {
      const t = e.target as HTMLElement | null;
      const tag = t?.tagName;
      if (tag === 'BUTTON' || tag === 'A' || tag === 'TEXTAREA') return;
      e.preventDefault();
      submit();
      return;
    }
    if (e.key !== 'Tab' || !dialogEl) return;
    const focusable = dialogEl.querySelectorAll<HTMLElement>(
      'a[href], button:not([disabled]), select:not([disabled]), input:not([disabled])',
    );
    if (focusable.length === 0) {
      e.preventDefault();
      return;
    }
    const first = focusable[0];
    const last = focusable[focusable.length - 1];
    const active = document.activeElement;
    if (e.shiftKey && (active === first || active === dialogEl)) {
      e.preventDefault();
      last.focus();
    } else if (!e.shiftKey && active === last) {
      e.preventDefault();
      first.focus();
    }
  }
</script>

<svelte:window onkeydown={onKeydown} />

<div class="overlay" role="presentation" onclick={onOverlayClick}>
  <div
    class="dialog"
    role="dialog"
    aria-modal="true"
    aria-labelledby="approve-title"
    aria-describedby="approve-desc"
    aria-busy={servicesLoading || optionsLoading}
    tabindex="-1"
    bind:this={dialogEl}
  >
    <h2 id="approve-title" class="title">
      Approve “{request.title}”{#if request.year}<span class="year"> ({request.year})</span>{/if}
    </h2>
    <p id="approve-desc" class="desc">
      Requested by <strong>{requester}</strong>{#if request.type === 'show'} · {seasonsLabel(request.seasons)}{/if}
    </p>

    {#if servicesLoading}
      <div class="loading" role="status">Loading {kindLabel} instances…</div>
    {:else if servicesError}
      <div class="err" role="alert">
        Couldn’t load {kindLabel} instances: {servicesError}
        <button type="button" class="link-btn" onclick={() => loadServices(true)}>Retry</button>
      </div>
      <p class="hint">“Approve with defaults” still sends it to the default instance.</p>
    {:else if noInstances}
      <div class="err" role="alert">
        No enabled {kindLabel} instance. <a href="/settings/arr-services">Set one up</a> to approve this request.
      </div>
    {:else}
      <div class="field">
        <div class="label-row">
          <label for="approve-instance">{kindLabel} instance</label>
          {#if selected && selected.id === request.requested_service_id}
            <span class="tag">Requested by {requester}</span>
          {/if}
        </div>
        <select id="approve-instance" value={serviceId} onchange={onInstanceChange} disabled={busy}>
          {#each candidates as s (s.id)}
            <option value={s.id}>{instanceLabel(s, request)}</option>
          {/each}
        </select>
        {#if note}<p class="hint">{note}</p>{/if}
      </div>

      {#if optionsError}
        <div class="err" role="alert">
          Couldn’t load options from {selected?.name ?? kindLabel}: {optionsError}
          <button type="button" class="link-btn" onclick={retryOptions}>Retry</button>
        </div>
        {#if profileId != null && rootFolder}
          <p class="hint">Approve uses the instance’s saved defaults.</p>
        {:else}
          <p class="hint">This instance has no saved default profile / folder — retry to pick one.</p>
        {/if}
      {:else}
        <div class="row-2">
          <div class="field">
            <label for="approve-profile">Quality profile</label>
            <select id="approve-profile" value={profileId == null ? '' : String(profileId)} onchange={onProfileChange} disabled={busy || optionsLoading || !options}>
              {#if optionsLoading}
                <option value="">Loading…</option>
              {:else}
                {#if profileId == null}<option value="" disabled>Choose a profile…</option>{/if}
                {#each options?.quality_profiles ?? [] as p (p.id)}
                  <option value={String(p.id)}>{p.name}</option>
                {/each}
              {/if}
            </select>
          </div>
          <div class="field">
            <label for="approve-folder">Root folder</label>
            <select id="approve-folder" value={rootFolder ?? ''} onchange={onFolderChange} disabled={busy || optionsLoading || !options}>
              {#if optionsLoading}
                <option value="">Loading…</option>
              {:else}
                {#if !rootFolder}<option value="" disabled>Choose a folder…</option>{/if}
                {#each options?.root_folders ?? [] as f (f.path)}
                  <option value={f.path}>{folderLabel(f)}</option>
                {/each}
              {/if}
            </select>
          </div>
        </div>
        {#if options && !optionsLoading && (options.quality_profiles.length === 0 || options.root_folders.length === 0)}
          <p class="hint">{selected?.name} reported no {options.quality_profiles.length === 0 ? 'quality profiles' : 'root folders'}.</p>
        {/if}
      {/if}
    {/if}

    <div class="actions">
      <button type="button" class="btn ghost sm" onclick={cancel} disabled={busy}>Cancel</button>
      <button type="button" class="btn ghost sm" onclick={approveDefaults} disabled={!canApproveDefaults}>
        Approve with defaults
      </button>
      <button type="button" class="btn primary sm" onclick={submit} disabled={!canApprove}>
        {busy ? 'Approving…' : 'Approve'}
      </button>
    </div>
  </div>
</div>

<style>
  .overlay {
    position: fixed; inset: 0; background: var(--shadow);
    display: flex; align-items: center; justify-content: center; z-index: 1000;
    padding: 1rem;
  }
  .dialog {
    background: var(--bg-elevated);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 1.5rem;
    max-width: 520px; width: 100%;
    max-height: calc(100vh - 2rem); overflow-y: auto;
  }
  .dialog:focus { outline: none; }
  .title { font-size: 0.95rem; font-weight: 600; color: var(--text-primary); margin: 0 0 0.3rem; }
  .year { color: var(--text-muted); font-weight: 400; }
  .desc { font-size: 0.78rem; color: var(--text-secondary); margin: 0 0 1.1rem; }
  .desc strong { color: var(--text-primary); font-weight: 600; }

  .field { display: flex; flex-direction: column; gap: 0.3rem; margin-bottom: 0.9rem; min-width: 0; }
  .row-2 { display: grid; grid-template-columns: 1fr 1fr; gap: 0.9rem; }
  .label-row { display: flex; align-items: center; justify-content: space-between; gap: 0.5rem; }
  label { font-size: 0.75rem; font-weight: 500; color: var(--text-muted); }
  .tag {
    font-size: 0.65rem; font-weight: 600; padding: 0.1rem 0.45rem; border-radius: 10px;
    background: var(--accent-bg); color: var(--accent-text);
  }
  select {
    background: var(--input-bg);
    border: 1px solid var(--border-strong);
    border-radius: 7px;
    padding: 0.5rem 0.75rem;
    font-size: 0.85rem;
    color: var(--text-primary);
    width: 100%;
    min-width: 0;
  }
  select:focus { outline: none; border-color: var(--accent); box-shadow: 0 0 0 3px var(--accent-bg); }
  select:disabled { opacity: 0.6; }

  .loading { font-size: 0.8rem; color: var(--text-muted); padding: 0.6rem 0 1rem; }
  .err {
    font-size: 0.78rem; color: var(--error); background: var(--error-bg);
    border-radius: 7px; padding: 0.55rem 0.75rem; margin-bottom: 0.6rem;
  }
  .err a { color: inherit; }
  .hint { font-size: 0.72rem; color: var(--text-muted); margin: 0.1rem 0 0.8rem; }
  .link-btn {
    background: none; border: none; padding: 0; margin-left: 0.35rem;
    color: inherit; font: inherit; font-weight: 600; text-decoration: underline; cursor: pointer;
  }

  .actions { display: flex; justify-content: flex-end; gap: 0.6rem; flex-wrap: wrap; margin-top: 0.4rem; }
  .btn {
    display: inline-flex; align-items: center; justify-content: center;
    padding: 0.35rem 0.65rem; font-size: 0.74rem; font-weight: 600;
    border-radius: 7px; cursor: pointer; border: 1px solid transparent;
    line-height: 1.1; transition: background 0.12s, color 0.12s, border-color 0.12s;
  }
  .btn.primary { background: var(--accent); color: #fff; }
  .btn.primary:hover:not(:disabled) { background: var(--accent-hover); }
  .btn.ghost { background: transparent; border-color: var(--border-strong); color: var(--text-secondary); }
  .btn.ghost:hover:not(:disabled) { background: var(--bg-hover); }
  .btn:disabled { opacity: 0.55; cursor: not-allowed; }

  @media (max-width: 600px) {
    .dialog { padding: 1.1rem; }
    .row-2 { grid-template-columns: 1fr; gap: 0; }
    .actions .btn { flex: 1 1 auto; }
  }
</style>
