// What the on-screen keyboard shows for a secret (a password, a TOTP or
// recovery code, a ListenBrainz token): one bullet per character typed, so the
// user can count what they entered without the room reading it off the TV.
// Android masks the same three fields.

export const MASK_CHAR = '•'; // •

/** One bullet per character of `value`. Counts code points, so an emoji or
 *  other astral character (two UTF-16 units) is still one bullet. */
export function maskText(value: string, bullet: string = MASK_CHAR): string {
  if (!value) return '';
  return bullet.repeat(Array.from(value).length);
}
