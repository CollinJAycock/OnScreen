<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$lib/stores/page.svelte';
  import { photoPeopleApi, photoSettingsApi } from '$lib/api';
  import { confirmAction } from '$lib/native';
  import { libraryApi, subtitleAutoApi, type Library, type LibraryTrickplayStatus, type NFOCollectionsMode, type EpisodeOrder } from '$lib/api';
  import DirPicker from '$lib/DirPicker.svelte';
  import { t, i18n } from '$lib/i18n/index.svelte';
  import { isVideoLibraryType, formatTrickplayProgress, trickplayQueuedMessage } from '$lib/trickplayProgress';

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
  let isPrivate = false;
  let autoGrantNewUsers = false;

  // Seek-bar thumbnails (trickplay): the toggle saves with the form; the
  // progress counts and "Generate now" talk to the per-library endpoints.
  let trickplayEnabled = false;
  let tpStatus: LibraryTrickplayStatus | null = null;
  let tpGenerating = false;
  let tpMessage = '';

  // Movie libraries: import movie.nfo <set> / <tag> as collections. Sent
  // only when changed — turning it on starts a catch-up import.
  let nfoCollections: NFOCollectionsMode = 'off';

  // TV / anime / cartoons libraries: the episode order shows follow unless
  // they set their own. Sent only when changed — a change re-enriches the
  // library's shows in the background.
  let episodeOrder: EpisodeOrder = 'aired';
  $: hasEpisodes = library?.type === 'show' || library?.type === 'anime' || library?.type === 'cartoons';

  // Movie / TV libraries: write Kodi-compatible NFO files next to the media.
  // Sent only when changed.
  let saveNFO = false;
  $: canSaveNFO = library?.type === 'movie' || hasEpisodes;

  // Automatic subtitle download override (movies and episodes only). ''
  // follows the server-wide setting (Settings ▸ General ▸ OpenSubtitles).
  // Saved with the form, only when changed.
  let subAutoMode: '' | 'on' | 'off' = '';
  let subAutoLangs = '';
  let subAutoLoaded: { mode: '' | 'on' | 'off'; langs: string } | null = null;
  $: subAutoApplies = library?.type === 'movie' || hasEpisodes;

  // Photo libraries: RAW + JPEG pairing (saved with the form, only when
  // changed; the next scan regroups) and deleting face data.
  let rawPairing = true;
  let rawPairingLoaded: boolean | null = null;
  let deletingFaces = false;
  let facesMessage = '';

  async function loadPhotoSettings() {
    if (library?.type !== 'photo') return;
    try {
      const s = await photoSettingsApi.get(id);
      rawPairing = s?.raw_jpeg_pairing ?? true;
      rawPairingLoaded = rawPairing;
    } catch {
      rawPairingLoaded = null; // older server: leave the option hidden
    }
  }

  async function deleteFaceData() {
    if (!(await confirmAction(t('librarySettings.confirmDeleteFaces')))) return;
    deletingFaces = true;
    facesMessage = '';
    try {
      await photoPeopleApi.deleteFaceData(id);
      facesMessage = t('librarySettings.facesDeleted');
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : t('librarySettings.facesDeleteFailed');
    } finally {
      deletingFaces = false;
    }
  }

  let mounted = false;
  let prevId = '';
  let savedTimeout: ReturnType<typeof setTimeout>;

  $: id = $page.params.id!;

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
    trickplayEnabled = false;
    nfoCollections = 'off';
    episodeOrder = 'aired';
    saveNFO = false;
    tpStatus = null;
    tpMessage = '';
    subAutoMode = '';
    subAutoLangs = '';
    subAutoLoaded = null;
    rawPairing = true;
    rawPairingLoaded = null;
    facesMessage = '';
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
      isPrivate = library.is_private ?? false;
      autoGrantNewUsers = library.auto_grant_new_users ?? false;
      trickplayEnabled = library.trickplay_enabled ?? false;
      nfoCollections = library.nfo_collections ?? 'off';
      episodeOrder = library.episode_order ?? 'aired';
      saveNFO = library.save_nfo ?? false;
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : t('librarySettings.loadFailed');
    } finally { loading = false; }
    void loadTrickplayStatus();
    void loadSubtitleAuto();
    void loadPhotoSettings();
  }

  async function loadSubtitleAuto() {
    if (!(library?.type === 'movie' || library?.type === 'show' || library?.type === 'anime' || library?.type === 'cartoons')) return;
    try {
      const o = await subtitleAutoApi.getLibrary(id);
      subAutoMode = o?.enabled == null ? '' : o.enabled ? 'on' : 'off';
      subAutoLangs = o?.languages ?? '';
      subAutoLoaded = { mode: subAutoMode, langs: subAutoLangs };
    } catch {
      // Leave the override hidden-as-default; saving won't touch it.
      subAutoLoaded = null;
    }
  }

  async function loadTrickplayStatus() {
    if (!isVideoLibraryType(library?.type)) { tpStatus = null; return; }
    try {
      tpStatus = await libraryApi.trickplayStatus(id);
    } catch {
      // Counts are informational; a failure just hides them.
      tpStatus = null;
    }
  }

  async function generateTrickplay() {
    tpGenerating = true;
    tpMessage = '';
    try {
      const res = await libraryApi.generateTrickplay(id);
      tpMessage = trickplayQueuedMessage(res?.queued ?? 0);
      await loadTrickplayStatus();
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : t('librarySettings.trickplayQueueFailed');
    } finally {
      tpGenerating = false;
    }
  }

  function addPath() { paths = [...paths, '']; }
  function removePath(i: number) { paths = paths.filter((_, j) => j !== i); }
  function updatePath(i: number, val: string) { paths = paths.map((p, j) => j === i ? val : p); }

  async function save() {
    error = ''; saved = false;
    if (!name.trim()) { error = t('libraryNew.nameRequired'); return; }
    const scanPaths = paths.map(p => p.trim()).filter(Boolean);
    if (!scanPaths.length) { error = t('libraryNew.pathRequired'); return; }
    saving = true;
    try {
      library = await libraryApi.update(id, {
        name: name.trim(),
        scan_paths: scanPaths,
        agent,
        language,
        scan_interval_minutes: scanIntervalMinutes,
        is_private: isPrivate,
        // The backend silently drops this on public libraries, but we
        // also clear the local toggle so the UI doesn't lie about state.
        auto_grant_new_users: isPrivate && autoGrantNewUsers,
        // Only video libraries show the switch; leave other types' flag alone.
        ...(isVideoLibraryType(library?.type) ? { trickplay_enabled: trickplayEnabled } : {}),
        ...(library?.type === 'movie' && nfoCollections !== (library?.nfo_collections ?? 'off')
          ? { nfo_collections: nfoCollections } : {}),
        ...(hasEpisodes && episodeOrder !== (library?.episode_order ?? 'aired')
          ? { episode_order: episodeOrder } : {}),
        ...(canSaveNFO && saveNFO !== (library?.save_nfo ?? false) ? { save_nfo: saveNFO } : {}),
      });
      if (subAutoApplies && subAutoLoaded &&
          (subAutoMode !== subAutoLoaded.mode || subAutoLangs.trim() !== subAutoLoaded.langs)) {
        const o = await subtitleAutoApi.updateLibrary(id, {
          enabled: subAutoMode === '' ? null : subAutoMode === 'on',
          languages: subAutoLangs.trim() === '' ? null : subAutoLangs.trim(),
        });
        subAutoLangs = o?.languages ?? '';
        subAutoLoaded = { mode: subAutoMode, langs: subAutoLangs };
      }
      if (library?.type === 'photo' && rawPairingLoaded !== null && rawPairing !== rawPairingLoaded) {
        const s = await photoSettingsApi.update(id, rawPairing);
        rawPairing = s?.raw_jpeg_pairing ?? rawPairing;
        rawPairingLoaded = rawPairing;
      }
      saved = true;
      savedTimeout = setTimeout(() => saved = false, 3000);
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : t('librarySettings.saveFailed');
    } finally { saving = false; }
  }

  async function del() {
    deleting = true;
    try { await libraryApi.del(id); goto('/'); }
    catch (e: unknown) {
      error = e instanceof Error ? e.message : t('librarySettings.deleteFailed');
      deleting = false; showDelete = false;
    }
  }

  let detecting = false;
  let detectSaved = '';
  async function runDetectIntros() {
    detecting = true;
    detectSaved = '';
    try {
      await libraryApi.detectIntros(id);
      detectSaved = t('librarySettings.detectStarted');
      setTimeout(() => detectSaved = '', 6000);
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : t('librarySettings.detectFailed');
    } finally {
      detecting = false;
    }
  }
