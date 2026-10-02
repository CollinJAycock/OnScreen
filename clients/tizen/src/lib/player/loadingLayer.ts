// "Starting playback…" while AVPlay opens a stream. Between open() and its
// first decoded frame the firmware's picture plane covers what the page
// draws over the player, so the in-page overlay doesn't show; a layer of its
// own on <body>, above everything (Jellyfin's Tizen player does the same),
// does. Built by hand: it lives outside the Svelte tree.

const ID = 'tv-loading-layer';
const KEYFRAMES_ID = 'tv-loading-keyframes';

function layer(doc: Document): HTMLDivElement {
  const found = doc.getElementById(ID) as HTMLDivElement | null;
  if (found) return found;
  const el = doc.createElement('div');
  el.id = ID;
  // Margins, not flexbox gap (Chrome 84): Tizen 5.5 runs Chromium 69.
  el.style.cssText = [
    'position: fixed',
    'top: 0',
    'left: 0',
    'right: 0',
    'bottom: 0',
    'z-index: 9999999',
    'display: none',
    'align-items: center',
    'justify-content: center',
    'flex-direction: column',
    'background: rgba(0, 0, 0, 0.75)',
    'color: #fff',
    'font-family: inherit',
  ].join(';');
  const spinner = doc.createElement('div');
  spinner.style.cssText =
    'width: 120px; height: 120px; border: 12px solid rgba(255, 255, 255, 0.2); border-top-color: #7c6af7; border-radius: 50%; animation: tv-loading-spin 0.9s linear infinite; margin-bottom: 28px';
  const label = doc.createElement('div');
  label.style.cssText = 'font-size: 36px; font-weight: 500';
  label.textContent = 'Starting playback…';
  const sub = doc.createElement('div');
  sub.setAttribute('data-sub', '');
  sub.style.cssText = 'font-size: 28px; color: rgba(255, 255, 255, 0.7); margin-top: 28px';
  el.append(spinner, label, sub);
  if (!doc.getElementById(KEYFRAMES_ID)) {
    const style = doc.createElement('style');
    style.id = KEYFRAMES_ID;
    style.textContent = '@keyframes tv-loading-spin { to { transform: rotate(360deg); } }';
    doc.head.appendChild(style);
  }
  doc.body.appendChild(el);
  return el;
}

/** Show the layer, with `subtitle` (the item's title) under the label. */
export function showLoadingLayer(subtitle = '', doc: Document = document): void {
  const el = layer(doc);
  const sub = el.querySelector<HTMLElement>('[data-sub]');
  // Text, never markup: the title comes from the server.
  if (sub) sub.textContent = subtitle;
  el.style.display = 'flex';
}

export function hideLoadingLayer(doc: Document = document): void {
  const el = doc.getElementById(ID);
  if (el) el.style.display = 'none';
}

/** Remove the layer (the player is gone). */
export function removeLoadingLayer(doc: Document = document): void {
  doc.getElementById(ID)?.remove();
}
