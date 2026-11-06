import { describe, expect, it, vi } from 'vitest';
import { render } from 'svelte/server';

vi.mock('$app/navigation', () => ({ goto: vi.fn() }));

import LibrarySearch from './LibrarySearch.svelte';

// The panel rendered on the server (no DOM, so no actions or onMount run:
// no search goes out, nothing takes focus). Its search rules are
// lib/librarySearch.test.ts; the keys, the focus manager's.

describe('LibrarySearch', () => {
  const html = (props: Record<string, unknown> = {}) =>
    render(LibrarySearch, {
      props: { libraryId: 'lib1', libraryName: 'Movies', onclose: vi.fn(), ...props },
    }).body;

  it('names the library it searches, as a dialog over the page', () => {
    const body = html();
    expect(body).toContain('Search Movies');
    expect(body).toContain('role="dialog"');
    expect(body).toContain('aria-modal="true"');
  });

  it('opens with the on-screen keyboard and asks for two characters', () => {
    const body = html();
    expect(body).toContain('class="osk');
    expect(body).toContain('Type at least 2 characters.');
  });

  it('shows the text it was opened on (Back from a result)', () => {
    expect(html({ query: 'alien' })).toContain('>alien<');
  });
});
