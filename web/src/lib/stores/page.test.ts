import { afterEach, describe, expect, it } from 'vitest';
import { get } from 'svelte/store';
import { page as statePage } from '$app/state';
import { page } from '$lib/stores/page.svelte';

// $app/state is the test stand-in (src/__mocks__/app-state.ts): a plain
// object, so changing it does not run the store's effect. That is exactly
// the window a navigation opens: `$app/state` already holds the new route
// while the effect that copies it into the store hasn't run yet.
describe('page bridge store', () => {
  const original = { params: statePage.params, url: statePage.url };
  afterEach(() => {
    statePage.params = original.params;
    statePage.url = original.url;
  });

  it('gives a new subscriber the current route, not the last copied one', () => {
    // A layout already follows the store (the store is running).
    const seenByLayout: string[] = [];
    const stopLayout = page.subscribe((p) => seenByLayout.push(p.params.id ?? 'none'));

    // Client-side navigation: $app/state moves on first.
    statePage.params = { id: 'lib-2' };
    statePage.url = new URL('http://localhost/libraries/lib-2');

    // The new page subscribes while the effect is still pending. It used to
    // read the previous route here (params.id undefined from Home) and
    // request /api/v1/libraries/undefined.
    let pageId: string | undefined;
    let pagePath = '';
    const stopPage = page.subscribe((p) => {
      pageId = p.params.id;
      pagePath = p.url.pathname;
    });
    expect(pageId).toBe('lib-2');
    expect(pagePath).toBe('/libraries/lib-2');
    expect(get(page).params.id).toBe('lib-2');

    stopPage();
    stopLayout();
    expect(seenByLayout[0]).toBe('none');
  });

  it('hands out a copy of the URL each time', () => {
    statePage.url = new URL('http://localhost/libraries/a?sort=title');
    const a = get(page).url;
    a.searchParams.set('sort', 'year');
    expect(get(page).url.search).toBe('?sort=title');
  });
});
