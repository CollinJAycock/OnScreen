<script lang="ts">
  import { onMount } from 'svelte';
  import { liveTvApi, type LiveTVTuner, type LiveTVEPGSource } from '$lib/api';
  import { toast } from '$lib/stores/toast';

  let tuners: LiveTVTuner[] = [];
  let epgSources: LiveTVEPGSource[] = [];
  let loading = true;
  let error = '';

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

  onMount(load);

  async function load() {
    loading = true; error = '';
    try {
      const [tRes, eRes] = await Promise.all([
        liveTvApi.listTuners(),
        liveTvApi.listEPGSources(),
      ]);
      tuners = tRes.items;
      epgSources = eRes.items;
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed to load settings';
    } finally { loading = false; }
  }

  async function addEPG() {
    if (!addEPGName.trim()) { toast.error('Name is required'); return; }
    if (!addEPGSource.trim()) { toast.error('Source URL or path is required'); return; }
    addingEPG = true;
    try {
      const created = await liveTvApi.createEPGSource({
        type: addEPGType,
        name: addEPGName.trim(),
        config: { source: addEPGSource.trim() },
        refresh_interval_min: addEPGInterval,
      });
      toast.success(`Added "${addEPGName.trim()}" — refreshing now…`);
      addEPGName = ''; addEPGSource = '';
      await load();
      // Auto-refresh on first add so the user sees ingestion stats immediately
      // rather than having to click "Refresh" as a follow-up.
      await refreshEPG(created);
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Failed to add EPG source');
    } finally { addingEPG = false; }
  }

  async function refreshEPG(s: LiveTVEPGSource) {
    busyEPGId = s.id;
    try {
      const res = await liveTvApi.refreshEPGSource(s.id);
      toast.success(`${s.name}: ingested ${res.programs_ingested.toLocaleString()} programs, auto-matched ${res.channels_auto_matched} channels`);
      await load();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Refresh failed');
      await load();
    } finally { busyEPGId = ''; }
  }

  async function removeEPG(s: LiveTVEPGSource) {
    if (!confirm(`Delete EPG source "${s.name}"? Already-ingested programs stay until they expire.`)) return;
    busyEPGId = s.id;
    try {
      await liveTvApi.deleteEPGSource(s.id);
      toast.success(`${s.name} deleted`);
      await load();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Delete failed');
    } finally { busyEPGId = ''; }
  }

  async function add() {
    if (!addName.trim()) { toast.error('Name is required'); return; }
    let config: Record<string, unknown>;
    if (addType === 'hdhomerun') {
      if (!addHostUrl.trim()) { toast.error('Host URL is required'); return; }
      // Allow plain "10.0.0.50" in the input; HDHomeRun config wants a full URL.
      const url = /^https?:\/\//.test(addHostUrl) ? addHostUrl : `http://${addHostUrl}`;
      config = { host_url: url };
    } else {
      if (!addM3USource.trim()) { toast.error('Source URL or path is required'); return; }
      config = { source: addM3USource };
      if (addM3UUserAgent) config.user_agent = addM3UUserAgent;
    }

    adding = true;
    try {
      await liveTvApi.createTuner({ type: addType, name: addName.trim(), config });
      toast.success(`Added ${addName.trim()} — discovering channels…`);
      addName = ''; addHostUrl = ''; addM3USource = ''; addM3UUserAgent = '';
      await load();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Failed to add tuner');
    } finally { adding = false; }
  }

  async function rescan(t: LiveTVTuner) {
    busyId = t.id;
    try {
      const res = await liveTvApi.rescanTuner(t.id);
      toast.success(`${t.name}: found ${res.channel_count} channels`);
      await load();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Rescan failed');
    } finally { busyId = ''; }
  }

  async function toggle(t: LiveTVTuner) {
    busyId = t.id;
    try {
      await liveTvApi.updateTuner(t.id, {
        name: t.name,
        config: t.config,
        tune_count: t.tune_count,
        enabled: !t.enabled,
      });
      await load();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Update failed');
    } finally { busyId = ''; }
  }

  async function remove(t: LiveTVTuner) {
    if (!confirm(`Delete tuner "${t.name}"? This removes all its channels and EPG data.`)) return;
    busyId = t.id;
    try {
      await liveTvApi.deleteTuner(t.id);
      toast.success(`${t.name} deleted`);
      await load();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Delete failed');
    } finally { busyId = ''; }
  }

  function fmtRelative(iso?: string): string {
    if (!iso) return 'never';
    const ms = Date.now() - new Date(iso).getTime();
    if (ms < 60_000) return 'just now';
    if (ms < 3_600_000) return `${Math.floor(ms / 60_000)}m ago`;
    if (ms < 86_400_000) return `${Math.floor(ms / 3_600_000)}h ago`;
    return `${Math.floor(ms / 86_400_000)}d ago`;
  }
