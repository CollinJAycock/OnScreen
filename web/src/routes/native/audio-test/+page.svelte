<script lang="ts">
  import { onMount } from 'svelte';
  import {
    isTauri, listAudioDevices, playTestTone, stopAudio,
    audioPlayUrl, audioState, audioPause, audioResume,
    type AudioDevice, type PlaybackStatus,
  } from '$lib/native';

  let loading = true;
  let devices: AudioDevice[] = [];
  let error = '';
  let frequency = 440;
  let durationMs = 2000;
  let busy: string | null = null;

  // FLAC streaming form state.
  let flacUrl = '';
  let flacBearer = '';
  let flacDevice = '';
  let flacError = '';
  let flacBusy = false;
  let lastStatus: PlaybackStatus | null = null;

  onMount(async () => {
    if (!isTauri()) {
      error = 'This diagnostic page only works inside the OnScreen desktop client.';
      loading = false;
      return;
    }
    try {
      devices = await listAudioDevices();
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  });

  async function play(device: string | null) {
    error = '';
    busy = device ?? '__default__';
    try {
      await playTestTone(device, frequency, durationMs);
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      // Match the auto-stop timer in Rust so the button enables itself
      // back at roughly the right moment. Slight overshoot is fine —
      // the engine's stop is idempotent.
      setTimeout(() => {
        if (busy === (device ?? '__default__')) busy = null;
      }, durationMs + 250);
    }
  }

  async function stopAll() {
    await stopAudio();
    busy = null;
    flacBusy = false;
    lastStatus = await audioState();
  }

  async function playFlac() {
    flacError = '';
    if (!flacUrl.trim()) { flacError = 'URL required'; return; }
    flacBusy = true;
    try {
      lastStatus = await audioPlayUrl(
        flacUrl.trim(),
        flacBearer.trim() || null,
        flacDevice || null,
      );
    } catch (e: unknown) {
      flacError = e instanceof Error ? e.message : String(e);
    } finally {
      flacBusy = false;
    }
  }

  async function togglePause() {
    if (!lastStatus) return;
    if (lastStatus.paused) {
      await audioResume();
    } else {
      await audioPause();
    }
    lastStatus = await audioState();
  }
</script>

<svelte:head><title>Audio diagnostic — OnScreen</title></svelte:head>

<div class="page">
  <h1>Native audio engine — diagnostic</h1>
  <p class="subtitle">
    Lists every output device the desktop client's <code>cpal</code> backend can see,
    and plays a brief sine-wave test tone on demand. Use this to verify the
    audio path works on your hardware before the engine takes over playback.
  </p>

  {#if error}
    <div class="error-bar">{error}</div>
  {/if}

  {#if loading}
    <div class="muted">Loading…</div>
  {:else if devices.length === 0 && !error}
    <div class="muted">No output devices reported by cpal.</div>
  {:else}
    <section class="controls">
      <label>
        Frequency (Hz)
        <input type="number" min="50" max="5000" bind:value={frequency} />
      </label>
      <label>
        Duration (ms)
        <input type="number" min="100" max="10000" step="100" bind:value={durationMs} />
      </label>
      <button type="button" class="stop" on:click={stopAll}>Stop</button>
    </section>

    <section class="flac-stream">
      <h2>FLAC streaming</h2>
      <p class="muted">
        Streams a FLAC file from any URL through the engine end-to-end —
        HTTP fetch → claxon decode → cpal at the file's native rate. Use
        <code>/media/files/&lt;file_id&gt;</code> against your server to test
        with real library content; supply the bearer token from
        <code>localStorage.onscreen_user</code> after a successful login.
      </p>
      <div class="flac-row">
        <label>
          FLAC URL
          <input type="url" bind:value={flacUrl} placeholder="https://onscreen.example.com/media/files/<id>" />
        </label>
      </div>
      <div class="flac-row">
        <label>
          Bearer token (optional)
          <input type="password" bind:value={flacBearer} placeholder="paste access_token from /auth/login response" />
        </label>
        <label class="flac-device">
          Output device
          <select bind:value={flacDevice}>
            <option value="">(default)</option>
            {#each devices as d (d.name)}
              {#if d.default_output_summary}
                <option value={d.name}>{d.name}</option>
              {/if}
            {/each}
          </select>
        </label>
      </div>
      {#if flacError}
        <div class="error-bar">{flacError}</div>
      {/if}
      {#if lastStatus?.playing && lastStatus.source_url?.startsWith('http')}
        <div class="status-bar">
          <span>
            {lastStatus.paused ? 'Paused' : 'Playing'} ·
            {lastStatus.bit_depth}-bit · {lastStatus.sample_rate_hz} Hz · {lastStatus.channels} ch
          </span>
          <button type="button" class="transport" on:click={togglePause}>
            {lastStatus.paused ? 'Resume' : 'Pause'}
          </button>
        </div>
      {/if}
      <button type="button" class="play wide" disabled={flacBusy} on:click={playFlac}>
        {flacBusy ? 'Connecting…' : 'Play FLAC URL'}
      </button>
    </section>

    <h2 class="devices-heading">Output devices</h2>
    <ul class="devices">
      {#each devices as d (d.name)}
        <li>
          <div class="device-name">
            {d.name}
            {#if d.is_default}<span class="badge">default</span>{/if}
          </div>
          {#if d.default_output_summary}
            <div class="device-summary">{d.default_output_summary}</div>
          {:else}
            <div class="device-summary muted">no output config (input-only?)</div>
          {/if}
          <button
            type="button"
            class="play"
            disabled={busy === d.name || !d.default_output_summary}
            on:click={() => play(d.name)}
          >
            {busy === d.name ? 'Playing…' : 'Play test tone'}
          </button>
        </li>
      {/each}
    </ul>
  {/if}
</div>

<style>
  .page { padding: 2.5rem; max-width: 720px; }
  h1 { font-size: 1.4rem; font-weight: 800; color: var(--text-primary); margin: 0 0 0.5rem; }
  .subtitle { font-size: 0.85rem; color: var(--text-muted); line-height: 1.55; margin: 0 0 1.5rem; }
  .subtitle code { background: var(--bg-hover); padding: 0.05rem 0.3rem; border-radius: 4px; font-size: 0.78rem; }

  .controls { display: flex; gap: 1rem; align-items: end; margin-bottom: 1.25rem; flex-wrap: wrap; }
  .controls label { display: flex; flex-direction: column; gap: 0.3rem; font-size: 0.72rem; color: var(--text-muted); font-weight: 500; }
  .controls input {
    background: var(--bg-hover); border: 1px solid var(--border-strong);
    border-radius: 7px; padding: 0.42rem 0.7rem; color: var(--text-primary); font-size: 0.85rem; width: 120px;
  }
  .controls input:focus { outline: none; border-color: var(--accent); }
  .controls .stop {
    margin-left: auto; padding: 0.42rem 0.85rem; background: var(--bg-hover);
    border: 1px solid var(--border-strong); border-radius: 7px;
    color: var(--text-secondary); font-size: 0.78rem; cursor: pointer;
  }
  .controls .stop:hover { border-color: rgba(248,113,113,0.4); color: #f87171; }

  .devices { list-style: none; padding: 0; margin: 0; display: flex; flex-direction: column; gap: 0.75rem; }
  .devices li {
    display: grid; grid-template-columns: 1fr auto; gap: 0.6rem 1rem; align-items: center;
    padding: 0.85rem 1rem; background: var(--bg-elevated);
    border: 1px solid var(--border); border-radius: 10px;
  }
  .device-name { font-size: 0.92rem; font-weight: 600; color: var(--text-primary); display: flex; align-items: center; gap: 0.5rem; }
  .device-summary { grid-column: 1; font-size: 0.75rem; color: var(--text-muted); font-family: monospace; }
  .badge {
    font-size: 0.6rem; padding: 0.1rem 0.4rem; border-radius: 4px;
    background: var(--accent-bg); color: var(--accent-text); font-weight: 600;
  }
  .play {
    grid-row: 1 / 3; padding: 0.42rem 0.85rem; background: var(--accent);
    border: none; border-radius: 7px; color: #fff; font-size: 0.78rem; font-weight: 600;
    cursor: pointer; transition: background 0.15s;
  }
  .play:hover { background: var(--accent-hover); }
  .play:disabled { opacity: 0.5; cursor: not-allowed; }

  .error-bar {
    background: var(--error-bg); border: 1px solid var(--error-bg); color: var(--error);
    padding: 0.6rem 0.9rem; border-radius: 8px; font-size: 0.8rem; margin-bottom: 1.25rem;
  }
  .muted { color: var(--text-muted); font-size: 0.85rem; }

  .flac-stream {
    background: var(--bg-elevated); border: 1px solid var(--border);
    border-radius: 10px; padding: 1.25rem; margin-bottom: 1.75rem;
    display: flex; flex-direction: column; gap: 0.75rem;
  }
  .flac-stream h2 { margin: 0; font-size: 1rem; font-weight: 700; color: var(--text-primary); }
  .flac-stream code { background: var(--bg-hover); padding: 0.05rem 0.3rem; border-radius: 4px; font-size: 0.78rem; }
  .flac-row { display: flex; gap: 0.75rem; flex-wrap: wrap; }
  .flac-row label { display: flex; flex-direction: column; gap: 0.3rem; font-size: 0.72rem; color: var(--text-muted); flex: 1; min-width: 220px; }
  .flac-row input, .flac-row select {
    background: var(--bg-hover); border: 1px solid var(--border-strong);
    border-radius: 7px; padding: 0.42rem 0.7rem; color: var(--text-primary); font-size: 0.85rem;
  }
  .flac-row input:focus, .flac-row select:focus { outline: none; border-color: var(--accent); }
  .flac-device { max-width: 260px; }
  .play.wide { width: 100%; padding: 0.55rem 0.85rem; }
  .status-bar {
    display: flex; align-items: center; justify-content: space-between; gap: 0.6rem;
    background: var(--accent-bg); color: var(--accent-text);
    padding: 0.5rem 0.8rem; border-radius: 7px; font-size: 0.78rem; font-weight: 600;
    font-family: monospace;
  }
  .transport {
    padding: 0.3rem 0.7rem; background: rgba(0, 0, 0, 0.18);
    border: 1px solid rgba(0, 0, 0, 0.28); border-radius: 6px;
    color: var(--accent-text); font-size: 0.74rem; font-weight: 700;
    cursor: pointer; transition: background 0.12s;
    font-family: inherit;
  }
  .transport:hover { background: rgba(0, 0, 0, 0.28); }
  .devices-heading { font-size: 1rem; font-weight: 700; color: var(--text-primary); margin: 0 0 0.75rem; }
</style>
