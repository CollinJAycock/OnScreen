<script lang="ts">
  // Settings ▸ Live TV ▸ "Recording post-processing" (admins). Everything is
  // off by default: a recording stays exactly as the tuner wrote it. The
  // script and comskip are executables, so the server only takes them from
  // its environment — this panel shows them read-only.
  import { settingsApi, type DVRSettings } from '$lib/api';
  import { toast } from '$lib/stores/toast';

  let cfg = $state<DVRSettings | null>(null);
  let loading = $state(true);
  let error = $state('');
  let saving = $state(false);

  async function load() {
    loading = true;
    error = '';
    try {
      cfg = await settingsApi.getDVR();
    } catch (e) {
      error = e instanceof Error && e.message ? e.message : 'Could not load DVR settings';
    } finally {
      loading = false;
    }
  }

  $effect(() => {
    load();
  });

  // Cutting needs a rewrite to cut during; fall back to marking when both
  // rewrite switches go off.
  let canCut = $derived(!!cfg && (cfg.remux || cfg.transcode));
  $effect(() => {
    if (cfg && cfg.commercials === 'cut' && !canCut) cfg.commercials = 'mark';
  });

  async function save(e: SubmitEvent) {
    e.preventDefault();
    if (!cfg) return;
    saving = true;
    try {
      await settingsApi.updateDVR({
        remux: cfg.remux,
        remux_container: cfg.remux_container,
        transcode: cfg.transcode,
        transcode_codec: cfg.transcode_codec,
        transcode_quality: cfg.transcode_quality,
        delete_original: cfg.delete_original,
        commercials: cfg.commercials,
      });
      toast.success('Post-processing settings saved');
    } catch (err) {
      toast.error(err instanceof Error && err.message ? err.message : 'Save failed');
    } finally {
      saving = false;
    }
  }
</script>

