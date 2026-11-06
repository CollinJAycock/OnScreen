<script lang="ts">
  // Change the signed-in user's own password (PUT /users/me/password). The
  // server checks the current password, then signs out every other device;
  // this browser stays signed in (its next request renews its sign-in). The
  // rare old sign-in that can't be told apart from the others is signed out
  // too, and is sent to the sign-in page.
  import { userApi } from '$lib/api';
  import { toast } from '$lib/stores/toast';
  import { t, tr } from '$lib/i18n/index.svelte';

  // Matches the server's password policy (MinPasswordLength).
  const MIN_LENGTH = 12;

  let current = '';
  let next = '';
  let confirm = '';
  let saving = false;
  let error = '';

  $: mismatch = confirm !== '' && next !== confirm;
  $: tooShort = next !== '' && next.length < MIN_LENGTH;
  $: canSave = !saving && current !== '' && next.length >= MIN_LENGTH && next === confirm;

  async function save() {
    if (!canSave) return;
    error = '';
    saving = true;
    try {
      const r = await userApi.changePassword(current, next);
      current = next = confirm = '';
      if (r.signed_out) {
        toast.success(t('ui.password.changedSignIn'));
        window.location.href = '/login';
        return;
      }
      toast.success(
        r.other_sessions_revoked > 0
          ? t('ui.password.changedOthers')
          : t('ui.password.changed'),
      );
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : t('ui.password.failed');
    } finally {
      saving = false;
    }
  }
</script>

<form class="pw" on:submit|preventDefault={save}>
  {#if error}
    <div class="banner error">{error}</div>
  {/if}
  <div class="field">
    <label for="pw-current">{$tr('ui.password.current')}</label>
    <input id="pw-current" type="password" bind:value={current} autocomplete="current-password" />
  </div>
  <div class="field">
    <label for="pw-new">{$tr('ui.password.new')}</label>
    <input id="pw-new" type="password" bind:value={next} autocomplete="new-password" minlength={MIN_LENGTH} />
    {#if tooShort}
      <p class="hint warn">{$tr('ui.password.minLength', { n: MIN_LENGTH })}</p>
    {/if}
  </div>
  <div class="field">
    <label for="pw-confirm">{$tr('ui.password.confirm')}</label>
    <input id="pw-confirm" type="password" bind:value={confirm} autocomplete="new-password" />
    {#if mismatch}
      <p class="hint warn">{$tr('ui.password.mismatch')}</p>
    {/if}
  </div>
  <div class="foot">
    <button type="submit" class="btn-save" disabled={!canSave}>
      {saving ? $tr('ui.password.changing') : $tr('ui.password.change')}
    </button>
  </div>
</form>

<style>
  .pw { display: flex; flex-direction: column; gap: 0.9rem; max-width: 360px; }
  .banner { padding: 0.6rem 0.9rem; border-radius: 10px; font-size: 0.8rem; background: var(--error-bg); border: 1px solid var(--error); color: var(--error); }
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
  .hint { font-size: 0.72rem; color: var(--text-muted); margin: 0; }
  .hint.warn { color: var(--error); }
  .foot { display: flex; }
  .btn-save {
    padding: 0.45rem 0.9rem; background: var(--accent);
    border: none; border-radius: 7px; color: #fff;
    font-size: 0.8rem; font-weight: 600; cursor: pointer; transition: background 0.15s;
  }
  .btn-save:hover { background: var(--accent-hover); }
  .btn-save:disabled { opacity: 0.5; cursor: not-allowed; }
</style>