</script>

{#if loading}
  <p class="muted">Loading…</p>
{:else if error}
  <p class="error">{error}</p>
{:else}
  <div class="wrap">
    <section>
      <header>
        <h2>Tuners</h2>
        <p class="hint">
          Live TV pulls channels from network-attached tuners (HDHomeRun) and
          IPTV playlists (M3U). Each tuner is scanned automatically when added.
        </p>
      </header>

      {#if tuners.length === 0}
        <p class="empty">No tuners configured yet.</p>
      {:else}
        <ul class="tuner-list">
          {#each tuners as t (t.id)}
            <li class="tuner" class:disabled={!t.enabled}>
              <div class="tuner-main">
                <div class="tuner-name">
                  {t.name}
                  <span class="badge">{t.type}</span>
                  {#if !t.enabled}<span class="badge badge-off">disabled</span>{/if}
                </div>
                <div class="tuner-meta">
                  {#if t.type === 'hdhomerun'}
                    {(t.config as { host_url?: string }).host_url ?? ''}
                  {:else if t.type === 'm3u'}
                    {(t.config as { source?: string }).source ?? ''}
                  {/if}
                  · {t.tune_count} tune slot{t.tune_count === 1 ? '' : 's'}
                  · last seen {fmtRelative(t.last_seen_at)}
                </div>
              </div>
              <div class="tuner-actions">
                <button class="btn" disabled={busyId === t.id} on:click={() => rescan(t)}>
                  {busyId === t.id ? '…' : 'Rescan'}
                </button>
                <button class="btn" disabled={busyId === t.id} on:click={() => toggle(t)}>
                  {t.enabled ? 'Disable' : 'Enable'}
                </button>
                <button class="btn btn-danger" disabled={busyId === t.id} on:click={() => remove(t)}>
                  Delete
                </button>
              </div>
            </li>
          {/each}
        </ul>
      {/if}
    </section>

    <section>
      <header>
        <h2>Add tuner</h2>
        <p class="hint">
          HDHomeRun: enter the device's IP or full URL — discovery runs
          immediately. M3U: paste a playlist URL or absolute file path; some
          IPTV providers gate on a custom User-Agent.
        </p>
      </header>

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
          Display name
          <input type="text" bind:value={addName} placeholder={addType === 'hdhomerun' ? 'Living Room HDHR' : 'My IPTV provider'} />
        </label>

        {#if addType === 'hdhomerun'}
          <label class="full">
            Device URL or IP
            <input type="text" bind:value={addHostUrl} placeholder="10.0.0.50  or  http://10.0.0.50" />
          </label>
        {:else}
          <label class="full">
            Playlist source
            <input type="text" bind:value={addM3USource} placeholder="https://provider/playlist.m3u  or  /var/lib/onscreen/iptv.m3u" />
          </label>
          <label class="full">
            User-Agent (optional)
            <input type="text" bind:value={addM3UUserAgent} placeholder="VLC/3.0.18" />
          </label>
        {/if}
      </div>

      <div class="actions">
        <button class="btn btn-primary" disabled={adding} on:click={add}>
          {adding ? 'Adding…' : 'Add tuner'}
        </button>
      </div>
    </section>

    <section>
      <header>
        <h2>EPG sources</h2>
        <p class="hint">
          Pulls program data (titles, descriptions, schedules) from XMLTV
          feeds. Channels are auto-matched by lcn → channel number,
          falling back to display-name → callsign. Schedules Direct support
          coming later.
        </p>
      </header>

      {#if epgSources.length === 0}
        <p class="empty">No EPG sources configured yet — the guide will be empty.</p>
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
                  · refresh every {s.refresh_interval_min}m
                  · last pull {s.last_pull_at ? fmtRelative(s.last_pull_at) : 'never'}
                </div>
                {#if s.last_error}
                  <div class="meta-error">Last error: {s.last_error}</div>
                {/if}
              </div>
              <div class="tuner-actions">
                <button class="btn" disabled={busyEPGId === s.id} on:click={() => refreshEPG(s)}>
                  {busyEPGId === s.id ? '…' : 'Refresh now'}
                </button>
                <button class="btn btn-danger" disabled={busyEPGId === s.id} on:click={() => removeEPG(s)}>
                  Delete
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
          <span>XMLTV file path</span>
        </label>
      </div>

      <div class="grid">
        <label class="full">
          Display name
          <input type="text" bind:value={addEPGName} placeholder={addEPGType === 'xmltv_url' ? 'IPTV provider EPG' : 'Local XMLTV grab'} />
        </label>
        <label class="full">
          {addEPGType === 'xmltv_url' ? 'Playlist URL' : 'File path'}
          <input type="text" bind:value={addEPGSource} placeholder={addEPGType === 'xmltv_url' ? 'https://provider/epg.xml.gz' : '/var/lib/onscreen/epg.xml'} />
        </label>
        <label>
          Refresh every (minutes)
          <input type="number" bind:value={addEPGInterval} min="15" max="1440" />
        </label>
      </div>

      <div class="actions">
        <button class="btn btn-primary" disabled={addingEPG} on:click={addEPG}>
          {addingEPG ? 'Adding…' : 'Add EPG source'}
        </button>
      </div>
    </section>
  </div>
{/if}

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
  .muted { color: var(--text-muted); padding: 1rem; }
  .error { color: var(--error); padding: 1rem; }
  .empty { color: var(--text-muted); font-size: 0.85rem; padding: 0.5rem 0; }

  .tuner-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 0.5rem; }
  .tuner {
    display: flex; align-items: center; justify-content: space-between; gap: 1rem;
    padding: 0.85rem 1rem;
    background: var(--bg);
    border: 1px solid rgba(255,255,255,0.05);
    border-radius: 6px;
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
    background: rgba(255,255,255,0.06); color: var(--text-secondary);
    border-radius: 3px; font-size: 0.65rem; font-weight: 500; text-transform: uppercase; letter-spacing: 0.04em;
  }
  .badge-off { background: rgba(255,100,100,0.12); color: rgb(255,140,140); }

  .type-toggle { display: flex; gap: 1.25rem; margin-bottom: 1rem; }
  .radio { display: flex; align-items: center; gap: 0.4rem; cursor: pointer; font-size: 0.85rem; }

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
    padding: 0.45rem 0.6rem; border-radius: 4px;
    border: 1px solid rgba(255,255,255,0.1);
    background: var(--bg); color: var(--text-primary);
    font-family: inherit; font-size: 0.85rem;
  }

  .actions { display: flex; gap: 0.5rem; }
  .btn {
    padding: 0.45rem 0.85rem; background: var(--bg);
    color: var(--text-primary); border: 1px solid rgba(255,255,255,0.1);
    border-radius: 4px; font-size: 0.8rem; cursor: pointer;
  }
  .btn:hover:not(:disabled) { background: var(--bg-hover); }
  .btn:disabled { opacity: 0.5; cursor: not-allowed; }
  .btn-primary { background: var(--accent); color: white; border-color: var(--accent); }
  .btn-primary:hover:not(:disabled) { filter: brightness(1.1); }
  .btn-danger { color: var(--error); }
  .btn-danger:hover:not(:disabled) { background: var(--error-bg); }
</style>
