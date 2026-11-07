<script lang="ts">
  import { onMount } from 'svelte';
  import { settingsApi, subtitleAutoApi, type HearingImpairedPref } from '$lib/api';
  import { toast } from '$lib/stores/toast';
  import LanguagePrefs from '$lib/components/LanguagePrefs.svelte';
  import { t, i18n } from '$lib/i18n/index.svelte';

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
      error = e instanceof Error ? e.message : t('settings.main.loadFailed');
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
      toast.success(t('settings.main.saved'));
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : t('settings.main.saveFailed'));
    } finally {
      saving = false;
    }
  }

</script>

<svelte:head><title>{$i18n('settings.main.pageTitle')}</title></svelte:head>

<div class="page">
  {#if error}
    <div class="banner error">{error}</div>
  {/if}

  {#if loading}
    <div class="skeleton-block"></div>
  {:else}
    <form on:submit|preventDefault={save}>
      <section>
        <div class="sec-label">{$i18n('settings.main.sharing')}</div>
        <div class="field toggle-field">
          <label class="toggle-label">
            <input type="checkbox" bind:checked={webDownloadsEnabled} />
            <span>{$i18n('settings.main.allowDownloads')}</span>
          </label>
          <div class="hint">
            {$i18n('settings.main.allowDownloadsHint')}
          </div>
        </div>
      </section>

      <section>
        <div class="sec-label">{$i18n('settings.main.server')}</div>
        <div class="hint" style="margin-top: -0.5rem;">
          {$i18n('settings.main.restartRequired')}
        </div>
        <div class="field">
          <label for="gen-base-url">{$i18n('settings.main.publicUrl')}</label>
          <input
            id="gen-base-url"
            type="text"
            bind:value={genBaseURL}
            placeholder="https://media.example.com"
            autocomplete="off"
            spellcheck="false"
          />
          <div class="hint">
            {#each $i18n.rich('settings.main.publicUrlHint', { fallback: 'http://localhost:<listen-port>' }) as seg}{#if seg.param}<code>{seg.text}</code>{:else}{seg.text}{/if}{/each}
          </div>
        </div>
        <div class="field">
          <label for="gen-log-level">{$i18n('settings.main.logLevel')}</label>
          <select id="gen-log-level" bind:value={genLogLevel}>
            <option value="debug">debug</option>
            <option value="info">info</option>
            <option value="warn">warn</option>
            <option value="error">error</option>
          </select>
        </div>
        <div class="field">
          <label for="gen-cors">{$i18n('settings.main.cors')}</label>
          <input
            id="gen-cors"
            type="text"
            bind:value={genCORS}
            placeholder="https://app.example.com, https://admin.example.com"
            autocomplete="off"
            spellcheck="false"
          />
          <div class="hint">
            {#each $i18n.rich('settings.main.corsHint', { star: '*' }) as seg}{#if seg.param}<code>{seg.text}</code>{:else}{seg.text}{/if}{/each}
          </div>
        </div>
      </section>

      <section>
        <div class="sec-label">{$i18n('settings.main.metadata')}</div>
        <div class="field">
          <label for="tmdb-key">{$i18n('settings.main.tmdbKey')}</label>
          <input
            id="tmdb-key"
            type="text"
            bind:value={tmdbApiKey}
            placeholder={$i18n('settings.main.tmdbPlaceholder')}
            autocomplete="off"
            spellcheck="false"
          />
          <div class="hint">
            {#each $i18n.rich('settings.main.tmdbHint', { link: 'themoviedb.org' }) as seg}{#if seg.param}<a href="https://www.themoviedb.org/settings/api" target="_blank" rel="noopener">{seg.text}</a>{:else}{seg.text}{/if}{/each}
          </div>
        </div>
        <div class="field">
          <label for="tvdb-key">{$i18n('settings.main.tvdbKey')}</label>
          <input
            id="tvdb-key"
            type="text"
            bind:value={tvdbApiKey}
            placeholder={$i18n('settings.main.tvdbPlaceholder')}
            autocomplete="off"
            spellcheck="false"
          />
          <div class="hint">
            {#each $i18n.rich('settings.main.tvdbHint', { link: 'thetvdb.com' }) as seg}{#if seg.param}<a href="https://thetvdb.com/dashboard/account/apikey" target="_blank" rel="noopener">{seg.text}</a>{:else}{seg.text}{/if}{/each}
          </div>
        </div>
        <div class="field">
          <label for="omdb-key">{$i18n('settings.main.omdbKey')}</label>
          <input
            id="omdb-key"
            type="text"
            bind:value={omdbApiKey}
            placeholder={$i18n('settings.main.omdbPlaceholder')}
            autocomplete="off"
            spellcheck="false"
          />
          <div class="hint">
            {#each $i18n.rich('settings.main.omdbHint', { link: 'omdbapi.com' }) as seg}{#if seg.param}<a href="https://www.omdbapi.com/apikey.aspx" target="_blank" rel="noopener">{seg.text}</a>{:else}{seg.text}{/if}{/each}
          </div>
        </div>
        <div class="field">
          <label for="omdb-limit">{$i18n('settings.main.omdbLimit')}</label>
          <input id="omdb-limit" type="number" min="1" max="1000000" step="1" bind:value={omdbDailyLimit} />
          <div class="hint">
            {$i18n('settings.main.omdbLimitHint', { free: 1000 })}
          </div>
        </div>
        <div class="field">
          <label for="fanart-key">{$i18n('settings.main.fanartKey')}</label>
          <input
            id="fanart-key"
            type="text"
            bind:value={fanartApiKey}
            placeholder={$i18n('settings.main.fanartPlaceholder')}
            autocomplete="off"
            spellcheck="false"
          />
          <div class="hint">
            {#each $i18n.rich('settings.main.fanartHint', { link: 'fanart.tv' }) as seg}{#if seg.param}<a href="https://fanart.tv/get-an-api-key/" target="_blank" rel="noopener">{seg.text}</a>{:else}{seg.text}{/if}{/each}
          </div>
        </div>
      </section>

      <section>
        <div class="sec-label">{$i18n('settings.main.arrNotifications')}</div>
        <div class="field">
          <label for="arr-key">{$i18n('settings.main.apiKey')}</label>
          <input
            id="arr-key"
            type="text"
            bind:value={arrApiKey}
            placeholder={$i18n('settings.main.arrKeyPlaceholder')}
            autocomplete="off"
            spellcheck="false"
          />
          <div class="hint">
            {$i18n('settings.main.arrKeyHint')}
          </div>
        </div>
        {#if arrWebhookUrl}
          <div class="field">
            <span class="field-heading">{$i18n('settings.main.webhookUrl', { service: 'Radarr' })}</span>
            <div class="url-box">
              <code class="webhook-url">{arrWebhookUrl}?source=radarr</code>
              <button type="button" class="btn-copy" on:click={() => { navigator.clipboard.writeText(arrWebhookUrl + '?source=radarr'); toast.success(t('settings.main.copied')); }}>{$i18n('settings.main.copy')}</button>
            </div>
            <div class="hint">
              {#each $i18n.rich('settings.main.arrHowTo', { service: 'Radarr', menu: 'Settings > Connect > + > Webhook', onImport: 'On Import', onRename: 'On Rename', header: 'X-Api-Key' }) as seg}{#if seg.param === 'header'}<code>{seg.text}</code>{:else if seg.param && seg.param !== 'service'}<strong>{seg.text}</strong>{:else}{seg.text}{/if}{/each}
            </div>
          </div>
          <div class="field">
            <span class="field-heading">{$i18n('settings.main.webhookUrl', { service: 'Sonarr' })}</span>
            <div class="url-box">
              <code class="webhook-url">{arrWebhookUrl}?source=sonarr</code>
              <button type="button" class="btn-copy" on:click={() => { navigator.clipboard.writeText(arrWebhookUrl + '?source=sonarr'); toast.success(t('settings.main.copied')); }}>{$i18n('settings.main.copy')}</button>
            </div>
            <div class="hint">
              {#each $i18n.rich('settings.main.arrHowTo', { service: 'Sonarr', menu: 'Settings > Connect > + > Webhook', onImport: 'On Import', onRename: 'On Rename', header: 'X-Api-Key' }) as seg}{#if seg.param === 'header'}<code>{seg.text}</code>{:else if seg.param && seg.param !== 'service'}<strong>{seg.text}</strong>{:else}{seg.text}{/if}{/each}
            </div>
          </div>
          <div class="field">
            <span class="field-heading">{$i18n('settings.main.webhookUrl', { service: 'Lidarr' })}</span>
            <div class="url-box">
              <code class="webhook-url">{arrWebhookUrl}?source=lidarr</code>
              <button type="button" class="btn-copy" on:click={() => { navigator.clipboard.writeText(arrWebhookUrl + '?source=lidarr'); toast.success(t('settings.main.copied')); }}>{$i18n('settings.main.copy')}</button>
            </div>
            <div class="hint">
              {#each $i18n.rich('settings.main.arrHowTo', { service: 'Lidarr', menu: 'Settings > Connect > + > Webhook', onImport: 'On Import', onRename: 'On Rename', header: 'X-Api-Key' }) as seg}{#if seg.param === 'header'}<code>{seg.text}</code>{:else if seg.param && seg.param !== 'service'}<strong>{seg.text}</strong>{:else}{seg.text}{/if}{/each}
            </div>
          </div>
        {/if}
        <div class="field">
          <span class="field-heading">{$i18n('settings.main.pathMappings')}</span>
          <div class="hint" style="margin-bottom: 0.4rem;">
            {#each $i18n.rich('settings.main.pathMappingsHint', { remote: '/Media/TV Shows', local: 'C:\\TV' }) as seg}{#if seg.param}<code>{seg.text}</code>{:else}{seg.text}{/if}{/each}
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
                title={$i18n('settings.main.remove')}
              >&times;</button>
            </div>
          {/each}
          <button
            type="button"
            class="btn-outline btn-add-mapping"
            on:click={() => { arrPathMappings = [...arrPathMappings, { remote: '', local: '' }]; }}
          >{$i18n('settings.main.addMapping')}</button>
        </div>
      </section>

      <section>
        <div class="sec-label">OpenSubtitles</div>
        <div class="field">
          <label class="toggle-row">
            <input type="checkbox" bind:checked={osEnabled} />
            <span>{$i18n('settings.main.osEnable')}</span>
          </label>
          <div class="hint">
            {$i18n('settings.main.osEnableHint')}
          </div>
        </div>
        <div class="field">
          <label for="os-key">{$i18n('settings.main.apiKey')}</label>
          <input
            id="os-key"
            type="text"
            bind:value={osApiKey}
            placeholder={$i18n('settings.main.osKeyPlaceholder')}
            autocomplete="off"
            spellcheck="false"
          />
          <div class="hint">
            {#each $i18n.rich('settings.main.osKeyHint', { link: 'opensubtitles.com/consumers' }) as seg}{#if seg.param}<a href="https://www.opensubtitles.com/en/consumers" target="_blank" rel="noopener">{seg.text}</a>{:else}{seg.text}{/if}{/each}
          </div>
        </div>
        <div class="field">
          <label for="os-user">{$i18n('settings.main.osUser')}</label>
          <input
            id="os-user"
            type="text"
            bind:value={osUsername}
            placeholder={$i18n('settings.main.osUserPlaceholder')}
            autocomplete="off"
            spellcheck="false"
          />
        </div>
        <div class="field">
          <label for="os-pass">{$i18n('settings.main.osPass')}</label>
          <input
            id="os-pass"
            type="password"
            bind:value={osPassword}
            on:input={() => { osPasswordMasked = false; }}
            placeholder={$i18n('settings.main.osPassPlaceholder')}
            autocomplete="new-password"
          />
          <div class="hint">
            {$i18n('settings.main.osPassHint')}
          </div>
        </div>
        <div class="field">
          <label for="os-langs">{$i18n('settings.main.osLangs')}</label>
          <input
            id="os-langs"
            type="text"
            bind:value={osLanguages}
            placeholder="en,es"
            autocomplete="off"
            spellcheck="false"
          />
          <div class="hint">
            {$i18n('settings.main.osLangsHint')}
          </div>
        </div>
      </section>

      {#if subAutoLoaded}
        <section>
          <div class="sec-label">{$i18n('settings.main.subAuto')}</div>
          <div class="field">
            <label class="toggle-row">
              <input type="checkbox" bind:checked={subAutoEnabled} />
              <span>{$i18n('settings.main.subAutoEnable')}</span>
            </label>
            <div class="hint">
              {$i18n('settings.main.subAutoHint')}
            </div>
          </div>
          <div class="field">
            <label for="sub-auto-langs">{$i18n('settings.main.languages')}</label>
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
              <span>{$i18n('settings.main.subAutoHash')}</span>
            </label>
          </div>
          <div class="field">
            <label for="sub-auto-hi">{$i18n('settings.main.subAutoHI')}</label>
            <select id="sub-auto-hi" value={subAutoHI}
                    on:change={(e) => subAutoHI = e.currentTarget.value as HearingImpairedPref}>
              <option value="any">{$i18n('settings.main.hiAny')}</option>
              <option value="prefer">{$i18n('settings.main.hiPrefer')}</option>
              <option value="avoid">{$i18n('settings.main.hiAvoid')}</option>
            </select>
          </div>
        </section>
      {/if}

      <section>
        <div class="sec-label">{$i18n('settings.main.scrobbling')}</div>
        <div class="hint" style="margin-top: -0.5rem;">
          {#each $i18n.rich('settings.main.scrobblingHint', { link: $i18n('settings.main.scrobblingLink') }) as seg}{#if seg.param}<a href="/account/scrobble">{seg.text}</a>{:else}{seg.text}{/if}{/each}
        </div>
        <div class="field">
          <label for="lfm-key">{$i18n('settings.main.lfmKey')}</label>
          <input
            id="lfm-key"
            type="text"
            bind:value={lastfmApiKey}
            placeholder={$i18n('settings.main.lfmKey')}
            autocomplete="off"
            spellcheck="false"
          />
        </div>
        <div class="field">
          <label for="lfm-secret">{$i18n('settings.main.lfmSecret')}</label>
          <input
            id="lfm-secret"
            type="password"
            bind:value={lastfmSecret}
            placeholder={$i18n('settings.main.lfmSecret')}
            autocomplete="new-password"
          />
          <div class="hint">
            {#each $i18n.rich('settings.main.lfmHint', { link: 'last.fm/api/account/create' }) as seg}{#if seg.param}<a href="https://www.last.fm/api/account/create" target="_blank" rel="noopener">{seg.text}</a>{:else}{seg.text}{/if}{/each}
          </div>
        </div>
        <div class="field">
          <label for="trakt-id">{$i18n('settings.main.traktId')}</label>
          <input
            id="trakt-id"
            type="text"
            bind:value={traktClientId}
            placeholder={$i18n('settings.main.traktId')}
            autocomplete="off"
            spellcheck="false"
          />
        </div>
        <div class="field">
          <label for="trakt-secret">{$i18n('settings.main.traktSecret')}</label>
          <input
            id="trakt-secret"
            type="password"
            bind:value={traktSecret}
            placeholder={$i18n('settings.main.traktSecret')}
            autocomplete="new-password"
          />
          <div class="hint">
            {#each $i18n.rich('settings.main.traktHint', { link: 'trakt.tv/oauth/applications', uri: 'urn:ietf:wg:oauth:2.0:oob' }) as seg}{#if seg.param === 'link'}<a href="https://trakt.tv/oauth/applications/new" target="_blank" rel="noopener">{seg.text}</a>{:else if seg.param}<code>{seg.text}</code>{:else}{seg.text}{/if}{/each}
          </div>
        </div>
      </section>

      <div class="form-foot">
        <button type="submit" class="btn-save" disabled={saving}>
          {saving ? $i18n('settings.main.saving') : $i18n('settings.main.save')}
        </button>
      </div>
    </form>

    <section>
      <div class="sec-label">{$i18n('settings.main.langPrefs')}</div>
      <div class="hint" style="margin-top: -0.5rem;">
        {$i18n('settings.main.langPrefsHint')}
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
