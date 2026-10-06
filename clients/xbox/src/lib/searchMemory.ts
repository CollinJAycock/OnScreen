// The last search, kept while the app runs. Back from a result brings the
// search page up with the same query, scope and results, and focus on the
// result that was opened, as Android's search screen comes back off its back
// stack. Any other arrival (the top nav) starts empty. A module rather than
// the page's own state so sign-out can clear it: the next user must not get
// the last one's query, or a library id from a server the TV has left.

import type { Library } from './api';

export interface SearchMemory {
  query: string;
  scope: Library | null;
}

let last: SearchMemory = { query: '', scope: null };

export function lastSearch(): SearchMemory {
  return last;
}

export function rememberSearch(s: SearchMemory): void {
  last = s;
}

export function forgetSearch(): void {
  last = { query: '', scope: null };
}
