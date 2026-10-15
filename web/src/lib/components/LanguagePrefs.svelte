<script lang="ts">
  // The signed-in user's preferred audio and subtitle languages and the
  // "series poster for episodes" display choice (GET / PUT
  // /users/me/preferences). Saved to the account, so every app uses them.
  // Shown on the Account page for everyone and on Settings ▸ General for admins.
  import { onMount } from 'svelte';
  import { userApi } from '$lib/api';
  import { toast } from '$lib/stores/toast';

  const LANGUAGES: [string, string][] = [
    ['eng', 'English'],
    ['jpn', 'Japanese'],
    ['spa', 'Spanish'],
    ['fra', 'French'],
    ['deu', 'German'],
    ['ita', 'Italian'],
    ['por', 'Portuguese'],
    ['rus', 'Russian'],
    ['zho', 'Chinese'],
    ['kor', 'Korean'],
    ['hin', 'Hindi'],
    ['ara', 'Arabic'],
    ['tha', 'Thai'],
    ['pol', 'Polish'],
    ['nld', 'Dutch'],
    ['swe', 'Swedish'],
    ['nor', 'Norwegian'],
    ['dan', 'Danish'],
    ['fin', 'Finnish'],
    ['tur', 'Turkish'],
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
      toast.success('Preferences saved');
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Failed to save preferences');
    } finally {
      saving = false;
    }
  }
</script>

<div class="prefs">
  <div class="field">
    <label for="pref-audio">Preferred Audio Language</label>
    <select id="pref-audio" bind:value={audioLang}>
      <option value="">None (use default)</option>
      {#each LANGUAGES as [code, name]}
        <option value={code}>{name}</option>
      {/each}
    </select>
  </div>
  <div class="field">
    <label for="pref-sub">Preferred Subtitle Language</label>
    <select id="pref-sub" bind:value={subtitleLang}>
      <option value="">None (disabled)</option>
      {#each LANGUAGES as [code, name]}
        <option value={code}>{name}</option>
      {/each}
    </select>
  </div>

  <div class="divider" role="separator"></div>

  <div class="sub-label">Display</div>
  <div class="field">
    <label for="pref-ep-show-poster" class="toggle-label">
      <input id="pref-ep-show-poster" type="checkbox" bind:checked={episodeUseShowPoster} />
      <span>Use the series poster for episodes</span>
    </label>
    <p class="hint">
      On Continue Watching, History, and Search, episode rows show the show's
      curated poster instead of an auto-extracted episode thumbnail. Episodes
      inside a season's list still show their own art so they stay
      distinguishable.
    </p>
  </div>

  <div class="foot">
    <button class="btn-save" disabled={saving} on:click={save}>
      {saving ? 'Saving...' : 'Save Preferences'}
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
