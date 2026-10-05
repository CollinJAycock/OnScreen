// Page options moved into svelte.config.js — hash-router mode
// rejects per-route `prerender` / `ssr` / `trailingSlash` because
// every route lives behind `#/...` in a single SPA bundle and SSR
// / per-route prerender are meaningless concepts in that model.
// SvelteKit infers SSR-off + client-only-render from the inline
// bundleStrategy + hash router pair.
export {};
