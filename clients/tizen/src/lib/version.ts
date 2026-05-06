// App version surfaced on the Settings/About screen. Kept in a small
// dedicated file so a release bump is a one-line change with a
// predictable git diff. Mirrors `package.json#version` and the
// `<widget version=...>` attribute in config.xml — keep all three in
// sync. (Could resolve at build time via Vite's `define` but the
// extra config + define magic isn't worth saving one bump.)
export const APP_VERSION = '0.1.0';
