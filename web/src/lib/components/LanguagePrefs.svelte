<script lang="ts">
  // The signed-in user's preferred audio and subtitle languages, when the
  // subtitles come on (subtitle_mode), and the "series poster for episodes"
  // display choice (GET / PUT /users/me/preferences; the subtitle mode is part
  // of the quality profile, PUT /users/me/quality-profile). Saved to the
  // account, so every app uses them. New accounts start with English for both
  // and the "foreign" mode (migration 00066).
  // Shown on the Account page for everyone and on Settings ▸ General for admins.
  import { onMount } from 'svelte';
  import { userApi } from '$lib/api';
  import { toast } from '$lib/stores/toast';
  import { t, i18n, type MessageKey } from '$lib/i18n/index.svelte';
  import { subtitleModeOf, type SubtitleMode } from '$lib/subtitleMode';

  const SUBTITLE_MODES: [SubtitleMode, MessageKey][] = [
    ['foreign', 'ui.langPrefs.modeForeign'],
    ['forced', 'ui.langPrefs.modeForced'],
    ['always', 'ui.langPrefs.modeAlways'],
  ];

  const LANGUAGES: [string, MessageKey][] = [
    ['eng', 'ui.langPrefs.lang.eng'],
    ['jpn', 'ui.langPrefs.lang.jpn'],
    ['spa', 'ui.langPrefs.lang.spa'],
    ['fra', 'ui.langPrefs.lang.fra'],
    ['deu', 'ui.langPrefs.lang.deu'],
    ['ita', 'ui.langPrefs.lang.ita'],
    ['por', 'ui.langPrefs.lang.por'],
    ['rus', 'ui.langPrefs.lang.rus'],
    ['zho', 'ui.langPrefs.lang.zho'],
    ['kor', 'ui.langPrefs.lang.kor'],
    ['hin', 'ui.langPrefs.lang.hin'],
    ['ara', 'ui.langPrefs.lang.ara'],
    ['tha', 'ui.langPrefs.lang.tha'],
    ['pol', 'ui.langPrefs.lang.pol'],
    ['nld', 'ui.langPrefs.lang.nld'],
    ['swe', 'ui.langPrefs.lang.swe'],
    ['nor', 'ui.langPrefs.lang.nor'],
    ['dan', 'ui.langPrefs.lang.dan'],
    ['fin', 'ui.langPrefs.lang.fin'],
    ['tur', 'ui.langPrefs.lang.tur'],
  ];

  let audioLang = '';
  let subtitleLang = '';
  let subtitleMode: SubtitleMode = 'always';
  let loadedSubtitleMode: SubtitleMode = 'always';
  let episodeUseShowPoster = true;
  let saving = false;

  onMount(async () => {
    try {
      const prefs = await userApi.getPreferences();
      audioLang = prefs.preferred_audio_lang ?? '';
      subtitleLang = prefs.preferred_subtitle_lang ?? '';
      subtitleMode = loadedSubtitleMode = subtitleModeOf(prefs);
      episodeUseShowPoster = prefs.episode_use_show_poster ?? true;
    } catch { /* ignore — non-critical; the defaults show */ }
  });

  async function save() {
    saving = true;
    try {
      await userApi.setPreferences({
        preferred_audio_lang: audioLang || null,
        preferred_subtitle_lang: subtitleLang || null,
        max_content_rating: null,
        episode_use_show_poster: episodeUseShowPoster,
      });
      if (subtitleMode !== loadedSubtitleMode) {
        // The quality-profile PUT replaces the whole profile: echo a fresh
        // read so the stream limits set on the Playback page stay. The
        // boolean rides along for a server that predates subtitle_mode
        // ("foreign" is forced-only there).
        const fresh = await userApi.getPreferences();
        await userApi.setQualityProfile({
          max_video_height: fresh.max_video_height ?? null,
          max_video_bitrate_kbps: fresh.max_video_bitrate_kbps ?? null,
          max_audio_bitrate_kbps: fresh.max_audio_bitrate_kbps ?? null,
          preferred_video_codec: fresh.preferred_video_codec ?? null,
          forced_subtitles_only: subtitleMode !== 'always',
          subtitle_mode: subtitleMode,
        });
        loadedSubtitleMode = subtitleMode;
      }
      toast.success(t('ui.langPrefs.saved'));
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : t('ui.langPrefs.saveFailed'));
    } finally {
      saving = false;
    }
  }
