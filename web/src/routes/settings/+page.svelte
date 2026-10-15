<script lang="ts">
  import { onMount } from 'svelte';
  import { settingsApi, subtitleAutoApi, type HearingImpairedPref } from '$lib/api';
  import { toast } from '$lib/stores/toast';
  import LanguagePrefs from '$lib/components/LanguagePrefs.svelte';

  let loading = true;
  let saving = false;
  let error = '';

  let tmdbApiKey = '';
  let tvdbApiKey = '';
  let omdbApiKey = '';
  let fanartApiKey = '';
  let omdbDailyLimit = 1000;
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

  // Automatic subtitle download (the "Download missing subtitles" task). Off
  // by default; libraries can override it on their own settings page.
  // subAutoLoaded stays false when the server didn't answer, so save()
  // doesn't overwrite a setting it never read.
  let subAutoLoaded = false;
  let subAutoEnabled = false;
  let subAutoLangs = 'en';
  let subAutoHash = true;
  let subAutoHI: HearingImpairedPref = 'any';

  // Scrobbling apps — the operator's Last.fm API account and Trakt app that
  // users' links run under. The secrets arrive as "****" when set and go back
  // unchanged, which the server reads as "keep".
  let lastfmApiKey = '';
  let lastfmSecret = '';
  let traktClientId = '';
  let traktSecret = '';

  // General server config (BaseURL, LogLevel, CORS) — restart-required.
  let genBaseURL = '';
  let genLogLevel = 'info';
  let genCORS = ''; // comma-separated

  // Web download toggle. Default off — operator opts in to letting users
  // pull the raw file off disk via the watch-page button. The capabilities
  // feed mirrors this so the button hides on the client too.
  let webDownloadsEnabled = false;


  onMount(async () => {
    try {
      const s = await settingsApi.get();
      tmdbApiKey = s.tmdb_api_key ?? '';
      tvdbApiKey = s.tvdb_api_key ?? '';
      omdbApiKey = s.omdb_api_key ?? '';
      fanartApiKey = s.fanarttv_api_key ?? '';
      omdbDailyLimit = s.omdb_daily_limit ?? 1000;
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
      lastfmApiKey = s.lastfm?.api_key ?? '';
      lastfmSecret = s.lastfm?.shared_secret ?? '';
      traktClientId = s.trakt?.client_id ?? '';
      traktSecret = s.trakt?.client_secret ?? '';
      const gen = s.general;
      if (gen) {
        genBaseURL = gen.base_url ?? '';
        genLogLevel = gen.log_level || 'info';
        genCORS = (gen.cors_allowed_origins ?? []).join(', ');
      }
      webDownloadsEnabled = !!s.web_downloads_enabled;
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed to load settings';
    } finally {
      loading = false;
    }
    try {
      const sa = await subtitleAutoApi.get();
      subAutoEnabled = !!sa.enabled;
      subAutoLangs = sa.languages ?? '';
      subAutoHash = !!sa.prefer_hash_match;
      subAutoHI = sa.hearing_impaired ?? 'any';
      subAutoLoaded = true;
    } catch { /* section stays hidden */ }
  });


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
        omdb_api_key: omdbApiKey.trim(),
        fanarttv_api_key: fanartApiKey.trim(),
        omdb_daily_limit: Math.min(Math.max(Math.round(Number(omdbDailyLimit) || 1000), 1), 1000000),
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
        lastfm: { api_key: lastfmApiKey.trim(), shared_secret: lastfmSecret.trim() },
        trakt: { client_id: traktClientId.trim(), client_secret: traktSecret.trim() },
        general: {
          base_url: genBaseURL.trim(),
          log_level: genLogLevel,
          cors_allowed_origins: genCORS
            .split(',')
            .map((s) => s.trim())
            .filter(Boolean),
        },
        web_downloads_enabled: webDownloadsEnabled,
      });
      if (subAutoLoaded) {
        const sa = await subtitleAutoApi.update({
          enabled: subAutoEnabled,
          languages: subAutoLangs.trim(),
          prefer_hash_match: subAutoHash,
          hearing_impaired: subAutoHI,
        });
        subAutoLangs = sa?.languages ?? subAutoLangs;
      }
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
        <div class="sec-label">Sharing</div>
        <div class="field toggle-field">
          <label class="toggle-label">
            <input type="checkbox" bind:checked={webDownloadsEnabled} />
            <span>Allow web downloads</span>
          </label>
          <div class="hint">
            Show a "Download" button on the watch page so users can save the
            original media file to disk. Off by default — turn on for personal
            servers where you trust every account; leave off for shared servers
            where users should stream only.
          </div>
        </div>
      </section>

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
            Leave blank for same-origin only. The TV and desktop apps are always
            allowed; they don't need to be listed.
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
        <div class="field">
          <label for="omdb-key">OMDb API Key</label>
          <input
            id="omdb-key"
            type="text"
            bind:value={omdbApiKey}
            placeholder="Enter your OMDb API key"
            autocomplete="off"
            spellcheck="false"
          />
          <div class="hint">
            Optional. Adds IMDb, Rotten Tomatoes and Metacritic scores to movies and shows, fetched by an hourly task
            (Settings ▸ Library ▸ Tasks runs it on demand). Get a free key at
            <a href="https://www.omdbapi.com/apikey.aspx" target="_blank" rel="noopener">omdbapi.com</a>.
          </div>
        </div>
        <div class="field">
          <label for="omdb-limit">OMDb Daily Lookups</label>
          <input id="omdb-limit" type="number" min="1" max="1000000" step="1" bind:value={omdbDailyLimit} />
          <div class="hint">
            How many lookups the key allows a day: 1,000 for a free key, more for a paid one. Lower it if another app
            uses the same key. Lookups never go over this in any 24 hours, so a large library fills in over a few days.
          </div>
        </div>
        <div class="field">
          <label for="fanart-key">fanart.tv API Key</label>
          <input
            id="fanart-key"
            type="text"
            bind:value={fanartApiKey}
            placeholder="Enter your fanart.tv API key"
            autocomplete="off"
            spellcheck="false"
          />
          <div class="hint">
            Optional. Adds clear logos, banners and disc art to movies, shows and music when they're enriched or
            refreshed. Art already next to your media (clearlogo.png, banner.jpg, disc.png) is used first. Get a free
            personal key at
            <a href="https://fanart.tv/get-an-api-key/" target="_blank" rel="noopener">fanart.tv</a>.
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
            <span class="field-heading">Radarr Webhook URL</span>
            <div class="url-box">
              <code class="webhook-url">{arrWebhookUrl}?source=radarr</code>
              <button type="button" class="btn-copy" on:click={() => { navigator.clipboard.writeText(arrWebhookUrl + '?source=radarr'); toast.success('Copied'); }}>Copy</button>
            </div>
            <div class="hint">
              In Radarr: <strong>Settings &gt; Connect &gt; + &gt; Webhook</strong>.
              Set the URL above, trigger on <strong>On Import</strong> and <strong>On Rename</strong>,
              and add a custom header <code>X-Api-Key</code> with your API key.
            </div>
          </div>
          <div class="field">
            <span class="field-heading">Sonarr Webhook URL</span>
            <div class="url-box">
              <code class="webhook-url">{arrWebhookUrl}?source=sonarr</code>
              <button type="button" class="btn-copy" on:click={() => { navigator.clipboard.writeText(arrWebhookUrl + '?source=sonarr'); toast.success('Copied'); }}>Copy</button>
            </div>
            <div class="hint">
              In Sonarr: <strong>Settings &gt; Connect &gt; + &gt; Webhook</strong>.
              Set the URL above, trigger on <strong>On Import</strong> and <strong>On Rename</strong>,
              and add a custom header <code>X-Api-Key</code> with your API key.
            </div>
          </div>
          <div class="field">
            <span class="field-heading">Lidarr Webhook URL</span>
            <div class="url-box">
              <code class="webhook-url">{arrWebhookUrl}?source=lidarr</code>
              <button type="button" class="btn-copy" on:click={() => { navigator.clipboard.writeText(arrWebhookUrl + '?source=lidarr'); toast.success('Copied'); }}>Copy</button>
            </div>
            <div class="hint">
              In Lidarr: <strong>Settings &gt; Connect &gt; + &gt; Webhook</strong>.
              Set the URL above, trigger on <strong>On Import</strong> and <strong>On Rename</strong>,
              and add a custom header <code>X-Api-Key</code> with your API key.
            </div>
          </div>
        {/if}
        <div class="field">
          <span class="field-heading">Path Mappings</span>
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

      {#if subAutoLoaded}
        <section>
          <div class="sec-label">Automatic subtitle download</div>
          <div class="field">
            <label class="toggle-row">
              <input type="checkbox" bind:checked={subAutoEnabled} />
              <span>Download missing subtitles for new movies and episodes</span>
            </label>
            <div class="hint">
              The "Download missing subtitles" task (Settings › Tasks) searches OpenSubtitles for each
              language below that a file has no subtitle in yet — embedded, a file next to it, or an earlier
              download — and saves the best match. Needs OpenSubtitles set up above; the daily download quota
              is shared with viewers' own downloads. Libraries can turn it on or off for themselves.
            </div>
          </div>
          <div class="field">
            <label for="sub-auto-langs">Languages</label>
            <input
              id="sub-auto-langs"
              type="text"
              bind:value={subAutoLangs}
              placeholder="en,es"
              autocomplete="off"
              spellcheck="false"
            />
          </div>
          <div class="field">
            <label class="toggle-row">
              <input type="checkbox" bind:checked={subAutoHash} />
              <span>Prefer subtitles made for the exact file (hash match)</span>
            </label>
          </div>
          <div class="field">
            <label for="sub-auto-hi">Hearing-impaired (SDH) subtitles</label>
            <select id="sub-auto-hi" value={subAutoHI}
                    on:change={(e) => subAutoHI = e.currentTarget.value as HearingImpairedPref}>
              <option value="any">No preference</option>
              <option value="prefer">Prefer</option>
              <option value="avoid">Avoid</option>
            </select>
          </div>
        </section>
      {/if}

      <section>
        <div class="sec-label">Scrobbling apps</div>
        <div class="hint" style="margin-top: -0.5rem;">
          Users link their own Last.fm and Trakt accounts under <a href="/account/scrobble">Account › Scrobbling</a>, and those links run
          under the credentials here. Replacing an app later breaks existing links: each user connects again.
        </div>
        <div class="field">
          <label for="lfm-key">Last.fm API key</label>
          <input
            id="lfm-key"
            type="text"
            bind:value={lastfmApiKey}
            placeholder="Last.fm API key"
            autocomplete="off"
            spellcheck="false"
          />
        </div>
        <div class="field">
          <label for="lfm-secret">Last.fm shared secret</label>
          <input
            id="lfm-secret"
            type="password"
            bind:value={lastfmSecret}
            placeholder="Last.fm shared secret"
            autocomplete="new-password"
          />
          <div class="hint">
            Create an API account at
            <a href="https://www.last.fm/api/account/create" target="_blank" rel="noopener">last.fm/api/account/create</a>.
            No callback URL is needed.
          </div>
        </div>
        <div class="field">
          <label for="trakt-id">Trakt client ID</label>
          <input
            id="trakt-id"
            type="text"
            bind:value={traktClientId}
            placeholder="Trakt client ID"
            autocomplete="off"
            spellcheck="false"
          />
        </div>
        <div class="field">
          <label for="trakt-secret">Trakt client secret</label>
          <input
            id="trakt-secret"
            type="password"
            bind:value={traktSecret}
            placeholder="Trakt client secret"
            autocomplete="new-password"
          />
          <div class="hint">
            Create an app at
            <a href="https://trakt.tv/oauth/applications/new" target="_blank" rel="noopener">trakt.tv/oauth/applications</a>
            with the redirect URI <code>urn:ietf:wg:oauth:2.0:oob</code> (users link with a code, not a redirect).
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
      <div class="sec-label">Language Preferences</div>
      <div class="hint" style="margin-top: -0.5rem;">
        Set default audio and subtitle languages. The player will auto-select matching tracks when available.
      </div>
      <LanguagePrefs />
    </section>

  {/if}
</div>

<style>
  .page { max-width: 520px; }

  .banner {
    padding: 0.6rem 0.9rem;
    border-radius: 10px;
    font-size: 0.8rem;
    margin-bottom: 1.25rem;
  }
  .banner.error { background: var(--error-bg); border: 1px solid var(--error); color: var(--error); }
  .banner.ok    { background: var(--success-bg);  border: 1px solid var(--success);  color: var(--success); }

  .skeleton-block {
    height: 120px; border-radius: 10px;
    background: linear-gradient(90deg, var(--bg-elevated) 25%, var(--bg-hover) 50%, var(--bg-elevated) 75%);
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

  label, .field-heading { font-size: 0.72rem; font-weight: 500; color: var(--text-muted); }
  /* .field-heading is for visual labels above read-only display
     blocks (the {service} webhook URL boxes) — they're not labelling
     a single form control, so a bare <label> would be a11y noise. */

  input, select {
    background: var(--input-bg);
    border: 1px solid var(--border-strong);
    border-radius: 7px;
    padding: 0.48rem 0.7rem;
    font-size: 0.85rem;
    color: var(--text-primary);
    font-family: inherit;
    transition: border-color 0.15s;
    width: 100%;
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
    padding: 0.45rem 0.9rem; background: var(--accent);
    border: none; border-radius: 7px; color: #fff;
    font-size: 0.8rem; font-weight: 600; cursor: pointer; transition: background 0.15s;
  }
  .btn-save:hover { background: var(--accent-hover); }
  .btn-save:disabled { opacity: 0.5; cursor: not-allowed; }

  select { cursor: pointer; }
  select:focus { outline: none; border-color: var(--accent); box-shadow: 0 0 0 3px var(--accent-bg); }
  select option { background: var(--bg-elevated); color: var(--text-primary); }


  .toggle-field { display: flex; flex-direction: column; gap: 0.4rem; }
  .toggle-label {
    display: flex; align-items: center; gap: 0.6rem;
    cursor: pointer; user-select: none;
    font-size: 0.85rem; color: var(--text-primary);
  }
  .toggle-label input[type="checkbox"] {
    width: 1rem; height: 1rem;
    accent-color: var(--accent);
    cursor: pointer;
  }

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
    background: var(--bg-hover);
    border: 1px solid var(--border-strong);
    border-radius: 7px;
    color: var(--text-secondary);
    font-size: 0.72rem;
    font-weight: 500;
    cursor: pointer;
    transition: background 0.12s;
  }
  .btn-copy:hover { background: var(--bg-hover); border-color: var(--text-muted); }

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
    background: var(--error-bg);
    border: 1px solid var(--error);
    border-radius: 7px;
    color: var(--error);
    font-size: 1rem;
    cursor: pointer;
    transition: background 0.12s;
    line-height: 1;
  }
  .btn-remove:hover { background: var(--error-bg); border-color: var(--error); }
  .btn-add-mapping {
    align-self: flex-start;
    font-size: 0.72rem;
    padding: 0.28rem 0.6rem;
    margin-top: 0.2rem;
  }

  code {
    background: var(--input-bg);
    padding: 0.1rem 0.35rem;
    border-radius: 4px;
    font-size: 0.72rem;
    color: var(--text-secondary);
  }

  .btn-outline {
    padding: 0.45rem 0.9rem;
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

  /* ── Mobile ────────────────────────────────────────────────────────────── */
  @media (max-width: 768px) {
    .page { max-width: 100%; }

    input { width: 100%; }

  }
</style>
