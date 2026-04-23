<script lang="ts">
  import { onMount } from 'svelte';
  import { settingsApi } from '$lib/api';
  import type { SMTPSettings } from '$lib/api';
  import { toast } from '$lib/stores/toast';

  let loading = true;
  let saving = false;
  let testing = false;
  let error = '';

  let smtp: SMTPSettings = {
    enabled: false,
    host: '',
    port: 587,
    username: '',
    password: '',
    from: '',
  };
  let passwordMasked = false;
  let testTo = '';

  onMount(async () => {
    try {
      const s = await settingsApi.get();
      if (s.smtp) {
        smtp = { ...s.smtp };
        passwordMasked = smtp.password === '****';
      }
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed to load settings';
    } finally {
      loading = false;
    }
  });

  async function save() {
    saving = true;
    try {
      const payload: Record<string, unknown> = {
        enabled: smtp.enabled,
        host: smtp.host,
        port: smtp.port,
        username: smtp.username,
        from: smtp.from,
      };
      // Only send password when the admin actually edited it.
      if (!passwordMasked || smtp.password !== '****') {
        payload.password = smtp.password;
      }
      await settingsApi.update({ smtp: payload } as never);
      toast.success('Email settings saved');
      const s = await settingsApi.get();
      smtp = { ...s.smtp };
      passwordMasked = smtp.password === '****';
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Save failed');
    } finally {
      saving = false;
    }
  }

  async function sendTest() {
    if (!testTo) {
      toast.error('Enter a recipient email first');
      return;
    }
    testing = true;
    try {
      await settingsApi.testEmail(testTo);
      toast.success(`Test email sent to ${testTo}`);
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Test send failed');
    } finally {
      testing = false;
    }
  }
</script>

{#if loading}
  <p class="muted">Loading…</p>
{:else if error}
  <p class="error">{error}</p>
{:else}
  <div class="wrap">
    <section>
      <header>
        <h2>SMTP</h2>
        <p class="hint">
          Outbound email for password resets and invitations. Without SMTP,
          admins must hand out invite URLs manually and the "Forgot password"
          flow is disabled.
        </p>
      </header>

      <label class="check">
        <input type="checkbox" bind:checked={smtp.enabled} />
        <span>Enable email sending</span>
      </label>

      <div class="grid">
        <label class="full">
          From address
          <input type="text" bind:value={smtp.from} placeholder="OnScreen <noreply@example.com>" />
        </label>
        <label>
          Host
          <input type="text" bind:value={smtp.host} placeholder="smtp.gmail.com" />
        </label>
        <label>
          Port
          <input type="number" bind:value={smtp.port} placeholder="587" min="1" max="65535" />
        </label>
        <label>
          Username
          <input type="text" bind:value={smtp.username} autocomplete="off" />
        </label>
        <label>
          Password
          <input
            type="password"
            bind:value={smtp.password}
            on:input={() => { passwordMasked = false; }}
            placeholder={passwordMasked ? 'unchanged' : ''}
            autocomplete="new-password"
          />
        </label>
      </div>

      <div class="actions">
        <button class="btn btn-primary" disabled={saving} on:click={save}>
          {saving ? 'Saving…' : 'Save email settings'}
        </button>
      </div>
    </section>

    <section>
      <header>
        <h2>Send test message</h2>
        <p class="hint">
          Sends a one-off SMTP test using the saved configuration above —
          handy for verifying credentials before relying on password reset.
        </p>
      </header>

      <div class="grid">
        <label class="full">
          Recipient
          <input type="email" bind:value={testTo} placeholder="you@example.com" />
        </label>
      </div>

      <div class="actions">
        <button class="btn" disabled={testing} on:click={sendTest}>
          {testing ? 'Sending…' : 'Send test email'}
        </button>
      </div>
    </section>
  </div>
{/if}

<style>
  .wrap { display: flex; flex-direction: column; gap: 2rem; }
  section {
    background: var(--surface);
    border: 1px solid rgba(255,255,255,0.05);
    border-radius: 8px;
    padding: 1.25rem 1.5rem;
  }
  h2 { font-size: 0.95rem; margin: 0 0 0.5rem; font-weight: 600; }
  .hint { color: var(--text-secondary); font-size: 0.82rem; line-height: 1.5; margin: 0 0 1rem; }
  .muted { color: var(--text-muted); }
  .error { color: var(--error); }

  .grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 0.75rem 1rem;
    margin: 1rem 0;
  }
  .grid .full { grid-column: 1 / -1; }
  label {
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
    font-size: 0.78rem;
    color: var(--text-secondary);
  }
  input[type="text"], input[type="email"], input[type="password"], input[type="number"] {
    padding: 0.45rem 0.6rem;
    border-radius: 4px;
    border: 1px solid rgba(255,255,255,0.1);
    background: var(--bg);
    color: var(--text-primary);
    font-family: inherit;
    font-size: 0.85rem;
  }

  .check {
    flex-direction: row;
    align-items: center;
    gap: 0.5rem;
    color: var(--text-secondary);
    font-size: 0.82rem;
    cursor: pointer;
  }

  .actions { display: flex; gap: 0.5rem; }
  .btn {
    padding: 0.55rem 1.1rem;
    border-radius: 4px;
    font-size: 0.82rem;
    font-weight: 500;
    border: 1px solid rgba(255,255,255,0.1);
    background: transparent;
    color: var(--text-primary);
    cursor: pointer;
    transition: background 0.12s, filter 0.12s;
  }
  .btn:disabled { opacity: 0.55; cursor: not-allowed; }
  .btn-primary { background: var(--accent); color: var(--accent-text); border-color: transparent; }
  .btn-primary:hover:not(:disabled) { filter: brightness(1.1); }
  .btn:hover:not(:disabled):not(.btn-primary) { background: rgba(255,255,255,0.04); }

  @media (max-width: 720px) {
    .grid { grid-template-columns: 1fr; }
  }
</style>
