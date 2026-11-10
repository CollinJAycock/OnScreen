// Numbers with a fixed count of decimals in the app language's style: a
// rating of 7.5 is "7.5" in English and "7,5" in Spanish. The module's
// formatNumber keeps up to three decimals and drops trailing zeros, so it
// can't show "8.0". Reading locale() keeps a template using it in step with a
// language change. (TV apps only: index.svelte.ts is the web app's, as is.)
import { locale } from './index.svelte';

const formats = new Map<string, Intl.NumberFormat>();

export function formatDecimal(n: number, digits: number): string {
  const loc = locale();
  const key = `${loc}|${digits}`;
  let f = formats.get(key);
  if (!f) {
    f = new Intl.NumberFormat(loc, { minimumFractionDigits: digits, maximumFractionDigits: digits });
    formats.set(key, f);
  }
  return f.format(n);
}
