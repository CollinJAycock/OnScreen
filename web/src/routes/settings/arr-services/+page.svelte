<script lang="ts">
  import { onMount } from 'svelte';
  import {
    arrServicesApi,
    type ArrService,
    type ArrServiceKind,
    type ArrProbeResult,
    type ArrQualityProfile,
    type ArrRootFolder,
  } from '$lib/api';
  import { toast } from '$lib/stores/toast';

  let loading = true;
  let error = '';
  let services: ArrService[] = [];

  // Add/edit form state — one form is shown at a time.
  type FormMode = { kind: 'create' } | { kind: 'edit'; id: string };
  let formMode: FormMode | null = null;
  let formKind: ArrServiceKind = 'radarr';
  let formName = '';
  let formBaseURL = '';
  let formAPIKey = '';
  let formIsDefault = false;
  let formEnabled = true;
  let formQualityProfileID: number | null = null;
  let formRootFolder: string | null = null;
  let formMinAvail: string = 'released';
  let formSeriesType: string = 'standard';
  let formSeasonFolder = true;
  let formLanguageProfileID: number | null = null;

  // Probe state — quality/root/lang dropdowns are populated by the probe call.
  let probeResult: ArrProbeResult | null = null;
  let probing = false;
  let probeError = '';
  let saving = false;

  let deleteId: string | null = null;

  onMount(load);

  async function load() {
    loading = true; error = '';
    try {
      const res = await arrServicesApi.list();
      services = res.items;
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed to load arr services';
    } finally {
      loading = false;
    }
  }

  function openCreate() {
    formMode = { kind: 'create' };
    formKind = 'radarr';
    formName = '';
    formBaseURL = '';
    formAPIKey = '';
    formIsDefault = false;
    formEnabled = true;
    formQualityProfileID = null;
    formRootFolder = null;
    formMinAvail = 'released';
    formSeriesType = 'standard';
    formSeasonFolder = true;
    formLanguageProfileID = null;
    probeResult = null;
    probeError = '';
  }

  function openEdit(s: ArrService) {
    formMode = { kind: 'edit', id: s.id };
    formKind = s.kind;
    formName = s.name;
    formBaseURL = s.base_url;
    formAPIKey = ''; // intentionally blank — leave empty to keep existing key
    formIsDefault = s.is_default;
    formEnabled = s.enabled;
    formQualityProfileID = s.default_quality_profile_id ?? null;
    formRootFolder = s.default_root_folder ?? null;
    formMinAvail = s.minimum_availability ?? 'released';
    formSeriesType = s.series_type ?? 'standard';
    formSeasonFolder = s.season_folder ?? true;
    formLanguageProfileID = s.language_profile_id ?? null;
    probeResult = null;
    probeError = '';
  }

  function closeForm() {
    formMode = null;
    probeResult = null;
    probeError = '';
  }

  async function runProbe() {
    probing = true;
    probeError = '';
    try {
      probeResult = await arrServicesApi.probe({
        kind: formKind,
        base_url: formBaseURL.trim(),
        api_key: formAPIKey.trim(),
        service_id: formMode?.kind === 'edit' ? formMode.id : undefined,
      });
      // Auto-select the matching options if the previously stored values exist.
      if (formQualityProfileID != null && !probeResult.quality_profiles.find(p => p.id === formQualityProfileID)) {
        formQualityProfileID = null;
      }
      if (formRootFolder && !probeResult.root_folders.find(f => f.path === formRootFolder)) {
        formRootFolder = null;
      }
      toast.success(`Connected (${probeResult.app_name ?? formKind} ${probeResult.version ?? ''})`);
    } catch (e: unknown) {
      probeError = e instanceof Error ? e.message : 'Probe failed';
      probeResult = null;
    } finally {
      probing = false;
    }
  }

  async function save() {
    if (!formMode) return;
    if (!formName.trim()) { toast.error('Name is required'); return; }
    if (!formBaseURL.trim()) { toast.error('Base URL is required'); return; }
    if (formMode.kind === 'create' && !formAPIKey.trim()) { toast.error('API key is required'); return; }

    saving = true;
    try {
      const common = {
        default_quality_profile_id: formQualityProfileID,
        default_root_folder: formRootFolder,
        minimum_availability: formKind === 'radarr' ? formMinAvail : null,
        series_type: formKind === 'sonarr' ? formSeriesType : null,
        season_folder: formKind === 'sonarr' ? formSeasonFolder : null,
        language_profile_id: formKind === 'sonarr' ? formLanguageProfileID : null,
      };

      if (formMode.kind === 'create') {
        await arrServicesApi.create({
          name: formName.trim(),
          kind: formKind,
          base_url: formBaseURL.trim(),
          api_key: formAPIKey.trim(),
          is_default: formIsDefault,
          enabled: formEnabled,
          ...common,
        });
        toast.success('Arr service added');
      } else {
        const body: Parameters<typeof arrServicesApi.update>[1] = {
          name: formName.trim(),
          base_url: formBaseURL.trim(),
          enabled: formEnabled,
          ...common,
        };
        if (formAPIKey.trim()) body.api_key = formAPIKey.trim();
        await arrServicesApi.update(formMode.id, body);
        // is_default is set via its own endpoint to keep the unique-default invariant atomic.
        const existing = services.find(s => s.id === (formMode as { kind: 'edit'; id: string }).id);
        if (existing && formIsDefault && !existing.is_default) {
          await arrServicesApi.setDefault(formMode.id);
        }
        toast.success('Arr service updated');
      }
      closeForm();
      await load();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Save failed');
    } finally {
      saving = false;
    }
  }

  async function setDefault(s: ArrService) {
    try {
      await arrServicesApi.setDefault(s.id);
      toast.success(`${s.name} is now the default ${s.kind}`);
      await load();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Set default failed');
    }
  }

  async function toggleEnabled(s: ArrService) {
    try {
      await arrServicesApi.update(s.id, { enabled: !s.enabled });
      await load();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Toggle failed');
    }
  }

  async function confirmDelete() {
    if (!deleteId) return;
    try {
      await arrServicesApi.del(deleteId);
      toast.success('Arr service deleted');
      deleteId = null;
      await load();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Delete failed');
    }
  }

  function profileLabel(p: ArrQualityProfile) { return p.name; }
  function rootLabel(f: ArrRootFolder) { return f.path; }
