// App version surfaced on Settings > About. Mirrors package.json#version
// (and the two root version fields in package-lock.json); keep them in sync
// on each release, version.test.ts fails when they drift.
export const APP_VERSION = '0.1.0';
