// Photo-library helpers shared by the photo pages: which URL shows a photo
// (camera RAW and other formats browsers can't draw go through the server's
// resize endpoint), face crops, the original-download file of a RAW + JPEG
// pair, and the memories selection the viewer walks.

import { assetUrl, type ItemFile } from '$lib/api';

/** Camera RAW extensions the server recognises (internal/rawpreview). */
export const RAW_EXTENSIONS = ['cr2', 'cr3', 'nef', 'arw', 'dng', 'raf', 'orf', 'rw2', 'pef'] as const;

// Formats every browser draws and the /artwork route serves as-is.
const BROWSER_IMAGE_EXTS = new Set(['jpg', 'jpeg', 'png', 'gif', 'webp', 'bmp', 'avif']);

export function extOf(path: string | undefined | null): string {
  if (!path) return '';
  const base = path.split(/[\\/]/).pop() ?? '';
  const dot = base.lastIndexOf('.');
  return dot > 0 ? base.slice(dot + 1).toLowerCase() : '';
}

/** Whether the /artwork route can serve this file to a browser as-is. */
export function isBrowserImage(path: string | undefined | null): boolean {
  return BROWSER_IMAGE_EXTS.has(extOf(path));
}

export function isRawPath(path: string | undefined | null): boolean {
  return (RAW_EXTENSIONS as readonly string[]).includes(extOf(path));
}

export interface PhotoRef {
  id: string;
  poster_path?: string;
  updated_at?: string | number;
}

export interface PhotoImageOpts {
  w?: number;
  h?: number;
  fit?: 'cover' | 'contain';
}

/** The server-side render of a photo: upright, resized, JPEG. Works for
 *  every format the scanner indexes, RAW included. */
export function serverImageUrl(itemId: string, opts: PhotoImageOpts = {}, version?: string | number): string {
  const qs = new URLSearchParams();
  if (opts.w) qs.set('w', String(opts.w));
  if (opts.h) qs.set('h', String(opts.h));
  if (opts.fit) qs.set('fit', opts.fit);
  if (version !== undefined) qs.set('v', String(version));
  const q = qs.toString();
  return assetUrl(`/api/v1/items/${encodeURIComponent(itemId)}/image${q ? `?${q}` : ''}`);
}

/** URL to show a photo. A browser-drawable original goes through /artwork
 *  as before (full size when no width is asked for); a camera RAW, HEIC or
 *  TIFF goes through the server's render, which decodes it. */
export function photoImageUrl(p: PhotoRef, opts: PhotoImageOpts = {}): string {
  const path = p.poster_path;
  if (path && BROWSER_IMAGE_EXTS.has(extOf(path))) {
    const qs = new URLSearchParams();
    if (p.updated_at !== undefined) qs.set('v', String(p.updated_at));
    if (opts.w) qs.set('w', String(opts.w));
    if (opts.h) qs.set('h', String(opts.h));
    return assetUrl(`/artwork/${encodeURI(path)}?${qs.toString()}`);
  }
  // Full-size viewing of a format the browser can't draw: the largest
  // render the server makes.
  const o = opts.w || opts.h ? opts : { w: 4096, h: 4096 };
  return serverImageUrl(p.id, o, p.updated_at);
}

/** The file "Download original" serves: the RAW of a RAW + JPEG pair, else
 *  the photo's own file. */
export function originalFile(files: ItemFile[] | undefined): ItemFile | undefined {
  if (!files?.length) return undefined;
  return files.find((f) => (RAW_EXTENSIONS as readonly string[]).includes((f.container ?? '').toLowerCase())) ?? files[0];
}

export interface FaceBox {
  x: number;
  y: number;
  w: number;
  h: number;
}

/** Grow a face box by pad (fraction of its size) on every side, clamped to
 *  the photo, so a crop shows some hair and chin. */
export function padBox(b: FaceBox, pad = 0.3): FaceBox {
  const x0 = Math.max(0, b.x - b.w * pad);
  const y0 = Math.max(0, b.y - b.h * pad);
  const x1 = Math.min(1, b.x + b.w * (1 + pad));
  const y1 = Math.min(1, b.y + b.h * (1 + pad));
  return { x: x0, y: y0, w: Math.max(0.0001, x1 - x0), h: Math.max(0.0001, y1 - y0) };
}

/** Inline style that crops a face out of `url` into its (square) element:
 *  the image is scaled so the box fills the element, then shifted to it. */
export function faceCropStyle(url: string, box: FaceBox, pad = 0.3): string {
  const b = padBox(box, pad);
  const sizeX = 100 / b.w;
  const sizeY = 100 / b.h;
  const posX = b.w >= 1 ? 0 : (b.x / (1 - b.w)) * 100;
  const posY = b.h >= 1 ? 0 : (b.y / (1 - b.h)) * 100;
  const safe = url.replace(/["\\]/g, (c) => `\\${c}`);
  return `background-image:url("${safe}");background-size:${sizeX.toFixed(2)}% ${sizeY.toFixed(2)}%;background-position:${posX.toFixed(2)}% ${posY.toFixed(2)}%`;
}

/** The viewer's local date as YYYY-MM-DD (memories follow the viewer's
 *  "today", not the server's). */
export function localISODate(d = new Date()): string {
  const p = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}

// ── memories selection ────────────────────────────────────────────────────
// The photos of an opened memory, remembered for the viewer's Previous /
// Next and slideshow (per tab, like the map selection).

const MEMORY_KEY_PREFIX = 'onscreen_memory:';

function storage(): Storage | null {
  try {
    return typeof sessionStorage === 'undefined' ? null : sessionStorage;
  } catch {
    return null;
  }
}

export function saveMemorySelection(key: string, ids: string[]): void {
  try {
    storage()?.setItem(MEMORY_KEY_PREFIX + key, JSON.stringify(ids));
  } catch { /* not remembered */ }
}

export function loadMemorySelection(key: string): string[] | null {
  try {
    const raw = storage()?.getItem(MEMORY_KEY_PREFIX + key);
    if (!raw) return null;
    const v = JSON.parse(raw);
    if (Array.isArray(v)) {
      const ids = v.filter((x): x is string => typeof x === 'string');
      return ids.length > 0 ? ids : null;
    }
  } catch { /* unreadable */ }
  return null;
}
