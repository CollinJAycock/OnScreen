// Scrobbling moved to the Account page (/account/scrobble) so every user can
// link their own accounts; Settings is admin-only. Old links land there.
import { redirect } from '@sveltejs/kit';

export function load() {
  redirect(308, '/account/scrobble');
}
