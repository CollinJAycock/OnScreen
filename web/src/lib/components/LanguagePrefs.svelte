<script lang="ts">
  // The signed-in user's preferred audio and subtitle languages and the
  // "series poster for episodes" display choice (GET / PUT
  // /users/me/preferences). Saved to the account, so every app uses them.
  // Shown on the Account page for everyone and on Settings ▸ General for admins.
  import { onMount } from 'svelte';
  import { userApi } from '$lib/api';
  import { toast } from '$lib/stores/toast';
  import { t, tr, type MessageKey } from '$lib/i18n/index.svelte';

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
  let episodeUseShowPoster = true;
  let saving = false;

  onMount(async () => {
    try {
      const prefs = await userApi.getPreferences();
      audioLang = prefs.preferred_audio_lang ?? '';
      subtitleLang = prefs.preferred_subtitle_lang ?? '';
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
    <label for="pref-audio">{$tr('ui.langPrefs.audio')}</label>
    <select id="pref-audio" bind:value={audioLang}>
      <option value="">{$tr('ui.langPrefs.audioNone')}</option>
      {#each LANGUAGES as [code, name]}
        <option value={code}>{$tr(name)}</option>
      {/each}
    </select>
  </div>
  <div class="field">
    <label for="pref-sub">{$tr('ui.langPrefs.subtitle')}</label>
    <select id="pref-sub" bind:value={subtitleLang}>
      <option value="">{$tr('ui.langPrefs.subtitleNone')}</option>
      {#each LANGUAGES as [code, name]}
        <option value={code}>{$tr(name)}</option>
      {/each}
    </select>
  </div>

  <div class="divider" role="separator"></div>

  <div class="sub-label">{$tr('ui.langPrefs.display')}</div>
  <div class="field">
    <label for="pref-ep-show-poster" class="toggle-label">
      <input id="pref-ep-show-poster" type="checkbox" bind:checked={episodeUseShowPoster} />
      <span>{$tr('ui.langPrefs.showPoster')}</span>
    </label>
    <p class="hint">
      {$tr('ui.langPrefs.showPosterHint')}
    </p>
  </div>

  <div class="foot">
    <button class="btn-save" disabled={saving} on:click={save}>
      {saving ? $tr('player.saving') : $tr('ui.langPrefs.save')}
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
