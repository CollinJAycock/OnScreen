// Prepares an image an admin picked as a collection cover for upload: scaled
// to fit the server's cover box and re-encoded as a JPEG under the API's
// 1 MB request limit. The server re-encodes it again; this only keeps big
// photos from being refused.

export const COVER_MAX_W = 1000;
export const COVER_MAX_H = 1500;
export const COVER_MAX_BYTES = 1024 * 1024 - 4096; // headroom under the 1 MB body cap

/** The size w×h scales to so it fits in maxW×maxH (never enlarged). */
export function fitWithin(w: number, h: number, maxW = COVER_MAX_W, maxH = COVER_MAX_H): { w: number; h: number } {
  if (w <= 0 || h <= 0) return { w: 0, h: 0 };
  const scale = Math.min(1, maxW / w, maxH / h);
  return { w: Math.max(1, Math.round(w * scale)), h: Math.max(1, Math.round(h * scale)) };
}

/** Decodes, scales and JPEG-encodes a picked file. Lowers the quality until
 *  it fits COVER_MAX_BYTES; rejects a file the browser can't decode. */
export async function fitCoverImage(file: Blob): Promise<Blob> {
  const bitmap = await createImageBitmap(file);
  try {
    const { w, h } = fitWithin(bitmap.width, bitmap.height);
    const canvas = document.createElement('canvas');
    canvas.width = w;
    canvas.height = h;
    const ctx = canvas.getContext('2d');
    if (!ctx) throw new Error('Could not prepare the image');
    ctx.drawImage(bitmap, 0, 0, w, h);
    for (const quality of [0.9, 0.8, 0.7, 0.6, 0.5]) {
      const blob = await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, 'image/jpeg', quality));
      if (blob && blob.size <= COVER_MAX_BYTES) return blob;
    }
    throw new Error('The image is too large to upload');
  } finally {
    bitmap.close();
  }
}