<section>
  <header>
    <h2>Recording post-processing</h2>
    <p class="hint">
      What happens to a recording after it finishes. Changes apply to the next
      recording; existing recordings can be re-run from the Recordings page.
      A failed step leaves the original recording untouched.
    </p>
  </header>

  {#if loading}
    <p class="muted">Loading…</p>
  {:else if error}
    <p class="error">{error}</p>
  {:else if cfg}
    <form onsubmit={save}>
      <label class="check">
        <input type="checkbox" bind:checked={cfg.remux} />
        <span>
          Remux recordings <em class="rec">recommended</em>
          <small>Copies the streams into a new container with clean timestamps, so seeking is reliable. Fast; no quality change.</small>
        </span>
      </label>

      <label class="check">
        <input type="checkbox" bind:checked={cfg.transcode} />
        <span>
          Transcode to save space
          <small>Re-encodes the video (the audio is copied). Uses the hardware encoders allowed under Transcode settings, falling back to software. Slow on CPU.</small>
        </span>
      </label>

      <div class="grid">
        <label>
          Container
          <select bind:value={cfg.remux_container} disabled={!cfg.remux && !cfg.transcode}>
            <option value="mkv">MKV</option>
            <option value="mp4">MP4</option>
          </select>
        </label>
        <label>
          Video codec
          <select bind:value={cfg.transcode_codec} disabled={!cfg.transcode}>
            <option value="h264">H.264</option>
            <option value="hevc">HEVC (smaller, less compatible)</option>
          </select>
        </label>
        <label>
          Quality
          <select bind:value={cfg.transcode_quality} disabled={!cfg.transcode}>
            <option value="high">High</option>
            <option value="balanced">Balanced</option>
            <option value="small">Smallest file</option>
          </select>
        </label>
      </div>

      <label class="check">
        <input type="checkbox" bind:checked={cfg.delete_original} disabled={!cfg.remux && !cfg.transcode} />
        <span>
          Delete the original after processing
          <small>Only once the new file has been checked (same length, all streams present). When off, the original is kept in a hidden <code>.dvr-originals</code> folder and removed with the recording.</small>
        </span>
      </label>

      <label class="field">
        Commercials
        <select bind:value={cfg.commercials}>
          <option value="off">Off</option>
          <option value="mark">Mark (show a Skip ad button)</option>
          <option value="cut" disabled={!canCut}>Cut them out{canCut ? '' : ' (needs remux or transcode)'}</option>
        </select>
        <small>
          Detector: {cfg.commercial_detector === 'comskip' ? 'comskip' : 'built-in (black frames + silence); conservative, so some ads may be missed'}.
          Commercials can also be detected per recording from the Recordings page.
        </small>
      </label>

      <div class="env">
        <span class="env-label">Post-processing script</span>
        {#if cfg.script_configured}
          <code>{cfg.script_path}</code>
        {:else}
          <span class="muted">None</span>
        {/if}
        <span class="muted">— configured via environment (ONSCREEN_DVR_POSTPROCESS_SCRIPT)</span>
      </div>
      <div class="env">
        <span class="env-label">comskip</span>
        <span class={cfg.comskip_configured ? '' : 'muted'}>{cfg.comskip_configured ? 'Configured' : 'Not configured'}</span>
        <span class="muted">— configured via environment (ONSCREEN_COMSKIP_PATH)</span>
      </div>

      <div class="actions">
        <button class="btn btn-primary" type="submit" disabled={saving}>{saving ? 'Saving…' : 'Save'}</button>
      </div>
    </form>
  {/if}
</section>

<style>
  section {
    background: var(--bg-elevated);
    border: 1px solid var(--border);
    border-radius: 10px;
    padding: 1.1rem 1.25rem;
  }
  h2 { font-size: 0.95rem; margin: 0 0 0.5rem; font-weight: 600; }
  .hint { color: var(--text-secondary); font-size: 0.82rem; line-height: 1.5; margin: 0 0 1rem; }
  .muted { color: var(--text-muted); }
  .error { color: var(--error); }
  form { display: flex; flex-direction: column; gap: 0.85rem; }
  .check { display: flex; gap: 0.6rem; align-items: flex-start; font-size: 0.85rem; color: var(--text-primary); cursor: pointer; }
  .check input { margin-top: 0.2rem; accent-color: var(--accent); }
  .check span { display: flex; flex-direction: column; gap: 0.15rem; }
  small { color: var(--text-muted); font-size: 0.74rem; line-height: 1.4; }
  .rec {
    font-style: normal; font-size: 0.65rem; text-transform: uppercase; letter-spacing: 0.04em;
    background: var(--accent-bg); color: var(--accent-text); border-radius: 4px; padding: 0.05rem 0.35rem; margin-left: 0.3rem;
  }
  .grid { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 0.75rem; }
  @media (max-width: 640px) { .grid { grid-template-columns: 1fr; } }
  label:not(.check) { display: flex; flex-direction: column; gap: 0.3rem; font-size: 0.78rem; color: var(--text-secondary); }
  select {
    padding: 0.45rem 0.6rem; border-radius: 7px; border: 1px solid var(--border-strong);
    background: var(--input-bg); color: var(--text-primary); font-size: 0.82rem;
  }
  select:disabled { opacity: 0.5; }
  .env { display: flex; gap: 0.5rem; align-items: baseline; flex-wrap: wrap; font-size: 0.8rem; }
  .env-label { color: var(--text-secondary); min-width: 11rem; }
  code {
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 0.76rem;
    background: var(--input-bg); border: 1px solid var(--border); border-radius: 4px; padding: 0.1rem 0.35rem;
  }
  .actions { display: flex; gap: 0.5rem; }
  .btn {
    padding: 0.45rem 0.9rem; border-radius: 7px; font-size: 0.8rem; font-weight: 600; cursor: pointer; border: none;
  }
  .btn:disabled { opacity: 0.5; cursor: not-allowed; }
  .btn-primary { background: var(--accent); color: #fff; }
  .btn-primary:hover:not(:disabled) { background: var(--accent-hover); }
</style>
