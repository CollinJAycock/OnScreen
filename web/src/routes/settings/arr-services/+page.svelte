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
  import ArrHealthPanel from './ArrHealthPanel.svelte';
  import { t, tr } from '$lib/i18n/index.svelte';

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
      error = e instanceof Error ? e.message : t('settings.arr.loadFailed');
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
      toast.success(t('settings.arr.connectedToast', { app: probeResult.app_name ?? formKind, version: probeResult.version ?? '' }));
    } catch (e: unknown) {
      probeError = e instanceof Error ? e.message : t('settings.arr.probeFailed');
      probeResult = null;
    } finally {
      probing = false;
    }
  }

  async function save() {
    if (!formMode) return;
    if (!formName.trim()) { toast.error(t('settings.arr.nameRequired')); return; }
    if (!formBaseURL.trim()) { toast.error(t('settings.arr.baseUrlRequired')); return; }
    if (formMode.kind === 'create' && !formAPIKey.trim()) { toast.error(t('settings.arr.apiKeyRequired')); return; }

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
        toast.success(t('settings.arr.added'));
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
        toast.success(t('settings.arr.updated'));
      }
      closeForm();
      await load();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : t('settings.arr.saveFailed'));
    } finally {
      saving = false;
    }
  }

  async function setDefault(s: ArrService) {
    try {
      await arrServicesApi.setDefault(s.id);
      toast.success(t('settings.arr.nowDefault', { name: s.name, kind: s.kind }));
      await load();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : t('settings.arr.setDefaultFailed'));
    }
  }

  async function toggleEnabled(s: ArrService) {
    try {
      await arrServicesApi.update(s.id, { enabled: !s.enabled });
      await load();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : t('settings.arr.toggleFailed'));
    }
  }

  async function confirmDelete() {
    if (!deleteId) return;
    try {
      await arrServicesApi.del(deleteId);
      toast.success(t('settings.arr.deleted'));
      deleteId = null;
      await load();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : t('settings.arr.deleteFailed'));
    }
  }

  function profileLabel(p: ArrQualityProfile) { return p.name; }
  function rootLabel(f: ArrRootFolder) { return f.path; }
</script>

<svelte:head><title>{$tr('settings.arr.pageTitle')}</title></svelte:head>

