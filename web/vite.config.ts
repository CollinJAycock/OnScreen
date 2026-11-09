import adapter from '@sveltejs/adapter-static';
import { sveltekit } from '@sveltejs/kit/vite';
import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';
import { defineConfig } from 'vite';

export default defineConfig({
  plugins: [
    // SvelteKit 3 takes its configuration here; svelte.config.js is no longer read.
    sveltekit({
      preprocess: vitePreprocess(),
      // Kit 3 replaced `$lib` with `#lib`; keep `$lib` (used across the app) as an alias.
      alias: { $lib: 'src/lib' },
      // Static adapter — SPA mode, no SSR (ADR-012).
      adapter: adapter({
        pages: 'dist',
        assets: 'dist',
        fallback: 'index.html', // SPA fallback for client-side routing
        precompress: false,
        strict: false
      }),
      // All routes are client-rendered.
      prerender: {
        entries: []
      }
    })
  ],
  server: {
    port: 5173,
    // In dev mode, proxy API requests to the Go server.
    proxy: {
      '/api': 'http://localhost:7070',
      '/media': 'http://localhost:7070',
      '/health': 'http://localhost:7070',
      '/artwork': 'http://localhost:7070'
    }
  }
});