</script>

<svelte:head><title>{$i18n('librarySettings.pageTitle', { name: library?.name ?? $i18n('library.fallbackName') })}</title></svelte:head>

<div class="page">
  <nav class="crumb">
    <a href="/">{$i18n('library.crumbLibraries')}</a>
    <span>/</span>
    <a href="/libraries/{id}">{library?.name ?? '…'}</a>
    <span>/</span>
    <span>{$i18n('librarySettings.heading')}</span>
  </nav>

  <h1>{$i18n('librarySettings.heading')}</h1>

  {#if error}
    <div class="banner error">{error}</div>
  {/if}
  {#if saved}
    <div class="banner ok">{$i18n('librarySettings.saved')}</div>
  {/if}

  {#if loading}
    <div class="skeleton-block"></div>
  {:else}
    <form on:submit|preventDefault={save}>
      <section>
        <div class="sec-label">{$i18n('librarySettings.identity')}</div>
        <div class="field">
          <label for="name">{$i18n('librarySettings.name')}</label>
          <input id="name" bind:value={name} autocomplete="off" />
        </div>
        <div class="field">
          <span class="field-heading">{$i18n('librarySettings.type')}</span>
          <div class="readonly">{library?.type}</div>
        </div>
      </section>

      <section>
        <div class="sec-label">{$i18n('library.scanPaths')}</div>
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
              <button type="button" class="browse-btn" title={$i18n('library.browseFolder')} on:click={() => openPicker(i)}>
                <svg viewBox="0 0 16 16" fill="currentColor" width="13" height="13">
                  <path d="M1.75 1A1.75 1.75 0 000 2.75v10.5C0 14.216.784 15 1.75 15h12.5A1.75 1.75 0 0016 13.25v-8.5A1.75 1.75 0 0014.25 3H7.5a.25.25 0 01-.2-.1l-.9-1.2C6.07 1.26 5.55 1 5 1H1.75z"/>
                </svg>
              </button>
              {#if paths.length > 1}
                <button type="button" class="path-rm" aria-label={$i18n('library.removePath')} on:click={() => removePath(i)}>×</button>
              {/if}
            </div>
          {/each}
        </div>
        <button type="button" class="add-path" on:click={addPath}>{$i18n('library.addPath')}</button>
      </section>

      <section>
        <div class="sec-label">{$i18n('library.metadata')}</div>
        <div class="row2">
          <div class="field">
            <label for="agent">{$i18n('library.agent')}</label>
            <select id="agent" bind:value={agent}>
              <option value="tmdb">TMDB</option>
              <option value="none">{$i18n('library.agentNone')}</option>
            </select>
          </div>
          <div class="field">
            <label for="lang">{$i18n('library.language')}</label>
            <select id="lang" bind:value={language}>
              <option value="en">{$i18n('library.langEn')}</option>
              <option value="fr">{$i18n('library.langFr')}</option>
              <option value="de">{$i18n('library.langDe')}</option>
              <option value="es">{$i18n('library.langEs')}</option>
              <option value="ja">{$i18n('library.langJa')}</option>
              <option value="ko">{$i18n('library.langKo')}</option>
              <option value="zh">{$i18n('library.langZh')}</option>
            </select>
          </div>
        </div>
        <p class="check-help">
          {$i18n('librarySettings.languageHelp')}
        </p>
        {#if hasEpisodes}
          <div class="field">
            <label for="episode-order">{$i18n('librarySettings.episodeOrder')}</label>
            <select id="episode-order" value={episodeOrder}
                    on:change={(e) => episodeOrder = e.currentTarget.value as EpisodeOrder}>
              <option value="aired">{$i18n('librarySettings.orderAired')}</option>
              <option value="dvd">{$i18n('librarySettings.orderDvd')}</option>
              <option value="absolute">{$i18n('librarySettings.orderAbsolute')}</option>
            </select>
          </div>
          <p class="check-help">
            {$i18n('librarySettings.episodeOrderHelp')}
          </p>
        {/if}
      </section>

      <section>
        <div class="sec-label">{$i18n('librarySettings.scanning')}</div>
        <div class="field">
          <label for="scan-interval">{$i18n('librarySettings.scanInterval')}</label>
          <select id="scan-interval" bind:value={scanIntervalMinutes}>
            <option value={5}>{$i18n.n('librarySettings.everyMinutes', 5)}</option>
            <option value={15}>{$i18n.n('librarySettings.everyMinutes', 15)}</option>
            <option value={30}>{$i18n.n('librarySettings.everyMinutes', 30)}</option>
            <option value={60}>{$i18n('librarySettings.everyHour')}</option>
            <option value={360}>{$i18n.n('librarySettings.everyHours', 6)}</option>
            <option value={720}>{$i18n.n('librarySettings.everyHours', 12)}</option>
            <option value={1440}>{$i18n('librarySettings.everyDay')}</option>
          </select>
        </div>
      </section>

      {#if isVideoLibraryType(library?.type)}
        <section>
          <div class="sec-label">{$i18n('librarySettings.trickplaySection')}</div>
          <label class="check-row">
            <input type="checkbox" bind:checked={trickplayEnabled} />
            <span>
              <span class="check-title">{$i18n('librarySettings.trickplayToggle')}</span>
              <span class="check-help">
                {$i18n('librarySettings.trickplayHelp')}
              </span>
            </span>
          </label>
          <div class="tp-row">
            <span class="tp-progress" data-testid="trickplay-progress">{formatTrickplayProgress(tpStatus, $i18n)}</span>
            <button type="button" class="btn-secondary" disabled={tpGenerating} on:click={generateTrickplay}>
              {tpGenerating ? $i18n('librarySettings.queuing') : $i18n('librarySettings.generateNow')}
            </button>
          </div>
          {#if tpMessage}
            <div class="tp-msg">{tpMessage}</div>
          {/if}
        </section>
      {/if}

      {#if subAutoApplies && subAutoLoaded}
        <section>
          <div class="sec-label">{$i18n('librarySettings.subAutoSection')}</div>
          <div class="row2">
            <div class="field">
              <label for="sub-auto">{$i18n('librarySettings.subAutoLabel')}</label>
              <select id="sub-auto" value={subAutoMode}
                      on:change={(e) => subAutoMode = e.currentTarget.value as typeof subAutoMode}>
                <option value="">{$i18n('librarySettings.subAutoFollow')}</option>
                <option value="on">{$i18n('librarySettings.subAutoOn')}</option>
                <option value="off">{$i18n('librarySettings.subAutoOff')}</option>
              </select>
            </div>
            <div class="field">
              <label for="sub-auto-langs">{$i18n('librarySettings.subAutoLangs')}</label>
              <input id="sub-auto-langs" type="text" bind:value={subAutoLangs}
                     placeholder={$i18n('librarySettings.subAutoLangsPlaceholder')} autocomplete="off" spellcheck="false" />
            </div>
          </div>
          <p class="check-help">
            {#each $i18n.rich('librarySettings.subAutoHelp', { example: 'en,es' }) as seg}{#if seg.param}<code>{seg.text}</code>{:else}{seg.text}{/if}{/each}
          </p>
        </section>
      {/if}

      {#if library?.type === 'photo' && rawPairingLoaded !== null}
        <section>
          <div class="sec-label">{$i18n('librarySettings.rawSection')}</div>
          <label class="check-row">
            <input type="checkbox" bind:checked={rawPairing} data-testid="raw-pairing" />
            <span>
              <span class="check-title">{$i18n('librarySettings.rawToggle')}</span>
              <span class="check-help">
                {$i18n('librarySettings.rawHelp')}
              </span>
            </span>
          </label>
        </section>
      {/if}

      {#if library?.type === 'movie'}
        <section>
          <div class="sec-label">{$i18n('librarySettings.nfoCollectionsSection')}</div>
          <div class="field">
            <label for="nfo-collections">{$i18n('librarySettings.nfoCollectionsLabel')}</label>
            <select id="nfo-collections" value={nfoCollections}
                    on:change={(e) => nfoCollections = e.currentTarget.value as NFOCollectionsMode}>
              <option value="off">{$i18n('librarySettings.nfoOff')}</option>
              <option value="sets">{$i18n('librarySettings.nfoSets')}</option>
              <option value="sets_and_tags">{$i18n('librarySettings.nfoSetsAndTags')}</option>
            </select>
          </div>
          <p class="check-help">
            {$i18n('librarySettings.nfoCollectionsHelp')}
          </p>
        </section>
      {/if}

      {#if canSaveNFO}
        <section>
          <div class="sec-label">{$i18n('librarySettings.nfoSection')}</div>
          <label class="check-row">
            <input type="checkbox" bind:checked={saveNFO} data-testid="save-nfo" />
            <span>
              <span class="check-title">{$i18n('librarySettings.saveNfoToggle')}</span>
              <span class="check-help">
                {$i18n('librarySettings.saveNfoHelp')}
              </span>
            </span>
          </label>
        </section>
      {/if}

      <section>
        <div class="sec-label">{$i18n('librarySettings.visibility')}</div>
        <label class="check-row">
          <input type="checkbox" bind:checked={isPrivate} />
          <span>
            <span class="check-title">{$i18n('librarySettings.private')}</span>
            <span class="check-help">
              {#each $i18n.rich('librarySettings.privateHelp', { users: $i18n('librarySettings.usersLink') }) as seg}{#if seg.param}<a href="/settings/users">{seg.text}</a>{:else}{seg.text}{/if}{/each}
            </span>
          </span>
        </label>
        {#if isPrivate}
          <label class="check-row sub">
            <input type="checkbox" bind:checked={autoGrantNewUsers} />
            <span>
              <span class="check-title">{$i18n('librarySettings.autoGrant')}</span>
              <span class="check-help">
                {$i18n('librarySettings.autoGrantHelp')}
              </span>
            </span>
          </label>
        {/if}
      </section>


      <div class="form-foot">
        <a href="/libraries/{id}" class="btn-ghost">{$i18n('browse.cancel')}</a>
        <button type="submit" class="btn-save" disabled={saving}>
          {saving ? $i18n('browse.saving') : $i18n('librarySettings.saveChanges')}
        </button>
      </div>
    </form>

    {#if library?.type === 'photo'}
      <section class="maint">
        <div class="sec-label">{$i18n('librarySettings.facesSection')}</div>
        <p class="maint-sub">
          {$i18n('librarySettings.facesHelp')}
        </p>
        {#if facesMessage}
          <div class="banner ok" style="margin-bottom: 0.6rem">{facesMessage}</div>
        {/if}
        <button type="button" class="btn-secondary danger" disabled={deletingFaces} on:click={deleteFaceData} data-testid="delete-face-data">
          {deletingFaces ? $i18n('browse.deleting') : $i18n('librarySettings.deleteFaces')}
        </button>
      </section>
    {/if}

    {#if library?.type === 'show'}
      <section class="maint">
        <div class="sec-label">{$i18n('librarySettings.introSection')}</div>
        <p class="maint-sub">{$i18n('librarySettings.introHelp')}</p>
        {#if detectSaved}
          <div class="banner ok" style="margin-bottom: 0.6rem">{detectSaved}</div>
        {/if}
        <button type="button" class="btn-secondary" disabled={detecting} on:click={runDetectIntros}>
          {detecting ? $i18n('librarySettings.starting') : $i18n('librarySettings.detectNow')}
        </button>
      </section>
    {/if}

    <!-- Danger -->
    <div class="danger-zone">
      <div class="dz-label">{$i18n('librarySettings.dangerZone')}</div>
      <div class="dz-row">
        <div>
          <div class="dz-title">{$i18n('librarySettings.deleteLibraryTitle')}</div>
          <div class="dz-sub">{$i18n('librarySettings.deleteLibrarySub')}</div>
        </div>
        <button class="btn-del-outline" on:click={() => showDelete = true}>{$i18n('browse.delete')}</button>
      </div>
    </div>
  {/if}
</div>

<DirPicker bind:open={pickerOpen} on:select={onPickerSelect} />

{#if showDelete}
  <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
  <div class="overlay" role="presentation" on:click={() => showDelete = false}>
    <!-- svelte-ignore a11y_click_events_have_key_events -->
    <div class="dialog" role="dialog" aria-modal="true" tabindex="-1" on:click|stopPropagation>
      <p class="dlg-title">{$i18n('librarySettings.deleteDialogTitle', { name: library?.name ?? '' })}</p>
      <p class="dlg-body">{$i18n('librarySettings.deleteDialogBody')}</p>
      <div class="dlg-foot">
        <button class="btn-ghost" on:click={() => showDelete = false}>{$i18n('browse.cancel')}</button>
        <button class="btn-del-solid" disabled={deleting} on:click={del}>
          {deleting ? $i18n('browse.deleting') : $i18n('librarySettings.deleteLibrary')}
        </button>
      </div>
    </div>
  </div>
{/if}

<style>
  .page { padding: 2.5rem; max-width: 560px; }

  .crumb {
    display: flex; align-items: center; gap: 0.4rem;
    font-size: 0.75rem; color: var(--text-muted); margin-bottom: 1.25rem;
  }
  .crumb a { color: var(--text-muted); text-decoration: none; }
  .crumb a:hover { color: var(--text-secondary); }

  h1 { font-size: 1.25rem; font-weight: 700; color: var(--text-primary); letter-spacing: -0.02em; margin-bottom: 1.75rem; }

  .banner {
    padding: 0.6rem 0.9rem;
    border-radius: 8px;
    font-size: 0.8rem;
    margin-bottom: 1.25rem;
  }
  .banner.error { background: var(--error-bg); border: 1px solid var(--error-bg); color: var(--error); }
  .banner.ok { background: var(--success-bg); border: 1px solid var(--success-bg); color: var(--success); }

  .skeleton-block {
    height: 380px; border-radius: 10px;
    background: linear-gradient(90deg, var(--bg-elevated) 25%, #16161f 50%, var(--bg-elevated) 75%);
    background-size: 200% 100%;
    animation: shimmer 1.4s infinite;
  }
  @keyframes shimmer { 0% { background-position: 200% 0; } 100% { background-position: -200% 0; } }

  form { display: flex; flex-direction: column; }

  section {
    padding: 1.25rem 0;
    border-bottom: 1px solid var(--border);
  }

  .sec-label {
    font-size: 0.68rem; font-weight: 700;
    text-transform: uppercase; letter-spacing: 0.09em;
    color: var(--text-muted); margin-bottom: 1rem;
  }

  .field { display: flex; flex-direction: column; gap: 0.3rem; margin-bottom: 0.65rem; }
  .field:last-child { margin-bottom: 0; }
  .check-row {
    display: flex; align-items: flex-start; gap: 0.6rem;
    cursor: pointer; padding: 0.4rem 0;
  }
  /* width: auto — the form-wide `input { width: 100% }` below would make the
     box 100% wide and crush the label text beside it. */
  .check-row input[type="checkbox"] { margin-top: 0.2rem; flex-shrink: 0; width: auto; }
  .check-row.sub { margin-top: 0.5rem; padding-left: 1.4rem; border-left: 2px solid var(--border); margin-left: 0.4rem; }
  .check-row > span { display: flex; flex-direction: column; gap: 0.2rem; }
  .check-title { font-size: 0.85rem; font-weight: 500; color: var(--text-primary); }
  .check-help { font-size: 0.75rem; color: var(--text-muted); line-height: 1.4; }
  .check-help a { color: var(--accent); text-decoration: none; }
  .row2 { display: grid; grid-template-columns: 1fr 1fr; gap: 0.75rem; }
  label, .field-heading { font-size: 0.75rem; font-weight: 500; color: var(--text-muted); }

  input, select {
    background: var(--bg-hover);
    border: 1px solid var(--border-strong);
    border-radius: 7px;
    padding: 0.48rem 0.7rem;
    font-size: 0.85rem;
    color: var(--text-primary);
    transition: border-color 0.15s;
    width: 100%;
  }
  input:focus, select:focus { outline: none; border-color: var(--accent); box-shadow: 0 0 0 3px var(--accent-bg); }
  select option { background: var(--bg-elevated); }
  ::placeholder { color: var(--text-muted); }

  .readonly {
    padding: 0.48rem 0.7rem;
    border: 1px solid var(--border);
    border-radius: 7px;
    font-size: 0.85rem;
    color: var(--text-muted);
    text-transform: capitalize;
  }

  .path-list { display: flex; flex-direction: column; gap: 0.4rem; margin-bottom: 0.6rem; }
  .path-row {
    display: flex; align-items: center;
    background: var(--bg-hover);
    border: 1px solid var(--border-strong);
    border-radius: 7px; overflow: hidden;
    transition: border-color 0.15s;
  }
  .path-row:focus-within { border-color: var(--accent); box-shadow: 0 0 0 3px var(--accent-bg); }
  .pfx { padding: 0 0.1rem 0 0.7rem; font-size: 0.82rem; color: var(--text-muted); font-family: monospace; }
  .path-in {
    flex: 1; background: none !important; border: none !important;
    box-shadow: none !important; padding: 0.48rem 0.4rem 0.48rem 0 !important;
    font-family: monospace; font-size: 0.8rem;
  }
  .path-in:focus { outline: none; box-shadow: none !important; }
  .browse-btn {
    padding: 0 0.55rem;
    background: none;
    border-left: 1px solid var(--border);
    color: var(--text-muted);
    cursor: pointer;
    align-self: stretch;
    display: flex;
    align-items: center;
    transition: color 0.12s, background 0.12s;
    flex-shrink: 0;
  }
  .browse-btn:hover { background: rgba(124,106,247,0.08); color: var(--accent-text); }

  .path-rm {
    padding: 0 0.6rem; background: none; border: none;
    font-size: 1rem; color: var(--text-muted); cursor: pointer; align-self: stretch;
    display: flex; align-items: center; transition: color 0.12s;
  }
  .path-rm:hover { color: var(--error); }
  .add-path {
    background: none; border: none; color: var(--text-muted);
    font-size: 0.75rem; cursor: pointer; padding: 0;
    transition: color 0.12s;
  }
  .add-path:hover { color: var(--accent-text); }

  .form-foot {
    display: flex; justify-content: flex-end; align-items: center;
    gap: 0.5rem; padding-top: 1.5rem;
  }
  .btn-ghost {
    padding: 0.42rem 0.85rem; background: none;
    border: 1px solid var(--border-strong);
    border-radius: 7px; color: var(--text-muted); font-size: 0.8rem;
    text-decoration: none; cursor: pointer; transition: all 0.12s;
  }
  .btn-ghost:hover { border-color: var(--accent-bg); color: var(--text-secondary); }
  .btn-save {
    padding: 0.42rem 0.9rem; background: var(--accent);
    border: none; border-radius: 7px; color: #fff;
    font-size: 0.8rem; font-weight: 600; cursor: pointer; transition: background 0.15s;
  }
  .btn-save:hover { background: var(--accent-hover); }
  .btn-save:disabled { opacity: 0.5; cursor: not-allowed; }

  .tp-row {
    display: flex; align-items: center; justify-content: space-between;
    flex-wrap: wrap; gap: 0.6rem; margin-top: 0.75rem;
  }
  .tp-progress { font-size: 0.75rem; color: var(--text-muted); line-height: 1.4; }
  .tp-msg { font-size: 0.75rem; color: var(--success); line-height: 1.4; margin-top: 0.6rem; }

  .maint { margin-top: 1.5rem; }
  .maint-sub {
    font-size: 0.75rem;
    color: var(--text-muted);
    margin: 0 0 0.75rem 0;
    max-width: 420px;
    line-height: 1.5;
  }
  .btn-secondary {
    padding: 0.55rem 0.95rem;
    background: var(--bg-elevated);
    border: 1px solid rgba(255,255,255,0.1);
    border-radius: 7px;
    color: var(--text-primary);
    font-size: 0.8rem;
    font-weight: 600;
    cursor: pointer;
    transition: background 0.15s, border-color 0.15s;
  }
  .btn-secondary:hover { background: rgba(124,106,247,0.1); border-color: rgba(124,106,247,0.3); }
  .btn-secondary:disabled { opacity: 0.5; cursor: not-allowed; }
  .btn-secondary.danger:hover:not(:disabled) { border-color: var(--error); color: var(--error); background: var(--bg-elevated); }

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
  .btn-del-outline:hover { background: rgba(248,113,113,0.08); color: var(--error); border-color: rgba(248,113,113,0.4); }

  /* Modal */
  .overlay {
    position: fixed; inset: 0; background: rgba(0,0,0,0.75);
    backdrop-filter: blur(6px);
    display: flex; align-items: center; justify-content: center;
    z-index: 100; padding: 1rem;
  }
  .dialog {
    background: var(--bg-elevated); border: 1px solid var(--border-strong);
    border-radius: 12px; padding: 1.5rem; max-width: 380px; width: 100%;
    box-shadow: 0 24px 48px var(--shadow);
  }
  .dlg-title { font-size: 0.9rem; font-weight: 700; color: var(--text-primary); margin-bottom: 0.5rem; }
  .dlg-body { font-size: 0.8rem; color: var(--text-muted); line-height: 1.55; margin-bottom: 1.25rem; }
  .dlg-foot { display: flex; justify-content: flex-end; gap: 0.5rem; }
  .btn-del-solid {
    padding: 0.42rem 0.9rem; background: rgba(248,113,113,0.15);
    border: 1px solid rgba(248,113,113,0.3);
    border-radius: 7px; color: var(--error);
    font-size: 0.8rem; font-weight: 600; cursor: pointer; transition: background 0.12s;
  }
  .btn-del-solid:hover { background: rgba(248,113,113,0.25); }
  .btn-del-solid:disabled { opacity: 0.45; cursor: not-allowed; }
</style>
