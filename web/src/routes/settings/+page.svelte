<script lang="ts">
  import { onMount } from 'svelte';
  import { settingsApi, userApi, api } from '$lib/api';
  import type { UserMeta, UserPreferences } from '$lib/api';
  import { toast } from '$lib/stores/toast';

  let loading = true;
  let saving = false;
  let error = '';

  let tmdbApiKey = '';
  let tvdbApiKey = '';
  let arrApiKey = '';
  let arrWebhookUrl = '';
  let arrPathMappings: Array<{ remote: string; local: string }> = [];

  // OpenSubtitles state. `osPasswordMasked` tracks whether the currently
  // displayed password is the masked sentinel from the server — editing it
  // flips the flag so save() knows to send the plaintext up.
  let osApiKey = '';
  let osUsername = '';
  let osPassword = '';
  let osPasswordMasked = false;
  let osLanguages = '';
  let osEnabled = false;

  // General server config (BaseURL, LogLevel, CORS) — restart-required.
  let genBaseURL = '';
  let genLogLevel = 'info';
  let genCORS = ''; // comma-separated

  // PIN management state
  let hasPin = false;
  let pinMode: 'idle' | 'set' | 'change' | 'clear' = 'idle';
  let pinValue = '';
  let pinPassword = '';
  let pinSaving = false;
  let pinError = '';

  // Language preferences state
  let prefAudioLang = '';
  let prefSubtitleLang = '';
  let prefSaving = false;



  onMount(async () => {
    try {
      const s = await settingsApi.get();
      tmdbApiKey = s.tmdb_api_key ?? '';
      tvdbApiKey = s.tvdb_api_key ?? '';
      arrApiKey = s.arr_api_key ?? '';
      arrWebhookUrl = s.arr_webhook_url ?? '';
      const pm = s.arr_path_mappings ?? {};
      arrPathMappings = Object.entries(pm).map(([remote, local]) => ({ remote, local }));
      if (arrPathMappings.length === 0) arrPathMappings = [{ remote: '', local: '' }];
      const os = s.opensubtitles;
      if (os) {
        osApiKey = os.api_key ?? '';
        osUsername = os.username ?? '';
        osPassword = os.password ?? '';
        osPasswordMasked = osPassword === '****';
        osLanguages = os.languages ?? '';
        osEnabled = !!os.enabled;
      }
      const gen = s.general;
      if (gen) {
        genBaseURL = gen.base_url ?? '';
        genLogLevel = gen.log_level || 'info';
        genCORS = (gen.cors_allowed_origins ?? []).join(', ');
      }
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed to load settings';
    } finally {
      loading = false;
    }
    // Check current user's PIN status
    try {
      const user: UserMeta | null = api.getUser();
      if (user) {
        const switchable = await userApi.listSwitchable();
        const me = switchable.find((u: { id: string }) => u.id === user.user_id);
        if (me) hasPin = me.has_pin;
      }
    } catch { /* ignore — non-critical */ }

    // Load language preferences
    try {
      const prefs = await userApi.getPreferences();
      prefAudioLang = prefs.preferred_audio_lang ?? '';
      prefSubtitleLang = prefs.preferred_subtitle_lang ?? '';
    } catch { /* ignore — non-critical */ }

  });

  async function savePreferences() {
    prefSaving = true;
    try {
      await userApi.setPreferences({
        preferred_audio_lang: prefAudioLang || null,
        preferred_subtitle_lang: prefSubtitleLang || null,
        max_content_rating: null
      });
      toast.success('Language preferences saved');
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Failed to save preferences');
    } finally {
      prefSaving = false;
    }
  }


  async function save() {
    error = '';
    saving = true;
    try {
      // Build path mappings object from the UI rows (skip empty rows).
      const pm: Record<string, string> = {};
      for (const m of arrPathMappings) {
        const r = m.remote.trim();
        const l = m.local.trim();
        if (r && l) pm[r] = l;
      }
      await settingsApi.update({
        tmdb_api_key: tmdbApiKey.trim(),
        tvdb_api_key: tvdbApiKey.trim(),
        arr_api_key: arrApiKey.trim(),
        arr_path_mappings: pm,
        opensubtitles: {
          api_key: osApiKey.trim(),
          username: osUsername.trim(),
          // If the password is still the masked sentinel, send "****" so the
          // backend knows to keep the stored one unchanged.
          password: osPasswordMasked ? '****' : osPassword,
          languages: osLanguages.trim(),
          enabled: osEnabled,
        },
        general: {
          base_url: genBaseURL.trim(),
          log_level: genLogLevel,
          cors_allowed_origins: genCORS
            .split(',')
            .map((s) => s.trim())
            .filter(Boolean),
        },
      });
      // Reload to get updated webhook URL
      const s = await settingsApi.get();
      arrApiKey = s.arr_api_key ?? '';
      arrWebhookUrl = s.arr_webhook_url ?? '';
      toast.success('Settings saved');
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Failed to save');
    } finally {
      saving = false;
    }
  }

  function startPinMode(mode: 'set' | 'change' | 'clear') {
    pinMode = mode;
    pinValue = '';
    pinPassword = '';
    pinError = '';
  }

  function cancelPinMode() {
    pinMode = 'idle';
    pinValue = '';
    pinPassword = '';
    pinError = '';
  }

  function handlePinInput(e: Event) {
    const input = e.target as HTMLInputElement;
    input.value = input.value.replace(/\D/g, '').slice(0, 4);
    pinValue = input.value;
  }

  async function submitPin() {
    pinError = '';
    pinSaving = true;
    try {
      if (pinMode === 'clear') {
        await userApi.clearPin(pinPassword);
        hasPin = false;
        toast.success('PIN removed');
      } else {
        await userApi.setPin(pinValue, pinPassword);
        hasPin = true;
        toast.success(pinMode === 'set' ? 'PIN set successfully' : 'PIN changed successfully');
      }
      pinMode = 'idle';
      pinValue = '';
      pinPassword = '';
    } catch (e: unknown) {
      pinError = e instanceof Error ? e.message : 'Failed to update PIN';
    } finally {
      pinSaving = false;
    }
  }
