// The account's interface language on the TV apps: users.ui_language, read
// with GET /users/me/preferences and set with PUT /users/me/ui-language.
//
// Every preferences read applies it (lib/api/endpoints users.preferences).
// The hub reads them on each visit and Settings when it opens, so a language
// chosen on another device arrives without a request of its own, and right
// after sign-in, whose next screen is the hub. A read already on its way
// when this TV changed the language is ignored: its answer predates the
// change and would put the old language back.

import { applyAccountLocale, setLocale, useDeviceLocale, type Locale } from './index.svelte';

let changes = 0;

/** Taken when a preferences read starts; call the result with the
 *  ui_language it answered. */
export function accountLocaleReader(): (uiLanguage: string | null | undefined) => Promise<void> {
  const at = changes;
  return async (uiLanguage) => {
    if (at === changes) await applyAccountLocale(uiLanguage);
  };
}

/**
 * The Settings picker: switch this TV to `next` (null = follow the device's
 * language) at once, then save it to the account. False when the save
 * failed: the language stays on this TV and the page says so.
 */
export async function chooseLocale(
  next: Locale | null,
  save: (uiLanguage: Locale | null) => Promise<unknown>,
): Promise<boolean> {
  changes++;
  if (next) await setLocale(next, { persist: true });
  else await useDeviceLocale();
  try {
    await save(next);
    return true;
  } catch {
    return false;
  }
}
