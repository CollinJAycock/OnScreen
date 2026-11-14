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
import type { Readable, Subscriber } from 'svelte/store';

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

const subscribers = new Set<Subscriber<PageSnapshot>>();
let stopFollowing: (() => void) | null = null;

// Every subscriber gets the page as it is NOW, not the store's last value.
// The effect that follows `$app/state` runs after the navigation has
// rendered, so a page component created by a client-side navigation used to
// subscribe while the store still held the previous route: from Home its
// `$page.params.id` was undefined, and a page that loads in onMount
// requested /api/v1/libraries/undefined (or /items/undefined) before the
// effect caught up and the page loaded everything a second time.
export const page: Readable<PageSnapshot> = {
  subscribe(run: Subscriber<PageSnapshot>) {
    run(snapshot());
    subscribers.add(run);
    if (!stopFollowing) {
      stopFollowing = $effect.root(() => {
        $effect(() => {
          const s = snapshot();
          for (const fn of subscribers) fn(s);
        });
      });
    }
    return () => {
      subscribers.delete(run);
      if (subscribers.size === 0 && stopFollowing) {
        stopFollowing();
        stopFollowing = null;
      }
    };
  }
};
