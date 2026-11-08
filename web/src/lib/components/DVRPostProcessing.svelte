<script lang="ts">
  // Settings ▸ Live TV ▸ "Recording post-processing" (admins). Everything is
  // off by default: a recording stays exactly as the tuner wrote it. The
  // script and comskip are executables, so the server only takes them from
  // its environment — this panel shows them read-only.
  import { settingsApi, type DVRSettings } from '$lib/api';
  import { toast } from '$lib/stores/toast';
  import { t, i18n } from '$lib/i18n/index.svelte';

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
      error = e instanceof Error && e.message ? e.message : t('ui.dvrPost.loadFailed');
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
      toast.success(t('ui.dvrPost.saved'));
    } catch (err) {
      toast.error(err instanceof Error && err.message ? err.message : t('player.saveFailed'));
    } finally {
      saving = false;
    }
  }
</script>

<section>
  <header>
    <h2>{$i18n('ui.dvrPost.title')}</h2>
    <p class="hint">
      {$i18n('ui.dvrPost.intro')}
    </p>
  </header>

  {#if loading}
    <p class="muted">{$i18n('player.loading')}</p>
  {:else if error}
    <p class="error">{error}</p>
  {:else if cfg}
    <form onsubmit={save}>
      <label class="check">
        <input type="checkbox" bind:checked={cfg.remux} />
        <span>
          <span class="name">{$i18n('ui.dvrPost.remux')} <em class="rec">{$i18n('ui.dvrPost.recommended')}</em></span>
          <small>{$i18n('ui.dvrPost.remuxHint')}</small>
        </span>
      </label>

      <label class="check">
        <input type="checkbox" bind:checked={cfg.transcode} />
        <span>
          {$i18n('ui.dvrPost.transcode')}
          <small>{$i18n('ui.dvrPost.transcodeHint')}</small>
        </span>
      </label>

      <div class="grid">
        <label>
          {$i18n('ui.trackInfo.container')}
          <select bind:value={cfg.remux_container} disabled={!cfg.remux && !cfg.transcode}>
            <option value="mkv">MKV</option>
            <option value="mp4">MP4</option>
          </select>
        </label>
        <label>
          {$i18n('ui.dvrPost.videoCodec')}
          <select bind:value={cfg.transcode_codec} disabled={!cfg.transcode}>
            <option value="h264">H.264</option>
            <option value="hevc">{$i18n('ui.dvrPost.hevc')}</option>
          </select>
        </label>
        <label>
          {$i18n('player.quality.title')}
          <select bind:value={cfg.transcode_quality} disabled={!cfg.transcode}>
            <option value="high">{$i18n('ui.dvrPost.qualityHigh')}</option>
            <option value="balanced">{$i18n('ui.dvrPost.qualityBalanced')}</option>
            <option value="small">{$i18n('ui.dvrPost.qualitySmall')}</option>
          </select>
        </label>
      </div>

      <label class="check">
        <input type="checkbox" bind:checked={cfg.delete_original} disabled={!cfg.remux && !cfg.transcode} />
        <span>
          {$i18n('ui.dvrPost.deleteOriginal')}
          <small>{#each $i18n.rich('ui.dvrPost.deleteOriginalHint', { folder: '.dvr-originals' }) as seg}{#if seg.param}<code>{seg.text}</code>{:else}{seg.text}{/if}{/each}</small>
        </span>
      </label>

      <label class="field">
        {$i18n('ui.dvrPost.commercials')}
        <select bind:value={cfg.commercials}>
          <option value="off">{$i18n('player.off')}</option>
          <option value="mark">{$i18n('ui.dvrPost.commercialsMark')}</option>
          <option value="cut" disabled={!canCut}>{canCut ? $i18n('ui.dvrPost.commercialsCut') : $i18n('ui.dvrPost.commercialsCutNeeds')}</option>
        </select>
        <small>
          {cfg.commercial_detector === 'comskip' ? $i18n('ui.dvrPost.detectorComskip') : $i18n('ui.dvrPost.detectorBuiltIn')}
          {$i18n('ui.dvrPost.detectPerRecording')}
        </small>
      </label>

      <div class="env">
        <span class="env-label">{$i18n('ui.dvrPost.script')}</span>
        {#if cfg.script_configured}
          <code>{cfg.script_path}</code>
        {:else}
          <span class="muted">{$i18n('player.subStyle.none')}</span>
        {/if}
        <span class="muted">— {$i18n('ui.dvrPost.viaEnv', { variable: 'ONSCREEN_DVR_POSTPROCESS_SCRIPT' })}</span>
      </div>
      <div class="env">
        <span class="env-label">comskip</span>
        <span class={cfg.comskip_configured ? '' : 'muted'}>{cfg.comskip_configured ? $i18n('ui.dvrPost.configured') : $i18n('ui.dvrPost.notConfigured')}</span>
        <span class="muted">— {$i18n('ui.dvrPost.viaEnv', { variable: 'ONSCREEN_COMSKIP_PATH' })}</span>
      </div>

      <div class="actions">
        <button class="btn btn-primary" type="submit" disabled={saving}>{saving ? $i18n('player.saving') : $i18n('player.markers.save')}</button>
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
  /* Title + badge on one line (the badge would stretch as a column item). */
  .check .name { display: block; }
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
