import adapter from '@sveltejs/adapter-static';
import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';

/** @type {import('@sveltejs/kit').Config} */
const config = {
  preprocess: vitePreprocess(),
  kit: {
    // Static adapter — SPA mode, no SSR (ADR-012).
    adapter: adapter({
      pages: 'dist',
      assets: 'dist',
      fallback: 'index.html',  // SPA fallback for client-side routing
      precompress: false,
      strict: false
    }),
    // All routes are client-rendered.
    prerender: {
      entries: []
    }
  }
};

export default config;
