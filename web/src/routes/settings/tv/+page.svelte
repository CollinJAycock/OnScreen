<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { liveTvApi, type LiveTVTuner, type LiveTVEPGSource, type LiveTVChannel, type LiveTVBroadcast } from '$lib/api';
  import { confirmAction } from '$lib/native';
  import { toast } from '$lib/stores/toast';
  import DVRPostProcessing from '$lib/components/DVRPostProcessing.svelte';
  import { t, tn, i18n, type MessageKey, type Translator } from '$lib/i18n/index.svelte';

  let tuners: LiveTVTuner[] = [];
  let epgSources: LiveTVEPGSource[] = [];
  let unmapped: Array<{ id: string; number: string; callsign?: string; name: string; logo_url?: string }> = [];
  let knownEPGIDs: string[] = [];
  let loading = true;
  let error = '';
  let busyMapId = '';

  // All channels (including disabled) for the reorder/hide section.
  let allChannels: LiveTVChannel[] = [];
  let busyChannelId = '';

  // Add-EPG-source form. Only XMLTV-URL and XMLTV-file backends in
  // Phase B Round 1 — Schedules Direct slots in here later.
  let addEPGType: 'xmltv_url' | 'xmltv_file' = 'xmltv_url';
  let addEPGName = '';
  let addEPGSource = '';
  let addEPGInterval = 360;
  let addingEPG = false;
  let busyEPGId = '';

  // Add-tuner form. The two backends need different fields, so we keep
  // both sets in scope and pick which to send based on `addType`.
  let addType: 'hdhomerun' | 'm3u' = 'hdhomerun';
  let addName = '';
  let addHostUrl = '';
  let addM3USource = '';
  let addM3UUserAgent = '';
  let adding = false;

  // Per-tuner action busy flags so two parallel ops on different rows
  // don't disable each other's button.
  let busyId = '';

  // Auto-discovery state.
  let discovering = false;
  let discovered: Array<{ device_id: string; base_url: string; tune_count: number; model?: string }> = [];

  // ── Live broadcasts ("go live" / RTMP ingest) ──
  let broadcasts: LiveTVBroadcast[] = [];
  let broadcastsUnavailable = false;
  let newBroadcastName = '';
  let creatingBroadcast = false;
  let busyBroadcastId = '';
  let revealedKeys: Record<string, boolean> = {};
  let statusPoll: ReturnType<typeof setInterval> | undefined;

  onMount(() => {
    load();
    loadBroadcasts();
    // Poll live/offline status so the badge flips when a broadcaster connects.
    statusPoll = setInterval(loadBroadcasts, 5000);
  });
  onDestroy(() => {
    if (statusPoll) clearInterval(statusPoll);
  });

  async function loadBroadcasts() {
    try {
      broadcasts = (await liveTvApi.listBroadcasts()).items;
      broadcastsUnavailable = false;
    } catch {
      // RTMP ingest disabled on the server (RTMP_ENABLED=false / bind failed).
      broadcasts = [];
      broadcastsUnavailable = true;
    }
  }

  async function createBroadcast() {
    const name = newBroadcastName.trim();
    if (!name) return;
    creatingBroadcast = true;
    try {
      await liveTvApi.createBroadcast(name);
      newBroadcastName = '';
      toast.success(t('settings.tv.broadcastCreated'));
      await loadBroadcasts();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t('settings.tv.createBroadcastFailed'));
    } finally {
      creatingBroadcast = false;
    }
  }

  async function removeBroadcast(b: LiveTVBroadcast) {
    if (!(await confirmAction(t('settings.tv.confirmDeleteBroadcast', { name: b.name })))) return;
    busyBroadcastId = b.tuner_id;
    try {
      await liveTvApi.deleteTuner(b.tuner_id);
      await loadBroadcasts();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t('settings.tv.deleteBroadcastFailed'));
    } finally {
      busyBroadcastId = '';
    }
  }

  async function copyText(text: string, copiedKey: MessageKey) {
    try {
      await navigator.clipboard.writeText(text);
      toast.success(t(copiedKey));
    } catch {
      toast.error(t('settings.tv.copyFailed'));
    }
  }

  async function load() {
    loading = true; error = '';
    try {
      const [tRes, eRes, uRes, idsRes, cRes] = await Promise.all([
        liveTvApi.listTuners(),
        liveTvApi.listEPGSources(),
        liveTvApi.listUnmappedChannels(),
        liveTvApi.listEPGIDs(),
        liveTvApi.listAllChannels(),
      ]);
      tuners = tRes.items;
      epgSources = eRes.items;
      unmapped = uRes.items;
      knownEPGIDs = idsRes.items;
      allChannels = cRes.items;
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : t('settings.tv.loadFailed');
    } finally { loading = false; }
  }

  // Reorder: swap adjacent, then PUT the new ordering. Local mutation
  // first for snappy UI; if the API call fails we reload from the server.
  async function moveChannel(ch: LiveTVChannel, delta: number) {
    const idx = allChannels.findIndex(c => c.id === ch.id);
    const target = idx + delta;
    if (idx < 0 || target < 0 || target >= allChannels.length) return;
    busyChannelId = ch.id;
    const newOrder = [...allChannels];
    [newOrder[idx], newOrder[target]] = [newOrder[target], newOrder[idx]];
    allChannels = newOrder;
    try {
      await liveTvApi.reorderChannels(newOrder.map(c => c.id));
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : t('settings.tv.reorderFailed'));
      await load();
    } finally { busyChannelId = ''; }
  }

  async function toggleChannel(ch: LiveTVChannel) {
    busyChannelId = ch.id;
    try {
      await liveTvApi.setChannelEnabled(ch.id, !ch.enabled);
      allChannels = allChannels.map(c => c.id === ch.id ? { ...c, enabled: !c.enabled } : c);
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : t('settings.tv.toggleFailed'));
      await load();
    } finally { busyChannelId = ''; }
  }

  async function setChannelEPGID(channelId: string, epgID: string) {
    if (!epgID) return;
    busyMapId = channelId;
    try {
      await liveTvApi.setChannelEPGID(channelId, epgID);
      toast.success(t('settings.tv.mapped'));
      await load();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : t('settings.tv.mappingFailed'));
    } finally { busyMapId = ''; }
  }

  async function addEPG() {
    if (!addEPGName.trim()) { toast.error(t('settings.tv.nameRequired')); return; }
    if (!addEPGSource.trim()) { toast.error(t('settings.tv.sourceRequired')); return; }
    addingEPG = true;
    try {
      const created = await liveTvApi.createEPGSource({
        type: addEPGType,
        name: addEPGName.trim(),
        config: { source: addEPGSource.trim() },
        refresh_interval_min: addEPGInterval,
      });
      toast.success(t('settings.tv.epgAdded', { name: addEPGName.trim() }));
      addEPGName = ''; addEPGSource = '';
      await load();
      // Auto-refresh on first add so the user sees ingestion stats immediately
      // rather than having to click "Refresh" as a follow-up.
      await refreshEPG(created);
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : t('settings.tv.addEpgFailed'));
    } finally { addingEPG = false; }
  }

  async function refreshEPG(s: LiveTVEPGSource) {
    busyEPGId = s.id;
    try {
      const res = await liveTvApi.refreshEPGSource(s.id);
      toast.success(t('settings.tv.epgRefreshed', { name: s.name, programs: res.programs_ingested, channels: res.channels_auto_matched }));
      await load();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : t('settings.tv.refreshFailed'));
      await load();
    } finally { busyEPGId = ''; }
  }

  async function removeEPG(s: LiveTVEPGSource) {
    if (!(await confirmAction(t('settings.tv.confirmDeleteEpg', { name: s.name })))) return;
    busyEPGId = s.id;
    try {
      await liveTvApi.deleteEPGSource(s.id);
      toast.success(t('settings.tv.deleted', { name: s.name }));
      await load();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : t('settings.tv.deleteFailed'));
    } finally { busyEPGId = ''; }
  }

  async function discover() {
    discovering = true;
    discovered = [];
    try {
      const res = await liveTvApi.discoverTuners();
      discovered = res;
      if (res.length === 0) {
        toast.error(t('settings.tv.noDevicesFound'));
      } else {
        toast.success(tn('settings.tv.devicesFound', res.length));
      }
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : t('settings.tv.discoveryFailed'));
    } finally { discovering = false; }
  }

  // Clicking a discovered device pre-fills the add form with its BaseURL
  // and name so the user just confirms + submits.
  function selectDiscovered(d: { device_id: string; base_url: string; tune_count: number }) {
    addType = 'hdhomerun';
    addHostUrl = d.base_url;
    addName = `HDHomeRun ${d.device_id}`;
    toast.success(t('settings.tv.prefilled'));
  }

  async function add() {
    if (!addName.trim()) { toast.error(t('settings.tv.nameRequired')); return; }
    let config: Record<string, unknown>;
    if (addType === 'hdhomerun') {
      if (!addHostUrl.trim()) { toast.error(t('settings.tv.hostRequired')); return; }
      // Allow plain "10.0.0.50" in the input; HDHomeRun config wants a full URL.
      const url = /^https?:\/\//.test(addHostUrl) ? addHostUrl : `http://${addHostUrl}`;
      config = { host_url: url };
    } else {
      if (!addM3USource.trim()) { toast.error(t('settings.tv.sourceRequired')); return; }
      config = { source: addM3USource };
      if (addM3UUserAgent) config.user_agent = addM3UUserAgent;
    }

    adding = true;
    try {
      await liveTvApi.createTuner({ type: addType, name: addName.trim(), config });
      toast.success(t('settings.tv.tunerAdded', { name: addName.trim() }));
      addName = ''; addHostUrl = ''; addM3USource = ''; addM3UUserAgent = '';
      await load();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : t('settings.tv.addTunerFailed'));
    } finally { adding = false; }
  }

  async function rescan(tuner: LiveTVTuner) {
    busyId = tuner.id;
    try {
      const res = await liveTvApi.rescanTuner(tuner.id);
      toast.success(tn('settings.tv.rescanFound', res.channel_count, { name: tuner.name }));
      await load();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : t('settings.tv.rescanFailed'));
    } finally { busyId = ''; }
  }

  async function toggle(tuner: LiveTVTuner) {
    busyId = tuner.id;
    try {
      await liveTvApi.updateTuner(tuner.id, {
        name: tuner.name,
        config: tuner.config,
        tune_count: tuner.tune_count,
        enabled: !tuner.enabled,
      });
      await load();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : t('settings.tv.updateFailed'));
    } finally { busyId = ''; }
  }

  async function remove(tuner: LiveTVTuner) {
    if (!(await confirmAction(t('settings.tv.confirmDeleteTuner', { name: tuner.name })))) return;
    busyId = tuner.id;
    try {
      await liveTvApi.deleteTuner(tuner.id);
      toast.success(t('settings.tv.deleted', { name: tuner.name }));
      await load();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : t('settings.tv.deleteFailed'));
    } finally { busyId = ''; }
  }

  // Takes the translator so the template re-renders it when the language changes.
  function fmtRelative(iso: string | undefined, tr: Translator): string {
    if (!iso) return tr('settings.tv.never');
    const ms = Date.now() - new Date(iso).getTime();
    if (ms < 60_000) return tr('settings.tv.justNow');
    if (ms < 3_600_000) return tr('settings.tv.minutesAgo', { n: Math.floor(ms / 60_000) });
    if (ms < 86_400_000) return tr('settings.tv.hoursAgo', { n: Math.floor(ms / 3_600_000) });
    return tr('settings.tv.daysAgo', { n: Math.floor(ms / 86_400_000) });
  }
