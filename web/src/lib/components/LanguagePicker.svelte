<script lang="ts">
  // The interface-language picker. On the sign-in screen it changes this
  // device only; on the Account page (saveToAccount) it also saves the choice
  // to the account (PUT /users/me/ui-language) so the user's other devices
  // follow. "Device language" clears the choice: the browser's language
  // decides again. Language names are shown in their own language.
  import { userApi } from '$lib/api';
  import {
    LOCALES, LOCALE_NAMES, isLocale, locale, setLocale, storedLocale, t, tr, useDeviceLocale,
  } from '$lib/i18n/index.svelte';

  interface Props {
    /** Also save the choice to the signed-in account. */
    saveToAccount?: boolean;
    /** Compact styling for the sign-in screen. */
    compact?: boolean;
  }

  let { saveToAccount = false, compact = false }: Props = $props();

  const uid = Math.random().toString(36).slice(2, 8);
  // '' = follow the device. Re-read when the active language changes (the
  // account's choice can arrive after this renders).
  let value = $derived.by(() => {
    locale();
    return storedLocale() ?? '';
  });
  let saveError = $state('');

  async function choose(next: string) {
    saveError = '';
    if (isLocale(next)) await setLocale(next, { persist: true });
    else await useDeviceLocale();
    if (!saveToAccount) return;
    try {
      await userApi.setUILanguage(isLocale(next) ? next : null);
    } catch {
      saveError = t('language.saveFailed');
    }
  }
</script>

<div class="language-picker" class:compact>
  <label for="language-{uid}">{$tr('language.label')}</label>
  <select
    id="language-{uid}"
    {value}
    onchange={(e) => choose((e.currentTarget as HTMLSelectElement).value)}
  >
    <option value="">{$tr('language.device')}</option>
    {#each LOCALES as loc}
      <option value={loc} lang={loc}>{LOCALE_NAMES[loc]}</option>
    {/each}
  </select>
  {#if !compact}
    <p class="help">{$tr('language.help')}</p>
  {/if}
  {#if saveError}
    <p class="error" role="alert">{saveError}</p>
  {/if}
</div>

<style>
  .language-picker { display: flex; flex-direction: column; gap: 0.35rem; }
  .language-picker.compact { flex-direction: row; align-items: center; justify-content: center; gap: 0.5rem; }
  label { font-size: 0.85rem; color: var(--text-secondary); }
  .compact label { font-size: 0.8rem; color: var(--text-muted); }
  select {
    background: var(--bg-secondary); color: var(--text-primary);
    border: 1px solid var(--border); border-radius: 6px;
    padding: 0.4rem 0.6rem; font-size: 0.9rem; max-width: 16rem;
  }
  .compact select { font-size: 0.8rem; padding: 0.25rem 0.5rem; }
  .help { margin: 0; font-size: 0.78rem; color: var(--text-muted); max-width: 32rem; }
  .error { margin: 0; font-size: 0.8rem; color: #f87171; }
</style>
