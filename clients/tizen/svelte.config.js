import adapter from '@sveltejs/adapter-static';
import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';

export default {
  preprocess: vitePreprocess(),
  kit: {
    adapter: adapter({
      pages: 'build',
      assets: 'build',
      fallback: 'index.html',
      precompress: false,
      strict: true
    }),
    paths: {
      relative: true
    },
    // webOS + Tizen load the app from a virtual file:// origin
    // (e.g. file://OnScreenTV.OnScreen/) but resolve relative
    // imports to the real install path. The two are different
    // origins under file://, which trips ESM cross-origin policy
    // and blocks every modulepreload + dynamic import. Inlining
    // the whole bundle into index.html eliminates sub-imports
    // entirely — one self-contained HTML, no chunk loads.
    output: {
      bundleStrategy: 'inline'
    },
    // bundleStrategy 'inline' requires client-side route resolution.
    // `type: 'hash'` is the file:// fix: the page is loaded from
    // /media/developer/.../index.html, but pathname-mode routing
    // would try to match that filesystem path against the route
    // table and 404. Hash-mode routes (#/discover, #/hub, …) don't
    // care what path the HTML was loaded from.
    router: {
      type: 'hash',
      resolution: 'client'
    }
  }
};
