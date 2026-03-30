<script lang="ts">
  import { onMount } from 'svelte';
  import { settingsApi, userApi, emailApi, api } from '$lib/api';
  import type { UserMeta } from '$lib/api';
  import { toast } from '$lib/stores/toast';

  let loading = true;
  let saving = false;
  let error = '';

  let tmdbApiKey = '';
  let tvdbApiKey = '';
  let arrApiKey = '';
  let arrWebhookUrl = '';
  let arrPathMappings: Array<{ remote: string; local: string }> = [];

  // Email test state
  let emailEnabled = false;
  let testEmail = '';
  let sendingTest = false;
  let testResult = '';
  let testError = '';

  // PIN management state
  let hasPin = false;
  let pinMode: 'idle' | 'set' | 'change' | 'clear' = 'idle';
  let pinValue = '';
  let pinPassword = '';
  let pinSaving = false;
  let pinError = '';

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
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed to load settings';
    } finally {
      loading = false;
    }
    // Check email status
    try {
      const e = await emailApi.enabled();
      emailEnabled = e.enabled;
    } catch { /* ignore */ }

    // Check current user's PIN status
    try {
      const user: UserMeta | null = api.getUser();
      if (user) {
        const switchable = await userApi.listSwitchable();
        const me = switchable.find((u: { id: string }) => u.id === user.user_id);
        if (me) hasPin = me.has_pin;
      }
    } catch { /* ignore — non-critical */ }
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
        arr_api_key: arrApiKey.trim(),
        arr_path_mappings: pm,
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

  async function sendTestEmail() {
    testResult = '';
    testError = '';
    sendingTest = true;
    try {
      const res = await emailApi.sendTest(testEmail);
      testResult = res.message;
      toast.success('Test email sent');
    } catch (e: unknown) {
      testError = e instanceof Error ? e.message : 'Failed to send test email';
    } finally {
      sendingTest = false;
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

      <div class="form-foot">
        <button type="submit" class="btn-save" disabled={saving}>
          {saving ? 'Saving…' : 'Save Changes'}
        </button>
      </div>
    </form>

    <section>
      <div class="sec-label">Email</div>
      {#if emailEnabled}
        <div class="hint" style="margin-top: -0.5rem;">
          SMTP is configured. Send a test email to verify it's working.
        </div>
        <form class="email-test-form" on:submit|preventDefault={sendTestEmail}>
          <div class="email-test-row">
            <input
              type="email"
              bind:value={testEmail}
              placeholder="recipient@example.com"
              required
              style="flex: 1;"
            />
            <button type="submit" class="btn-save" disabled={sendingTest || !testEmail}>
              {sendingTest ? 'Sending...' : 'Send Test'}
            </button>
          </div>
        </form>
        {#if testResult}
          <div class="banner ok">{testResult}</div>
        {/if}
        {#if testError}
          <div class="banner error">{testError}</div>
        {/if}
      {:else}
        <div class="hint" style="margin-top: -0.5rem;">
          SMTP is not configured. Set <code>SMTP_HOST</code>, <code>SMTP_PORT</code>, <code>SMTP_FROM</code> (and optionally <code>SMTP_USERNAME</code>/<code>SMTP_PASSWORD</code>) environment variables to enable email features like password reset.
        </div>
      {/if}
    </section>

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
  .banner.error { background: rgba(248,113,113,0.1); border: 1px solid rgba(248,113,113,0.2); color: #fca5a5; }
  .banner.ok    { background: rgba(52,211,153,0.1);  border: 1px solid rgba(52,211,153,0.2);  color: #6ee7b7; }

  .skeleton-block {
    height: 100px; border-radius: 10px;
    background: linear-gradient(90deg, #111118 25%, #16161f 50%, #111118 75%);
    background-size: 200% 100%;
    animation: shimmer 1.4s infinite;
  }
  @keyframes shimmer { 0% { background-position: 200% 0; } 100% { background-position: -200% 0; } }

  form { display: flex; flex-direction: column; }

  section {
    padding: 1.25rem 0;
    border-bottom: 1px solid rgba(255,255,255,0.055);
    display: flex; flex-direction: column; gap: 1.25rem;
  }

  .sec-label {
    font-size: 0.68rem; font-weight: 700;
    text-transform: uppercase; letter-spacing: 0.09em;
    color: #33333d;
  }

  .field { display: flex; flex-direction: column; gap: 0.3rem; }

  label { font-size: 0.75rem; font-weight: 500; color: #44445a; }

  input {
    background: rgba(255,255,255,0.04);
    border: 1px solid rgba(255,255,255,0.09);
    border-radius: 7px;
    padding: 0.48rem 0.7rem;
    font-size: 0.85rem;
    color: #eeeef8;
    font-family: monospace;
    transition: border-color 0.15s;
    width: 100%;
  }
  input:focus { outline: none; border-color: #7c6af7; box-shadow: 0 0 0 3px rgba(124,106,247,0.12); }
  ::placeholder { color: #2a2a3d; }

  .hint {
    font-size: 0.72rem;
    color: #33333d;
    line-height: 1.5;
    margin-top: 0.15rem;
  }
  .hint a { color: #55556a; text-decoration: underline; }
  .hint a:hover { color: #a89ffa; }

  .form-foot {
    display: flex; justify-content: flex-end;
    padding-top: 1.5rem;
  }
  .btn-save {
    padding: 0.42rem 0.9rem; background: #7c6af7;
    border: none; border-radius: 7px; color: #fff;
    font-size: 0.8rem; font-weight: 600; cursor: pointer; transition: background 0.15s;
  }
  .btn-save:hover { background: #8f7ef9; }
  .btn-save:disabled { opacity: 0.5; cursor: not-allowed; }

  /* Arr webhook URL */
  .url-box {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    background: rgba(255,255,255,0.03);
    border: 1px solid rgba(255,255,255,0.07);
    border-radius: 7px;
    padding: 0.4rem 0.6rem;
  }
  .webhook-url {
    flex: 1;
    font-size: 0.75rem;
    color: #8888a0;
    word-break: break-all;
    background: none;
    padding: 0;
    border-radius: 0;
  }
  .btn-copy {
    flex-shrink: 0;
    padding: 0.3rem 0.6rem;
    background: rgba(255,255,255,0.06);
    border: 1px solid rgba(255,255,255,0.1);
    border-radius: 5px;
    color: #aaaacc;
    font-size: 0.72rem;
    font-weight: 500;
    cursor: pointer;
    transition: background 0.12s;
  }
  .btn-copy:hover { background: rgba(255,255,255,0.1); }

  /* Path mappings */
  .path-mapping-row {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    margin-bottom: 0.4rem;
  }
  .path-mapping-row input { flex: 1; min-width: 0; }
  .arrow { color: #55556a; font-size: 0.85rem; flex-shrink: 0; }
  .btn-remove {
    flex-shrink: 0;
    width: 26px; height: 26px;
    display: flex; align-items: center; justify-content: center;
    background: rgba(248,113,113,0.08);
    border: 1px solid rgba(248,113,113,0.2);
    border-radius: 5px;
    color: #fca5a5;
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

  /* Email test */
  .email-test-row {
    display: flex;
    gap: 0.5rem;
    align-items: center;
  }
  .email-test-row input { font-family: inherit; }
  code {
    background: rgba(255,255,255,0.06);
    padding: 0.1rem 0.35rem;
    border-radius: 4px;
    font-size: 0.72rem;
    color: #8888a0;
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
    color: #6ee7b7;
    font-weight: 500;
    margin-right: 0.3rem;
  }
  .pin-status.off { color: #55556a; }
  .btn-outline {
    padding: 0.36rem 0.75rem;
    background: transparent;
    border: 1px solid rgba(255,255,255,0.12);
    border-radius: 7px;
    color: #aaaacc;
    font-size: 0.78rem;
    font-weight: 500;
    cursor: pointer;
    transition: background 0.12s, border-color 0.12s;
  }
  .btn-outline:hover { background: rgba(255,255,255,0.05); border-color: rgba(255,255,255,0.2); }
  .btn-outline.btn-danger { color: #fca5a5; border-color: rgba(248,113,113,0.25); }
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