</script>

<svelte:head><title>Settings — OnScreen</title></svelte:head>

<div class="page">
  {#if error}
    <div class="banner error">{error}</div>
  {/if}

  {#if loading}
    <div class="skeleton-block"></div>
  {:else}
    <form on:submit|preventDefault={save}>
      <section>
        <div class="sec-label">Server</div>
        <div class="hint" style="margin-top: -0.5rem;">
          Restart required after changes — these are read at process startup.
        </div>
        <div class="field">
          <label for="gen-base-url">Public URL</label>
          <input
            id="gen-base-url"
            type="text"
            bind:value={genBaseURL}
            placeholder="https://media.example.com"
            autocomplete="off"
            spellcheck="false"
          />
          <div class="hint">
            Used in OAuth/OIDC redirect URIs and LAN discovery responses.
            Leave blank to fall back to <code>http://localhost:&lt;listen-port&gt;</code>.
          </div>
        </div>
        <div class="field">
          <label for="gen-log-level">Log level</label>
          <select id="gen-log-level" bind:value={genLogLevel}>
            <option value="debug">debug</option>
            <option value="info">info</option>
            <option value="warn">warn</option>
            <option value="error">error</option>
          </select>
        </div>
        <div class="field">
          <label for="gen-cors">CORS allowed origins</label>
          <input
            id="gen-cors"
            type="text"
            bind:value={genCORS}
            placeholder="https://app.example.com, https://admin.example.com"
            autocomplete="off"
            spellcheck="false"
          />
          <div class="hint">
            Comma-separated origin list for cross-origin XHR. Use <code>*</code> to allow any
            origin (safe — the API authenticates via Bearer headers, not cookies).
            Leave blank for same-origin only.
          </div>
        </div>
      </section>

      <section>
        <div class="sec-label">Metadata</div>
        <div class="field">
          <label for="tmdb-key">TMDB API Key</label>
          <input
            id="tmdb-key"
            type="text"
            bind:value={tmdbApiKey}
            placeholder="Enter your TMDB API key"
            autocomplete="off"
            spellcheck="false"
          />
          <div class="hint">
            Used to fetch movie/show metadata and cover art.
            Get a free key at
            <a href="https://www.themoviedb.org/settings/api" target="_blank" rel="noopener">themoviedb.org</a>.
          </div>
        </div>
        <div class="field">
          <label for="tvdb-key">TheTVDB API Key</label>
          <input
            id="tvdb-key"
            type="text"
            bind:value={tvdbApiKey}
            placeholder="Enter your TheTVDB API key"
            autocomplete="off"
            spellcheck="false"
          />
          <div class="hint">
            Optional. Used as a fallback for TV episode metadata when TMDB doesn't have the right numbering (anime, etc.).
            Get a free key at
            <a href="https://thetvdb.com/dashboard/account/apikey" target="_blank" rel="noopener">thetvdb.com</a>.
          </div>
        </div>
      </section>

      <section>
        <div class="sec-label">Arr Notifications</div>
        <div class="field">
          <label for="arr-key">API Key</label>
          <input
            id="arr-key"
            type="text"
            bind:value={arrApiKey}
            placeholder="Enter an API key for Radarr/Sonarr webhooks"
            autocomplete="off"
            spellcheck="false"
          />
          <div class="hint">
            Set a shared API key to allow Radarr, Sonarr, and Lidarr to notify OnScreen when new media is imported.
            OnScreen will automatically scan the affected directory.
          </div>
        </div>
        {#if arrWebhookUrl}
          <div class="field">
            <label>Radarr Webhook URL</label>
            <div class="url-box">
              <code class="webhook-url">{arrWebhookUrl}&source=radarr</code>
              <button type="button" class="btn-copy" on:click={() => { navigator.clipboard.writeText(arrWebhookUrl + '&source=radarr'); toast.success('Copied'); }}>Copy</button>
            </div>
            <div class="hint">
              In Radarr: <strong>Settings &gt; Connect &gt; + &gt; Webhook</strong>.
              Set URL to the above, trigger on <strong>On Import</strong> and <strong>On Rename</strong>.
            </div>
          </div>
          <div class="field">
            <label>Sonarr Webhook URL</label>
            <div class="url-box">
              <code class="webhook-url">{arrWebhookUrl}&source=sonarr</code>
              <button type="button" class="btn-copy" on:click={() => { navigator.clipboard.writeText(arrWebhookUrl + '&source=sonarr'); toast.success('Copied'); }}>Copy</button>
            </div>
            <div class="hint">
              In Sonarr: <strong>Settings &gt; Connect &gt; + &gt; Webhook</strong>.
              Set URL to the above, trigger on <strong>On Import</strong> and <strong>On Rename</strong>.
            </div>
          </div>
          <div class="field">
            <label>Lidarr Webhook URL</label>
            <div class="url-box">
              <code class="webhook-url">{arrWebhookUrl}&source=lidarr</code>
              <button type="button" class="btn-copy" on:click={() => { navigator.clipboard.writeText(arrWebhookUrl + '&source=lidarr'); toast.success('Copied'); }}>Copy</button>
            </div>
            <div class="hint">
              In Lidarr: <strong>Settings &gt; Connect &gt; + &gt; Webhook</strong>.
              Set URL to the above, trigger on <strong>On Import</strong> and <strong>On Rename</strong>.
            </div>
          </div>
        {/if}
        <div class="field">
          <label>Path Mappings</label>
          <div class="hint" style="margin-bottom: 0.4rem;">
            If your arr apps run in Docker, their file paths won't match local paths.
            Map the remote prefix (e.g. <code>/Media/TV Shows</code>) to the local prefix (e.g. <code>C:\TV</code>).
          </div>
          {#each arrPathMappings as mapping, i}
            <div class="path-mapping-row">
              <input
                type="text"
                bind:value={mapping.remote}
                placeholder="/Media/TV Shows"
                autocomplete="off"
                spellcheck="false"
              />
              <span class="arrow">→</span>
              <input
                type="text"
                bind:value={mapping.local}
                placeholder="C:\TV"
                autocomplete="off"
                spellcheck="false"
              />
              <button
                type="button"
                class="btn-remove"
                on:click={() => { arrPathMappings = arrPathMappings.filter((_, j) => j !== i); }}
                title="Remove"
              >&times;</button>
            </div>
          {/each}
          <button
            type="button"
            class="btn-outline btn-add-mapping"
            on:click={() => { arrPathMappings = [...arrPathMappings, { remote: '', local: '' }]; }}
          >+ Add Mapping</button>
        </div>
      </section>

      <section>
        <div class="sec-label">OpenSubtitles</div>
        <div class="field">
          <label class="toggle-row">
            <input type="checkbox" bind:checked={osEnabled} />
            <span>Enable online subtitle search</span>
          </label>
          <div class="hint">
            Lets viewers search OpenSubtitles.com from the player subtitle picker. Requires an API key below.
          </div>
        </div>
        <div class="field">
          <label for="os-key">API Key</label>
          <input
            id="os-key"
            type="text"
            bind:value={osApiKey}
            placeholder="Enter your OpenSubtitles API key"
            autocomplete="off"
            spellcheck="false"
          />
          <div class="hint">
            Get one at
            <a href="https://www.opensubtitles.com/en/consumers" target="_blank" rel="noopener">opensubtitles.com/consumers</a>.
          </div>
        </div>
        <div class="field">
          <label for="os-user">Username (optional)</label>
          <input
            id="os-user"
            type="text"
            bind:value={osUsername}
            placeholder="Your OpenSubtitles username"
            autocomplete="off"
            spellcheck="false"
          />
        </div>
        <div class="field">
          <label for="os-pass">Password (optional)</label>
          <input
            id="os-pass"
            type="password"
            bind:value={osPassword}
            on:input={() => { osPasswordMasked = false; }}
            placeholder="Your OpenSubtitles password"
            autocomplete="new-password"
          />
          <div class="hint">
            Logging in bumps the daily download quota from 5 to 20+. Credentials are stored server-side only.
          </div>
        </div>
        <div class="field">
          <label for="os-langs">Default languages</label>
          <input
            id="os-langs"
            type="text"
            bind:value={osLanguages}
            placeholder="en,es"
            autocomplete="off"
            spellcheck="false"
          />
          <div class="hint">
            Comma-separated ISO-639-1 codes used as the default search filter. Leave blank to let viewers pick per-search.
          </div>
        </div>
      </section>

      <div class="form-foot">
        <button type="submit" class="btn-save" disabled={saving}>
          {saving ? 'Saving…' : 'Save Changes'}
        </button>
      </div>
    </form>

    <section>
      <div class="sec-label">Profile PIN</div>
      <div class="hint" style="margin-top: -0.5rem;">
        A 4-digit PIN lets other users on this server switch to your profile without needing your password.
      </div>

      {#if pinError}
        <div class="banner error">{pinError}</div>
      {/if}

      {#if pinMode === 'idle'}
        <div class="pin-actions">
          {#if hasPin}
            <span class="pin-status">PIN is set</span>
            <button class="btn-outline" on:click={() => startPinMode('change')}>Change PIN</button>
            <button class="btn-outline btn-danger" on:click={() => startPinMode('clear')}>Clear PIN</button>
          {:else}
            <span class="pin-status off">No PIN set</span>
            <button class="btn-outline" on:click={() => startPinMode('set')}>Set PIN</button>
          {/if}
        </div>
      {:else}
        <form class="pin-form" on:submit|preventDefault={submitPin}>
          {#if pinMode !== 'clear'}
            <div class="field">
              <label for="pin-input">4-digit PIN</label>
              <input
                id="pin-input"
                type="password"
                inputmode="numeric"
                pattern="\d{4}"
                maxlength="4"
                value={pinValue}
                on:input={handlePinInput}
                placeholder="0000"
                autocomplete="off"
                style="max-width: 120px; text-align: center; letter-spacing: 0.3em;"
              />
            </div>
          {/if}
          <div class="field">
            <label for="pin-password">Current password</label>
            <input
              id="pin-password"
              type="password"
              bind:value={pinPassword}
              placeholder="Confirm your password"
              autocomplete="current-password"
            />
          </div>
          <div class="pin-form-actions">
            <button
              type="submit"
              class="btn-save"
              disabled={pinSaving || (pinMode !== 'clear' && pinValue.length !== 4) || !pinPassword}
            >
              {pinSaving ? 'Saving...' : pinMode === 'clear' ? 'Remove PIN' : pinMode === 'set' ? 'Set PIN' : 'Change PIN'}
            </button>
            <button type="button" class="btn-outline" on:click={cancelPinMode}>Cancel</button>
          </div>
        </form>
      {/if}
    </section>

    <section>
      <div class="sec-label">Language Preferences</div>
      <div class="hint" style="margin-top: -0.5rem;">
        Set default audio and subtitle languages. The player will auto-select matching tracks when available.
      </div>
      <div class="field">
        <label for="pref-audio">Preferred Audio Language</label>
        <select id="pref-audio" bind:value={prefAudioLang}>
          <option value="">None (use default)</option>
          <option value="eng">English</option>
          <option value="jpn">Japanese</option>
          <option value="spa">Spanish</option>
          <option value="fra">French</option>
          <option value="deu">German</option>
          <option value="ita">Italian</option>
          <option value="por">Portuguese</option>
          <option value="rus">Russian</option>
          <option value="zho">Chinese</option>
          <option value="kor">Korean</option>
          <option value="hin">Hindi</option>
          <option value="ara">Arabic</option>
          <option value="tha">Thai</option>
          <option value="pol">Polish</option>
          <option value="nld">Dutch</option>
          <option value="swe">Swedish</option>
          <option value="nor">Norwegian</option>
          <option value="dan">Danish</option>
          <option value="fin">Finnish</option>
          <option value="tur">Turkish</option>
        </select>
      </div>
      <div class="field">
        <label for="pref-sub">Preferred Subtitle Language</label>
        <select id="pref-sub" bind:value={prefSubtitleLang}>
          <option value="">None (disabled)</option>
          <option value="eng">English</option>
          <option value="jpn">Japanese</option>
          <option value="spa">Spanish</option>
          <option value="fra">French</option>
          <option value="deu">German</option>
          <option value="ita">Italian</option>
          <option value="por">Portuguese</option>
          <option value="rus">Russian</option>
          <option value="zho">Chinese</option>
          <option value="kor">Korean</option>
          <option value="hin">Hindi</option>
          <option value="ara">Arabic</option>
          <option value="tha">Thai</option>
          <option value="pol">Polish</option>
          <option value="nld">Dutch</option>
          <option value="swe">Swedish</option>
          <option value="nor">Norwegian</option>
          <option value="dan">Danish</option>
          <option value="fin">Finnish</option>
          <option value="tur">Turkish</option>
        </select>
      </div>
      <div class="pref-foot">
        <button class="btn-save" disabled={prefSaving} on:click={savePreferences}>
          {prefSaving ? 'Saving...' : 'Save Preferences'}
        </button>
      </div>
    </section>

  {/if}
</div>

<style>
  .page { max-width: 520px; }

  .banner {
    padding: 0.6rem 0.9rem;
    border-radius: 8px;
    font-size: 0.8rem;
    margin-bottom: 1.25rem;
  }
  .banner.error { background: var(--error-bg); border: 1px solid var(--error-bg); color: var(--error); }
  .banner.ok    { background: var(--success-bg);  border: 1px solid var(--success-bg);  color: var(--success); }

  .skeleton-block {
    height: 100px; border-radius: 10px;
    background: linear-gradient(90deg, var(--bg-elevated) 25%, #16161f 50%, var(--bg-elevated) 75%);
    background-size: 200% 100%;
    animation: shimmer 1.4s infinite;
  }
  @keyframes shimmer { 0% { background-position: 200% 0; } 100% { background-position: -200% 0; } }

  form { display: flex; flex-direction: column; }

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

  .toggle-row {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    font-size: 0.85rem;
    font-weight: 500;
    color: var(--text-primary);
    cursor: pointer;
  }
  .toggle-row input[type="checkbox"] {
    width: auto;
    margin: 0;
    accent-color: var(--accent);
  }

  label { font-size: 0.75rem; font-weight: 500; color: var(--text-muted); }

  input, select {
    background: var(--bg-hover);
    border: 1px solid var(--border-strong);
    border-radius: 7px;
    padding: 0.48rem 0.7rem;
    font-size: 0.85rem;
    color: var(--text-primary);
    transition: border-color 0.15s;
    width: 100%;
  }
  input {
    font-family: monospace;
  }
  input:focus { outline: none; border-color: var(--accent); box-shadow: 0 0 0 3px var(--accent-bg); }
  ::placeholder { color: var(--text-muted); }

  .hint {
    font-size: 0.72rem;
    color: var(--text-muted);
    line-height: 1.5;
    margin-top: 0.15rem;
  }
  .hint a { color: var(--text-muted); text-decoration: underline; }
  .hint a:hover { color: var(--accent-text); }

  .form-foot {
    display: flex; justify-content: flex-end;
    padding-top: 1.5rem;
  }
  .btn-save {
    padding: 0.42rem 0.9rem; background: var(--accent);
    border: none; border-radius: 7px; color: #fff;
    font-size: 0.8rem; font-weight: 600; cursor: pointer; transition: background 0.15s;
  }
  .btn-save:hover { background: var(--accent-hover); }
  .btn-save:disabled { opacity: 0.5; cursor: not-allowed; }

  select { cursor: pointer; }
  select:focus { outline: none; border-color: var(--accent); box-shadow: 0 0 0 3px var(--accent-bg); }
  select option { background: var(--bg-elevated); color: var(--text-primary); }

  .pref-foot { display: flex; justify-content: flex-end; }

  /* Arr webhook URL */
  .url-box {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    background: var(--input-bg);
    border: 1px solid var(--border);
    border-radius: 7px;
    padding: 0.4rem 0.6rem;
  }
  .webhook-url {
    flex: 1;
    font-size: 0.75rem;
    color: var(--text-secondary);
    word-break: break-all;
    background: none;
    padding: 0;
    border-radius: 0;
  }
  .btn-copy {
    flex-shrink: 0;
    padding: 0.3rem 0.6rem;
    background: var(--border);
    border: 1px solid var(--border-strong);
    border-radius: 5px;
    color: var(--text-secondary);
    font-size: 0.72rem;
    font-weight: 500;
    cursor: pointer;
    transition: background 0.12s;
  }
  .btn-copy:hover { background: var(--border-strong); }

  /* Path mappings */
  .path-mapping-row {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    margin-bottom: 0.4rem;
  }
  .path-mapping-row input { flex: 1; min-width: 0; }
  .arrow { color: var(--text-muted); font-size: 0.85rem; flex-shrink: 0; }
  .btn-remove {
    flex-shrink: 0;
    width: 26px; height: 26px;
    display: flex; align-items: center; justify-content: center;
    background: rgba(248,113,113,0.08);
    border: 1px solid var(--error-bg);
    border-radius: 5px;
    color: var(--error);
    font-size: 1rem;
    cursor: pointer;
    transition: background 0.12s;
    line-height: 1;
  }
  .btn-remove:hover { background: rgba(248,113,113,0.15); }
  .btn-add-mapping {
    align-self: flex-start;
    font-size: 0.72rem;
    padding: 0.28rem 0.6rem;
    margin-top: 0.2rem;
  }

  code {
    background: var(--border);
    padding: 0.1rem 0.35rem;
    border-radius: 4px;
    font-size: 0.72rem;
    color: var(--text-secondary);
  }

  /* PIN management */
  .pin-actions {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    flex-wrap: wrap;
  }
  .pin-status {
    font-size: 0.78rem;
    color: var(--success);
    font-weight: 500;
    margin-right: 0.3rem;
  }
  .pin-status.off { color: var(--text-muted); }
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
  .btn-outline.btn-danger { color: var(--error); border-color: rgba(248,113,113,0.25); }
  .btn-outline.btn-danger:hover { background: rgba(248,113,113,0.08); }
  .pin-form {
    display: flex;
    flex-direction: column;
    gap: 0.9rem;
  }
  .pin-form-actions {
    display: flex;
    gap: 0.5rem;
  }

  /* ── Mobile ────────────────────────────────────────────────────────────── */
  @media (max-width: 768px) {
    .page { max-width: 100%; }

    input { width: 100%; }

    .pin-actions { flex-direction: column; align-items: flex-start; gap: 0.5rem; }
    .pin-form-actions { flex-wrap: wrap; }
  }
</style>
