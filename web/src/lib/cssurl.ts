/**
 * cssUrl renders a URL as a CSS `url("…")` value that is safe to interpolate
 * into an inline `style` attribute.
 *
 * Svelte escapes the attribute for HTML, but the HTML parser decodes that
 * escaping again before the CSS parser sees the value — so a quote, paren or
 * backslash in the URL (artwork paths come from library file names, which an
 * uploader controls) could close the `url()` and inject further declarations.
 * Percent-encoding those characters keeps the URL identical to the server
 * (which decodes the path) while giving CSS nothing to break out with.
 */
export function cssUrl(u: string): string {
  const safe = u.replace(/["'()\\\s]/g, (c) =>
    /['()]/.test(c)
      ? '%' + c.charCodeAt(0).toString(16).toUpperCase().padStart(2, '0')
      : encodeURIComponent(c)
  );
  return `url("${safe}")`;
}
