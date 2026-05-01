<script lang="ts">
  import { onMount } from 'svelte';
  import {
    isTauri,
    replayGainSetMode,
    replayGainSetPreamp,
    audioSetExclusiveMode,
    audioGetActiveBackend,
    audioGetOutputIsBluetooth,
    audioSetBluetoothOverride,
    audioState,
    type ReplayGainMode,
    type ActiveBackend,
    type PlaybackStatus,
  } from '$lib/native';
  import { nativeEngine } from '$lib/stores/nativeEngine';

  // Defaults match the Rust-side atomics: off + 0 dB preamp. Stored
  // in localStorage so reopening the desktop app picks up the user's
  // last choice without a Tauri-side prefs file. The layout's onMount
  // re-applies these via the IPC commands so the engine state is in
  // sync with the UI on every launch.
  let mode: ReplayGainMode = $state('off');
  let preampDb: number = $state(0);
  let exclusive: boolean = $state(false);
  let engineEnabled: boolean = $state(false);
  // Subscribe to the nativeEngine store so the toggle reflects the
  // current state and other surfaces (the legacy /native/server
  // toggle) stay in sync if the user changes it from somewhere else.
  $effect(() => {
    const unsub = nativeEngine.subscribe((v) => { engineEnabled = v; });
    return unsub;
  });
  let busyMode = $state(false);
  let busyPreamp = $state(false);
  let busyExclusive = $state(false);
  let saveError = $state('');
  let activeBackend: ActiveBackend = $state('none');
  // Snapshot of the currently-playing track's format. Used by the
  // bit-perfect hint below to surface "this file is 192 kHz but the
  // OS is resampling it down" guidance — only meaningful when there's
  // an active source. Polled alongside activeBackend.
  let playback: PlaybackStatus | null = $state(null);
  // Output is a Bluetooth endpoint — Windows BT audio service always
  // re-encodes through SBC/AAC/aptX/LDAC, so the chain is lossy
  // regardless of WASAPI mode. Drives the BT-aware badge text.
  // Reflects the union of auto-detection + the manual override below.
  let outputIsBluetooth: boolean = $state(false);
  // Manual override for BT detection. Many BT headsets (Soundcore,
  // Sony, Bose, etc.) report device strings that are shape-identical
  // to wired headphones, so auto-detection misses them. User flips
  // this once; persisted in localStorage so it survives restarts.
  let btOverride: boolean = $state(false);
  let busyBtOverride: boolean = $state(false);
  // Poll the engine while this page is open so the badge updates
  // when a track starts or stops. 1 s is fine — the badge isn't on
  // the audio hot path, and polling gives us "what's actually
  // running right now" without wiring a separate event channel.
  let backendPoll: ReturnType<typeof setInterval> | null = null;

  // Tauri-only page — surface a clear message in browser builds so a
  // confused user navigating here from the web shell doesn't see a
  // silent broken UI.
  let inTauri = $state(false);

  onMount(() => {
    inTauri = isTauri();
    const storedMode = localStorage.getItem('onscreen_native_rg_mode');
    if (storedMode === 'off' || storedMode === 'track' || storedMode === 'album') {
      mode = storedMode;
    }
    const storedPreamp = parseFloat(localStorage.getItem('onscreen_native_rg_preamp') ?? '');
    if (Number.isFinite(storedPreamp)) {
      preampDb = storedPreamp;
    }
    exclusive = localStorage.getItem('onscreen_native_exclusive') === '1';
    btOverride = localStorage.getItem('onscreen_native_bt_override') === '1';
    if (btOverride && isTauri()) {
      // Re-apply the override on launch so the engine flag reflects
      // the user's persisted choice without requiring them to re-toggle.
      void audioSetBluetoothOverride(true);
    }

    // Hydrate the active-backend badge immediately + start polling.
    // Only meaningful inside the desktop client, so gate on inTauri.
    if (inTauri) {
      void audioGetActiveBackend().then((b) => { activeBackend = b; });
      void audioState().then((s) => { playback = s; });
      void audioGetOutputIsBluetooth().then((bt) => { outputIsBluetooth = bt; });
      backendPoll = setInterval(async () => {
        activeBackend = await audioGetActiveBackend();
        playback = await audioState();
        outputIsBluetooth = await audioGetOutputIsBluetooth();
      }, 1000);
    }
    return () => {
      if (backendPoll) clearInterval(backendPoll);
    };
  });

  // Map the wire identifier to a user-facing label + tone (good /
  // ok / muted) for the badge styling. Kept inline with the data
  // so the strings live next to the cases they describe.
  function backendLabel(b: ActiveBackend, bt: boolean): { text: string; tone: 'good' | 'ok' | 'muted' } {
    // Bluetooth always carries a lossy codec downstream of WASAPI —
    // exclusive mode bypasses the mixer but not the BT audio
    // service's encoder. Demote the badge from "bit-perfect" to a
    // BT-specific label whenever the output endpoint is BT, no
    // matter which backend opened it.
    if (bt) {
      switch (b) {
        case 'wasapi-exclusive':
          return { text: 'WASAPI exclusive · Bluetooth (lossy codec to headset)', tone: 'ok' };
        case 'wasapi-shared':
          return { text: 'WASAPI shared · Bluetooth (lossy codec + OS resample)', tone: 'ok' };
        case 'cpal-tight':
        case 'cpal-shared':
          return { text: 'cpal shared · Bluetooth (lossy codec to headset)', tone: 'ok' };
        case 'none':
        default:
          return { text: 'No active playback', tone: 'muted' };
      }
    }
    switch (b) {
      case 'wasapi-exclusive':
        return { text: 'WASAPI exclusive · bit-perfect', tone: 'good' };
      case 'wasapi-shared':
        return { text: 'WASAPI shared (auto-convert) · OS mixer resampling', tone: 'ok' };
      case 'cpal-tight':
        return { text: 'cpal shared (tight buffer) · OS mixer still resampling', tone: 'ok' };
      case 'cpal-shared':
        return { text: 'cpal shared (default) · OS mixer routing', tone: 'ok' };
      case 'none':
      default:
        return { text: 'No active playback', tone: 'muted' };
    }
  }

  async function applyBtOverride() {
    busyBtOverride = true;
    saveError = '';
    try {
      await audioSetBluetoothOverride(btOverride);
      localStorage.setItem('onscreen_native_bt_override', btOverride ? '1' : '0');
    } catch (e) {
      saveError = e instanceof Error ? e.message : String(e);
      btOverride = !btOverride;
    } finally {
      busyBtOverride = false;
    }
  }

  async function applyExclusive() {
    busyExclusive = true;
    saveError = '';
    try {
      await audioSetExclusiveMode(exclusive);
      localStorage.setItem('onscreen_native_exclusive', exclusive ? '1' : '0');
    } catch (e) {
      saveError = e instanceof Error ? e.message : String(e);
      // Revert UI state on failure so what the user sees matches
      // what's actually configured.
      exclusive = !exclusive;
    } finally {
      busyExclusive = false;
    }
  }

  async function applyMode(next: ReplayGainMode) {
    busyMode = true;
    saveError = '';
    try {
      await replayGainSetMode(next);
      localStorage.setItem('onscreen_native_rg_mode', next);
      mode = next;
    } catch (e) {
      saveError = e instanceof Error ? e.message : String(e);
    } finally {
      busyMode = false;
    }
  }

  async function applyPreamp() {
    busyPreamp = true;
    saveError = '';
    // Clamp client-side to mirror the Rust-side ±15 dB clamp; the
    // user sees the actual value the engine will use rather than a
    // silently-saturated one.
    const clamped = Math.max(-15, Math.min(15, preampDb));
    if (clamped !== preampDb) preampDb = clamped;
    try {
      await replayGainSetPreamp(clamped);
      localStorage.setItem('onscreen_native_rg_preamp', String(clamped));
    } catch (e) {
      saveError = e instanceof Error ? e.message : String(e);
    } finally {
      busyPreamp = false;
    }
  }