</script>

<div class="prefs">
  <div class="field">
    <label for="pref-audio">{$i18n('ui.langPrefs.audio')}</label>
    <select id="pref-audio" bind:value={audioLang}>
      <option value="">{$i18n('ui.langPrefs.audioNone')}</option>
      {#each LANGUAGES as [code, name]}
        <option value={code}>{$i18n(name)}</option>
      {/each}
    </select>
  </div>
  <div class="field">
    <label for="pref-sub">{$i18n('ui.langPrefs.subtitle')}</label>
    <select id="pref-sub" bind:value={subtitleLang}>
      <option value="">{$i18n('ui.langPrefs.subtitleNone')}</option>
      {#each LANGUAGES as [code, name]}
        <option value={code}>{$i18n(name)}</option>
      {/each}
    </select>
  </div>
  <div class="field">
    <label for="pref-sub-mode">{$i18n('ui.langPrefs.mode')}</label>
    <select
      id="pref-sub-mode"
      value={subtitleMode}
      on:change={(e) => (subtitleMode = e.currentTarget.value as SubtitleMode)}
    >
      {#each SUBTITLE_MODES as [mode, name]}
        <option value={mode}>{$i18n(name)}</option>
      {/each}
    </select>
    <p class="hint">{$i18n('ui.langPrefs.modeHint')}</p>
  </div>

  <div class="divider" role="separator"></div>

  <div class="sub-label">{$i18n('ui.langPrefs.display')}</div>
  <div class="field">
    <label for="pref-ep-show-poster" class="toggle-label">
      <input id="pref-ep-show-poster" type="checkbox" bind:checked={episodeUseShowPoster} />
      <span>{$i18n('ui.langPrefs.showPoster')}</span>
    </label>
    <p class="hint">
      {$i18n('ui.langPrefs.showPosterHint')}
    </p>
  </div>

  <div class="foot">
    <button class="btn-save" disabled={saving} on:click={save}>
      {saving ? $i18n('player.saving') : $i18n('ui.langPrefs.save')}
    </button>
  </div>
</div>

<style>
  .prefs { display: flex; flex-direction: column; gap: 1.1rem; }
  .field { display: flex; flex-direction: column; gap: 0.3rem; }
  label { font-size: 0.72rem; font-weight: 500; color: var(--text-muted); }
  select {
    background: var(--input-bg);
    border: 1px solid var(--border-strong);
    border-radius: 7px;
    padding: 0.48rem 0.7rem;
    font-size: 0.85rem;
    color: var(--text-primary);
    font-family: inherit;
    width: 100%;
    cursor: pointer;
  }
  select:focus { outline: none; border-color: var(--accent); box-shadow: 0 0 0 3px var(--accent-bg); }
  select option { background: var(--bg-elevated); color: var(--text-primary); }
  .divider { height: 1px; background: var(--border); }
  .sub-label {
    font-size: 0.68rem; font-weight: 700;
    text-transform: uppercase; letter-spacing: 0.09em;
    color: var(--text-muted);
  }
  .toggle-label {
    display: flex; align-items: center; gap: 0.6rem;
    cursor: pointer; user-select: none;
    font-size: 0.85rem; color: var(--text-primary);
  }
  .toggle-label input { width: 1rem; height: 1rem; accent-color: var(--accent); cursor: pointer; }
  .hint { font-size: 0.72rem; color: var(--text-muted); line-height: 1.5; margin: 0.15rem 0 0; }
  .foot { display: flex; justify-content: flex-end; }
  .btn-save {
    padding: 0.45rem 0.9rem; background: var(--accent);
    border: none; border-radius: 7px; color: #fff;
    font-size: 0.8rem; font-weight: 600; cursor: pointer; transition: background 0.15s;
  }
  .btn-save:hover { background: var(--accent-hover); }
  .btn-save:disabled { opacity: 0.5; cursor: not-allowed; }
</style>
