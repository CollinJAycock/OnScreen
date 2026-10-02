// App version surfaced on the Settings/About screen. Mirrors
// `package.json#version` and `appinfo.json#version` (and the two root
// version fields in package-lock.json) — keep them in sync on each
// release; version.test.ts fails when they drift. (Could resolve via
// Vite's `define` but the extra config isn't worth saving one bump.)
export const APP_VERSION = '0.2.1';
