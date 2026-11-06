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
  import { replayGainSettings, setReplayGainMode, setReplayGainPreamp } from '$lib/stores/replayGain';
  import { tr, type MessageKey } from '$lib/i18n/index.svelte';

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
  // Follow the shared ReplayGain setting so a change made from the
  // player's RG menu shows up here while the page is open.
  $effect(() =>
    replayGainSettings.subscribe((s) => {
      mode = s.mode;
      preampDb = s.preampDb;
    }),
  );
  // ReplayGain mode buttons: value, label and hint (message keys).
  const RG_MODE_OPTIONS: [ReplayGainMode, MessageKey, MessageKey][] = [
    ['off', 'ui.audioPlayer.rg.off', 'native.audio.rgOffHint'],
    ['track', 'ui.audioPlayer.rg.track', 'native.audio.rgTrackHint'],
    ['album', 'ui.audioPlayer.rg.album', 'native.audio.rgAlbumHint'],
  ];
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
  function backendLabel(b: ActiveBackend, bt: boolean): { text: MessageKey; tone: 'good' | 'ok' | 'muted' } {
    // Bluetooth always carries a lossy codec downstream of WASAPI —
    // exclusive mode bypasses the mixer but not the BT audio
    // service's encoder. Demote the badge from "bit-perfect" to a
    // BT-specific label whenever the output endpoint is BT, no
    // matter which backend opened it.
    if (bt) {
      switch (b) {
        case 'wasapi-exclusive':
          return { text: 'native.audio.backend.btWasapiExclusive', tone: 'ok' };
        case 'wasapi-shared':
          return { text: 'native.audio.backend.btWasapiShared', tone: 'ok' };
        case 'cpal-tight':
        case 'cpal-shared':
          return { text: 'native.audio.backend.btCpalShared', tone: 'ok' };
        case 'none':
        default:
          return { text: 'native.audio.backend.none', tone: 'muted' };
      }
    }
    switch (b) {
      case 'wasapi-exclusive':
        return { text: 'native.audio.backend.wasapiExclusive', tone: 'good' };
      case 'wasapi-shared':
        return { text: 'native.audio.backend.wasapiShared', tone: 'ok' };
      case 'cpal-tight':
        return { text: 'native.audio.backend.cpalTight', tone: 'ok' };
      case 'cpal-shared':
        return { text: 'native.audio.backend.cpalShared', tone: 'ok' };
      case 'none':
      default:
        return { text: 'native.audio.backend.none', tone: 'muted' };
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
      // Native engine first (no-op outside the desktop app), then the
      // shared per-device setting the browser player also reads.
      await replayGainSetMode(next);
      setReplayGainMode(next);
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
      setReplayGainPreamp(clamped);
    } catch (e) {
      saveError = e instanceof Error ? e.message : String(e);
    } finally {
      busyPreamp = false;
    }
  }
</script>

<svelte:head><title>{$tr('native.audio.pageTitle')} · OnScreen</title></svelte:head>

