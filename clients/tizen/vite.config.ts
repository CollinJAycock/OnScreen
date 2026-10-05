import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig, type Plugin } from 'vite';

// Svelte 5 scopes every compound after the first in a selector as
// `:where(.svelte-HASH)` (to keep specificity at one class). :where() is
// Chrome 88, and an unknown pseudo-class makes Chromium drop the whole
// rule, so on Tizen 5.5 to 6.5 (Chromium 69 to 85) every scoped descendant/child/sibling
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
  plugins: [sveltekit(), unwrapScopedWhere()],
  build: {
    target: 'es2019',
    // The CSS minifier needs a browser to target: with only 'es2019' it
    // assumes current CSS and folds top/right/bottom/left into `inset`
    // (Chrome 87), which Tizen's Chromium 69-85 drops. chrome69 makes it
    // write the longhands instead, `inset: 0` in the source included. It
    // can't lower flexbox `gap` (84), which the components avoid by hand;
    // :where() (88) is handled by unwrapScopedWhere above.
    cssTarget: 'chrome69',
    cssCodeSplit: false
  },
  server: {
    fs: { strict: false }
  }
});
