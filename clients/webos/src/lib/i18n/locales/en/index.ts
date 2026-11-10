// The English catalog (the source) of the TV apps, one file per area of the app so each
// area can be translated on its own (docs/i18n.md). A key lives in exactly
// one file; the catalog test checks. The same files are in all three TV apps
// (webOS, Tizen, Xbox): scripts/tv-i18n.mjs keeps them in step.
import core from './core.json';
import languages from './languages.json';

export default { ...core, ...languages };