<!-- ReplayGain + preamp are shared: the desktop native engine and the
     browser player (Web Audio gain) both read this one per-device
     setting, so both builds render these two sections. -->
{#snippet replayGainSection()}
  <section>
    <h2>ReplayGain</h2>
    <p class="desc">
      {#each $tr.rich('native.audio.rgDesc', { tags: 'REPLAYGAIN_*' }) as seg}{#if seg.param}<code>{seg.text}</code>{:else}{seg.text}{/if}{/each}
      {#if inTauri}
        {$tr('native.audio.rgNextTrack')}
      {:else}
        {$tr('native.audio.rgImmediate')}
      {/if}
    </p>

    <div class="mode-row">
      {#each RG_MODE_OPTIONS as [val, label, hint] (val)}
        <button
          class="mode"
          class:active={mode === val}
          disabled={busyMode}
          onclick={() => applyMode(val as ReplayGainMode)}
        >
          <div class="mode-label">{$tr(label)}</div>
          <div class="mode-hint">{$tr(hint)}</div>
        </button>
      {/each}
    </div>
  </section>
{/snippet}

{#snippet preampSection()}
  <section>
    <h2>{$tr('ui.audioPlayer.rg.preamp')}</h2>
    <p class="desc">
      {$tr('native.audio.preampDesc')}
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
{/snippet}

<div class="page">
  <h1>{inTauri ? $tr('native.audio.titleNative') : $tr('native.audio.pageTitle')}</h1>

  {#if !inTauri}
    <p class="hint">
      {$tr('native.audio.browserHint')}
    </p>
    {#if saveError}
      <p class="err">{saveError}</p>
    {/if}
    {@render replayGainSection()}
    {@render preampSection()}
  {:else}
    {#if saveError}
      <p class="err">{saveError}</p>
    {/if}

    <section>
      <h2>{$tr('native.audio.engine')}</h2>
      <p class="desc">
        {#each $tr.rich('native.audio.engineDesc', { element: '<audio>' }) as seg}{#if seg.param}<code>{seg.text}</code>{:else}{seg.text}{/if}{/each}
      </p>
      <label class="toggle">
        <input
          type="checkbox"
          bind:checked={engineEnabled}
          onchange={(e) => nativeEngine.set((e.target as HTMLInputElement).checked)}
        />
        <span>{$tr('native.audio.engineToggle')}</span>
      </label>
    </section>

    {@render replayGainSection()}

    <section>
      <h2>{$tr('native.audio.exclusive')}</h2>
      <p class="desc">
        {#each $tr.rich('native.audio.exclusiveDesc', { bitPerfect: $tr('native.audio.bitPerfect'), hw: 'hw:' }) as seg}{#if seg.param && seg.param === 'hw'}<code>{seg.text}</code>{:else if seg.param}<strong>{seg.text}</strong>{:else}{seg.text}{/if}{/each}
      </p>
      <label class="toggle">
        <input
          type="checkbox"
          bind:checked={exclusive}
          disabled={busyExclusive}
          onchange={applyExclusive}
        />
        <span>{$tr('native.audio.tightBuffer')}</span>
      </label>

      <div class="status status-{backendLabel(activeBackend, outputIsBluetooth).tone}">
        <span class="status-dot"></span>
        <span>{$tr('native.audio.currently', { status: $tr(backendLabel(activeBackend, outputIsBluetooth).text) })}</span>
      </div>

      <label class="toggle">
        <input
          type="checkbox"
          bind:checked={btOverride}
          disabled={busyBtOverride}
          onchange={applyBtOverride}
        />
        <span>{$tr('native.audio.btOverride')}</span>
      </label>
      <p class="hint hint-soft">
        {$tr('native.audio.btOverrideHint')}
      </p>

      {#if outputIsBluetooth}
        <p class="hint hint-warn">
          {#each $tr.rich('native.audio.btWarning', { device: $tr('native.audio.btDevice') }) as seg}{#if seg.param}<strong>{seg.text}</strong>{:else}{seg.text}{/if}{/each}
        </p>
      {:else if activeBackend === 'wasapi-shared' && playback?.sample_rate_hz && playback.sample_rate_hz > 48000 && !exclusive}
        <p class="hint hint-warn">
          {#each $tr.rich('native.audio.resampleWarning', { rate: (playback.sample_rate_hz / 1000).toFixed(playback.sample_rate_hz % 1000 === 0 ? 0 : 1), bits: String(playback.bit_depth ?? '?'), enable: $tr('native.audio.enableExclusive') }) as seg}{#if seg.param && seg.param === 'rate' || seg.param === 'bits'}{seg.text}{:else if seg.param}<strong>{seg.text}</strong>{:else}{seg.text}{/if}{/each}
        </p>
      {:else if activeBackend === 'wasapi-shared' && !exclusive}
        <p class="hint hint-soft">
          {$tr('native.audio.sharedHint')}
        </p>
      {/if}
    </section>

    {@render preampSection()}
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