</script>

{#if loading}
  <div class="skeleton-block"></div>
{:else if error}
  <p class="error">{error}</p>
{:else}
  <div class="wrap">
    <section>
      <header>
        <h2>{$i18n('settings.tv.broadcastsTitle')}</h2>
        <p class="hint">{$i18n('settings.tv.broadcastsHint')}</p>
      </header>

      {#if broadcastsUnavailable}
        <p class="empty">
          {#each $i18n.rich('settings.tv.rtmpDisabled', { env: 'RTMP_ENABLED=true', port: '1935' }) as seg}{#if seg.param === 'env'}<code>{seg.text}</code>{:else}{seg.text}{/if}{/each}
        </p>
      {:else}
        {#if broadcasts.length === 0}
          <p class="empty">{$i18n('settings.tv.noBroadcasts')}</p>
        {:else}
          <ul class="broadcast-list">
            {#each broadcasts as b (b.tuner_id)}
              <li class="broadcast">
                <div class="broadcast-head">
                  <span class="broadcast-name">{b.name}</span>
                  {#if b.is_live}
                    <span class="badge badge-live"><span class="live-dot"></span>{$i18n('settings.tv.live')}</span>
                  {:else}
                    <span class="badge badge-off">{$i18n('settings.tv.offline')}</span>
                  {/if}
                </div>

                <div class="kv">
                  <span class="kv-label">{$i18n('settings.tv.ingestUrl')}</span>
                  <code class="mono">{b.ingest_url.replace(b.stream_key, '<key>')}</code>
                  <button class="btn btn-sm" on:click={() => copyText(b.ingest_url.slice(0, b.ingest_url.lastIndexOf('/')), 'settings.tv.serverUrlCopied')}>{$i18n('settings.tv.copyUrl')}</button>
                </div>

                <div class="kv">
                  <span class="kv-label">{$i18n('settings.tv.streamKey')}</span>
                  <code class="mono">{revealedKeys[b.tuner_id] ? b.stream_key : '••••••••••••••••'}</code>
                  <button class="btn btn-sm" on:click={() => (revealedKeys[b.tuner_id] = !revealedKeys[b.tuner_id])}>
                    {revealedKeys[b.tuner_id] ? $i18n('settings.tv.hide') : $i18n('settings.tv.reveal')}
                  </button>
                  <button class="btn btn-sm" on:click={() => copyText(b.stream_key, 'settings.tv.streamKeyCopied')}>{$i18n('settings.tv.copyKey')}</button>
                </div>

                <div class="broadcast-actions">
                  {#if b.channel_id}
                    <a class="btn btn-sm" href={`/tv/${b.channel_id}`}>{$i18n('settings.tv.watch')}</a>
                  {/if}
                  <button class="btn btn-sm btn-danger" disabled={busyBroadcastId === b.tuner_id} on:click={() => removeBroadcast(b)}>
                    {$i18n('settings.tv.delete')}
                  </button>
                </div>
              </li>
            {/each}
          </ul>
        {/if}

        <div class="grid">
          <label class="full">
            {$i18n('settings.tv.newBroadcastName')}
            <input type="text" bind:value={newBroadcastName} placeholder={$i18n('settings.tv.newBroadcastPlaceholder')} on:keydown={(e) => e.key === 'Enter' && createBroadcast()} />
          </label>
        </div>
        <div class="actions">
          <button class="btn btn-primary" disabled={creatingBroadcast || !newBroadcastName.trim()} on:click={createBroadcast}>
            {creatingBroadcast ? $i18n('settings.tv.creating') : $i18n('settings.tv.createBroadcast')}
          </button>
        </div>
      {/if}
    </section>

    <section>
      <header>
        <h2>{$i18n('settings.tv.tunersTitle')}</h2>
        <p class="hint">{$i18n('settings.tv.tunersHint')}</p>
      </header>

      {#if tuners.length === 0}
        <p class="empty">{$i18n('settings.tv.noTuners')}</p>
      {:else}
        <ul class="tuner-list">
          {#each tuners as t (t.id)}
            <li class="tuner" class:disabled={!t.enabled}>
              <div class="tuner-main">
                <div class="tuner-name">
                  {t.name}
                  <span class="badge">{t.type}</span>
                  {#if !t.enabled}<span class="badge badge-off">{$i18n('settings.tv.disabled')}</span>{/if}
                </div>
                <div class="tuner-meta">
                  {#if t.type === 'hdhomerun'}
                    {(t.config as { host_url?: string }).host_url ?? ''}
                  {:else if t.type === 'm3u'}
                    {(t.config as { source?: string }).source ?? ''}
                  {/if}
                  · {$i18n.n('settings.tv.tuneSlots', t.tune_count)}
                  · {$i18n('settings.tv.lastSeen', { when: fmtRelative(t.last_seen_at, $i18n) })}
                </div>
              </div>
              <div class="tuner-actions">
                <button class="btn" disabled={busyId === t.id} on:click={() => rescan(t)}>
                  {busyId === t.id ? '…' : $i18n('settings.tv.rescan')}
                </button>
                <button class="btn" disabled={busyId === t.id} on:click={() => toggle(t)}>
                  {t.enabled ? $i18n('settings.tv.disable') : $i18n('settings.tv.enable')}
                </button>
                <button class="btn btn-danger" disabled={busyId === t.id} on:click={() => remove(t)}>
                  {$i18n('settings.tv.delete')}
                </button>
              </div>
            </li>
          {/each}
        </ul>
      {/if}
    </section>

    <section>
      <header>
        <h2>{$i18n('settings.tv.addTuner')}</h2>
        <p class="hint">{$i18n('settings.tv.addTunerHint')}</p>
      </header>

      <div class="discover-row">
        <button class="btn" disabled={discovering} on:click={discover}>
          {discovering ? $i18n('settings.tv.searching') : $i18n('settings.tv.findDevices')}
        </button>
        <span class="discover-hint">{$i18n('settings.tv.discoverHint')}</span>
      </div>

      {#if discovered.length > 0}
        <ul class="discovered-list">
          {#each discovered as d (d.device_id)}
            <li class="discovered-item">
              <div>
                <strong>{d.device_id}</strong>
                <span class="muted">{d.base_url} · {$i18n.n('settings.tv.tuneSlots', d.tune_count)}</span>
              </div>
              <button class="btn" on:click={() => selectDiscovered(d)}>{$i18n('settings.tv.use')}</button>
            </li>
          {/each}
        </ul>
      {/if}

      <div class="type-toggle">
        <label class="radio">
          <input type="radio" bind:group={addType} value="hdhomerun" />
          <span>HDHomeRun</span>
        </label>
        <label class="radio">
          <input type="radio" bind:group={addType} value="m3u" />
          <span>M3U / IPTV</span>
        </label>
      </div>

      <div class="grid">
        <label class="full">
          {$i18n('settings.tv.displayName')}
          <input type="text" bind:value={addName} placeholder={addType === 'hdhomerun' ? $i18n('settings.tv.hdhrNamePlaceholder') : $i18n('settings.tv.iptvNamePlaceholder')} />
        </label>

        {#if addType === 'hdhomerun'}
          <label class="full">
            {$i18n('settings.tv.deviceUrl')}
            <input type="text" bind:value={addHostUrl} placeholder={$i18n('settings.tv.deviceUrlPlaceholder', { ip: '10.0.0.50', url: 'http://10.0.0.50' })} />
          </label>
        {:else}
          <label class="full">
            {$i18n('settings.tv.playlistSource')}
            <input type="text" bind:value={addM3USource} placeholder={$i18n('settings.tv.playlistPlaceholder', { url: 'https://provider/playlist.m3u', path: '/var/lib/onscreen/iptv.m3u' })} />
          </label>
          <label class="full">
            {$i18n('settings.tv.userAgent')}
            <input type="text" bind:value={addM3UUserAgent} placeholder="VLC/3.0.18" />
          </label>
        {/if}
      </div>

      <div class="actions">
        <button class="btn btn-primary" disabled={adding} on:click={add}>
          {adding ? $i18n('settings.tv.adding') : $i18n('settings.tv.addTuner')}
        </button>
      </div>
    </section>

    {#if unmapped.length > 0}
      <section>
        <header>
          <h2>{$i18n('settings.tv.unmappedTitle')}</h2>
          <p class="hint">{$i18n('settings.tv.unmappedHint')}</p>
        </header>
        <ul class="tuner-list">
          {#each unmapped as ch (ch.id)}
            <li class="tuner">
              <div class="tuner-main">
                <div class="tuner-name">
                  {ch.name}
                  <span class="badge">{ch.number}</span>
                  {#if ch.callsign}<span class="badge">{ch.callsign}</span>{/if}
                </div>
              </div>
              <div class="tuner-actions">
                <select
                  class="epg-pick"
                  disabled={busyMapId === ch.id}
                  on:change={(e) => setChannelEPGID(ch.id, (e.currentTarget as HTMLSelectElement).value)}
                >
                  <option value="">{$i18n('settings.tv.pickEpgId')}</option>
                  {#each knownEPGIDs as id}
                    <option value={id}>{id}</option>
                  {/each}
                </select>
              </div>
            </li>
          {/each}
        </ul>
      </section>
    {/if}

    <DVRPostProcessing />

    <section>
      <header>
        <h2>{$i18n('settings.tv.epgTitle')}</h2>
        <p class="hint">{$i18n('settings.tv.epgHint')}</p>
      </header>

      {#if epgSources.length === 0}
        <p class="empty">{$i18n('settings.tv.noEpg')}</p>
      {:else}
        <ul class="tuner-list">
          {#each epgSources as s (s.id)}
            <li class="tuner" class:disabled={!s.enabled}>
              <div class="tuner-main">
                <div class="tuner-name">
                  {s.name}
                  <span class="badge">{s.type}</span>
                </div>
                <div class="tuner-meta">
                  {(s.config as { source?: string }).source ?? ''}
                  · {$i18n('settings.tv.refreshEvery', { n: s.refresh_interval_min })}
                  · {$i18n('settings.tv.lastPull', { when: fmtRelative(s.last_pull_at, $i18n) })}
                </div>
                {#if s.last_error}
                  <div class="meta-error">{$i18n('settings.tv.lastError', { error: s.last_error })}</div>
                {/if}
              </div>
              <div class="tuner-actions">
                <button class="btn" disabled={busyEPGId === s.id} on:click={() => refreshEPG(s)}>
                  {busyEPGId === s.id ? '…' : $i18n('settings.tv.refreshNow')}
                </button>
                <button class="btn btn-danger" disabled={busyEPGId === s.id} on:click={() => removeEPG(s)}>
                  {$i18n('settings.tv.delete')}
                </button>
              </div>
            </li>
          {/each}
        </ul>
      {/if}

      <div class="type-toggle">
        <label class="radio">
          <input type="radio" bind:group={addEPGType} value="xmltv_url" />
          <span>XMLTV URL</span>
        </label>
        <label class="radio">
          <input type="radio" bind:group={addEPGType} value="xmltv_file" />
          <span>{$i18n('settings.tv.xmltvFile')}</span>
        </label>
      </div>

      <div class="grid">
        <label class="full">
          {$i18n('settings.tv.displayName')}
          <input type="text" bind:value={addEPGName} placeholder={addEPGType === 'xmltv_url' ? $i18n('settings.tv.epgUrlNamePlaceholder') : $i18n('settings.tv.epgFileNamePlaceholder')} />
        </label>
        <label class="full">
          {addEPGType === 'xmltv_url' ? $i18n('settings.tv.playlistUrl') : $i18n('settings.tv.filePath')}
          <input type="text" bind:value={addEPGSource} placeholder={addEPGType === 'xmltv_url' ? 'https://provider/epg.xml.gz' : '/var/lib/onscreen/epg.xml'} />
        </label>
        <label>
          {$i18n('settings.tv.refreshMinutes')}
          <input type="number" bind:value={addEPGInterval} min="15" max="1440" />
        </label>
      </div>

      <div class="actions">
        <button class="btn btn-primary" disabled={addingEPG} on:click={addEPG}>
          {addingEPG ? $i18n('settings.tv.adding') : $i18n('settings.tv.addEpg')}
        </button>
      </div>
    </section>

    {#if allChannels.length > 0}
      <section>
        <header>
          <h2>{$i18n('settings.tv.channelsTitle')}</h2>
          <p class="hint">{$i18n('settings.tv.channelsHint')}</p>
        </header>
        <ul class="channel-list">
          {#each allChannels as ch, i (ch.id)}
            <li class="channel-row" class:disabled={!ch.enabled}>
              <div class="channel-info">
                {#if ch.logo_url}
                  <img class="chan-logo" src={ch.logo_url} alt="" loading="lazy" />
                {:else}
                  <div class="chan-logo-blank">{ch.number}</div>
                {/if}
                <div>
                  <div class="channel-label">
                    <span class="num">{ch.number}</span>
                    {ch.name}
                    {#if !ch.enabled}<span class="badge badge-off">{$i18n('settings.tv.hidden')}</span>{/if}
                  </div>
                  <div class="channel-sub">{ch.tuner_name} · {ch.tuner_type}</div>
                </div>
              </div>
              <div class="tuner-actions">
                <button class="btn" disabled={busyChannelId === ch.id || i === 0} on:click={() => moveChannel(ch, -1)}>↑</button>
                <button class="btn" disabled={busyChannelId === ch.id || i === allChannels.length - 1} on:click={() => moveChannel(ch, 1)}>↓</button>
                <button class="btn" disabled={busyChannelId === ch.id} on:click={() => toggleChannel(ch)}>
                  {ch.enabled ? $i18n('settings.tv.hide') : $i18n('settings.tv.show')}
                </button>
              </div>
            </li>
          {/each}
        </ul>
      </section>
    {/if}
  </div>
{/if}

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
  .muted { color: var(--text-muted); }
  .error { color: var(--error); padding: 1rem; }
  .empty { color: var(--text-muted); font-size: 0.85rem; padding: 0.5rem 0; }

  .skeleton-block {
    height: 120px; border-radius: 10px;
    background: linear-gradient(90deg, var(--bg-elevated) 25%, var(--bg-hover) 50%, var(--bg-elevated) 75%);
    background-size: 200% 100%;
    animation: shimmer 1.4s infinite;
  }
  @keyframes shimmer {
    0% { background-position: 200% 0; }
    100% { background-position: -200% 0; }
  }

  .tuner-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 0.5rem; }
  .tuner {
    display: flex; align-items: center; justify-content: space-between; gap: 1rem;
    padding: 0.85rem 1rem;
    background: var(--bg-hover);
    border: 1px solid var(--border);
    border-radius: 7px;
  }
  .tuner.disabled { opacity: 0.6; }
  .tuner-main { flex: 1; min-width: 0; }
  .tuner-name {
    display: flex; align-items: center; gap: 0.5rem;
    color: var(--text-primary); font-weight: 600; font-size: 0.92rem;
  }
  .tuner-meta {
    color: var(--text-muted); font-size: 0.75rem; margin-top: 0.2rem;
    overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  }
  .meta-error {
    color: var(--error); font-size: 0.72rem; margin-top: 0.25rem;
    overflow: hidden; text-overflow: ellipsis;
  }
  .tuner-actions { display: flex; gap: 0.4rem; }

  .badge {
    display: inline-block; padding: 0.05rem 0.4rem;
    background: var(--bg-hover); color: var(--text-secondary);
    border-radius: 4px; font-size: 0.65rem; font-weight: 500; text-transform: uppercase; letter-spacing: 0.04em;
  }
  .badge-off { background: var(--error-bg); color: var(--error); }
  .badge-live {
    background: var(--error-bg); color: var(--error);
    display: inline-flex; align-items: center; gap: 0.3rem;
  }
  /* live dot keeps a solid red literal for the pulsing on-air indicator */
  .live-dot {
    width: 6px; height: 6px; border-radius: 50%; background: rgb(255,60,60);
    animation: live-pulse 1.4s ease-in-out infinite;
  }
  @keyframes live-pulse { 0%,100% { opacity: 1; } 50% { opacity: 0.3; } }

  .broadcast-list { list-style: none; margin: 0 0 1rem; padding: 0; display: flex; flex-direction: column; gap: 0.6rem; }
  .broadcast {
    padding: 0.85rem 1rem;
    background: var(--bg-hover);
    border: 1px solid var(--border);
    border-radius: 7px;
    display: flex; flex-direction: column; gap: 0.5rem;
  }
  .broadcast-head { display: flex; align-items: center; gap: 0.5rem; }
  .broadcast-name { color: var(--text-primary); font-weight: 600; font-size: 0.92rem; }
  .kv { display: flex; align-items: center; gap: 0.5rem; flex-wrap: wrap; }
  .kv-label { font-size: 0.72rem; color: var(--text-muted); min-width: 72px; text-transform: uppercase; letter-spacing: 0.04em; }
  .mono {
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 0.78rem;
    color: var(--text-primary); background: var(--input-bg);
    padding: 0.25rem 0.5rem; border-radius: 4px; border: 1px solid var(--border);
  }
  .broadcast-actions { display: flex; gap: 0.4rem; margin-top: 0.15rem; }
  .btn-sm { padding: 0.3rem 0.6rem; font-size: 0.74rem; text-decoration: none; display: inline-flex; align-items: center; }

  .discover-row { display: flex; gap: 0.75rem; align-items: center; margin-bottom: 0.75rem; }
  .discover-hint { font-size: 0.75rem; color: var(--text-muted); }
  .discovered-list { list-style: none; margin: 0 0 1rem; padding: 0; display: flex; flex-direction: column; gap: 0.3rem; }
  .discovered-item {
    display: flex; justify-content: space-between; align-items: center;
    padding: 0.55rem 0.8rem; background: var(--bg-hover); border: 1px solid var(--border);
    border-radius: 7px; font-size: 0.82rem;
  }
  .discovered-item .muted { margin-left: 0.75rem; }

  .channel-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 0.35rem; max-height: 540px; overflow-y: auto; }
  .channel-row {
    display: flex; justify-content: space-between; align-items: center; gap: 0.75rem;
    padding: 0.6rem 0.85rem;
    background: var(--bg-hover);
    border: 1px solid var(--border);
    border-radius: 7px;
  }
  .channel-row.disabled { opacity: 0.55; }
  .channel-info { display: flex; align-items: center; gap: 0.65rem; min-width: 0; }
  /* logo sits on a pure-black plate for max contrast against any artwork */
  .chan-logo { width: 40px; height: 28px; object-fit: contain; background: var(--bg-primary); border-radius: 4px; flex-shrink: 0; }
  .chan-logo-blank {
    width: 40px; height: 28px; display: flex; align-items: center; justify-content: center;
    font-weight: 700; font-size: 0.7rem; background: var(--bg-elevated); border-radius: 4px; flex-shrink: 0;
  }
  .channel-label { font-size: 0.88rem; color: var(--text-primary); display: flex; gap: 0.4rem; align-items: center; }
  .channel-label .num { color: var(--text-muted); font-size: 0.75rem; font-weight: 600; }
  .channel-sub { font-size: 0.72rem; color: var(--text-muted); }

  .epg-pick {
    padding: 0.48rem 0.7rem; border-radius: 7px;
    border: 1px solid var(--border-strong);
    background: var(--input-bg); color: var(--text-primary);
    font-size: 0.82rem; min-width: 200px;
  }
  .epg-pick:focus {
    outline: none; border-color: var(--accent);
    box-shadow: 0 0 0 3px var(--accent-bg);
  }

  .type-toggle { display: flex; gap: 1.25rem; margin-bottom: 1rem; }
  .radio { display: flex; align-items: center; gap: 0.4rem; cursor: pointer; font-size: 0.85rem; }
  .radio input[type="radio"] { accent-color: var(--accent); }

  .grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 0.75rem 1rem;
    margin: 0 0 1rem;
  }
  .grid .full { grid-column: 1 / -1; }
  label {
    display: flex; flex-direction: column; gap: 0.3rem;
    font-size: 0.78rem; color: var(--text-secondary);
  }
  input[type="text"], input[type="number"] {
    padding: 0.48rem 0.7rem; border-radius: 7px;
    border: 1px solid var(--border-strong);
    background: var(--input-bg); color: var(--text-primary);
    font-family: inherit; font-size: 0.85rem;
  }
  input[type="text"]:focus, input[type="number"]:focus {
    outline: none; border-color: var(--accent);
    box-shadow: 0 0 0 3px var(--accent-bg);
  }
  input::placeholder { color: var(--text-muted); }

  .actions { display: flex; gap: 0.5rem; }
  .btn {
    padding: 0.45rem 0.9rem; background: transparent;
    color: var(--text-secondary); border: 1px solid var(--border-strong);
    border-radius: 7px; font-size: 0.78rem; font-weight: 500; cursor: pointer;
    transition: background .15s, border-color .15s, color .15s;
  }
  .btn:hover:not(:disabled) { background: var(--bg-hover); border-color: var(--text-muted); }
  .btn:focus-visible { outline: none; border-color: var(--accent); box-shadow: 0 0 0 3px var(--accent-bg); }
  .btn:disabled { opacity: 0.5; cursor: not-allowed; }
  .btn-primary {
    background: var(--accent); color: #fff; border: none;
    font-size: 0.8rem; font-weight: 600;
  }
  .btn-primary:hover:not(:disabled) { background: var(--accent-hover); }
  .btn-danger {
    background: var(--error-bg); border: 1px solid var(--error); color: var(--error);
    font-size: 0.8rem; font-weight: 600;
  }
  .btn-danger:hover:not(:disabled) { background: var(--error-bg); border-color: var(--error); }
</style>
