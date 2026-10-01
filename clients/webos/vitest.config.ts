// Unit tests for the pure logic in src/lib (no DOM, no SvelteKit runtime):
// the player's session/offset math, progress reporting, subtitle parsing,
// label building and the like. Standalone on purpose, so the SvelteKit vite
// config (static adapter, file-path rewrites for the IPK) stays out of it.
import { defineConfig } from 'vitest/config';

export default defineConfig({
  test: {
    environment: 'node',
    include: ['src/**/*.test.ts'],
  },
});
