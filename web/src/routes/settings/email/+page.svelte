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
  <div class="skeleton-block"></div>
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
  .wrap { display: flex; flex-direction: column; gap: 1.5rem; }
  section {
    background: var(--bg-elevated);
    border: 1px solid var(--border);
    border-radius: 10px;
    padding: 1.1rem 1.25rem;
  }
  h2 { font-size: 0.95rem; margin: 0 0 0.5rem; font-weight: 600; }
  .hint { color: var(--text-secondary); font-size: 0.82rem; line-height: 1.5; margin: 0 0 1rem; }
  .error { color: var(--error); }

  .skeleton-block {
    height: 120px;
    border-radius: 10px;
    background: linear-gradient(90deg, var(--bg-elevated) 25%, var(--bg-hover) 50%, var(--bg-elevated) 75%);
    background-size: 200% 100%;
    animation: shimmer 1.4s infinite;
  }
  @keyframes shimmer {
    0% { background-position: 200% 0; }
    100% { background-position: -200% 0; }
  }

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
    padding: 0.48rem 0.7rem;
    border-radius: 7px;
    border: 1px solid var(--border-strong);
    background: var(--input-bg);
    color: var(--text-primary);
    font-family: inherit;
    font-size: 0.85rem;
  }
  input[type="text"]:focus, input[type="email"]:focus, input[type="password"]:focus, input[type="number"]:focus {
    outline: none;
    border-color: var(--accent);
    box-shadow: 0 0 0 3px var(--accent-bg);
  }
  input::placeholder { color: var(--text-muted); }

  .check {
    flex-direction: row;
    align-items: center;
    gap: 0.5rem;
    color: var(--text-secondary);
    font-size: 0.82rem;
    cursor: pointer;
  }
  .check input { accent-color: var(--accent); width: 16px; height: 16px; }

  .actions { display: flex; gap: 0.5rem; }
  .btn {
    padding: 0.45rem 0.9rem;
    border-radius: 7px;
    font-size: 0.78rem;
    font-weight: 500;
    border: 1px solid var(--border-strong);
    background: transparent;
    color: var(--text-secondary);
    cursor: pointer;
    transition: background 0.15s, border-color 0.15s, color 0.15s;
  }
  .btn:disabled { opacity: 0.5; cursor: not-allowed; }
  .btn-primary { background: var(--accent); color: #fff; border-color: transparent; font-size: 0.8rem; font-weight: 600; }
  .btn-primary:hover:not(:disabled) { background: var(--accent-hover); }
  .btn:hover:not(:disabled):not(.btn-primary) { background: var(--bg-hover); border-color: var(--text-muted); }

  @media (max-width: 720px) {
    .grid { grid-template-columns: 1fr; }
  }
</style>
