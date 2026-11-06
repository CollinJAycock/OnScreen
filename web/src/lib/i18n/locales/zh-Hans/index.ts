// The Simplified Chinese catalog, one file per area of the app so each area can be
// translated on its own (docs/i18n.md). A key lives in exactly one file; the
// catalog test checks.
import core from './core.json';
import settingsA from './settings-a.json';
import settingsB from './settings-b.json';
import components from './components.json';
import media from './media.json';
import account from './account.json';

export default { ...core, ...settingsA, ...settingsB, ...components, ...media, ...account };