</script>

<svelte:head><title>Audio · OnScreen</title></svelte:head>

<div class="page">
  <h1>Audio (native engine)</h1>

  {#if !inTauri}
    <p class="hint">
      This page configures the OnScreen desktop client's native audio
      engine. Open the desktop app to adjust these settings.
    </p>
  {:else}
    {#if saveError}
      <p class="err">{saveError}</p>
    {/if}

    <section>
      <h2>Native engine</h2>
      <p class="desc">
        When on, music playback routes through the OS-native audio
        engine (cpal + claxon, with the WASAPI / CoreAudio / ALSA
        exclusive-mode paths layered on top) instead of the browser's
        <code>&lt;audio&gt;</code> element. The audiophile-pillar
        settings below — ReplayGain, exclusive output — only apply on
        this path; with the native engine off they're inert. Switch
        takes effect on the next track.
      </p>
      <label class="toggle">
        <input
          type="checkbox"
          bind:checked={engineEnabled}
          onchange={(e) => nativeEngine.set((e.target as HTMLInputElement).checked)}
        />
        <span>Use the native audio engine for music playback</span>
      </label>
    </section>

    <section>
      <h2>ReplayGain</h2>
      <p class="desc">
        Normalises perceived loudness across the catalog by applying the
        gain encoded in each file's <code>REPLAYGAIN_*</code> tags. Track
        mode varies song-to-song and is best for shuffle; album mode
        preserves intentional loudness differences within an album and
        is best for sequential listening. Settings apply on the next
        track — the currently-playing track stays at its original level
        until it ends or you skip.
      </p>

      <div class="mode-row">
        {#each [
          ['off', 'Off', 'Play at native level — no normalisation'],
          ['track', 'Track', 'Normalise per-track loudness'],
          ['album', 'Album', 'Normalise per-album, preserve in-album dynamics'],
        ] as [val, label, hint] (val)}
          <button
            class="mode"
            class:active={mode === val}
            disabled={busyMode}
            onclick={() => applyMode(val as ReplayGainMode)}
          >
            <div class="mode-label">{label}</div>
            <div class="mode-hint">{hint}</div>
          </button>
        {/each}
      </div>
    </section>

    <section>
      <h2>Exclusive output</h2>
      <p class="desc">
        Tightens the audio buffer so the OS mixer's resampler runs at
        lower latency. <strong>Bit-perfect output</strong> (no mixer
        resampling at all) needs platform-specific work — Windows
        WASAPI exclusive mode, macOS CoreAudio HOG mode, or Linux
        ALSA <code>hw:</code> direct — and isn't on by default yet.
        This switch is the on-ramp: when those backends ship per
        platform, your existing choice carries over. New tracks pick
        up the change; the currently-playing track keeps its current
        config until it ends.
      </p>
      <label class="toggle">
        <input
          type="checkbox"
          bind:checked={exclusive}
          disabled={busyExclusive}
          onchange={applyExclusive}
        />
        <span>Tight-buffer mode (~10 ms at file native rate)</span>
      </label>

      <div class="status status-{backendLabel(activeBackend, outputIsBluetooth).tone}">
        <span class="status-dot"></span>
        <span>Currently: {backendLabel(activeBackend, outputIsBluetooth).text}</span>
      </div>

      <label class="toggle">
        <input
          type="checkbox"
          bind:checked={btOverride}
          disabled={busyBtOverride}
          onchange={applyBtOverride}
        />
        <span>Treat output as Bluetooth (lossy codec, not bit-perfect)</span>
      </label>
      <p class="hint hint-soft">
        Auto-detection only catches devices Windows reports through the
        Bluetooth bus driver — wireless brands like Soundcore, Sony, and
        Bose look identical to wired headphones in Windows' device
        properties. Flip this on if your output is a Bluetooth headset
        and the badge above doesn't already say so.
      </p>

      {#if outputIsBluetooth}
        <p class="hint hint-warn">
          Output is a <strong>Bluetooth device</strong>. Bluetooth audio
          always inserts a lossy codec (SBC, AAC, aptX, or LDAC) between
          OnScreen and your headset — bit-perfect playback is not
          achievable on this output regardless of WASAPI mode. The
          Windows system volume is also applied digitally before the
          BT encoder, so volume changes will modify samples.
          For the audiophile path, switch to a wired DAC or USB headset.
        </p>
      {:else if activeBackend === 'wasapi-shared' && playback?.sample_rate_hz && playback.sample_rate_hz > 48000 && !exclusive}
        <p class="hint hint-warn">
          The OS audio engine is resampling this {(playback.sample_rate_hz / 1000).toFixed(playback.sample_rate_hz % 1000 === 0 ? 0 : 1)} kHz / {playback.bit_depth ?? '?'}-bit
          file down to your device's mix-format before it reaches the
          DAC. Decoded samples are lossless, but the playback chain is
          not bit-perfect. <strong>Enable exclusive output above</strong>
          to send the file at its native rate.
        </p>
      {:else if activeBackend === 'wasapi-shared' && !exclusive}
        <p class="hint hint-soft">
          Shared mode lets the OS mix OnScreen with other apps, but it
          inserts a sample-rate converter between us and your DAC. For
          bit-perfect output enable exclusive mode above.
        </p>
      {/if}
    </section>

    <section>
      <h2>Preamp</h2>
      <p class="desc">
        Adjusts the overall ReplayGain output by a fixed dB offset.
        ReplayGain's reference is conservative; +6 dB is a common boost
        for catalogs mastered hot enough that the default attenuation
        feels quiet. Clamped to ±15 dB — peak limiting still applies, so
        positive boosts won't clip.
      </p>
      <div class="preamp-row">
        <input
          type="range"
          min="-15"
          max="15"
          step="0.5"
          bind:value={preampDb}
          disabled={busyPreamp || mode === 'off'}
          onchange={applyPreamp}
        />
        <div class="preamp-value">
          {preampDb > 0 ? '+' : ''}{preampDb.toFixed(1)} dB
        </div>
      </div>
    </section>
  {/if}
</div>

<style>
  .page { padding: 2.5rem 2.5rem 5rem; max-width: 800px; margin: 0 auto; }
  h1 { font-size: 1.6rem; margin: 0 0 1.5rem; }
  h2 { font-size: 1.1rem; margin: 0 0 0.75rem; }
  .hint { color: var(--text-muted); }
  .hint-warn {
    background: color-mix(in oklab, var(--warning, orange) 12%, transparent);
    border-left: 3px solid var(--warning, orange);
    padding: 0.6rem 0.8rem;
    margin-top: 0.5rem;
    border-radius: 4px;
  }
  .hint-soft {
    margin-top: 0.5rem;
    font-size: 0.9em;
  }
  .err { color: var(--danger, #f87171); margin-bottom: 1rem; }
  .desc { color: var(--text-secondary); line-height: 1.5; margin: 0 0 1.25rem; }
  .desc code { background: var(--surface); padding: 0 0.25rem; border-radius: 3px; }

  section + section { margin-top: 2.5rem; }

  .mode-row { display: grid; grid-template-columns: repeat(3, 1fr); gap: 0.75rem; }
  .mode {
    text-align: left; padding: 1rem; border-radius: 8px;
    border: 2px solid var(--border); background: var(--surface);
    color: var(--text-primary); cursor: pointer;
    transition: border-color 0.1s ease, background 0.1s ease;
  }
  .mode:hover:not(:disabled) { border-color: var(--accent); }
  .mode.active { border-color: var(--accent); background: rgba(124, 106, 247, 0.12); }
  .mode-label { font-weight: 600; margin-bottom: 0.25rem; }
  .mode-hint { font-size: 0.8rem; color: var(--text-muted); line-height: 1.3; }

  .preamp-row { display: flex; align-items: center; gap: 1rem; }
  .preamp-row input[type="range"] { flex: 1; }
  .preamp-value { width: 6rem; text-align: right; font-variant-numeric: tabular-nums; }

  .toggle { display: flex; align-items: center; gap: 0.75rem; cursor: pointer; }
  .toggle input { width: 1.1rem; height: 1.1rem; }

  .status { display: flex; align-items: center; gap: 0.5rem; margin-top: 1rem; font-size: 0.85rem; }
  .status-dot { width: 0.5rem; height: 0.5rem; border-radius: 50%; }
  .status-good { color: var(--text-primary); }
  .status-good .status-dot { background: #4ade80; box-shadow: 0 0 6px rgba(74, 222, 128, 0.6); }
  .status-ok { color: var(--text-secondary); }
  .status-ok .status-dot { background: var(--accent); }
  .status-muted { color: var(--text-muted); }
  .status-muted .status-dot { background: var(--text-muted); }

  @media (max-width: 600px) {
    .page { padding: 1.5rem 1rem 4rem; }
    .mode-row { grid-template-columns: 1fr; }
  }
</style>
