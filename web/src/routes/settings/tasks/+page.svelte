<script lang="ts">
  import { onMount } from 'svelte';
  import {
    tasksApi,
    libraryApi,
    type ScheduledTask,
    type TaskRun,
    type Library
  } from '$lib/api';
  import { toast } from '$lib/stores/toast';
  // Aliased: `t` is the loop/param name for a task all over this file.
  import { t as translate, i18n, type MessageKey, type Translator } from '$lib/i18n/index.svelte';

  type CronPreset = { label: MessageKey; value: string };
  const CRON_PRESETS: CronPreset[] = [
    { label: 'settings.tasks.cronEvery5m', value: '*/5 * * * *' },
    { label: 'settings.tasks.cronEvery15m', value: '*/15 * * * *' },
    { label: 'settings.tasks.cronHourly', value: '0 * * * *' },
    { label: 'settings.tasks.cronMidnight', value: '0 0 * * *' },
    { label: 'settings.tasks.cronDaily3', value: '0 3 * * *' },
    { label: 'settings.tasks.cronWeekly', value: '0 3 * * 0' },
    { label: 'settings.tasks.cronMonthly', value: '0 3 1 * *' },
    { label: 'settings.tasks.cronCustom', value: '' }
  ];

  let loading = true;
  let error = '';
  let tasks: ScheduledTask[] = [];
  let taskTypes: string[] = [];
  let libraries: Library[] = [];

  type FormState = {
    name: string;
    taskType: string;
    cronPreset: string;
    cronCustom: string;
    enabled: boolean;
    // typed config
    outputDir: string;
    retainCount: number;
    libraryId: string;
    skipExisting: boolean;
    // raw fallback
    configJson: string;
  };

  function newFormState(): FormState {
    return {
      name: '',
      taskType: '',
      cronPreset: '0 3 * * *',
      cronCustom: '',
      enabled: true,
      outputDir: '/var/backups/onscreen',
      retainCount: 7,
      libraryId: 'all',
      skipExisting: true,
      configJson: '{}'
    };
  }

  // Add form
  let showAdd = false;
  let addForm = newFormState();
  let addSaving = false;

  // Edit
  let editId: string | null = null;
  let editForm = newFormState();
  let editSaving = false;

  // Delete
  let deleteId: string | null = null;

  // Run-now in flight
  let runningId: string | null = null;

  // Runs viewer
  let runsTaskId: string | null = null;
  let runs: TaskRun[] = [];
  let runsLoading = false;

  onMount(async () => {
    await Promise.all([loadTasks(), loadTypes(), loadLibraries()]);
  });

  async function loadTasks() {
    loading = true;
    error = '';
    try {
      tasks = (await tasksApi.list()) ?? [];
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : translate('settings.tasks.loadFailed');
    } finally {
      loading = false;
    }
  }

  async function loadTypes() {
    try {
      taskTypes = (await tasksApi.types()) ?? [];
    } catch {
      taskTypes = [];
    }
  }

  async function loadLibraries() {
    try {
      libraries = (await libraryApi.list()) ?? [];
    } catch {
      libraries = [];
    }
  }

  function effectiveCron(f: FormState): string {
    return f.cronPreset === '' ? f.cronCustom.trim() : f.cronPreset;
  }

  function buildConfig(f: FormState): Record<string, unknown> | null {
    if (f.taskType === 'backup_database') {
      const cfg: Record<string, unknown> = {};
      if (f.outputDir.trim()) cfg.output_dir = f.outputDir.trim();
      if (f.retainCount > 0) cfg.retain_count = f.retainCount;
      return cfg;
    }
    if (f.taskType === 'scan_library') {
      return f.libraryId === 'all' ? {} : { library_id: f.libraryId };
    }
    if (f.taskType === 'ocr_subtitles') {
      const cfg: Record<string, unknown> = { skip_existing: f.skipExisting };
      if (f.libraryId !== 'all') cfg.library_id = f.libraryId;
      return cfg;
    }
    // Fallback: parse JSON textarea.
    try {
      const parsed = JSON.parse(f.configJson || '{}');
      if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
        return parsed as Record<string, unknown>;
      }
      return null;
    } catch {
      return null;
    }
  }

  function hydrateForm(t: ScheduledTask): FormState {
    const f = newFormState();
    f.name = t.name;
    f.taskType = t.task_type;
    f.enabled = t.enabled;

    const presetMatch = CRON_PRESETS.find(p => p.value === t.cron_expr && p.value !== '');
    if (presetMatch) {
      f.cronPreset = presetMatch.value;
      f.cronCustom = '';
    } else {
      f.cronPreset = '';
      f.cronCustom = t.cron_expr;
    }

    const cfg = t.config ?? {};
    if (t.task_type === 'backup_database') {
      f.outputDir = (cfg.output_dir as string) ?? '';
      f.retainCount = (cfg.retain_count as number) ?? 7;
    } else if (t.task_type === 'scan_library') {
      f.libraryId = (cfg.library_id as string) ?? 'all';
    } else if (t.task_type === 'ocr_subtitles') {
      f.libraryId = (cfg.library_id as string) ?? 'all';
      f.skipExisting = (cfg.skip_existing as boolean) ?? true;
    }
    f.configJson = JSON.stringify(cfg, null, 2);
    return f;
  }

  function openAdd() {
    addForm = newFormState();
    if (taskTypes.length > 0) addForm.taskType = taskTypes[0];
    showAdd = true;
    error = '';
  }

  function cancelAdd() {
    showAdd = false;
  }

  async function submitAdd() {
    if (!addForm.name.trim()) { error = translate('settings.tasks.nameRequired'); return; }
    if (!addForm.taskType) { error = translate('settings.tasks.typeRequired'); return; }
    const cron = effectiveCron(addForm);
    if (!cron) { error = translate('settings.tasks.cronRequired'); return; }
    const config = buildConfig(addForm);
    if (config === null) { error = translate('settings.tasks.configInvalid'); return; }
    error = '';
    addSaving = true;
    try {
      await tasksApi.create({
        name: addForm.name.trim(),
        task_type: addForm.taskType,
        cron_expr: cron,
        config,
        enabled: addForm.enabled
      });
      showAdd = false;
      toast.success(translate('settings.tasks.created'));
      await loadTasks();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : translate('settings.tasks.createFailed'));
    } finally {
      addSaving = false;
    }
  }

  function startEdit(t: ScheduledTask) {
    editForm = hydrateForm(t);
    editId = t.id;
    error = '';
  }

  function cancelEdit() {
    editId = null;
  }

  async function submitEdit() {
    if (!editId) return;
    if (!editForm.name.trim()) { error = translate('settings.tasks.nameRequired'); return; }
    const cron = effectiveCron(editForm);
    if (!cron) { error = translate('settings.tasks.cronRequired'); return; }
    const config = buildConfig(editForm);
    if (config === null) { error = translate('settings.tasks.configInvalid'); return; }
    error = '';
    editSaving = true;
    try {
      await tasksApi.update(editId, {
        name: editForm.name.trim(),
        task_type: editForm.taskType,
        cron_expr: cron,
        config,
        enabled: editForm.enabled
      });
      editId = null;
      toast.success(translate('settings.tasks.updated'));
      await loadTasks();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : translate('settings.tasks.updateFailed'));
    } finally {
      editSaving = false;
    }
  }

  async function confirmDelete() {
    if (!deleteId) return;
    try {
      await tasksApi.del(deleteId);
      deleteId = null;
      toast.success(translate('settings.tasks.deleted'));
      await loadTasks();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : translate('settings.tasks.deleteFailed'));
    }
  }

  async function runNow(t: ScheduledTask) {
    runningId = t.id;
    try {
      await tasksApi.runNow(t.id);
      toast.success(translate('settings.tasks.queued', { seconds: 30 }));
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : translate('settings.tasks.queueFailed'));
    } finally {
      runningId = null;
    }
  }

  async function toggleEnabled(t: ScheduledTask) {
    try {
      await tasksApi.update(t.id, { enabled: !t.enabled });
      toast.info(translate(!t.enabled ? 'settings.tasks.enabledToast' : 'settings.tasks.disabledToast'));
      await loadTasks();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : translate('settings.tasks.updateFailed'));
    }
  }

  async function viewRuns(t: ScheduledTask) {
    if (runsTaskId === t.id) {
      runsTaskId = null;
      return;
    }
    runsTaskId = t.id;
    runsLoading = true;
    runs = [];
    try {
      runs = (await tasksApi.runs(t.id, 25)) ?? [];
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : translate('settings.tasks.runsFailed'));
    } finally {
      runsLoading = false;
    }
  }

  function formatTime(iso: string | null | undefined): string {
    if (!iso) return '—';
    const d = new Date(iso);
    if (Number.isNaN(d.getTime())) return '—';
    return d.toLocaleString();
  }

  function relative(iso: string | null | undefined, tx: Translator): string {
    if (!iso) return '';
    const d = new Date(iso);
    if (Number.isNaN(d.getTime())) return '';
    const diff = d.getTime() - Date.now();
    const abs = Math.abs(diff);
    const sec = Math.round(abs / 1000);
    const min = Math.round(sec / 60);
    const hr = Math.round(min / 60);
    const day = Math.round(hr / 24);
    let val: string;
    if (sec < 60) val = tx('settings.tasks.unitSec', { n: sec });
    else if (min < 60) val = tx('settings.tasks.unitMin', { n: min });
    else if (hr < 24) val = tx('settings.tasks.unitHour', { n: hr });
    else val = tx('settings.tasks.unitDay', { n: day });
    return diff > 0 ? tx('settings.tasks.relIn', { value: val }) : tx('settings.tasks.relAgo', { value: val });
  }

  function durationMs(start: string, end: string | null, tx: Translator): string {
    if (!end) return tx('settings.tasks.runningDur');
    const ms = new Date(end).getTime() - new Date(start).getTime();
    if (Number.isNaN(ms) || ms < 0) return '—';
    if (ms < 1000) return `${ms} ms`;
    if (ms < 60000) return `${(ms / 1000).toFixed(1)} s`;
    return `${Math.round(ms / 1000)} s`;
  }

  function statusClass(s: string): string {
    if (s === 'success') return 'status-success';
    if (s === 'failed' || s === 'panic') return 'status-failed';
    if (s === 'running') return 'status-running';
    return 'status-idle';
  }

  // The run statuses the server reports, in words; anything else shows as sent.
  const STATUS_LABELS: Record<string, MessageKey> = {
    success: 'settings.tasks.statusSuccess',
    failed: 'settings.tasks.statusFailed',
    panic: 'settings.tasks.statusPanic',
    running: 'settings.tasks.statusRunning',
  };
  function statusLabel(s: string, tx: Translator): string {
    return STATUS_LABELS[s] ? tx(STATUS_LABELS[s]) : s;
  }
