<script lang="ts">
  import { onMount } from 'svelte';
  import { settingsApi } from '$lib/api';
  import type { EncoderInfo, FleetStatus, TranscodeConfig, WorkerCredentials } from '$lib/api';
  import { toast } from '$lib/stores/toast';
  import { t, tr } from '$lib/i18n/index.svelte';

  // Fleet management state
  interface FleetWorkerRow {
    id: string;
    addr: string;
    name: string;
    encoder: string;
    online: boolean;
    active_sessions: number;
    max_sessions: number;
    load_percent: number;
    gpu_tonemap: boolean;
    capabilities: string[];
    /** Per-worker encoder→label map. Used to render each worker row's
     *  Device dropdown so it only lists the encoders THIS worker has,
     *  with the worker's own GPU label — historically the dropdown
     *  re-used the host's merged encoder list, which on a laptop joined
     *  to a desktop primary showed the desktop's RTX 5080 as a choice
     *  for the laptop. */
    encoder_labels?: Record<string, string>;
    isNew?: boolean;
  }

  let fleetLoaded = false;
  let fleetEmbeddedEnabled = true;
  let fleetEmbeddedDisabledByEnv = false;
  let fleetEmbeddedEncoder = '';

  // Color tier for the cost-weighted load badge: green under half, amber
  // approaching saturation, red when effectively full.
  function loadClass(p: number): string {
    if (p >= 85) return 'load-high';
    if (p >= 50) return 'load-mid';
    return 'load-low';
  }

  // Friendly codec names for the device-group label.
  function codecForEncoder(enc: string): string {
    if (enc.includes('av1')) return 'AV1';
    if (enc.startsWith('hevc') || enc === 'libx265') return 'HEVC';
    return 'H.264';
  }

  /** Group (encoder, label) entries by physical device.
   *  Modern GPUs (NVIDIA RTX, Intel Arc, AMD RDNA3) hardware-encode
   *  multiple codecs — h264_nvenc and hevc_nvenc on a 5080 are the
   *  same physical card, just different output formats. The detect
   *  probe surfaces them as separate entries with identical labels;
   *  we collapse those into one selectable device whose value carries
   *  every codec it supports, so the operator picks "RTX 5080" once
   *  and the worker can transcode H.264 *or* HEVC depending on what
   *  the session needs (h264 for 1080p, hevc for 4K bitrate savings).
   *  The wire format stays a comma-separated string for backwards
   *  compatibility with the existing `embedded_encoder` field.
   *
   *  Takes the encoder capabilities array + a label map (encoder →
   *  human label). For the embedded section, callers pass the host's
   *  filtered detected encoders. For each remote worker, callers pass
   *  that worker's capabilities + its own encoder_labels — otherwise
   *  the dropdown would show the primary's labels for the worker's
   *  identically-coded encoders (e.g. a laptop's "h264_nvenc" rendered
   *  as the desktop's "RTX 5080" instead of the laptop's "RTX 4080
   *  Laptop GPU"). */
  type DeviceGroup = { label: string; value: string; codecs: string[] };
  function groupedDevices(caps: string[], labels: Record<string, string>): DeviceGroup[] {
    if (!caps || caps.length === 0) return [];
    const groups = new Map<string, { label: string; encs: string[] }>();
    for (const enc of caps) {
      const label = labels[enc] || enc;
      let group = groups.get(label);
      if (!group) {
        group = { label, encs: [] };
        groups.set(label, group);
      }
      if (!group.encs.includes(enc)) group.encs.push(enc);
    }
    return Array.from(groups.values()).map((g) => ({
      label: g.label,
      value: g.encs.join(','),
      codecs: g.encs.map(codecForEncoder).filter((c, i, a) => a.indexOf(c) === i),
    }));
  }

  /** Labels-by-encoder map sourced from the host-side `encoderInfo.detected`
   *  list. `encoderInfo.detected` is merged with remote workers' encoders
   *  on the server side (so an admin can see the fleet's total capability
   *  surface in one place); per-worker dropdowns now use the per-worker
   *  encoder_labels map instead, but the embedded section reads from this
   *  filtered to whatever the embedded worker actually advertises. */
  function embeddedLabelMap(): Record<string, string> {
    if (!encoderInfo) return {};
    const out: Record<string, string> = {};
    for (const entry of encoderInfo.detected) {
      out[entry.encoder] = entry.label;
    }
    return out;
  }
  let fleetEmbeddedOnline = false;
  let fleetEmbeddedActiveSessions = 0;
  let fleetEmbeddedMaxSessions = 0;
  let fleetEmbeddedLoad = 0;
  let fleetEmbeddedTonemap = false;
  let fleetEmbeddedCapabilities: string[] = [];
  let fleetWorkers: FleetWorkerRow[] = [];
  let fleetSaving = false;

  // Encoder info (available encoders)
  let encoderInfo: EncoderInfo | null = null;

  // "Add a worker node" helper. Prefer the server's detected LAN IP (from the
  // fleet API) — that's the address a remote worker can actually reach. Fall
  // back to the host the admin used to reach the UI, but never "localhost"
  // (useless for a remote worker) — show a clear placeholder instead.
  let serverHost = '‹this-server-LAN-IP›';
  $: dbExample = `postgres://postgres:‹password›@${serverHost}:5432/onscreen?sslmode=disable`;
  $: valkeyExample = `redis://${serverHost}:6379`;

  async function copyText(s: string) {
    try {
      await navigator.clipboard.writeText(s);
      toast.success(t('settings.transcode.copied'));
    } catch {
      toast.error(t('settings.transcode.copyFailed'));
    }
  }

  // Step-up reveal of the real DATABASE_URL + SECRET_KEY (re-enter password).
  let showRevealForm = false;
  let revealPassword = '';
  let revealTotp = '';
  let revealLoading = false;
  let revealed: WorkerCredentials | null = null;

  // Once revealed, show the real values; otherwise the copy-shapes with the
  // password elided. database_url from the API already has the LAN-IP host.
  $: dbValue = revealed ? revealed.database_url : dbExample;
  $: valkeyValue = revealed ? revealed.valkey_url : valkeyExample;

  async function doReveal() {
    revealLoading = true;
    try {
      revealed = await settingsApi.revealWorkerCredentials(revealPassword, revealTotp);
      showRevealForm = false;
      revealPassword = '';
      revealTotp = '';
      toast.success(t('settings.transcode.revealed'));
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : t('settings.transcode.revealFailed'));
    } finally {
      revealLoading = false;
    }
  }

  function hideRevealed() {
    revealed = null;
    showRevealForm = false;
    revealPassword = '';
    revealTotp = '';
  }

  // Encoder tuning state
  let nvencPreset = 'p4';
  let nvencTune = 'hq';
  let nvencRc = 'vbr';
  let maxrateRatio = 1.5;
  // Global output ceilings (formerly env-only). Restart-required.
  let maxBitrateKbps = 40000;
  let maxWidth = 3840;
  let maxHeight = 2160;
  // Adaptive-bitrate HLS ladder. Restart-required (read once at startup).
  let abrEnabled = false;
  let abrMaxHeight = 0;
  let abrAutoMaxHeight = 1080;
  // Internet streaming limit (read live — no restart). 0 = unlimited.
  let remoteLimitEnabled = false;
  let remoteLimitMbps = 8;
  // Extra home networks, one CIDR per line.
  let localNetworksText = '';
  let tuningSaving = false;
  let tuningLoaded = false;

  onMount(async () => {
    // The browser host is a usable fallback only when it isn't loopback (a
    // remote worker can't reach the admin's localhost). The fleet API's
    // detected LAN IP, loaded below, takes precedence.
    if (typeof location !== 'undefined' && location.hostname &&
        !['localhost', '127.0.0.1', '::1', '[::1]'].includes(location.hostname)) {
      serverHost = location.hostname;
    }

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
      fleetEmbeddedLoad = f.embedded_load_percent ?? 0;
      fleetEmbeddedTonemap = f.embedded_gpu_tonemap ?? false;
      fleetEmbeddedCapabilities = f.embedded_capabilities || [];
      fleetWorkers = (f.workers || []).map(w => ({ ...w }));
      if (f.server_lan_ip) serverHost = f.server_lan_ip; // best: worker-reachable
      fleetLoaded = true;
    } catch (e) { console.error('fleet load failed:', e); fleetLoaded = true; }

    // Load encoder tuning config
    try {
      const tc = await settingsApi.getTranscodeConfig();
      nvencPreset = tc.nvenc_preset || 'p4';
      nvencTune = tc.nvenc_tune || 'hq';
      nvencRc = tc.nvenc_rc || 'vbr';
      maxrateRatio = tc.maxrate_ratio || 1.5;
      maxBitrateKbps = tc.max_bitrate_kbps || 40000;
      maxWidth = tc.max_width || 3840;
      maxHeight = tc.max_height || 2160;
      // Server always backfills these (env default if no stored override),
      // so straight assignment is correct — don't || the bool away.
      abrEnabled = tc.abr;
      abrMaxHeight = tc.abr_max_height;
      abrAutoMaxHeight = tc.abr_auto_max_height || 1080;
      remoteLimitEnabled = (tc.remote_max_bitrate_kbps ?? 0) > 0;
      remoteLimitMbps = remoteLimitEnabled
        ? Math.round(((tc.remote_max_bitrate_kbps ?? 0) / 1000) * 10) / 10
        : 8;
      localNetworksText = (tc.local_networks ?? []).join('\n');
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
      load_percent: 0,
      gpu_tonemap: false,
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
        // Already a comma-separated string — the device dropdown
        // emits all codecs for the picked card joined by commas.
        embedded_encoder: fleetEmbeddedEncoder,
        embedded_max_sessions: fleetEmbeddedMaxSessions || undefined,
        workers
      });
      toast.success(t('settings.transcode.fleetSaved'));
      const updated = await settingsApi.getFleet();
      fleetEmbeddedEnabled = updated.embedded_enabled;
      fleetEmbeddedDisabledByEnv = updated.embedded_disabled_by_env || false;
      fleetEmbeddedEncoder = updated.embedded_encoder || '';
      fleetEmbeddedOnline = updated.embedded_online;
      fleetEmbeddedActiveSessions = updated.embedded_active_sessions;
      fleetEmbeddedMaxSessions = updated.embedded_max_sessions;
      fleetEmbeddedLoad = updated.embedded_load_percent ?? 0;
      fleetEmbeddedTonemap = updated.embedded_gpu_tonemap ?? false;
      fleetEmbeddedCapabilities = updated.embedded_capabilities || [];
      fleetWorkers = (updated.workers || []).map(w => ({ ...w }));
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : t('settings.transcode.fleetSaveFailed'));
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
        max_bitrate_kbps: maxBitrateKbps,
        max_width: maxWidth,
        max_height: maxHeight,
        abr: abrEnabled,
        abr_max_height: abrMaxHeight,
        abr_auto_max_height: abrAutoMaxHeight,
        remote_max_bitrate_kbps: remoteLimitEnabled ? Math.max(1, Math.round(remoteLimitMbps * 1000)) : 0,
        local_networks: localNetworksText.split(/[\s,]+/).map((n) => n.trim()).filter(Boolean),
      });
      toast.success(t('settings.transcode.tuningSaved'));
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : t('settings.transcode.tuningSaveFailed'));
    } finally {
      tuningSaving = false;
    }
  }
