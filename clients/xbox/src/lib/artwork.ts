// Clear logos (v2.6): an item's logo_path is a transparent PNG served through
// /artwork/ like its poster. Pure; unit-tested.

/** The types whose detail page shows the logo in place of the title. */
const LOGO_TYPES = new Set(['movie', 'show']);

/** The API path of an item's clear logo, '' without one. Never resized:
 *  ?w= / ?h= re-encode the image as JPEG, which loses the transparency and
 *  draws the logo on a black box. */
export function logoArtworkPath(logoPath: string | null | undefined): string {
  const p = (logoPath ?? '').trim();
  return p ? `/artwork/${p}` : '';
}

/** Whether a detail page (or the player) shows `type`'s logo in place of
 *  its title: movies and shows. */
export function showsLogo(type: string | null | undefined): boolean {
  return LOGO_TYPES.has(type ?? '');
}
