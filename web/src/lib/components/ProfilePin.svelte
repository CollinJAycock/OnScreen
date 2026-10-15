<script lang="ts">
  // The signed-in user's own 4-digit profile PIN (PUT / DELETE
  // /users/me/pin), used by the Switch User panel. Shown on the Account page
  // for everyone and on Settings ▸ General for admins. has_pin comes from the
  // self-scoped preferences call, not the switchable list: that list holds the
  // caller's managed children and never the caller. An account with no
  // OnScreen password (SSO / LDAP-only) isn't asked for one; the server skips
  // the check for it.
  import { onMount } from 'svelte';
  import { userApi } from '$lib/api';
  import { toast } from '$lib/stores/toast';

  let loaded = false;
  let hasPin = false;
  let hasPassword = true;
  let mode: 'idle' | 'set' | 'change' | 'clear' = 'idle';
  let pinValue = '';
  let password = '';
  let saving = false;
  let error = '';

  onMount(async () => {
    try {
      const prefs = await userApi.getPreferences();
      hasPin = prefs.has_pin ?? false;
      hasPassword = prefs.has_password ?? true;
    } catch { /* show the "no PIN" state; saving still reports errors */ }
    loaded = true;
  });

  function start(m: 'set' | 'change' | 'clear') {
    mode = m;
    pinValue = '';
    password = '';
    error = '';
  }

  function cancel() {
    mode = 'idle';
    pinValue = '';
    password = '';
    error = '';
  }

  function handlePinInput(e: Event) {
    const input = e.target as HTMLInputElement;
    input.value = input.value.replace(/\D/g, '').slice(0, 4);
    pinValue = input.value;
  }

  async function submit() {
    error = '';
    saving = true;
    try {
      if (mode === 'clear') {
        await userApi.clearPin(password);
        hasPin = false;
        toast.success('PIN removed');
      } else {
        await userApi.setPin(pinValue, password);
        hasPin = true;
        toast.success(mode === 'set' ? 'PIN set successfully' : 'PIN changed successfully');
      }
      mode = 'idle';
      pinValue = '';
      password = '';
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed to update PIN';
    } finally {
      saving = false;
    }
  }
</script>

<div class="pin">
  {#if error}
    <div class="banner error">{error}</div>
  {/if}

  {#if !loaded}
    <!-- one quick request; nothing to show until it answers -->
  {:else if mode === 'idle'}
    <div class="pin-actions">
      {#if hasPin}
        <span class="pin-status">PIN is set</span>
        <button class="btn-outline" on:click={() => start('change')}>Change PIN</button>
        <button class="btn-outline btn-danger" on:click={() => start('clear')}>Clear PIN</button>
      {:else}
        <span class="pin-status off">No PIN set</span>
        <button class="btn-outline" on:click={() => start('set')}>Set PIN</button>
      {/if}
    </div>
  {:else}
    <form class="pin-form" on:submit|preventDefault={submit}>
      {#if mode !== 'clear'}
        <div class="field">
          <label for="pin-input">4-digit PIN</label>
          <input
            id="pin-input"
            class="pin-input"
            type="password"
            inputmode="numeric"
            pattern={'\\d{4}'}
            maxlength="4"
            value={pinValue}
            on:input={handlePinInput}
            placeholder="0000"
            autocomplete="off"
          />
        </div>
      {/if}
      {#if hasPassword}
        <div class="field">
          <label for="pin-password">Current password</label>
          <input
            id="pin-password"
            type="password"
            bind:value={password}
            placeholder="Confirm your password"
            autocomplete="current-password"
          />
        </div>
      {/if}
      <div class="pin-form-actions">
        <button
          type="submit"
          class="btn-save"
          disabled={saving || (mode !== 'clear' && pinValue.length !== 4) || (hasPassword && !password)}
        >
          {saving ? 'Saving...' : mode === 'clear' ? 'Remove PIN' : mode === 'set' ? 'Set PIN' : 'Change PIN'}
        </button>
        <button type="button" class="btn-outline" on:click={cancel}>Cancel</button>
      </div>
    </form>
  {/if}
</div>

<style>
  .pin { display: flex; flex-direction: column; gap: 0.9rem; }
  .banner { padding: 0.6rem 0.9rem; border-radius: 10px; font-size: 0.8rem; }
  .banner.error { background: var(--error-bg); border: 1px solid var(--error); color: var(--error); }
  .pin-actions { display: flex; align-items: center; gap: 0.6rem; flex-wrap: wrap; }
  .pin-status { font-size: 0.78rem; color: var(--success); font-weight: 500; margin-right: 0.3rem; }
  .pin-status.off { color: var(--text-muted); }
  .pin-form { display: flex; flex-direction: column; gap: 0.9rem; }
  .pin-form-actions { display: flex; gap: 0.5rem; flex-wrap: wrap; }
  .field { display: flex; flex-direction: column; gap: 0.3rem; }
  label { font-size: 0.72rem; font-weight: 500; color: var(--text-muted); }
  input {
    background: var(--input-bg);
    border: 1px solid var(--border-strong);
    border-radius: 7px;
    padding: 0.48rem 0.7rem;
    font-size: 0.85rem;
    color: var(--text-primary);
    font-family: inherit;
    width: 100%;
    box-sizing: border-box;
  }
  input:focus { outline: none; border-color: var(--accent); box-shadow: 0 0 0 3px var(--accent-bg); }
  .pin-input { max-width: 120px; text-align: center; letter-spacing: 0.3em; }
  .btn-save {
    padding: 0.45rem 0.9rem; background: var(--accent);
    border: none; border-radius: 7px; color: #fff;
    font-size: 0.8rem; font-weight: 600; cursor: pointer; transition: background 0.15s;
  }
  .btn-save:hover { background: var(--accent-hover); }
  .btn-save:disabled { opacity: 0.5; cursor: not-allowed; }
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
  .btn-outline.btn-danger { color: var(--error); border-color: var(--error); }
  .btn-outline.btn-danger:hover { background: var(--error-bg); border-color: var(--error); }
  @media (max-width: 768px) {
    .pin-actions { flex-direction: column; align-items: flex-start; gap: 0.5rem; }
  }
</style>