</script>

<svelte:head><title>{$i18n('settings.tasks.pageTitle')}</title></svelte:head>

<div class="page">
  <div class="header">
    <div class="header-left"></div>
    {#if !showAdd && !loading}
      <button class="btn-add" on:click={openAdd}>
        <svg viewBox="0 0 20 20" fill="currentColor" width="14" height="14">
          <path d="M10.75 4.75a.75.75 0 00-1.5 0v4.5h-4.5a.75.75 0 000 1.5h4.5v4.5a.75.75 0 001.5 0v-4.5h4.5a.75.75 0 000-1.5h-4.5v-4.5z"/>
        </svg>
        {$i18n('settings.tasks.newTask')}
      </button>
    {/if}
  </div>

  {#if error}
    <div class="banner error">{error}</div>
  {/if}

  {#if loading}
    <div class="skeleton-block"></div>
  {:else}
    {#if showAdd}
      <div class="card form-card">
        <div class="card-title">{$i18n('settings.tasks.newTask')}</div>

        <div class="field">
          <label for="add-name">{$i18n('settings.tasks.name')}</label>
          <input id="add-name" type="text" bind:value={addForm.name} placeholder={$i18n('settings.tasks.namePlaceholder')} autocomplete="off" />
        </div>

        <div class="field">
          <label for="add-type">{$i18n('settings.tasks.type')}</label>
          <select id="add-type" bind:value={addForm.taskType}>
            {#each taskTypes as t}
              <option value={t}>{t}</option>
            {/each}
          </select>
        </div>

        <div class="field">
          <label for="add-cron">{$i18n('settings.tasks.schedule')}</label>
          <select id="add-cron" bind:value={addForm.cronPreset}>
            {#each CRON_PRESETS as p}
              <option value={p.value}>{$i18n(p.label)}{p.value ? ` (${p.value})` : ''}</option>
            {/each}
          </select>
          {#if addForm.cronPreset === ''}
            <input
              type="text"
              bind:value={addForm.cronCustom}
              placeholder="*/10 * * * *"
              class="cron-input"
              autocomplete="off"
              spellcheck="false"
            />
            <div class="hint">{$i18n('settings.tasks.cronHint')}</div>
          {/if}
        </div>

        {#if addForm.taskType === 'backup_database'}
          <div class="field">
            <label for="add-output-dir">{$i18n('settings.tasks.outputDir')}</label>
            <input
              id="add-output-dir"
              type="text"
              bind:value={addForm.outputDir}
              placeholder="/var/backups/onscreen"
              autocomplete="off"
              spellcheck="false"
            />
          </div>
          <div class="field">
            <label for="add-retain">{$i18n('settings.tasks.retain')}</label>
            <input id="add-retain" type="number" min="1" bind:value={addForm.retainCount} />
            <div class="hint">{$i18n('settings.tasks.retainHint')}</div>
          </div>
        {:else if addForm.taskType === 'scan_library'}
          <div class="field">
            <label for="add-library">{$i18n('settings.tasks.library')}</label>
            <select id="add-library" bind:value={addForm.libraryId}>
              <option value="all">{$i18n('settings.tasks.allLibraries')}</option>
              {#each libraries as lib}
                <option value={lib.id}>{lib.name}</option>
              {/each}
            </select>
          </div>
        {:else if addForm.taskType === 'ocr_subtitles'}
          <div class="field">
            <label for="add-ocr-library">{$i18n('settings.tasks.library')}</label>
            <select id="add-ocr-library" bind:value={addForm.libraryId}>
              <option value="all">{$i18n('settings.tasks.allLibraries')}</option>
              {#each libraries as lib}
                <option value={lib.id}>{lib.name}</option>
              {/each}
            </select>
            <div class="hint">{$i18n('settings.tasks.ocrHint')}</div>
          </div>
          <div class="field">
            <label class="toggle-label">
              <span>{$i18n('settings.tasks.skipOcr')}</span>
              <button
                class="toggle"
                class:toggle-on={addForm.skipExisting}
                on:click={() => addForm.skipExisting = !addForm.skipExisting}
                type="button"
                title={$i18n('settings.tasks.skipOcr')}
                aria-label={$i18n('settings.tasks.skipOcr')}
                aria-pressed={addForm.skipExisting}
              >
                <span class="toggle-knob"></span>
              </button>
            </label>
          </div>
        {:else if addForm.taskType}
          <div class="field">
            <label for="add-config">{$i18n('settings.tasks.config')}</label>
            <textarea id="add-config" bind:value={addForm.configJson} rows="5" spellcheck="false"></textarea>
          </div>
        {/if}

        <div class="field">
          <label class="toggle-label">
            <span>{$i18n('settings.tasks.enabled')}</span>
            <button
              class="toggle"
              class:toggle-on={addForm.enabled}
              on:click={() => addForm.enabled = !addForm.enabled}
              type="button"
              title={$i18n('settings.tasks.enabled')}
              aria-label={$i18n('settings.tasks.enabled')}
              aria-pressed={addForm.enabled}
            >
              <span class="toggle-knob"></span>
            </button>
          </label>
        </div>

        <div class="form-actions">
          <button class="btn-cancel" on:click={cancelAdd}>{$i18n('settings.tasks.cancel')}</button>
          <button class="btn-save" on:click={submitAdd} disabled={addSaving}>
            {addSaving ? $i18n('settings.tasks.creating') : $i18n('settings.tasks.create')}
          </button>
        </div>
      </div>
    {/if}

    {#if tasks.length === 0 && !showAdd}
      <div class="empty">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" width="40" height="40">
          <path stroke-linecap="round" stroke-linejoin="round" d="M12 6v6l3.75 3.75M21 12a9 9 0 11-18 0 9 9 0 0118 0z"/>
        </svg>
        <p>{$i18n('settings.tasks.empty')}</p>
        <p class="empty-sub">{$i18n('settings.tasks.emptySub')}</p>
        <button class="btn-add" on:click={openAdd}>
          <svg viewBox="0 0 20 20" fill="currentColor" width="14" height="14">
            <path d="M10.75 4.75a.75.75 0 00-1.5 0v4.5h-4.5a.75.75 0 000 1.5h4.5v4.5a.75.75 0 001.5 0v-4.5h4.5a.75.75 0 000-1.5h-4.5v-4.5z"/>
          </svg>
          {$i18n('settings.tasks.newTask')}
        </button>
      </div>
    {:else}
      {#each tasks as t (t.id)}
        <div class="card" class:disabled-card={!t.enabled}>
          {#if editId === t.id}
            <div class="card-title">{$i18n('settings.tasks.editTask')}</div>

            <div class="field">
              <label for="edit-name">{$i18n('settings.tasks.name')}</label>
              <input id="edit-name" type="text" bind:value={editForm.name} autocomplete="off" />
            </div>

            <div class="field">
              <label for="edit-type">{$i18n('settings.tasks.type')}</label>
              <select id="edit-type" bind:value={editForm.taskType}>
                {#each taskTypes as tt}
                  <option value={tt}>{tt}</option>
                {/each}
              </select>
            </div>

            <div class="field">
              <label for="edit-cron">{$i18n('settings.tasks.schedule')}</label>
              <select id="edit-cron" bind:value={editForm.cronPreset}>
                {#each CRON_PRESETS as p}
                  <option value={p.value}>{$i18n(p.label)}{p.value ? ` (${p.value})` : ''}</option>
                {/each}
              </select>
              {#if editForm.cronPreset === ''}
                <input
                  type="text"
                  bind:value={editForm.cronCustom}
                  placeholder="*/10 * * * *"
                  class="cron-input"
                  autocomplete="off"
                  spellcheck="false"
                />
              {/if}
            </div>

            {#if editForm.taskType === 'backup_database'}
              <div class="field">
                <label for="edit-output-dir">{$i18n('settings.tasks.outputDir')}</label>
                <input id="edit-output-dir" type="text" bind:value={editForm.outputDir} autocomplete="off" spellcheck="false" />
              </div>
              <div class="field">
                <label for="edit-retain">{$i18n('settings.tasks.retain')}</label>
                <input id="edit-retain" type="number" min="1" bind:value={editForm.retainCount} />
              </div>
            {:else if editForm.taskType === 'scan_library'}
              <div class="field">
                <label for="edit-library">{$i18n('settings.tasks.library')}</label>
                <select id="edit-library" bind:value={editForm.libraryId}>
                  <option value="all">{$i18n('settings.tasks.allLibraries')}</option>
                  {#each libraries as lib}
                    <option value={lib.id}>{lib.name}</option>
                  {/each}
                </select>
              </div>
            {:else if editForm.taskType === 'ocr_subtitles'}
              <div class="field">
                <label for="edit-ocr-library">{$i18n('settings.tasks.library')}</label>
                <select id="edit-ocr-library" bind:value={editForm.libraryId}>
                  <option value="all">{$i18n('settings.tasks.allLibraries')}</option>
                  {#each libraries as lib}
                    <option value={lib.id}>{lib.name}</option>
                  {/each}
                </select>
              </div>
              <div class="field">
                <label class="toggle-label">
                  <span>{$i18n('settings.tasks.skipOcr')}</span>
                  <button
                    class="toggle"
                    class:toggle-on={editForm.skipExisting}
                    on:click={() => editForm.skipExisting = !editForm.skipExisting}
                    type="button"
                    title={$i18n('settings.tasks.skipOcr')}
                    aria-label={$i18n('settings.tasks.skipOcr')}
                    aria-pressed={editForm.skipExisting}
                  >
                    <span class="toggle-knob"></span>
                  </button>
                </label>
              </div>
            {:else}
              <div class="field">
                <label for="edit-config">{$i18n('settings.tasks.config')}</label>
                <textarea id="edit-config" bind:value={editForm.configJson} rows="5" spellcheck="false"></textarea>
              </div>
            {/if}

            <div class="field">
              <label class="toggle-label">
                <span>{$i18n('settings.tasks.enabled')}</span>
                <button
                  class="toggle"
                  class:toggle-on={editForm.enabled}
                  on:click={() => editForm.enabled = !editForm.enabled}
                  type="button"
                  title={$i18n('settings.tasks.enabled')}
                  aria-label={$i18n('settings.tasks.enabled')}
                  aria-pressed={editForm.enabled}
                >
                  <span class="toggle-knob"></span>
                </button>
              </label>
            </div>

            <div class="form-actions">
              <button class="btn-cancel" on:click={cancelEdit}>{$i18n('settings.tasks.cancel')}</button>
              <button class="btn-save" on:click={submitEdit} disabled={editSaving}>
                {editSaving ? $i18n('settings.tasks.saving') : $i18n('settings.tasks.save')}
              </button>
            </div>
          {:else}
            <div class="task-row">
              <div class="task-info">
                <div class="task-head">
                  <span class="task-name">{t.name}</span>
                  <span class="type-badge">{t.task_type}</span>
                  <button
                    class="toggle-sm"
                    class:toggle-on={t.enabled}
                    on:click={() => toggleEnabled(t)}
                    title={t.enabled ? $i18n('settings.tasks.disable') : $i18n('settings.tasks.enable')}
                    type="button"
                  >
                    <span class="toggle-knob"></span>
                  </button>
                </div>
                <div class="task-meta">
                  <span class="meta-item"><span class="meta-key">cron</span> <code>{t.cron_expr}</code></span>
                  <span class="meta-item"><span class="meta-key">{$i18n('settings.tasks.next')}</span> {formatTime(t.next_run_at)} <span class="meta-rel">{relative(t.next_run_at, $i18n)}</span></span>
                  <span class="meta-item"><span class="meta-key">{$i18n('settings.tasks.last')}</span> {formatTime(t.last_run_at)} {#if t.last_status}<span class="status {statusClass(t.last_status)}">{statusLabel(t.last_status, $i18n)}</span>{/if}</span>
                </div>
                {#if t.last_error}
                  <div class="last-error">{t.last_error}</div>
                {/if}
              </div>

              <div class="task-actions">
                <button class="btn-icon" title={$i18n('settings.tasks.runNow')} on:click={() => runNow(t)} disabled={runningId === t.id}>
                  {#if runningId === t.id}
                    <svg class="spinner" viewBox="0 0 20 20" width="15" height="15"><circle cx="10" cy="10" r="7" fill="none" stroke="currentColor" stroke-width="2" stroke-dasharray="30 14"/></svg>
                  {:else}
                    <svg viewBox="0 0 20 20" fill="currentColor" width="15" height="15">
                      <path d="M6.3 2.84A1 1 0 005 3.7v12.6a1 1 0 001.5.86l11-6.3a1 1 0 000-1.72l-11-6.3z"/>
                    </svg>
                  {/if}
                </button>
                <button class="btn-icon" title={$i18n('settings.tasks.runHistory')} on:click={() => viewRuns(t)} class:active={runsTaskId === t.id}>
                  <svg viewBox="0 0 20 20" fill="currentColor" width="15" height="15">
                    <path fill-rule="evenodd" d="M10 18a8 8 0 100-16 8 8 0 000 16zM10 5a.75.75 0 01.75.75v4.5h3a.75.75 0 010 1.5H10a.75.75 0 01-.75-.75v-5.25A.75.75 0 0110 5z" clip-rule="evenodd"/>
                  </svg>
                </button>
                <button class="btn-icon" title={$i18n('settings.tasks.edit')} on:click={() => startEdit(t)}>
                  <svg viewBox="0 0 20 20" fill="currentColor" width="15" height="15">
                    <path d="M2.695 14.763l-1.262 3.154a.5.5 0 00.65.65l3.155-1.262a4 4 0 001.343-.885L17.5 5.5a2.121 2.121 0 00-3-3L3.58 13.42a4 4 0 00-.885 1.343z"/>
                  </svg>
                </button>
                <button class="btn-icon btn-danger" title={$i18n('settings.tasks.delete')} on:click={() => deleteId = t.id}>
                  <svg viewBox="0 0 20 20" fill="currentColor" width="15" height="15">
                    <path fill-rule="evenodd" d="M8.75 1A2.75 2.75 0 006 3.75v.443c-.795.077-1.584.176-2.365.298a.75.75 0 10.23 1.482l.149-.022.841 10.518A2.75 2.75 0 007.596 19h4.807a2.75 2.75 0 002.742-2.53l.841-10.519.149.023a.75.75 0 00.23-1.482A41.03 41.03 0 0014 4.193V3.75A2.75 2.75 0 0011.25 1h-2.5zM10 4c.84 0 1.673.025 2.5.075V3.75c0-.69-.56-1.25-1.25-1.25h-2.5c-.69 0-1.25.56-1.25 1.25v.325C8.327 4.025 9.16 4 10 4zM8.58 7.72a.75.75 0 00-1.5.06l.3 7.5a.75.75 0 101.5-.06l-.3-7.5zm4.34.06a.75.75 0 10-1.5-.06l-.3 7.5a.75.75 0 101.5.06l.3-7.5z" clip-rule="evenodd"/>
                  </svg>
                </button>
              </div>
            </div>

            {#if runsTaskId === t.id}
              <div class="runs-panel">
                <div class="runs-head">{$i18n('settings.tasks.recentRuns')}</div>
                {#if runsLoading}
                  <div class="runs-empty">{$i18n('settings.tasks.loading')}</div>
                {:else if runs.length === 0}
                  <div class="runs-empty">{$i18n('settings.tasks.noRuns')}</div>
                {:else}
                  <div class="runs-list">
                    {#each runs as r}
                      <div class="run-row">
                        <span class="run-status status {statusClass(r.status)}">{statusLabel(r.status, $i18n)}</span>
                        <span class="run-time">{formatTime(r.started_at)}</span>
                        <span class="run-dur">{durationMs(r.started_at, r.ended_at, $i18n)}</span>
                        {#if r.error}
                          <span class="run-error" title={r.error}>{r.error}</span>
                        {:else if r.output}
                          <span class="run-output" title={r.output}>{r.output}</span>
                        {/if}
                      </div>
                    {/each}
                  </div>
                {/if}
              </div>
            {/if}
          {/if}
        </div>

        {#if deleteId === t.id}
          <div class="modal-overlay" on:click={() => deleteId = null} on:keydown={e => e.key === 'Escape' && (deleteId = null)} role="button" tabindex="-1">
            <!-- svelte-ignore a11y_click_events_have_key_events -->
            <div class="modal" on:click|stopPropagation role="dialog" aria-label={$i18n('settings.tasks.confirmDelete')}>
              <p class="modal-text">{$i18n('settings.tasks.deleteConfirm')}</p>
              <p class="modal-sub">{t.name}</p>
              <div class="modal-actions">
                <button class="btn-cancel" on:click={() => deleteId = null}>{$i18n('settings.tasks.cancel')}</button>
                <button class="btn-delete" on:click={confirmDelete}>{$i18n('settings.tasks.delete')}</button>
              </div>
            </div>
          </div>
        {/if}
      {/each}
    {/if}
  {/if}
</div>

<style>
  .page { max-width: 720px; }

  .header {
    display: flex;
    align-items: flex-end;
    justify-content: space-between;
    margin-bottom: 1.75rem;
    gap: 1rem;
  }
  .header-left { display: flex; flex-direction: column; gap: 0.5rem; }

  .banner {
    padding: 0.6rem 0.9rem;
    border-radius: 7px;
    font-size: 0.8rem;
    margin-bottom: 1.25rem;
  }
  .banner.error { background: var(--error-bg); border: 1px solid var(--error); color: var(--error); }

  .skeleton-block {
    height: 100px; border-radius: 10px;
    background: linear-gradient(90deg, var(--bg-elevated) 25%, var(--bg-hover) 50%, var(--bg-elevated) 75%);
    background-size: 200% 100%;
    animation: shimmer 1.4s infinite;
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
  .card-title {
    font-size: 0.78rem;
    font-weight: 700;
    color: var(--accent-text);
    margin-bottom: 1rem;
    text-transform: uppercase;
    letter-spacing: 0.06em;
  }
  .form-card { border-color: var(--accent); }

  .field { display: flex; flex-direction: column; gap: 0.3rem; margin-bottom: 1rem; }
  label { font-size: 0.72rem; font-weight: 500; color: var(--text-muted); }

  input[type="text"],
  input[type="number"],
  select,
  textarea,
  .cron-input {
    background: var(--input-bg);
    border: 1px solid var(--border-strong);
    border-radius: 7px;
    padding: 0.48rem 0.7rem;
    font-size: 0.85rem;
    color: var(--text-primary);
    transition: border-color 0.15s;
    width: 100%;
    font-family: inherit;
  }
  textarea, .cron-input { font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, monospace; }
  input:focus, select:focus, textarea:focus {
    outline: none;
    border-color: var(--accent);
    box-shadow: 0 0 0 3px var(--accent-bg);
  }
  ::placeholder { color: var(--text-muted); }

  .hint {
    font-size: 0.72rem;
    color: var(--text-muted);
    line-height: 1.5;
    margin-top: 0.15rem;
  }

  .form-actions {
    display: flex;
    justify-content: flex-end;
    gap: 0.6rem;
    margin-top: 0.5rem;
  }
  .btn-save {
    padding: 0.45rem 0.9rem;
    background: var(--accent);
    border: none;
    border-radius: 7px;
    color: #fff;
    font-size: 0.8rem;
    font-weight: 600;
    cursor: pointer;
    transition: background 0.15s;
  }
  .btn-save:hover { background: var(--accent-hover); }
  .btn-save:disabled { opacity: 0.5; cursor: not-allowed; }

  .btn-cancel {
    padding: 0.45rem 0.9rem;
    background: transparent;
    border: 1px solid var(--border-strong);
    border-radius: 7px;
    color: var(--text-secondary);
    font-size: 0.78rem;
    font-weight: 500;
    cursor: pointer;
    transition: background 0.15s, border-color 0.15s, color 0.15s;
  }
  .btn-cancel:hover { background: var(--bg-hover); border-color: var(--text-muted); color: var(--text-secondary); }

  .btn-add {
    display: inline-flex;
    align-items: center;
    gap: 0.35rem;
    padding: 0.45rem 0.9rem;
    background: var(--accent);
    border: none;
    border-radius: 7px;
    color: #fff;
    font-size: 0.8rem;
    font-weight: 600;
    cursor: pointer;
    transition: background 0.15s;
    white-space: nowrap;
  }
  .btn-add:hover { background: var(--accent-hover); }

  .task-row {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 1rem;
  }
  .task-info { flex: 1; min-width: 0; }
  .task-head {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    margin-bottom: 0.5rem;
    flex-wrap: wrap;
  }
  .task-name {
    font-size: 0.92rem;
    font-weight: 600;
    color: var(--text-primary);
  }
  .type-badge {
    font-size: 0.65rem;
    font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, monospace;
    padding: 0.15rem 0.45rem;
    border-radius: 4px;
    background: var(--accent-bg);
    color: var(--accent-text);
  }
  .task-meta {
    display: flex;
    flex-wrap: wrap;
    gap: 0.85rem;
    font-size: 0.74rem;
    color: var(--text-secondary);
  }
  .meta-item { display: inline-flex; align-items: center; gap: 0.3rem; }
  .meta-key { color: var(--text-muted); text-transform: uppercase; font-size: 0.62rem; letter-spacing: 0.06em; }
  .meta-rel { color: var(--text-muted); }
  .meta-item code {
    font-size: 0.72rem;
    color: var(--text-secondary);
    background: var(--bg-hover);
    padding: 0.05rem 0.3rem;
    border-radius: 4px;
  }

  .status {
    font-size: 0.62rem;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    padding: 0.1rem 0.4rem;
    border-radius: 4px;
  }
  .status-success { background: var(--success-bg); color: var(--success); }
  .status-failed  { background: var(--error-bg); color: var(--error); }
  .status-running { background: var(--info-bg); color: var(--info); }
  .status-idle    { background: var(--input-bg); color: var(--text-muted); }

  .last-error {
    margin-top: 0.5rem;
    padding: 0.35rem 0.6rem;
    border-radius: 7px;
    font-size: 0.72rem;
    background: var(--error-bg);
    border: 1px solid var(--error);
    color: var(--error);
    font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, monospace;
    word-break: break-word;
  }

  .task-actions {
    display: flex;
    gap: 0.25rem;
    flex-shrink: 0;
  }
  .btn-icon {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 30px;
    height: 30px;
    background: transparent;
    border: 1px solid var(--border-strong);
    border-radius: 7px;
    color: var(--text-muted);
    cursor: pointer;
    transition: background 0.12s, color 0.12s, border-color 0.12s;
  }
  .btn-icon:hover { background: var(--bg-hover); color: var(--text-primary); border-color: var(--accent); }
  .btn-icon:disabled { opacity: 0.4; cursor: not-allowed; }
  .btn-icon.active { background: var(--accent-bg); color: var(--accent-text); border-color: var(--accent); }
  .btn-icon.btn-danger:hover { background: var(--error-bg); color: var(--error); border-color: var(--error); }

  .toggle, .toggle-sm {
    position: relative;
    width: 32px;
    height: 18px;
    border-radius: 9px;
    border: none;
    background: rgba(255,255,255,0.1);
    cursor: pointer;
    transition: background 0.2s;
    flex-shrink: 0;
  }
  .toggle-sm { width: 30px; height: 16px; border-radius: 8px; }
  .toggle.toggle-on, .toggle-sm.toggle-on { background: var(--accent); }
  .toggle-knob {
    position: absolute;
    top: 2px;
    left: 2px;
    width: 14px;
    height: 14px;
    border-radius: 50%;
    background: #fff;
    transition: transform 0.2s;
  }
  .toggle-sm .toggle-knob { top: 3px; width: 10px; height: 10px; }
  .toggle.toggle-on .toggle-knob { transform: translateX(14px); }
  .toggle-sm.toggle-on .toggle-knob { transform: translateX(14px); }
  .toggle-label {
    display: flex;
    align-items: center;
    justify-content: space-between;
    color: var(--text-secondary);
    font-size: 0.8rem;
  }

  .spinner { animation: spin 0.7s linear infinite; }
  @keyframes spin { from { transform: rotate(0deg); } to { transform: rotate(360deg); } }

  .empty {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 0.6rem;
    padding: 3rem 1rem;
    color: var(--text-muted);
    font-size: 0.85rem;
    text-align: center;
  }
  .empty p { font-size: 0.85rem; color: var(--text-muted); }
  .empty .empty-sub { font-size: 0.78rem; color: var(--text-muted); max-width: 360px; line-height: 1.5; }

  .runs-panel {
    margin-top: 0.85rem;
    padding-top: 0.85rem;
    border-top: 1px dashed var(--border);
  }
  .runs-head {
    font-size: 0.68rem;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: var(--text-muted);
    margin-bottom: 0.5rem;
  }
  .runs-empty { font-size: 0.78rem; color: var(--text-muted); padding: 0.5rem 0; }
  .runs-list { display: flex; flex-direction: column; gap: 0.3rem; }
  .run-row {
    display: grid;
    grid-template-columns: 70px 1fr 60px 2fr;
    gap: 0.6rem;
    align-items: center;
    font-size: 0.74rem;
    color: var(--text-secondary);
    padding: 0.3rem 0.5rem;
    border-radius: 4px;
    background: var(--bg-hover);
  }
  .run-status { text-align: center; }
  .run-dur { color: var(--text-muted); font-variant-numeric: tabular-nums; }
  .run-error, .run-output {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, monospace;
    font-size: 0.7rem;
  }
  .run-error { color: var(--error); }
  .run-output { color: var(--text-muted); }

  .modal-overlay {
    position: fixed;
    inset: 0;
    background: var(--shadow);
    backdrop-filter: blur(4px);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 1000;
  }
  .modal {
    background: var(--bg-elevated);
    border: 1px solid var(--border-strong);
    border-radius: 12px;
    padding: 1.5rem;
    max-width: 400px;
    width: 90%;
    box-shadow: 0 12px 32px var(--shadow);
  }
  .modal-text { font-size: 1rem; font-weight: 600; color: var(--text-primary); margin-bottom: 0.6rem; }
  .modal-sub { font-size: 0.85rem; color: var(--text-secondary); margin-bottom: 1.25rem; }
  .modal-actions { display: flex; justify-content: flex-end; gap: 0.5rem; }
  .btn-delete {
    padding: 0.45rem 0.9rem;
    background: var(--error-bg);
    border: 1px solid var(--error);
    border-radius: 7px;
    color: var(--error);
    font-size: 0.8rem;
    font-weight: 600;
    cursor: pointer;
    transition: background 0.15s, border-color 0.15s;
  }
  .btn-delete:hover { background: var(--error-bg); border-color: var(--error); }
</style>
