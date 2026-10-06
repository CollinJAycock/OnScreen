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
    // (e.g. file://com.onscreen.tv-webos/) but resolve relative
    // imports to the real install path (file:///media/developer/...).
    // The two are different origins under file://, which trips ESM
    // cross-origin policy and blocks every modulepreload + dynamic
    // import. Inlining the whole bundle into index.html eliminates
    // sub-imports entirely — one self-contained HTML, zero chunk
    // loads, no cross-origin policy in play.
    output: {
      bundleStrategy: 'inline'
    },
    // bundleStrategy 'inline' requires client-side route resolution
    // (server-resolved routing would fetch route manifests that we
    // just inlined). `type: 'hash'` is the file:// fix: the page is
    // loaded from /media/developer/.../index.html, but SvelteKit's
    // pathname-mode router would try to match that filesystem path
    // against the route table and 404. Hash-mode routes live in
    // the URL fragment (#/discover, #/hub, …) which doesn't care
    // what path the HTML was loaded from.
    router: {
      type: 'hash',
      resolution: 'client'
    }
  }
};
