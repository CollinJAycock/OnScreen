<script lang="ts">
  import { onMount } from 'svelte';
  import { settingsApi } from '$lib/api';
  import type { EncoderInfo, FleetStatus, TranscodeConfig } from '$lib/api';
  import { toast } from '$lib/stores/toast';

  // Fleet management state
  interface FleetWorkerRow {
    id: string;
    addr: string;
    name: string;
    encoder: string;
    online: boolean;
    active_sessions: number;
    max_sessions: number;
    capabilities: string[];
    isNew?: boolean;
  }

  let fleetLoaded = false;
  let fleetEmbeddedEnabled = true;
  let fleetEmbeddedDisabledByEnv = false;
  let fleetEmbeddedEncoder = '';
  let fleetEmbeddedOnline = false;
  let fleetEmbeddedActiveSessions = 0;
  let fleetEmbeddedMaxSessions = 0;
  let fleetEmbeddedCapabilities: string[] = [];
  let fleetWorkers: FleetWorkerRow[] = [];
  let fleetSaving = false;

  // Encoder info (available encoders)
  let encoderInfo: EncoderInfo | null = null;

  // Encoder tuning state
  let nvencPreset = 'p4';
  let nvencTune = 'hq';
  let nvencRc = 'vbr';
  let maxrateRatio = 1.5;
  let tuningSaving = false;
  let tuningLoaded = false;

  onMount(async () => {
    // Load encoder info
    try {
      encoderInfo = await settingsApi.getEncoders();
    } catch { /* ignore */ }

    // Load fleet config
    try {
      const f = await settingsApi.getFleet();
      fleetEmbeddedEnabled = f.embedded_enabled;
      fleetEmbeddedDisabledByEnv = f.embedded_disabled_by_env || false;
      fleetEmbeddedEncoder = f.embedded_encoder || '';
      fleetEmbeddedOnline = f.embedded_online;
      fleetEmbeddedActiveSessions = f.embedded_active_sessions;
      fleetEmbeddedMaxSessions = f.embedded_max_sessions;
      fleetEmbeddedCapabilities = f.embedded_capabilities || [];
      fleetWorkers = (f.workers || []).map(w => ({ ...w }));
      fleetLoaded = true;
    } catch (e) { console.error('fleet load failed:', e); fleetLoaded = true; }

    // Load encoder tuning config
    try {
      const tc = await settingsApi.getTranscodeConfig();
      nvencPreset = tc.nvenc_preset || 'p4';
      nvencTune = tc.nvenc_tune || 'hq';
      nvencRc = tc.nvenc_rc || 'vbr';
      maxrateRatio = tc.maxrate_ratio || 1.5;
      tuningLoaded = true;
    } catch { tuningLoaded = true; }
  });

  let nextManualId = 1;
  function addWorker() {
    fleetWorkers = [...fleetWorkers, {
      id: `new-${nextManualId++}`,
      addr: '',
      name: '',
      encoder: '',
      online: false,
      active_sessions: 0,
      max_sessions: 0,
      capabilities: [],
      isNew: true
    }];
  }

  function removeWorker(id: string) {
    fleetWorkers = fleetWorkers.filter(w => w.id !== id);
  }

  async function saveFleet() {
    fleetSaving = true;
    try {
      const workers = fleetWorkers
        .filter(w => w.name.trim())
        .map(w => ({ addr: w.addr || '', name: w.name.trim(), encoder: w.encoder, max_sessions: w.max_sessions || undefined }));
      await settingsApi.updateFleet({
        embedded_enabled: fleetEmbeddedEnabled,
        embedded_encoder: fleetEmbeddedEncoder,
        workers
      });
      toast.success('Fleet config saved');
      const updated = await settingsApi.getFleet();
      fleetEmbeddedEnabled = updated.embedded_enabled;
      fleetEmbeddedDisabledByEnv = updated.embedded_disabled_by_env || false;
      fleetEmbeddedEncoder = updated.embedded_encoder || '';
      fleetEmbeddedOnline = updated.embedded_online;
      fleetEmbeddedActiveSessions = updated.embedded_active_sessions;
      fleetEmbeddedMaxSessions = updated.embedded_max_sessions;
      fleetEmbeddedCapabilities = updated.embedded_capabilities || [];
      fleetWorkers = (updated.workers || []).map(w => ({ ...w }));
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Failed to save fleet config');
    } finally {
      fleetSaving = false;
    }
  }

  async function saveTuning() {
    tuningSaving = true;
    try {
      await settingsApi.updateTranscodeConfig({
        nvenc_preset: nvencPreset,
        nvenc_tune: nvencTune,
        nvenc_rc: nvencRc,
        maxrate_ratio: maxrateRatio,
      });
      toast.success('Encoder tuning saved');
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Failed to save encoder tuning');
    } finally {
      tuningSaving = false;
    }
  }
