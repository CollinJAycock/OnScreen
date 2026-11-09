// A `page` store for components that still use `$page`, `derived(page)` or
// `get(page)`.
//
// SvelteKit 3 removed `$app/stores`; its replacement, `page` from
// `$app/state`, is a reactive object that only updates runes-mode code. Most
// of our routes are still legacy-mode (`$:` statements), where reading
// `$app/state` directly would stop reacting to navigation. This store follows
// `$app/state` through an effect, so `$page` keeps updating on every
// navigation. New and converted (runes) components should import `page`
// from `$app/state` instead.
import { page as statePage } from '$app/state';
import { readable } from 'svelte/store';

// `url` is a plain (mutable) URL copy, as `$app/stores` gave: Kit 3 types
// `page.url` as read-only, and our helpers take and modify a URL.
export type PageSnapshot = Pick<
  typeof statePage,
  'params' | 'route' | 'status' | 'error' | 'data' | 'form' | 'state'
> & { url: URL };

function snapshot(): PageSnapshot {
  return {
    url: new URL(statePage.url.href),
    params: statePage.params,
    route: statePage.route,
    status: statePage.status,
    error: statePage.error,
    data: statePage.data,
    form: statePage.form,
    state: statePage.state
  };
}

export const page = readable<PageSnapshot>(undefined as unknown as PageSnapshot, (set) => {
  // Set synchronously so the first subscriber never sees undefined; the
  // effect below runs after a microtask and then on every change.
  set(snapshot());
  return $effect.root(() => {
    $effect(() => {
      set(snapshot());
    });
  });
});
