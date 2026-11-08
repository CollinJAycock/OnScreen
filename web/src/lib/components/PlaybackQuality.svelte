<script lang="ts">
  // "Playback quality": the signed-in user's own quality profile
  // (PUT /users/me/quality-profile). The server applies it on every device —
  // the play decision transcodes anything above it, at home or away — so it
  // lives on the Account page rather than in one player's settings.
  import { onMount } from 'svelte';
  import { userApi } from '$lib/api';
  import { toast } from '$lib/stores/toast';
  import { t, i18n } from '$lib/i18n/index.svelte';

  type Opt = [value: string, label: string];

  let loading = true;
  let saving = false;
  let error = '';
  // '' = no limit / automatic.
  let height = '';
  let videoKbps = '';
  let audioKbps = '';
  let codec = '';

  const HEIGHTS: Opt[] = [['', 'Original'], ['2160', '4K'], ['1080', '1080p'], ['720', '720p'], ['480', '480p']];
  const VIDEO_KBPS: Opt[] = [
    ['', 'No limit'], ['40000', '40 Mbps'], ['20000', '20 Mbps'], ['12000', '12 Mbps'],
    ['8000', '8 Mbps'], ['4000', '4 Mbps'], ['2000', '2 Mbps'], ['1000', '1 Mbps'],
  ];
  const AUDIO_KBPS: Opt[] = [
    ['', 'Original'], ['320', '320 kbps'], ['256', '256 kbps'], ['192', '192 kbps'], ['128', '128 kbps'], ['96', '96 kbps'],
  ];

  // A stored value no option covers (set through the API or another client)
  // keeps its own option instead of reading as "no limit".
  function withStored(opts: Opt[], v: string, label: (v: string) => string): Opt[] {
    return v === '' || opts.some(([o]) => o === v) ? opts : [...opts, [v, label(v)]];
  }
  const str = (n: number | null | undefined) => (n ? String(n) : '');
  const num = (v: string) => (v === '' ? null : Number(v));

  onMount(async () => {
    try {
      const p = await userApi.getPreferences();
      height = str(p.max_video_height);
      videoKbps = str(p.max_video_bitrate_kbps);
      audioKbps = str(p.max_audio_bitrate_kbps);
      codec = p.preferred_video_codec === 'h264' || p.preferred_video_codec === 'hevc' ? p.preferred_video_codec : '';
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : t('ui.quality.loadFailed');
    } finally {
      loading = false;
    }
  });

  async function save() {
    saving = true;
    try {
      // The endpoint replaces the whole profile, forced_subtitles_only (set
      // by the TV apps) included: echo a fresh read so saving here keeps it.
      const fresh = await userApi.getPreferences();
      await userApi.setQualityProfile({
        max_video_height: num(height),
        max_video_bitrate_kbps: num(videoKbps),
        max_audio_bitrate_kbps: num(audioKbps),
        preferred_video_codec: codec || null,
        forced_subtitles_only: fresh.forced_subtitles_only ?? false,
      });
      toast.success(t('ui.quality.saved'));
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : t('ui.metadata.saveFailed'));
    } finally {
      saving = false;
    }
  }
</script>

{#if loading}
  <p class="muted">{$i18n('player.loading')}</p>
{:else if error}
  <p class="error">{error}</p>
{:else}
  <div class="grid">
    <label>
      <span>{$i18n('ui.quality.maxResolution')}</span>
      <select bind:value={height}>
        {#each withStored(HEIGHTS, height, (v) => `${v}p`) as [v, l] (v)}
          <option value={v}>{v === '' ? $i18n('ui.quality.original') : l}</option>
        {/each}
      </select>
    </label>
    <label>
      <span>{$i18n('ui.quality.maxVideoBitrate')}</span>
      <select bind:value={videoKbps}>
        {#each withStored(VIDEO_KBPS, videoKbps, (v) => `${Number(v) / 1000} Mbps`) as [v, l] (v)}
          <option value={v}>{v === '' ? $i18n('ui.quality.noLimit') : l}</option>
        {/each}
      </select>
    </label>
    <label>
      <span>{$i18n('ui.quality.maxAudioBitrate')}</span>
      <select bind:value={audioKbps}>
        {#each withStored(AUDIO_KBPS, audioKbps, (v) => `${v} kbps`) as [v, l] (v)}
          <option value={v}>{v === '' ? $i18n('ui.quality.original') : l}</option>
        {/each}
      </select>
    </label>
    <label>
      <span>{$i18n('ui.quality.codec')}</span>
      <select bind:value={codec}>
        <option value="">{$i18n('ui.quality.automatic')}</option>
        <option value="h264">{$i18n('ui.quality.h264')}</option>
        <option value="hevc">{$i18n('ui.quality.hevc')}</option>
      </select>
    </label>
  </div>
  <button type="button" class="save" disabled={saving} on:click={save}>
    {saving ? $i18n('player.saving') : $i18n('player.markers.save')}
  </button>
{/if}

<style>
  .grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(13rem, 1fr)); gap: 0.75rem 1rem; }
  label { display: flex; flex-direction: column; gap: 0.3rem; font-size: 0.8rem; color: var(--text-secondary); }
  select {
    font: inherit; font-size: 0.85rem; padding: 0.42rem 0.55rem; border-radius: 6px;
    border: 1px solid var(--border-strong); background: var(--input-bg); color: var(--text-primary);
  }
  .save {
    margin-top: 0.9rem; width: auto; padding: 0.45rem 1.1rem; font: inherit; font-size: 0.82rem; font-weight: 600;
    border: 0; border-radius: 6px; cursor: pointer; background: var(--accent); color: var(--accent-contrast, #fff);
  }
  .save:disabled { opacity: 0.6; cursor: progress; }
  .muted { color: var(--text-muted); font-size: 0.82rem; margin: 0; }
  .error { color: var(--error); font-size: 0.82rem; margin: 0; }
</style>