</script>

<svelte:head><title>Arr Services — OnScreen</title></svelte:head>

<div class="page">
  <div class="header">
    <p class="intro">
      Configure Radarr/Sonarr instances for the request workflow. Multiple instances
      per kind are supported — set one as default per kind, and use the additional
      instances for things like a separate 4K Radarr or alternate language Sonarr.
    </p>
    {#if !formMode && !loading}
      <button class="btn-add" on:click={openCreate}>+ Add Service</button>
    {/if}
  </div>

  {#if error}
    <div class="banner error">{error}</div>
  {/if}

  {#if loading}
    <div class="skeleton-block"></div>
  {:else}
    {#if formMode}
      <div class="card form-card">
        <div class="card-title">{formMode.kind === 'create' ? 'New Arr Service' : 'Edit Arr Service'}</div>

        <div class="row-2">
          <div class="field">
            <label for="srv-kind">Kind</label>
            <select id="srv-kind" bind:value={formKind} disabled={formMode.kind === 'edit'}>
              <option value="radarr">Radarr (movies)</option>
              <option value="sonarr">Sonarr (shows)</option>
            </select>
          </div>
          <div class="field">
            <label for="srv-name">Name</label>
            <input id="srv-name" type="text" bind:value={formName} placeholder="e.g. Radarr 4K" />
          </div>
        </div>

        <div class="field">
          <label for="srv-url">Base URL</label>
          <input id="srv-url" type="url" bind:value={formBaseURL} placeholder="http://radarr.local:7878" />
        </div>

        <div class="field">
          <label for="srv-key">API Key {#if formMode.kind === 'edit'}<span class="optional">(blank = keep existing)</span>{/if}</label>
          <input id="srv-key" type="password" bind:value={formAPIKey} autocomplete="new-password" />
        </div>

        <div class="probe-row">
          <button class="btn ghost sm" on:click={runProbe} disabled={probing || !formBaseURL.trim() || (formMode.kind === 'create' && !formAPIKey.trim())}>
            {probing ? 'Connecting…' : 'Test connection & load options'}
          </button>
          {#if probeError}
            <div class="probe-err">{probeError}</div>
          {:else if probeResult}
            <div class="probe-ok">Connected · {probeResult.app_name ?? formKind} {probeResult.version ?? ''}</div>
          {/if}
        </div>

        {#if probeResult}
          <div class="row-2">
            <div class="field">
              <label for="srv-qp">Default quality profile</label>
              <select id="srv-qp" bind:value={formQualityProfileID}>
                <option value={null}>(none — admin will pick at approval time)</option>
                {#each probeResult.quality_profiles as p}
                  <option value={p.id}>{profileLabel(p)}</option>
                {/each}
              </select>
            </div>
            <div class="field">
              <label for="srv-rf">Default root folder</label>
              <select id="srv-rf" bind:value={formRootFolder}>
                <option value={null}>(none — admin will pick at approval time)</option>
                {#each probeResult.root_folders as f}
                  <option value={f.path}>{rootLabel(f)}</option>
                {/each}
              </select>
            </div>
          </div>

          {#if formKind === 'radarr'}
            <div class="field">
              <label for="srv-min">Minimum availability</label>
              <select id="srv-min" bind:value={formMinAvail}>
                <option value="announced">Announced</option>
                <option value="inCinemas">In cinemas</option>
                <option value="released">Released</option>
              </select>
            </div>
          {:else}
            <div class="row-2">
              <div class="field">
                <label for="srv-st">Series type</label>
                <select id="srv-st" bind:value={formSeriesType}>
                  <option value="standard">Standard</option>
                  <option value="daily">Daily</option>
                  <option value="anime">Anime</option>
                </select>
              </div>
              <div class="field">
                <label for="srv-lp">Language profile</label>
                <select id="srv-lp" bind:value={formLanguageProfileID}>
                  <option value={null}>(none)</option>
                  {#each probeResult.language_profiles as l}
                    <option value={l.id}>{l.name}</option>
                  {/each}
                </select>
              </div>
            </div>
            <div class="field">
              <label class="toggle-label">
                <span>Use season folders</span>
                <button class="toggle" class:toggle-on={formSeasonFolder}
                        on:click={() => (formSeasonFolder = !formSeasonFolder)} type="button"
                        title="Use season folders" aria-label="Use season folders"
                        aria-pressed={formSeasonFolder}>
                  <span class="toggle-knob"></span>
                </button>
              </label>
            </div>
          {/if}
        {/if}

        <div class="row-2">
          <div class="field">
            <label class="toggle-label">
              <span>Enabled</span>
              <button class="toggle" class:toggle-on={formEnabled}
                      on:click={() => (formEnabled = !formEnabled)} type="button"
                      title="Enabled" aria-label="Enabled" aria-pressed={formEnabled}>
                <span class="toggle-knob"></span>
              </button>
            </label>
          </div>
          <div class="field">
            <label class="toggle-label">
              <span>Set as default for {formKind}</span>
              <button class="toggle" class:toggle-on={formIsDefault}
                      on:click={() => (formIsDefault = !formIsDefault)} type="button"
                      title="Set as default for {formKind}"
                      aria-label="Set as default for {formKind}"
                      aria-pressed={formIsDefault}>
                <span class="toggle-knob"></span>
              </button>
            </label>
          </div>
        </div>

        <div class="form-actions">
          <button class="btn ghost sm" on:click={closeForm}>Cancel</button>
          <button class="btn primary sm" on:click={save} disabled={saving}>
            {saving ? 'Saving…' : (formMode.kind === 'create' ? 'Create' : 'Save')}
          </button>
        </div>
      </div>
    {/if}

    {#if services.length === 0 && !formMode}
      <div class="empty">
        <p>No arr services configured.</p>
        <p class="empty-sub">Add a Radarr or Sonarr instance to enable the request workflow's auto-dispatch.</p>
        <button class="btn-add" on:click={openCreate}>+ Add Service</button>
      </div>
    {:else}
      {#each services as s (s.id)}
        <div class="card" class:disabled-card={!s.enabled}>
          <div class="srv-row">
            <div class="srv-info">
              <div class="srv-header">
                <span class="srv-name">{s.name}</span>
                <span class="kind-pill kind-{s.kind}">{s.kind}</span>
                {#if s.is_default}<span class="default-pill">default</span>{/if}
                <button
                  class="toggle-sm"
                  class:toggle-on={s.enabled}
                  on:click={() => toggleEnabled(s)}
                  title={s.enabled ? 'Disable' : 'Enable'}
                  type="button"
                >
                  <span class="toggle-knob"></span>
                </button>
              </div>
              <div class="srv-url">{s.base_url}</div>
              <div class="srv-meta">
                {s.api_key_set ? 'API key configured' : 'no API key'}
                {#if s.default_quality_profile_id != null}· QP #{s.default_quality_profile_id}{/if}
                {#if s.default_root_folder}· {s.default_root_folder}{/if}
              </div>
            </div>
            <div class="srv-actions">
              {#if !s.is_default}
                <button class="btn ghost sm" on:click={() => setDefault(s)}>Set default</button>
              {/if}
              <button class="btn ghost sm" on:click={() => openEdit(s)}>Edit</button>
              <button class="btn danger sm" on:click={() => (deleteId = s.id)}>Delete</button>
            </div>
          </div>
        </div>
      {/each}
    {/if}
  {/if}

  {#if deleteId}
    {@const target = services.find(s => s.id === deleteId)}
    <div class="modal-overlay" on:click={() => (deleteId = null)} on:keydown={e => e.key === 'Escape' && (deleteId = null)} role="button" tabindex="-1">
      <!-- svelte-ignore a11y_click_events_have_key_events -->
      <div class="modal" on:click|stopPropagation role="dialog" aria-label="Confirm delete">
        <p class="modal-text">Delete this arr service?</p>
        <p class="modal-sub">{target?.name ?? ''}</p>
        <p class="modal-warn">In-flight requests routed to this service will lose their service binding.</p>
        <div class="modal-actions">
          <button class="btn ghost sm" on:click={() => (deleteId = null)}>Cancel</button>
          <button class="btn danger sm" on:click={confirmDelete}>Delete</button>
        </div>
      </div>
    </div>
  {/if}
</div>

<style>
  .page { max-width: 720px; }

  .header { display: flex; align-items: flex-start; justify-content: space-between; gap: 1rem; margin-bottom: 1.5rem; }
  .intro { font-size: 0.78rem; color: var(--text-muted); line-height: 1.5; margin: 0; flex: 1; }

  .banner { padding: 0.6rem 0.9rem; border-radius: 8px; font-size: 0.8rem; margin-bottom: 1.25rem; }
  .banner.error { background: rgba(248,113,113,0.1); border: 1px solid rgba(248,113,113,0.2); color: #fca5a5; }

  .skeleton-block {
    height: 100px; border-radius: 10px;
    background: linear-gradient(90deg, var(--bg-elevated) 25%, var(--bg-hover) 50%, var(--bg-elevated) 75%);
    background-size: 200% 100%; animation: shimmer 1.4s infinite;
  }
  @keyframes shimmer { 0% { background-position: 200% 0; } 100% { background-position: -200% 0; } }

  .card {
    background: rgba(255,255,255,0.025);
    border: 1px solid var(--border);
    border-radius: 10px;
    padding: 1.1rem 1.25rem;
    margin-bottom: 0.75rem;
  }
  .card.disabled-card { opacity: 0.55; }
  .card-title { font-size: 0.78rem; font-weight: 700; color: var(--accent-text); margin-bottom: 1rem; text-transform: uppercase; letter-spacing: 0.06em; }
  .form-card { border-color: rgba(124,106,247,0.2); }

  .field { display: flex; flex-direction: column; gap: 0.3rem; margin-bottom: 1rem; }
  .row-2 { display: grid; grid-template-columns: 1fr 1fr; gap: 1rem; }
  label { font-size: 0.75rem; font-weight: 500; color: var(--text-muted); }
  .optional { font-weight: 400; }
  input, select { background: var(--input-bg); border: 1px solid var(--border-strong); border-radius: 7px; padding: 0.48rem 0.7rem; font-size: 0.85rem; color: var(--text-primary); width: 100%; }
  input:focus, select:focus { outline: none; border-color: var(--accent); box-shadow: 0 0 0 3px var(--accent-bg); }

  .probe-row { display: flex; align-items: center; gap: 0.75rem; margin: 0.5rem 0 1rem; flex-wrap: wrap; }
  .probe-err { font-size: 0.78rem; color: #fca5a5; }
  .probe-ok  { font-size: 0.78rem; color: #6ee7b7; }

  .form-actions { display: flex; justify-content: flex-end; gap: 0.6rem; margin-top: 0.5rem; }

  .btn { display: inline-flex; align-items: center; justify-content: center; padding: 0.45rem 0.85rem; font-size: 0.8rem; font-weight: 600; border-radius: 7px; cursor: pointer; border: 1px solid transparent; text-decoration: none; transition: background 0.12s, color 0.12s, border-color 0.12s; }
  .btn.sm { padding: 0.38rem 0.7rem; font-size: 0.76rem; }
  .btn.primary { background: var(--accent); color: #fff; }
  .btn.primary:hover:not(:disabled) { background: var(--accent-hover); }
  .btn.ghost { background: transparent; border-color: var(--border-strong); color: var(--text-secondary); }
  .btn.ghost:hover:not(:disabled) { background: var(--bg-hover); }
  .btn.danger { background: rgba(248,113,113,0.12); color: #fca5a5; border-color: rgba(248,113,113,0.25); }
  .btn.danger:hover { background: rgba(248,113,113,0.22); }
  .btn:disabled { opacity: 0.55; cursor: not-allowed; }

  .btn-add { padding: 0.42rem 0.8rem; background: var(--accent); border: none; border-radius: 7px; color: #fff; font-size: 0.8rem; font-weight: 600; cursor: pointer; }
  .btn-add:hover { background: var(--accent-hover); }

  .srv-row { display: flex; align-items: flex-start; justify-content: space-between; gap: 1rem; }
  .srv-info { flex: 1; min-width: 0; }
  .srv-header { display: flex; align-items: center; gap: 0.6rem; margin-bottom: 0.35rem; flex-wrap: wrap; }
  .srv-name { font-size: 0.92rem; font-weight: 600; color: var(--text-primary); }
  .srv-url { font-size: 0.78rem; font-family: monospace; color: var(--text-secondary); word-break: break-all; margin-bottom: 0.4rem; }
  .srv-meta { font-size: 0.72rem; color: var(--text-muted); }
  .srv-actions { display: flex; gap: 0.4rem; flex-shrink: 0; flex-wrap: wrap; justify-content: flex-end; }

  .kind-pill { font-size: 0.62rem; font-weight: 600; padding: 0.15rem 0.5rem; border-radius: 10px; text-transform: uppercase; letter-spacing: 0.05em; }
  .kind-radarr { background: rgba(251,191,36,0.15); color: #fcd34d; }
  .kind-sonarr { background: rgba(96,165,250,0.15); color: #93c5fd; }
  .default-pill { font-size: 0.62rem; padding: 0.15rem 0.5rem; border-radius: 10px; background: rgba(52,211,153,0.15); color: #6ee7b7; text-transform: uppercase; letter-spacing: 0.05em; }

  .toggle, .toggle-sm { position: relative; width: 36px; height: 20px; border-radius: 10px; border: none; background: rgba(255,255,255,0.1); cursor: pointer; transition: background 0.2s; flex-shrink: 0; }
  .toggle-sm { width: 30px; height: 16px; border-radius: 8px; }
  .toggle.toggle-on, .toggle-sm.toggle-on { background: var(--accent); }
  .toggle-knob { position: absolute; top: 3px; left: 3px; width: 14px; height: 14px; border-radius: 50%; background: #fff; transition: transform 0.2s; }
  .toggle-sm .toggle-knob { width: 10px; height: 10px; }
  .toggle.toggle-on .toggle-knob { transform: translateX(16px); }
  .toggle-sm.toggle-on .toggle-knob { transform: translateX(14px); }
  .toggle-label { display: flex; align-items: center; justify-content: space-between; color: var(--text-secondary); font-size: 0.8rem; padding-top: 0.35rem; }

  .empty { display: flex; flex-direction: column; align-items: center; gap: 0.6rem; padding: 3rem 1rem; color: var(--text-muted); text-align: center; }
  .empty p { font-size: 0.88rem; }
  .empty .empty-sub { font-size: 0.75rem; max-width: 360px; }

  .modal-overlay { position: fixed; inset: 0; background: var(--shadow); display: flex; align-items: center; justify-content: center; z-index: 1000; }
  .modal { background: var(--bg-elevated); border: 1px solid var(--border); border-radius: 12px; padding: 1.5rem; max-width: 420px; width: 90%; }
  .modal-text { font-size: 0.92rem; font-weight: 600; color: var(--text-primary); margin-bottom: 0.35rem; }
  .modal-sub { font-size: 0.78rem; color: var(--text-muted); font-family: monospace; word-break: break-all; margin-bottom: 0.5rem; }
  .modal-warn { font-size: 0.75rem; color: #fcd34d; margin-bottom: 1.25rem; }
  .modal-actions { display: flex; justify-content: flex-end; gap: 0.6rem; }

  @media (max-width: 600px) {
    .row-2 { grid-template-columns: 1fr; gap: 0; }
    .srv-row { flex-direction: column; }
    .srv-actions { justify-content: flex-start; }
  }
</style>
