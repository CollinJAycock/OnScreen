// App version surfaced on the Settings/About screen. Mirrors
// `package.json#version` and `appinfo.json#version` — keep all
// three in sync on each release. (Could resolve via Vite's
// `define` but the extra config isn't worth saving one bump.)
export const APP_VERSION = '0.1.0';
