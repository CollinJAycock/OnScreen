// Saving a file the server sent (an export) from a Blob, and the OPDS
// catalog address shown on the Account page.

/** Hand a Blob to the browser as a download named `filename`. */
export function saveBlob(blob: Blob, filename: string, doc: Document = document): void {
  const url = URL.createObjectURL(blob);
  const a = doc.createElement('a');
  a.href = url;
  a.download = filename;
  doc.body.appendChild(a);
  a.click();
  a.remove();
  // Revoke on the next tick: some browsers start the download after click()
  // returns.
  setTimeout(() => URL.revokeObjectURL(url), 0);
}

/** The OPDS catalog URL for this server. `apiBase` is the API client's base
 *  ("/api/v1" in the browser, an absolute URL in the desktop app). */
export function opdsCatalogUrl(apiBase: string, origin: string): string {
  if (/^https?:\/\//i.test(apiBase)) {
    return apiBase.replace(/\/api\/v1\/?$/, '').replace(/\/+$/, '') + '/opds/';
  }
  return origin.replace(/\/+$/, '') + '/opds/';
}