</script>

<svelte:head><title>{$tr('settings.transcode.pageTitle')}</title></svelte:head>

<div class="page">

  <!-- ── Encoder Tuning ─────────────────────────────────────────────── -->
  {#if tuningLoaded}
  <section>
    <div class="sec-label">{$tr('settings.transcode.encoderTuning')}</div>
    <div class="hint" style="margin-top: -0.5rem;">
      {#each $tr.rich('settings.transcode.encoderTuningHint', { restart: $tr('settings.transcode.restartNote'), nvencEnv: 'TRANSCODE_NVENC_*', ratioEnv: 'TRANSCODE_MAXRATE_RATIO' }) as seg}{#if seg.param === 'restart'}<strong>{seg.text}</strong>{:else if seg.param === 'nvencEnv' || seg.param === 'ratioEnv'}<code>{seg.text}</code>{:else}{seg.text}{/if}{/each}
    </div>

    <div class="field-row">
      <div class="field" style="flex:1;">
        <label for="nvenc-preset">{$tr('settings.transcode.nvencPreset')}</label>
        <select id="nvenc-preset" bind:value={nvencPreset}>
          <option value="p1">p1 — {$tr('settings.transcode.fastest')}</option>
          <option value="p2">p2</option>
          <option value="p3">p3</option>
          <option value="p4">p4 — {$tr('settings.transcode.balancedDefault')}</option>
          <option value="p5">p5</option>
          <option value="p6">p6</option>
          <option value="p7">p7 — {$tr('settings.transcode.bestQuality')}</option>
        </select>
        <div class="hint">{$tr('settings.transcode.presetHint')}</div>
      </div>

      <div class="field" style="flex:1;">
        <label for="nvenc-tune">{$tr('settings.transcode.nvencTune')}</label>
        <select id="nvenc-tune" bind:value={nvencTune}>
          <option value="hq">{$tr('settings.transcode.tuneHq')}</option>
          <option value="ll">{$tr('settings.transcode.tuneLl')}</option>
          <option value="ull">{$tr('settings.transcode.tuneUll')}</option>
        </select>
        <div class="hint">{$tr('settings.transcode.tuneHint')}</div>
      </div>
    </div>

    <div class="field-row">
      <div class="field" style="flex:1;">
        <label for="nvenc-rc">{$tr('settings.transcode.rateControl')}</label>
        <select id="nvenc-rc" bind:value={nvencRc}>
          <option value="vbr">VBR — {$tr('settings.transcode.rcVbr')}</option>
          <option value="cbr">CBR — {$tr('settings.transcode.rcCbr')}</option>
          <option value="constqp">CQP — {$tr('settings.transcode.rcCqp')}</option>
        </select>
        <div class="hint">{$tr('settings.transcode.rcHint')}</div>
      </div>

      <div class="field" style="flex:1;">
        <label for="maxrate-ratio">{$tr('settings.transcode.peakRatio')}</label>
        <select id="maxrate-ratio" bind:value={maxrateRatio}>
          <option value={1.0}>1.0x — {$tr('settings.transcode.ratioStrict')}</option>
          <option value={1.2}>1.2x — {$tr('settings.transcode.ratioTight')}</option>
          <option value={1.5}>1.5x — {$tr('settings.transcode.balancedDefault')}</option>
          <option value={2.0}>2.0x — {$tr('settings.transcode.ratioGenerous')}</option>
          <option value={3.0}>3.0x — {$tr('settings.transcode.ratioUnconstrained')}</option>
        </select>
        <div class="hint">
          {$tr('settings.transcode.ratioHint')}
        </div>
      </div>
    </div>

    <div class="sec-label" style="margin-top: 1.25rem;">{$tr('settings.transcode.outputLimits')}</div>
    <div class="hint" style="margin-top: -0.5rem;">
      {#each $tr.rich('settings.transcode.outputLimitsHint', { restart: $tr('settings.transcode.restartNote') }) as seg}{#if seg.param === 'restart'}<strong>{seg.text}</strong>{:else}{seg.text}{/if}{/each}
    </div>

    <div class="field-row">
      <div class="field" style="flex:1;">
        <label for="max-bitrate">{$tr('settings.transcode.maxBitrate')}</label>
        <input id="max-bitrate" type="number" min="1000" max="200000" step="1000" bind:value={maxBitrateKbps} />
        <div class="hint">{$tr('settings.transcode.maxBitrateHint', { value: ' + 40000 + ' })}</div>
      </div>
      <div class="field" style="flex:1;">
        <label for="max-width">{$tr('settings.transcode.maxWidth')}</label>
        <input id="max-width" type="number" min="640" max="7680" step="2" bind:value={maxWidth} />
        <div class="hint">{$tr('settings.transcode.default4k', { value: ' + 3840 + ' })}</div>
      </div>
      <div class="field" style="flex:1;">
        <label for="max-height">{$tr('settings.transcode.maxHeight')}</label>
        <input id="max-height" type="number" min="360" max="4320" step="2" bind:value={maxHeight} />
        <div class="hint">{$tr('settings.transcode.default4k', { value: ' + 2160 + ' })}</div>
      </div>
    </div>

    <div class="sec-label" style="margin-top: 1.25rem;">{$tr('settings.transcode.internet')}</div>
    <div class="hint" style="margin-top: -0.5rem;">
      {#each $tr.rich('settings.transcode.internetHint', { noRestart: $tr('settings.transcode.noRestartNote') }) as seg}{#if seg.param === 'noRestart'}<strong>{seg.text}</strong>{:else}{seg.text}{/if}{/each}
    </div>

    <label class="toggle-label" style="margin-top: 0.5rem;">
      <input type="checkbox" bind:checked={remoteLimitEnabled} />
      {$tr('settings.transcode.limitInternet')}
    </label>
    {#if remoteLimitEnabled}
    <div class="field-row">
      <div class="field" style="flex:1;">
        <label for="remote-limit">{$tr('settings.transcode.internetLimit')}</label>
        <input id="remote-limit" type="number" min="0.5" max="200" step="0.5" bind:value={remoteLimitMbps} />
        <div class="hint">{$tr('settings.transcode.internetLimitHint')}</div>
      </div>
    </div>
    {/if}

    <div class="field" style="margin-top: 0.75rem;">
      <label for="local-networks">{$tr('settings.transcode.localNetworks')}</label>
      <textarea id="local-networks" class="nets" rows="3" bind:value={localNetworksText}
        placeholder={$tr('settings.transcode.localNetworksPlaceholder', { example: '203.0.113.0/24' })}></textarea>
      <div class="hint">
        {$tr('settings.transcode.localNetworksHint')}
      </div>
    </div>

    <div class="sec-label" style="margin-top: 1.25rem;">{$tr('settings.transcode.abr')}</div>
    <div class="hint" style="margin-top: -0.5rem;">
      {#each $tr.rich('settings.transcode.abrHint', { restart: $tr('settings.transcode.restartNote') }) as seg}{#if seg.param === 'restart'}<strong>{seg.text}</strong>{:else}{seg.text}{/if}{/each}
    </div>

    <label class="toggle-label" style="margin-top: 0.5rem;">
      <input type="checkbox" bind:checked={abrEnabled} />
      {$tr('settings.transcode.abrEnable')}
    </label>

    {#if abrEnabled}
    <div class="field-row">
      <div class="field" style="flex:1;">
        <label for="abr-max-height">{$tr('settings.transcode.abrMax')}</label>
        <input id="abr-max-height" type="number" min="0" max="4320" bind:value={abrMaxHeight} />
        <div class="hint">{$tr('settings.transcode.abrMaxHint')}</div>
      </div>
      <div class="field" style="flex:1;">
        <label for="abr-auto-max-height">{$tr('settings.transcode.abrAuto')}</label>
        <input id="abr-auto-max-height" type="number" min="0" max="4320" bind:value={abrAutoMaxHeight} />
        <div class="hint">{$tr('settings.transcode.abrAutoHint', { value: ' + 1080 + ' })}</div>
      </div>
    </div>
    {/if}

    <div class="section-foot">
      <button class="btn-save" disabled={tuningSaving} on:click={saveTuning}>
        {tuningSaving ? $tr('settings.transcode.saving') : $tr('settings.transcode.saveTuning')}
      </button>
    </div>
  </section>
  {:else}
    <div class="skeleton-block"></div>
  {/if}

  <!-- ── Transcode Fleet ────────────────────────────────────────────── -->
  {#if fleetLoaded}
  <section>
    <div class="sec-label">{$tr('settings.transcode.fleet')}</div>
    <div class="hint" style="margin-top: -0.5rem;">
      {$tr('settings.transcode.fleetHint')}
    </div>

    <!-- Embedded worker -->
    <div class="fleet-group">
      <div class="fleet-group-header">
        <label class="toggle-label">
          <input type="checkbox" bind:checked={fleetEmbeddedEnabled} disabled={fleetEmbeddedDisabledByEnv} />
          {$tr('settings.transcode.embedded')}
        </label>
        {#if fleetEmbeddedDisabledByEnv}
          <span class="hint" style="margin-left: 0.5rem; font-size: 0.75rem;">{$tr('settings.transcode.embeddedDisabledByEnv', { env: 'DISABLE_EMBEDDED_WORKER' })}</span>
        {:else if fleetEmbeddedOnline}
          <span class="status-dot online"></span>
        {:else if fleetEmbeddedEnabled}
          <span class="status-dot offline"></span>
        {/if}
      </div>
      {#if fleetEmbeddedEnabled && !fleetEmbeddedDisabledByEnv}
      <div class="field">
        <label for="embedded-encoder">{$tr('settings.transcode.device')}</label>
        <select id="embedded-encoder" bind:value={fleetEmbeddedEncoder}>
          <option value="">{$tr('settings.transcode.autoDetect')}</option>
          {#each groupedDevices(fleetEmbeddedCapabilities, embeddedLabelMap()) as g}
            <option value={g.value}>{g.label} — {g.codecs.join(' + ')}</option>
          {/each}
        </select>
        <p class="hint">
          {$tr('settings.transcode.deviceHint')}
        </p>
      </div>
      <div class="field" style="max-width: 12rem;">
        <label for="embedded-max">{$tr('settings.transcode.maxSessions')}</label>
        <input id="embedded-max" type="number" min="0" max="100"
          bind:value={fleetEmbeddedMaxSessions} placeholder={$tr('settings.transcode.maxSessionsDefault')} />
        <p class="hint">
          {$tr('settings.transcode.maxSessionsHint', { env: 'TRANSCODE_MAX_SESSIONS' })}
        </p>
      </div>
      {#if fleetEmbeddedOnline}
      <div class="fleet-live-info">
        <span>{$tr('settings.transcode.sessions', { active: fleetEmbeddedActiveSessions, max: fleetEmbeddedMaxSessions })}</span>
        <span class="load-badge {loadClass(fleetEmbeddedLoad)}" title={$tr('settings.transcode.loadTitle')}>{$tr('settings.transcode.load', { percent: fleetEmbeddedLoad })}</span>
        {#if fleetEmbeddedTonemap}
          <span class="worker-cap cap-tonemap" title={$tr('settings.transcode.tonemapTitle')}>{$tr('settings.transcode.tonemap')}</span>
        {/if}
        {#each fleetEmbeddedCapabilities as cap}
          <span class="worker-cap">{cap}</span>
        {/each}
      </div>
      {/if}
      {/if}
    </div>

    <!-- Local workers -->
    <div class="fleet-group" style="margin-top: 1rem;">
      <div class="fleet-group-header">
        <span class="fleet-group-title">{$tr('settings.transcode.localWorkers')}</span>
      </div>
      {#each fleetWorkers as row (row.id)}
        <div class="worker-card">
          <div class="fleet-row">
            <div class="field" style="flex:2;">
              <label for="worker-name-{row.id}">{$tr('settings.transcode.name')}</label>
              <input id="worker-name-{row.id}" type="text" bind:value={row.name} placeholder={$tr('settings.transcode.namePlaceholder')} />
            </div>
            <div class="field" style="flex:2;">
              <label for="worker-encoder-{row.id}">{$tr('settings.transcode.encoder')}</label>
              <select id="worker-encoder-{row.id}" bind:value={row.encoder}>
                <option value="">{$tr('settings.transcode.autoDetect')}</option>
                {#each groupedDevices(row.capabilities || [], row.encoder_labels || {}) as g}
                  <option value={g.value}>{g.label} — {g.codecs.join(' + ')}</option>
                {/each}
              </select>
            </div>
            <div class="field" style="flex:1;">
              <label for="worker-max-{row.id}">{$tr('settings.transcode.maxSessions')}</label>
              <input id="worker-max-{row.id}" type="number" bind:value={row.max_sessions} min="0" max="100" placeholder={$tr('settings.transcode.maxSessionsAuto')} />
            </div>
            {#if !row.online}
              <button type="button" class="btn-remove" title={$tr('settings.transcode.removeWorker')}
                on:click={() => removeWorker(row.id)}>&times;</button>
            {/if}
          </div>
          <div class="fleet-live-info">
            {#if row.online}
              <span class="status-dot online"></span>
              <span>{$tr('settings.transcode.sessions', { active: row.active_sessions, max: row.max_sessions })}</span>
              <span class="load-badge {loadClass(row.load_percent)}" title={$tr('settings.transcode.loadTitle')}>{$tr('settings.transcode.load', { percent: row.load_percent })}</span>
              {#if row.gpu_tonemap}
                <span class="worker-cap cap-tonemap" title={$tr('settings.transcode.tonemapTitle')}>{$tr('settings.transcode.tonemap')}</span>
              {/if}
              {#each row.capabilities || [] as cap}
                <span class="worker-cap">{cap}</span>
              {/each}
            {:else}
              <span class="status-dot offline"></span>
              <span class="text-muted">{row.isNew ? $tr('settings.transcode.notSaved') : $tr('settings.transcode.offline')}</span>
            {/if}
          </div>
        </div>
      {/each}

      {#if fleetWorkers.length === 0}
        <div class="hint">{$tr('settings.transcode.noWorkers')}</div>
      {/if}

      <button type="button" class="btn-outline btn-add-mapping" style="margin-top: 0.5rem;" on:click={addWorker}>
        {$tr('settings.transcode.addWorker')}
      </button>
    </div>

    <div class="section-foot" style="margin-top: 1rem;">
      <button class="btn-save" disabled={fleetSaving} on:click={saveFleet}>
        {fleetSaving ? $tr('settings.transcode.saving') : $tr('settings.transcode.saveFleet')}
      </button>
    </div>

    <!-- How to add a worker on another machine -->
    <div class="fleet-group" style="margin-top: 1.25rem;">
      <div class="fleet-group-header">
        <span class="fleet-group-title">{$tr('settings.transcode.remoteTitle')}</span>
      </div>
      <p class="hint" style="margin-top: -0.25rem;">
        {#each $tr.rich('settings.transcode.remoteHint', { workerOnly: 'Worker only', localWorkers: $tr('settings.transcode.localWorkers') }) as seg}{#if seg.param === 'workerOnly'}<strong>{seg.text}</strong>{:else if seg.param === 'localWorkers'}<em>{seg.text}</em>{:else}{seg.text}{/if}{/each}
      </p>

      <div class="worker-info">
        <div class="info-row">
          <span class="info-key">DATABASE_URL</span>
          <code class="info-val">{dbValue}</code>
          <button class="btn-copy" title={$tr('settings.transcode.copy')} on:click={() => copyText(dbValue)}>{$tr('settings.transcode.copy')}</button>
        </div>
        <div class="info-row">
          <span class="info-key">VALKEY_URL</span>
          <code class="info-val">{valkeyValue}</code>
          <button class="btn-copy" title={$tr('settings.transcode.copy')} on:click={() => copyText(valkeyValue)}>{$tr('settings.transcode.copy')}</button>
        </div>
        <div class="info-row">
          <span class="info-key">SECRET_KEY</span>
          {#if revealed}
            <code class="info-val">{revealed.secret_key}</code>
            <button class="btn-copy" title={$tr('settings.transcode.copy')} on:click={() => revealed && copyText(revealed.secret_key)}>{$tr('settings.transcode.copy')}</button>
          {:else}
            <span class="info-val muted">{#each $tr.rich('settings.transcode.secretHidden', { show: $tr('settings.transcode.showFull') }) as seg}{#if seg.param === 'show'}<em>{seg.text}</em>{:else}{seg.text}{/if}{/each}</span>
          {/if}
        </div>
        <div class="info-row">
          <span class="info-key">WORKER_ADDR</span>
          <span class="info-val muted">{#each $tr.rich('settings.transcode.workerAddrHint', { example: '10.0.0.5:7073' }) as seg}{#if seg.param === 'example'}<code>{seg.text}</code>{:else}{seg.text}{/if}{/each}</span>
        </div>
      </div>

      <!-- Step-up reveal -->
      {#if revealed}
        <div class="reveal-row">
          <span class="hint warn">{$tr('settings.transcode.revealedWarn')}</span>
          <button class="btn-outline btn-add-mapping" on:click={hideRevealed}>{$tr('settings.transcode.hide')}</button>
        </div>
      {:else if showRevealForm}
        <div class="reveal-form">
          <p class="hint" style="margin:0;">{$tr('settings.transcode.revealPrompt', { db: 'DATABASE_URL', secret: 'SECRET_KEY' })}</p>
          <div class="field-row">
            <div class="field" style="flex:2;">
              <label for="reveal-pw">{$tr('settings.transcode.password')}</label>
              <!-- svelte-ignore a11y-autofocus -->
              <input id="reveal-pw" type="password" bind:value={revealPassword} autocomplete="current-password" autofocus />
            </div>
            <div class="field" style="flex:1;">
              <label for="reveal-totp">{$tr('settings.transcode.twoFactor')} <span class="muted">{$tr('settings.transcode.ifEnabled')}</span></label>
              <input id="reveal-totp" type="text" inputmode="numeric" bind:value={revealTotp} placeholder="000000" autocomplete="one-time-code" />
            </div>
          </div>
          <div class="reveal-actions">
            <button class="btn-save" disabled={revealLoading || !revealPassword} on:click={doReveal}>
              {revealLoading ? $tr('settings.transcode.verifying') : $tr('settings.transcode.reveal')}
            </button>
            <button class="btn-outline btn-add-mapping" on:click={() => { showRevealForm = false; revealPassword = ''; revealTotp = ''; }}>{$tr('settings.transcode.cancel')}</button>
          </div>
        </div>
      {:else}
        <button class="btn-outline btn-add-mapping" style="margin-top:0.25rem;" on:click={() => showRevealForm = true}>
          {$tr('settings.transcode.showFullButton')}
        </button>
      {/if}

      <p class="hint">
        {#each $tr.rich('settings.transcode.envHint', { password: '‹password›', db: 'DATABASE_URL', secret: 'SECRET_KEY', env: '.env', path: 'C:\\Program\u00a0Files\\OnScreen\\.env' }) as seg}{#if seg.param === 'password' || seg.param === 'env' || seg.param === 'path'}<code>{seg.text}</code>{:else}{seg.text}{/if}{/each}
      </p>
      <p class="hint warn">
        {#each $tr.rich('settings.transcode.lanWarn', { listen: 'listen_addresses', pgHba: 'pg_hba.conf' }) as seg}{#if seg.param === 'listen' || seg.param === 'pgHba'}<code>{seg.text}</code>{:else}{seg.text}{/if}{/each}
      </p>
    </div>
  </section>
  {:else}
    <div class="skeleton-block"></div>
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

  label { font-size: 0.72rem; font-weight: 500; color: var(--text-muted); }

  input, select {
    background: var(--input-bg);
    border: 1px solid var(--border-strong);
    border-radius: 7px;
    padding: 0.48rem 0.7rem;
    font-size: 0.85rem;
    color: var(--text-primary);
    font-family: inherit;
    transition: border-color 0.15s;
    width: 100%;
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
    padding: 0.45rem 0.9rem; background: var(--accent);
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
    background: var(--bg-elevated);
    border: 1px solid var(--border);
    border-radius: 10px;
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
  .nets {
    width: 100%;
    box-sizing: border-box;
    font-family: ui-monospace, monospace;
    font-size: 0.8rem;
    padding: 0.48rem 0.7rem;
    background: var(--input-bg);
    border: 1px solid var(--border-strong);
    border-radius: 7px;
    color: var(--text-primary);
    resize: vertical;
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
    flex-wrap: wrap;
    align-items: center;
    gap: 0.5rem 0.5rem;
    margin-top: 0.5rem;
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
  .status-dot.offline { background: var(--text-muted); }
  .text-muted { color: var(--text-muted); }
  .worker-card {
    background: var(--bg-elevated);
    border: 1px solid var(--border);
    border-radius: 10px;
    padding: 0.75rem 1rem;
    margin-bottom: 0.5rem;
  }
  .worker-cap {
    font-size: 0.7rem;
    padding: 0.125rem 0.5rem;
    border-radius: 999px;
    background: var(--accent-bg);
    color: var(--accent-text);
  }
  .cap-tonemap {
    background: var(--accent-bg);
    color: var(--accent-text);
    font-weight: 600;
  }
  .load-badge {
    font-size: 0.7rem;
    font-variant-numeric: tabular-nums;
    padding: 0.125rem 0.5rem;
    border-radius: 999px;
    border: 1px solid transparent;
  }
  .load-badge.load-low { background: var(--success-bg); color: var(--success); }
  .load-badge.load-mid { background: rgba(251,191,36,0.12); color: #fcd34d; }
  .load-badge.load-high { background: var(--error-bg); color: var(--error); }
  .btn-remove {
    flex-shrink: 0;
    background: transparent;
    border: 1px solid var(--error);
    color: var(--error);
    border-radius: 7px;
    padding: 0.3rem 0.55rem;
    cursor: pointer;
    font-size: 0.75rem;
    font-weight: 600;
    line-height: 1;
    margin-bottom: 0.25rem;
  }
  .btn-remove:hover { background: var(--error-bg); }

  /* ── Add-a-worker info panel ───────────────────────────────────────────── */
  .worker-info {
    display: flex;
    flex-direction: column;
    gap: 0.4rem;
    margin: 0.25rem 0 0.25rem;
  }
  .info-row {
    display: flex;
    align-items: center;
    gap: 0.6rem;
  }
  .info-key {
    flex-shrink: 0;
    width: 7.5rem;
    font-size: 0.72rem;
    font-weight: 600;
    color: var(--text-secondary);
    font-family: ui-monospace, monospace;
  }
  .info-val {
    flex: 1;
    font-size: 0.72rem;
    color: var(--text-primary);
    word-break: break-all;
    font-family: ui-monospace, monospace;
  }
  .info-val.muted { color: var(--text-muted); font-family: inherit; }
  .info-val code, .hint code {
    background: var(--bg-hover);
    padding: 0.05rem 0.3rem;
    border-radius: 4px;
    font-size: 0.92em;
  }
  .btn-copy {
    flex-shrink: 0;
    background: transparent;
    border: 1px solid var(--border-strong);
    color: var(--text-secondary);
    border-radius: 7px;
    padding: 0.2rem 0.5rem;
    font-size: 0.68rem;
    font-weight: 600;
    cursor: pointer;
    transition: background 0.12s, border-color 0.12s;
  }
  .btn-copy:hover { background: var(--bg-hover); border-color: var(--text-muted); }
  .hint.warn { color: #fcd34d; }
  .muted { color: var(--text-muted); }

  .reveal-form {
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
    margin: 0.5rem 0;
    padding: 0.75rem 0.9rem;
    background: var(--bg-elevated);
    border: 1px solid var(--border);
    border-radius: 10px;
  }
  .reveal-actions { display: flex; gap: 0.5rem; align-items: center; }
  .reveal-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.75rem;
    margin: 0.4rem 0;
  }

  /* ── Loading skeleton ──────────────────────────────────────────────────── */
  .skeleton-block {
    height: 120px;
    border-radius: 10px;
    margin: 1.25rem 0;
    background: linear-gradient(90deg, var(--bg-elevated) 25%, var(--bg-hover) 50%, var(--bg-elevated) 75%);
    background-size: 200% 100%;
    animation: shimmer 1.4s infinite;
  }
  @keyframes shimmer {
    0% { background-position: 200% 0; }
    100% { background-position: -200% 0; }
  }

  /* ── Mobile ────────────────────────────────────────────────────────────── */
  @media (max-width: 768px) {
    .page { max-width: 100%; }
    .field-row { flex-direction: column; gap: 0.75rem; }
    input { width: 100%; }
  }
</style>
