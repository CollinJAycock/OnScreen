import adapter from '@sveltejs/adapter-static';
import { sveltekit } from '@sveltejs/kit/vite';
import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';
import { defineConfig, type Plugin } from 'vite';

// Svelte 5 scopes every compound after the first in a selector as
// `:where(.svelte-HASH)` (to keep specificity at one class). :where() is
// Chrome 88, and an unknown pseudo-class makes Chromium drop the whole
// rule, so on webOS 6 (Chromium 79) every scoped descendant/child/sibling
// rule — `.overlay .title`, `.row > span + span` — silently did nothing.
// Rewriting to a plain `.svelte-HASH` restores Svelte 4's output: the rule
// applies everywhere, only a little more specific. The CSS is a single
// asset (cssCodeSplit: false) that kit then inlines into index.html.
function unwrapScopedWhere(): Plugin {
  return {
    name: 'onscreen-unwrap-scoped-where',
    apply: 'build',
    // After vite:css-post, which only emits the merged CSS asset in its own
    // generateBundle.
    enforce: 'post',
    generateBundle(_options, bundle) {
      for (const file of Object.values(bundle)) {
        if (file.type !== 'asset' || !file.fileName.endsWith('.css')) continue;
        const css = typeof file.source === 'string' ? file.source : new TextDecoder().decode(file.source);
        file.source = css.replace(/:where\((\.svelte-[a-z0-9]+)\)/g, '$1');
      }
    }
  };
}

export default defineConfig({
  plugins: [
    // SvelteKit 3 takes its configuration here; svelte.config.js is no
    // longer read (Kit 3 errors if one exists).
    sveltekit({
      preprocess: vitePreprocess(),
      // Kit 3 renamed `$lib` to `#lib`; keep `$lib`, used across the app.
      alias: { $lib: 'src/lib' },
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
      // (e.g. file://tv.onscreen.webos-webos/) but resolve relative
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
    }),
    unwrapScopedWhere()
  ],
  build: {
    target: 'es2019',
    // The CSS minifier needs a browser to target: with only 'es2019' it
    // assumes current CSS and folds top/right/bottom/left into `inset`
    // (Chrome 87), which webOS 6's Chromium 79 drops. chrome79 makes it
    // write the longhands instead, `inset: 0` in the source included. It
    // can't lower flexbox `gap` (84), which the components avoid by hand;
    // :where() (88) is handled by unwrapScopedWhere above.
    cssTarget: 'chrome79',
    cssCodeSplit: false
  },
  server: {
    fs: { strict: false }
  }
});