</script>

<svelte:head><title>Transcode Settings — OnScreen</title></svelte:head>

<div class="page">

  <!-- ── Encoder Tuning ─────────────────────────────────────────────── -->
  {#if tuningLoaded}
  <section>
    <div class="sec-label">Encoder Tuning</div>
    <div class="hint" style="margin-top: -0.5rem;">
      Fine-tune FFmpeg encoding parameters for your GPU and network. These settings apply to all
      new transcode sessions. Changes take effect on the next transcode job without restarting.
    </div>

    <div class="field-row">
      <div class="field" style="flex:1;">
        <label for="nvenc-preset">NVENC Preset</label>
        <select id="nvenc-preset" bind:value={nvencPreset}>
          <option value="p1">p1 — Fastest</option>
          <option value="p2">p2</option>
          <option value="p3">p3</option>
          <option value="p4">p4 — Balanced (default)</option>
          <option value="p5">p5</option>
          <option value="p6">p6</option>
          <option value="p7">p7 — Best Quality</option>
        </select>
        <div class="hint">Speed vs quality trade-off. Lower presets reduce GPU load.</div>
      </div>

      <div class="field" style="flex:1;">
        <label for="nvenc-tune">NVENC Tune</label>
        <select id="nvenc-tune" bind:value={nvencTune}>
          <option value="hq">High Quality (default)</option>
          <option value="ll">Low Latency</option>
          <option value="ull">Ultra-Low Latency</option>
        </select>
        <div class="hint">HQ is best for media servers. LL/ULL are for live streaming.</div>
      </div>
    </div>

    <div class="field-row">
      <div class="field" style="flex:1;">
        <label for="nvenc-rc">Rate Control</label>
        <select id="nvenc-rc" bind:value={nvencRc}>
          <option value="vbr">VBR — Variable Bitrate (default)</option>
          <option value="cbr">CBR — Constant Bitrate</option>
          <option value="constqp">CQP — Constant Quantizer</option>
        </select>
        <div class="hint">VBR gives the best quality per bit. CBR provides stable bandwidth.</div>
      </div>

      <div class="field" style="flex:1;">
        <label for="maxrate-ratio">Peak Bitrate Ratio</label>
        <select id="maxrate-ratio" bind:value={maxrateRatio}>
          <option value={1.0}>1.0x — Strict (no headroom)</option>
          <option value={1.2}>1.2x — Tight</option>
          <option value={1.5}>1.5x — Balanced (default)</option>
          <option value={2.0}>2.0x — Generous</option>
          <option value={3.0}>3.0x — Unconstrained</option>
        </select>
        <div class="hint">
          Peak bitrate = target bitrate x ratio. Higher values handle complex scenes
          (action, grain) better but use more bandwidth.
        </div>
      </div>
    </div>

    <div class="section-foot">
      <button class="btn-save" disabled={tuningSaving} on:click={saveTuning}>
        {tuningSaving ? 'Saving...' : 'Save Tuning'}
      </button>
    </div>
  </section>
  {/if}

  <!-- ── Transcode Fleet ────────────────────────────────────────────── -->
  {#if fleetLoaded}
  <section>
    <div class="sec-label">Transcode Fleet</div>
    <div class="hint" style="margin-top: -0.5rem;">
      Manage the transcode workers that process HLS jobs. The embedded worker runs inside the server process.
      Local workers are separate processes on this machine. Workers auto-register when started.
    </div>

    <!-- Embedded worker -->
    <div class="fleet-group">
      <div class="fleet-group-header">
        <label class="toggle-label">
          <input type="checkbox" bind:checked={fleetEmbeddedEnabled} disabled={fleetEmbeddedDisabledByEnv} />
          Embedded Worker
        </label>
        {#if fleetEmbeddedDisabledByEnv}
          <span class="hint" style="margin-left: 0.5rem; font-size: 0.75rem;">Disabled by DISABLE_EMBEDDED_WORKER env</span>
        {:else if fleetEmbeddedOnline}
          <span class="status-dot online"></span>
        {:else if fleetEmbeddedEnabled}
          <span class="status-dot offline"></span>
        {/if}
      </div>
      {#if fleetEmbeddedEnabled && !fleetEmbeddedDisabledByEnv}
      <div class="fleet-row">
        <div class="field" style="flex:1;">
          <label for="embedded-encoder">Encoder</label>
          <select id="embedded-encoder" bind:value={fleetEmbeddedEncoder}>
            <option value="">Auto-detect</option>
            {#if encoderInfo}
              {#each encoderInfo.detected as entry}
                <option value={entry.encoder}>{entry.label}</option>
              {/each}
            {/if}
          </select>
        </div>
        {#if fleetEmbeddedOnline}
        <div class="fleet-live-info">
          <span>{fleetEmbeddedActiveSessions}/{fleetEmbeddedMaxSessions} sessions</span>
          {#each fleetEmbeddedCapabilities as cap}
            <span class="worker-cap">{cap}</span>
          {/each}
        </div>
        {/if}
      </div>
      {/if}
    </div>

    <!-- Local workers -->
    <div class="fleet-group" style="margin-top: 1rem;">
      <div class="fleet-group-header">
        <span class="fleet-group-title">Local Workers</span>
      </div>
      {#each fleetWorkers as row (row.id)}
        <div class="worker-card">
          <div class="fleet-row">
            <div class="field" style="flex:2;">
              <label>Name</label>
              <input type="text" bind:value={row.name} placeholder="e.g. NVIDIA Box" />
            </div>
            <div class="field" style="flex:2;">
              <label>Encoder</label>
              <select bind:value={row.encoder}>
                <option value="">Auto-detect</option>
                {#if encoderInfo}
                  {#each encoderInfo.detected as entry}
                    <option value={entry.encoder}>{entry.label}</option>
                  {/each}
                {/if}
              </select>
            </div>
            <div class="field" style="flex:1;">
              <label>Max Sessions</label>
              <input type="number" bind:value={row.max_sessions} min="0" max="100" placeholder="auto" />
            </div>
            {#if !row.online}
              <button type="button" class="btn-remove" title="Remove worker"
                on:click={() => removeWorker(row.id)}>&times;</button>
            {/if}
          </div>
          <div class="fleet-live-info">
            {#if row.online}
              <span class="status-dot online"></span>
              <span>{row.active_sessions}/{row.max_sessions} sessions</span>
              {#each row.capabilities || [] as cap}
                <span class="worker-cap">{cap}</span>
              {/each}
            {:else}
              <span class="status-dot offline"></span>
              <span class="text-muted">{row.isNew ? 'Not yet saved' : 'Offline'}</span>
            {/if}
          </div>
        </div>
      {/each}

      {#if fleetWorkers.length === 0}
        <div class="hint">No local workers running. Workers auto-register when started — add one here to pre-configure its name and encoder.</div>
      {/if}

      <button type="button" class="btn-outline btn-add-mapping" style="margin-top: 0.5rem;" on:click={addWorker}>
        + Add Worker
      </button>
    </div>

    <div class="section-foot" style="margin-top: 1rem;">
      <button class="btn-save" disabled={fleetSaving} on:click={saveFleet}>
        {fleetSaving ? 'Saving...' : 'Save Fleet Config'}
      </button>
    </div>
  </section>
  {/if}

</div>

<style>
  .page { max-width: 520px; }

  section {
    padding: 1.25rem 0;
    border-bottom: 1px solid var(--border);
    display: flex; flex-direction: column; gap: 1.25rem;
  }

  .sec-label {
    font-size: 0.68rem; font-weight: 700;
    text-transform: uppercase; letter-spacing: 0.09em;
    color: var(--text-muted);
  }

  .field { display: flex; flex-direction: column; gap: 0.3rem; }

  .field-row {
    display: flex;
    gap: 1rem;
    align-items: flex-start;
  }

  label { font-size: 0.75rem; font-weight: 500; color: var(--text-muted); }

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
  input {
    font-family: monospace;
  }
  input:focus { outline: none; border-color: var(--accent); box-shadow: 0 0 0 3px var(--accent-bg); }
  ::placeholder { color: var(--text-muted); }

  .hint {
    font-size: 0.72rem;
    color: var(--text-muted);
    line-height: 1.5;
    margin-top: 0.15rem;
  }

  select { cursor: pointer; }
  select:focus { outline: none; border-color: var(--accent); box-shadow: 0 0 0 3px var(--accent-bg); }
  select option { background: var(--bg-elevated); color: var(--text-primary); }

  .section-foot { display: flex; justify-content: flex-end; }

  .btn-save {
    padding: 0.42rem 0.9rem; background: var(--accent);
    border: none; border-radius: 7px; color: #fff;
    font-size: 0.8rem; font-weight: 600; cursor: pointer; transition: background 0.15s;
  }
  .btn-save:hover { background: var(--accent-hover); }
  .btn-save:disabled { opacity: 0.5; cursor: not-allowed; }

  .btn-outline {
    padding: 0.36rem 0.75rem;
    background: transparent;
    border: 1px solid var(--border-strong);
    border-radius: 7px;
    color: var(--text-secondary);
    font-size: 0.78rem;
    font-weight: 500;
    cursor: pointer;
    transition: background 0.12s, border-color 0.12s;
  }
  .btn-outline:hover { background: var(--bg-hover); border-color: var(--text-muted); }

  .btn-add-mapping {
    align-self: flex-start;
    font-size: 0.72rem;
    padding: 0.28rem 0.6rem;
    margin-top: 0.2rem;
  }

  /* ── Fleet ────────────────────────────────────────────────────────────── */
  .fleet-group {
    background: rgba(255,255,255,0.02);
    border: 1px solid rgba(255,255,255,0.06);
    border-radius: 0.5rem;
    padding: 0.75rem 1rem;
  }
  .fleet-group-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-bottom: 0.5rem;
  }
  .fleet-group-title {
    font-size: 0.85rem;
    font-weight: 600;
    color: var(--text-primary);
  }
  .toggle-label {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    font-size: 0.85rem;
    font-weight: 600;
    color: var(--text-primary);
    cursor: pointer;
  }
  .toggle-label input[type="checkbox"] {
    width: 1rem;
    height: 1rem;
    accent-color: var(--accent);
  }
  .fleet-row {
    display: flex;
    gap: 0.75rem;
    align-items: flex-end;
  }
  .fleet-live-info {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    margin-top: 0.375rem;
    font-size: 0.75rem;
    color: var(--text-muted);
  }
  .status-dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    display: inline-block;
    flex-shrink: 0;
  }
  .status-dot.online { background: var(--success); }
  .status-dot.offline { background: #555; }
  .text-muted { color: var(--text-muted); }
  .worker-card {
    background: rgba(255,255,255,0.03);
    border: 1px solid rgba(255,255,255,0.06);
    border-radius: 0.5rem;
    padding: 0.75rem 1rem;
    margin-bottom: 0.5rem;
  }
  .worker-cap {
    font-size: 0.7rem;
    padding: 0.125rem 0.5rem;
    border-radius: 999px;
    background: var(--accent-bg);
    color: var(--accent);
  }
  .btn-remove {
    flex-shrink: 0;
    background: transparent;
    border: 1px solid rgba(248,113,113,0.25);
    color: var(--error);
    border-radius: 6px;
    padding: 0.3rem 0.55rem;
    cursor: pointer;
    font-size: 0.75rem;
    font-weight: 600;
    line-height: 1;
    margin-bottom: 0.25rem;
  }
  .btn-remove:hover { background: rgba(248,113,113,0.08); }

  /* ── Mobile ────────────────────────────────────────────────────────────── */
  @media (max-width: 768px) {
    .page { max-width: 100%; }
    .field-row { flex-direction: column; gap: 0.75rem; }
    input { width: 100%; }
  }
</style>