<div class="page">
  <div class="header">
    <p class="intro">{$tr('settings.arr.intro')}</p>
    {#if !formMode && !loading}
      <button class="btn-add" on:click={openCreate}>{$tr('settings.arr.addService')}</button>
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
        <div class="card-title">{formMode.kind === 'create' ? $tr('settings.arr.newService') : $tr('settings.arr.editService')}</div>

        <div class="row-2">
          <div class="field">
            <label for="srv-kind">{$tr('settings.arr.kind')}</label>
            <select id="srv-kind" bind:value={formKind} disabled={formMode.kind === 'edit'}>
              <option value="radarr">{$tr('settings.arr.radarrMovies')}</option>
              <option value="sonarr">{$tr('settings.arr.sonarrShows')}</option>
            </select>
          </div>
          <div class="field">
            <label for="srv-name">{$tr('settings.arr.name')}</label>
            <input id="srv-name" type="text" bind:value={formName} placeholder={$tr('settings.arr.namePlaceholder')} />
          </div>
        </div>

        <div class="field">
          <label for="srv-url">{$tr('settings.arr.baseUrl')}</label>
          <input id="srv-url" type="url" bind:value={formBaseURL} placeholder="http://radarr.local:7878" />
        </div>

        <div class="field">
          <label for="srv-key">{$tr('settings.arr.apiKey')} {#if formMode.kind === 'edit'}<span class="optional">{$tr('settings.arr.apiKeyKeep')}</span>{/if}</label>
          <input id="srv-key" type="password" bind:value={formAPIKey} autocomplete="new-password" />
        </div>

        <div class="probe-row">
          <button class="btn ghost sm" on:click={runProbe} disabled={probing || !formBaseURL.trim() || (formMode.kind === 'create' && !formAPIKey.trim())}>
            {probing ? $tr('settings.arr.connecting') : $tr('settings.arr.testConnection')}
          </button>
          {#if probeError}
            <div class="probe-err">{probeError}</div>
          {:else if probeResult}
            <div class="probe-ok">{$tr('settings.arr.connected', { app: probeResult.app_name ?? formKind, version: probeResult.version ?? '' })}</div>
          {/if}
        </div>

        {#if probeResult}
          <div class="row-2">
            <div class="field">
              <label for="srv-qp">{$tr('settings.arr.qualityProfile')}</label>
              <select id="srv-qp" bind:value={formQualityProfileID}>
                <option value={null}>{$tr('settings.arr.noneAdminPicks')}</option>
                {#each probeResult.quality_profiles as p}
                  <option value={p.id}>{profileLabel(p)}</option>
                {/each}
              </select>
            </div>
            <div class="field">
              <label for="srv-rf">{$tr('settings.arr.rootFolder')}</label>
              <select id="srv-rf" bind:value={formRootFolder}>
                <option value={null}>{$tr('settings.arr.noneAdminPicks')}</option>
                {#each probeResult.root_folders as f}
                  <option value={f.path}>{rootLabel(f)}</option>
                {/each}
              </select>
            </div>
          </div>

          {#if formKind === 'radarr'}
            <div class="field">
              <label for="srv-min">{$tr('settings.arr.minAvailability')}</label>
              <select id="srv-min" bind:value={formMinAvail}>
                <option value="announced">{$tr('settings.arr.announced')}</option>
                <option value="inCinemas">{$tr('settings.arr.inCinemas')}</option>
                <option value="released">{$tr('settings.arr.released')}</option>
              </select>
            </div>
          {:else}
            <div class="row-2">
              <div class="field">
                <label for="srv-st">{$tr('settings.arr.seriesType')}</label>
                <select id="srv-st" bind:value={formSeriesType}>
                  <option value="standard">{$tr('settings.arr.standard')}</option>
                  <option value="daily">{$tr('settings.arr.daily')}</option>
                  <option value="anime">{$tr('settings.arr.anime')}</option>
                </select>
              </div>
              <div class="field">
                <label for="srv-lp">{$tr('settings.arr.languageProfile')}</label>
                <select id="srv-lp" bind:value={formLanguageProfileID}>
                  <option value={null}>{$tr('settings.arr.none')}</option>
                  {#each probeResult.language_profiles as l}
                    <option value={l.id}>{l.name}</option>
                  {/each}
                </select>
              </div>
            </div>
            <div class="field">
              <label class="toggle-label">
                <span>{$tr('settings.arr.seasonFolders')}</span>
                <button class="toggle" class:toggle-on={formSeasonFolder}
                        on:click={() => (formSeasonFolder = !formSeasonFolder)} type="button"
                        title={$tr('settings.arr.seasonFolders')} aria-label={$tr('settings.arr.seasonFolders')}
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
              <span>{$tr('settings.arr.enabled')}</span>
              <button class="toggle" class:toggle-on={formEnabled}
                      on:click={() => (formEnabled = !formEnabled)} type="button"
                      title={$tr('settings.arr.enabled')} aria-label={$tr('settings.arr.enabled')} aria-pressed={formEnabled}>
                <span class="toggle-knob"></span>
              </button>
            </label>
          </div>
          <div class="field">
            <label class="toggle-label">
              <span>{$tr('settings.arr.setDefaultFor', { kind: formKind })}</span>
              <button class="toggle" class:toggle-on={formIsDefault}
                      on:click={() => (formIsDefault = !formIsDefault)} type="button"
                      title={$tr('settings.arr.setDefaultFor', { kind: formKind })}
                      aria-label={$tr('settings.arr.setDefaultFor', { kind: formKind })}
                      aria-pressed={formIsDefault}>
                <span class="toggle-knob"></span>
              </button>
            </label>
          </div>
        </div>

        <div class="form-actions">
          <button class="btn ghost sm" on:click={closeForm}>{$tr('settings.arr.cancel')}</button>
          <button class="btn primary sm" on:click={save} disabled={saving}>
            {saving ? $tr('settings.arr.saving') : (formMode.kind === 'create' ? $tr('settings.arr.create') : $tr('settings.arr.save'))}
          </button>
        </div>
      </div>
    {/if}

    {#if services.length === 0 && !formMode}
      <div class="empty">
        <p>{$tr('settings.arr.empty')}</p>
        <p class="empty-sub">{$tr('settings.arr.emptySub')}</p>
        <button class="btn-add" on:click={openCreate}>{$tr('settings.arr.addService')}</button>
      </div>
    {:else}
      {#each services as s (s.id)}
        <div class="card" class:disabled-card={!s.enabled}>
          <div class="srv-row">
            <div class="srv-info">
              <div class="srv-header">
                <span class="srv-name">{s.name}</span>
                <span class="kind-pill kind-{s.kind}">{s.kind}</span>
                {#if s.is_default}<span class="default-pill">{$tr('settings.arr.defaultPill')}</span>{/if}
                <button
                  class="toggle-sm"
                  class:toggle-on={s.enabled}
                  on:click={() => toggleEnabled(s)}
                  title={s.enabled ? $tr('settings.arr.disable') : $tr('settings.arr.enable')}
                  type="button"
                >
                  <span class="toggle-knob"></span>
                </button>
              </div>
              <div class="srv-url">{s.base_url}</div>
              <div class="srv-meta">
                {s.api_key_set ? $tr('settings.arr.keyConfigured') : $tr('settings.arr.noKey')}
                {#if s.default_quality_profile_id != null}· {$tr('settings.arr.qpShort', { id: s.default_quality_profile_id })}{/if}
                {#if s.default_root_folder}· {s.default_root_folder}{/if}
              </div>
              <ArrHealthPanel serviceId={s.id} enabled={s.enabled} />
            </div>
            <div class="srv-actions">
              {#if !s.is_default}
                <button class="btn ghost sm" on:click={() => setDefault(s)}>{$tr('settings.arr.setDefault')}</button>
              {/if}
              <button class="btn ghost sm" on:click={() => openEdit(s)}>{$tr('settings.arr.edit')}</button>
              <button class="btn danger sm" on:click={() => (deleteId = s.id)}>{$tr('settings.arr.delete')}</button>
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
      <div class="modal" on:click|stopPropagation role="dialog" aria-label={$tr('settings.arr.confirmDelete')}>
        <p class="modal-text">{$tr('settings.arr.deleteQuestion')}</p>
        <p class="modal-sub">{target?.name ?? ''}</p>
        <p class="modal-warn">{$tr('settings.arr.deleteWarn')}</p>
        <div class="modal-actions">
          <button class="btn ghost sm" on:click={() => (deleteId = null)}>{$tr('settings.arr.cancel')}</button>
          <button class="btn danger sm" on:click={confirmDelete}>{$tr('settings.arr.delete')}</button>
        </div>
      </div>
    </div>
  {/if}
</div>

<style>
  .page { max-width: 720px; }

  .header { display: flex; align-items: flex-start; justify-content: space-between; gap: 1rem; margin-bottom: 1.5rem; }
  .intro { font-size: 0.78rem; color: var(--text-muted); line-height: 1.5; margin: 0; flex: 1; }

  .banner { padding: 0.6rem 0.9rem; border-radius: 7px; font-size: 0.8rem; margin-bottom: 1.25rem; }
  .banner.error { background: var(--error-bg); border: 1px solid var(--error); color: var(--error); }

  .skeleton-block {
    height: 100px; border-radius: 10px;
    background: linear-gradient(90deg, var(--bg-elevated) 25%, var(--bg-hover) 50%, var(--bg-elevated) 75%);
    background-size: 200% 100%; animation: shimmer 1.4s infinite;
  }
  @keyframes shimmer { 0% { background-position: 200% 0; } 100% { background-position: -200% 0; } }

  .card {
    background: var(--bg-elevated);
    border: 1px solid var(--border);
    border-radius: 10px;
    padding: 1.1rem 1.25rem;
    margin-bottom: 0.75rem;
  }
  .card.disabled-card { opacity: 0.55; }
  .card-title { font-size: 0.78rem; font-weight: 700; color: var(--accent-text); margin-bottom: 1rem; text-transform: uppercase; letter-spacing: 0.06em; }
  .form-card { border-color: var(--accent); }

  .field { display: flex; flex-direction: column; gap: 0.3rem; margin-bottom: 1rem; }
  .row-2 { display: grid; grid-template-columns: 1fr 1fr; gap: 1rem; }
  label { font-size: 0.75rem; font-weight: 500; color: var(--text-muted); }
  .optional { font-weight: 400; }
  input, select { background: var(--input-bg); border: 1px solid var(--border-strong); border-radius: 7px; padding: 0.48rem 0.7rem; font-size: 0.85rem; color: var(--text-primary); width: 100%; }
  input:focus, select:focus { outline: none; border-color: var(--accent); box-shadow: 0 0 0 3px var(--accent-bg); }

  .probe-row { display: flex; align-items: center; gap: 0.75rem; margin: 0.5rem 0 1rem; flex-wrap: wrap; }
  .probe-err { font-size: 0.78rem; color: var(--error); }
  .probe-ok  { font-size: 0.78rem; color: var(--success); }

  .form-actions { display: flex; justify-content: flex-end; gap: 0.6rem; margin-top: 0.5rem; }

  .btn { display: inline-flex; align-items: center; justify-content: center; padding: 0.45rem 0.85rem; font-size: 0.8rem; font-weight: 600; border-radius: 7px; cursor: pointer; border: 1px solid transparent; text-decoration: none; transition: background 0.12s, color 0.12s, border-color 0.12s; }
  .btn.sm { padding: 0.38rem 0.7rem; font-size: 0.76rem; }
  .btn.primary { background: var(--accent); color: #fff; }
  .btn.primary:hover:not(:disabled) { background: var(--accent-hover); }
  .btn.ghost { background: transparent; border-color: var(--border-strong); color: var(--text-secondary); }
  .btn.ghost:hover:not(:disabled) { background: var(--bg-hover); }
  .btn.danger { background: var(--error-bg); color: var(--error); border-color: var(--error); }
  .btn.danger:hover { background: var(--error-bg); border-color: var(--error); }
  .btn:disabled { opacity: 0.55; cursor: not-allowed; }

  .btn-add { padding: 0.45rem 0.9rem; background: var(--accent); border: none; border-radius: 7px; color: #fff; font-size: 0.8rem; font-weight: 600; cursor: pointer; }
  .btn-add:hover { background: var(--accent-hover); }

  .srv-row { display: flex; align-items: flex-start; justify-content: space-between; gap: 1rem; }
  .srv-info { flex: 1; min-width: 0; }
  .srv-header { display: flex; align-items: center; gap: 0.6rem; margin-bottom: 0.35rem; flex-wrap: wrap; }
  .srv-name { font-size: 0.92rem; font-weight: 600; color: var(--text-primary); }
  .srv-url { font-size: 0.78rem; font-family: monospace; color: var(--text-secondary); word-break: break-all; margin-bottom: 0.4rem; }
  .srv-meta { font-size: 0.72rem; color: var(--text-muted); }
  .srv-actions { display: flex; gap: 0.4rem; flex-shrink: 0; flex-wrap: wrap; justify-content: flex-end; }

  .kind-pill { font-size: 0.62rem; font-weight: 600; padding: 0.15rem 0.5rem; border-radius: 4px; text-transform: uppercase; letter-spacing: 0.05em; }
  .kind-radarr { background: rgba(251,191,36,0.12); color: #fcd34d; }
  .kind-sonarr { background: var(--info-bg); color: var(--info); }
  .default-pill { font-size: 0.62rem; padding: 0.15rem 0.5rem; border-radius: 4px; background: var(--success-bg); color: var(--success); text-transform: uppercase; letter-spacing: 0.05em; }

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

  .modal-overlay { position: fixed; inset: 0; background: var(--shadow); backdrop-filter: blur(4px); display: flex; align-items: center; justify-content: center; z-index: 1000; }
  .modal { background: var(--bg-elevated); border: 1px solid var(--border); border-radius: 12px; padding: 1.5rem; max-width: 400px; width: 90%; }
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
