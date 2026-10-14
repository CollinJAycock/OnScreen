<script lang="ts">
  // Admin: the server-wide digest email setting (GET/PUT /settings/digest)
  // and "Send me a test digest now". Users opt in themselves on their
  // Account page; nothing goes out until this is on and SMTP is configured.
  import { onMount } from 'svelte';
  import { digestApi, type DigestSettings } from '$lib/userStats';
  import { toast } from '$lib/stores/toast';

  let cfg: DigestSettings | null = null;
  let loading = true;
  let saving = false;
  let testing = false;
  let error = '';

  const DAYS = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];
  const HOURS = Array.from({ length: 24 }, (_, h) => h);

  onMount(load);

  async function load() {
    try {
      cfg = await digestApi.getSettings();
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Couldn’t load the digest settings';
    } finally {
      loading = false;
    }
  }

  async function save() {
    if (!cfg) return;
    saving = true;
    try {
      cfg = await digestApi.updateSettings({
        enabled: cfg.enabled,
        weekday: Number(cfg.weekday),
        hour: Number(cfg.hour),
        include_continue_watching: cfg.include_continue_watching,
        include_stats: cfg.include_stats,
      });
      toast.success('Digest settings saved');
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Save failed');
    } finally {
      saving = false;
    }
  }

  async function sendTest() {
    testing = true;
    try {
      const res = await digestApi.sendTest();
      toast.success(res.message);
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Test send failed');
    } finally {
      testing = false;
    }
  }

  function hourLabel(h: number): string {
    return new Date(2000, 0, 1, h).toLocaleTimeString(undefined, { hour: 'numeric' });
  }
</script>

<section>
  <header>
    <h2>Digest email</h2>
    <p class="hint">
      A weekly or monthly email of what was added to the libraries each person
      can see, with posters. People opt in on their Account page; only titles
      they're allowed to see are included.
    </p>
  </header>

  {#if loading}
    <p class="hint">Loading…</p>
  {:else if error}
    <p class="error">{error}</p>
  {:else if cfg}
    {#if !cfg.smtp_configured}
      <p class="banner">SMTP isn't configured above, so no digests will be sent. Save working SMTP settings first.</p>
    {/if}

    <label class="check">
      <input type="checkbox" bind:checked={cfg.enabled} />
      <span>Send digests to people who opted in</span>
    </label>

    <div class="grid">
      <label>
        Weekly digests go out on
        <select bind:value={cfg.weekday}>
          {#each DAYS as d, i}<option value={i}>{d}</option>{/each}
        </select>
      </label>
      <label>
        At (monthly ones on the 1st)
        <select bind:value={cfg.hour}>
          {#each HOURS as h}<option value={h}>{hourLabel(h)}</option>{/each}
        </select>
      </label>
    </div>
    <p class="hint small">
      Server time{cfg.server_timezone ? ` (${cfg.server_timezone})` : ''}. A weekly digest covers the
      seven days before; a monthly one the previous month.
    </p>

    <label class="check">
      <input type="checkbox" bind:checked={cfg.include_continue_watching} />
      <span>Include what they're in the middle of (Continue Watching)</span>
    </label>
    <label class="check">
      <input type="checkbox" bind:checked={cfg.include_stats} />
      <span>Include their own numbers for the period</span>
    </label>

    <p class="hint small">
      {#if cfg.base_url}
        Links in the email point to <code>{cfg.base_url}</code> (Settings ▸ General ▸ Base URL).
      {:else}
        No Base URL is set under Settings ▸ General, so links in the email use this server's
        local address. Set it to the address people reach OnScreen at.
      {/if}
    </p>

    <div class="actions">
      <button class="btn btn-primary" disabled={saving} on:click={save}>
        {saving ? 'Saving…' : 'Save digest settings'}
      </button>
      <button class="btn" disabled={testing || !cfg.smtp_configured} on:click={sendTest}>
        {testing ? 'Sending…' : 'Send me a test digest now'}
      </button>
    </div>
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
  .hint.small { font-size: 0.78rem; color: var(--text-muted); }
  .hint code { font-size: 0.78rem; color: var(--text-primary); }
  .error { color: var(--error); }
  .banner {
    background: var(--info-bg); color: var(--text-primary); border-radius: 8px;
    padding: 0.6rem 0.8rem; font-size: 0.8rem; margin: 0 0 1rem;
  }

  .grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 0.75rem 1rem;
    margin: 0.9rem 0 0.4rem;
  }
  label {
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
    font-size: 0.78rem;
    color: var(--text-secondary);
  }
  select {
    padding: 0.45rem 0.6rem;
    border-radius: 7px;
    border: 1px solid var(--border-strong);
    background: var(--input-bg);
    color: var(--text-primary);
    font-family: inherit;
    font-size: 0.85rem;
  }
  select:focus { outline: none; border-color: var(--accent); box-shadow: 0 0 0 3px var(--accent-bg); }

  .check {
    flex-direction: row;
    align-items: center;
    gap: 0.5rem;
    font-size: 0.82rem;
    cursor: pointer;
    margin-bottom: 0.55rem;
  }
  .check input { accent-color: var(--accent); width: 16px; height: 16px; flex: none; margin: 0; }

  .actions { display: flex; gap: 0.5rem; flex-wrap: wrap; margin-top: 0.4rem; }
  .btn {
    padding: 0.45rem 0.9rem;
    border-radius: 7px;
    font-size: 0.78rem;
    font-weight: 500;
    border: 1px solid var(--border-strong);
    background: transparent;
    color: var(--text-secondary);
    cursor: pointer;
  }
  .btn:disabled { opacity: 0.5; cursor: not-allowed; }
  .btn-primary { background: var(--accent); color: #fff; border-color: transparent; font-weight: 600; }
  .btn-primary:hover:not(:disabled) { background: var(--accent-hover); }
  .btn:hover:not(:disabled):not(.btn-primary) { background: var(--bg-hover); border-color: var(--text-muted); }

  @media (max-width: 720px) {
    .grid { grid-template-columns: minmax(0, 1fr); }
  }
</style>
