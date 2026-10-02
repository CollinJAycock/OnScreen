// Unit tests for the pure logic in src/lib (no DOM, no SvelteKit runtime):
// the player's session/offset math, progress reporting, subtitle parsing,
// label building and the like. Standalone on purpose, so the SvelteKit vite
// config (static adapter, file-path rewrites for the IPK) stays out of it.
//
// A component test renders on the server (svelte/server render, no DOM), so
// the plain Svelte plugin compiles .svelte files (Svelte 5 reads the
// `lang="ts"` scripts itself; no svelte.config.js, no preprocess), and $lib
// resolves as in the app. $app/* stays the test's to mock.
import { fileURLToPath } from 'node:url';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import { defineConfig } from 'vitest/config';

export default defineConfig({
  plugins: [svelte({ configFile: false })],
  resolve: {
    alias: { $lib: fileURLToPath(new URL('./src/lib', import.meta.url)) },
  },
  test: {
    environment: 'node',
    include: ['src/**/*.test.ts'],
  },
});
