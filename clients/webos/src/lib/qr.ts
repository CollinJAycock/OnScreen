// Minimal QR Code encoder (ISO/IEC 18004, model 2): byte mode, UTF-8,
// versions 1–40, error-correction levels L/M/Q/H, automatic mask choice.
// Dependency-free so the TV bundles stay small — the Scrobbling screen
// renders the Trakt / Last.fm link URL as a code the user scans with a
// phone instead of typing it. Pure: no DOM, no Svelte; the QrCode component
// turns the module grid into an SVG.
//
// The structure follows the standard's construction steps (and the shape of
// Project Nayuki's reference implementation): pick the smallest version that
// fits, build the bit stream, split into Reed–Solomon blocks, interleave,
// draw the function patterns, place the codewords in the zigzag order, then
// try each of the eight masks and keep the one with the lowest penalty.
//
// Ported from Project Nayuki's QR Code generator library
// (https://www.nayuki.io/page/qr-code-generator-library), whose notice
// follows; THIRD_PARTY_NOTICES.md and lib/legal list it too.
//
//   Copyright (c) Project Nayuki. (MIT License)
//
//   Permission is hereby granted, free of charge, to any person obtaining a
//   copy of this software and associated documentation files (the
//   "Software"), to deal in the Software without restriction, including
//   without limitation the rights to use, copy, modify, merge, publish,
//   distribute, sublicense, and/or sell copies of the Software, and to
//   permit persons to whom the Software is furnished to do so, subject to
//   the following conditions:
//   - The above copyright notice and this permission notice shall be
//     included in all copies or substantial portions of the Software.
//   - The Software is provided "as is", without warranty of any kind,
//     express or implied, including but not limited to the warranties of
//     merchantability, fitness for a particular purpose and noninfringement.
//     In no event shall the authors or copyright holders be liable for any
//     claim, damages or other liability, whether in an action of contract,
//     tort or otherwise, arising from, out of or in connection with the
//     Software or the use or other dealings in the Software.

export type QrEcc = 'L' | 'M' | 'Q' | 'H';

export interface QrMatrix {
  /** Modules per side (21 for version 1, +4 per version). */
  size: number;
  version: number;
  mask: number;
  /** modules[y][x] — true = dark. Excludes the quiet zone. */
  modules: boolean[][];
}

export interface QrOptions {
  /** Force a mask (0–7) instead of choosing by penalty. */
  mask?: number;
  /** Smallest version to consider (default 1). */
  minVersion?: number;
}

// Format-info bits per level (L=01, M=00, Q=11, H=10) and table column.
const ECC_FORMAT_BITS: Record<QrEcc, number> = { L: 1, M: 0, Q: 3, H: 2 };
const ECC_ORDINAL: Record<QrEcc, number> = { L: 0, M: 1, Q: 2, H: 3 };

// Error-correction codewords per block, indexed [level][version] (index 0 unused).
const ECC_CODEWORDS_PER_BLOCK: number[][] = [
  [-1, 7, 10, 15, 20, 26, 18, 20, 24, 30, 18, 20, 24, 26, 30, 22, 24, 28, 30, 28, 28, 28, 28, 30, 30, 26, 28, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30],
  [-1, 10, 16, 26, 18, 24, 16, 18, 22, 22, 26, 30, 22, 22, 24, 24, 28, 28, 26, 26, 26, 26, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28],
  [-1, 13, 22, 18, 26, 18, 24, 18, 22, 20, 24, 28, 26, 24, 20, 30, 24, 28, 28, 26, 30, 28, 30, 30, 30, 30, 28, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30],
  [-1, 17, 28, 22, 16, 22, 28, 26, 26, 24, 28, 24, 28, 22, 24, 24, 30, 28, 28, 26, 28, 30, 24, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30],
];

// Number of error-correction blocks, indexed [level][version] (index 0 unused).
const NUM_ECC_BLOCKS: number[][] = [
  [-1, 1, 1, 1, 1, 1, 2, 2, 2, 2, 4, 4, 4, 4, 4, 6, 6, 6, 6, 7, 8, 8, 9, 9, 10, 12, 12, 12, 13, 14, 15, 16, 17, 18, 19, 19, 20, 21, 22, 24, 25],
  [-1, 1, 1, 1, 2, 2, 4, 4, 4, 5, 5, 5, 8, 9, 9, 10, 10, 11, 13, 14, 16, 17, 17, 18, 20, 21, 23, 25, 26, 28, 29, 31, 33, 35, 37, 38, 40, 43, 45, 47, 49],
  [-1, 1, 1, 2, 2, 4, 4, 6, 6, 8, 8, 8, 10, 12, 16, 12, 17, 16, 18, 21, 20, 23, 23, 25, 27, 29, 34, 34, 35, 38, 40, 43, 45, 48, 51, 53, 56, 59, 62, 65, 68],
  [-1, 1, 1, 2, 4, 4, 4, 5, 6, 8, 8, 11, 11, 16, 16, 18, 16, 19, 21, 25, 25, 25, 34, 30, 32, 35, 37, 40, 42, 45, 48, 51, 54, 57, 60, 63, 66, 70, 74, 77, 81],
];

/** Modules available for data + ECC in a version (everything but function patterns). */
function rawDataModules(ver: number): number {
  let result = (16 * ver + 128) * ver + 64;
  if (ver >= 2) {
    const numAlign = Math.floor(ver / 7) + 2;
    result -= (25 * numAlign - 10) * numAlign - 55;
    if (ver >= 7) result -= 36;
  }
  return result;
}

/** Data codewords (excluding ECC) a version holds at a level. */
function dataCodewords(ver: number, ecc: QrEcc): number {
  const o = ECC_ORDINAL[ecc];
  return Math.floor(rawDataModules(ver) / 8) - ECC_CODEWORDS_PER_BLOCK[o][ver] * NUM_ECC_BLOCKS[o][ver];
}

// ── Reed–Solomon over GF(2^8), primitive polynomial x^8+x^4+x^3+x^2+1 ──

function gfMul(x: number, y: number): number {
  let z = 0;
  for (let i = 7; i >= 0; i--) {
    z = (z << 1) ^ ((z >>> 7) * 0x11d);
    z ^= ((y >>> i) & 1) * x;
  }
  return z & 0xff;
}

/** Generator polynomial coefficients (highest power first, leading 1 dropped). */
function rsDivisor(degree: number): number[] {
  const result = new Array<number>(degree).fill(0);
  result[degree - 1] = 1;
  let root = 1;
  for (let i = 0; i < degree; i++) {
    for (let j = 0; j < result.length; j++) {
      result[j] = gfMul(result[j], root);
      if (j + 1 < result.length) result[j] ^= result[j + 1];
    }
    root = gfMul(root, 0x02);
  }
  return result;
}

/** ECC codewords for one block. Exported for the known-answer check. */
export function rsRemainder(data: readonly number[], degree: number): number[] {
  const divisor = rsDivisor(degree);
  const result = new Array<number>(degree).fill(0);
  for (const b of data) {
    const factor = b ^ (result.shift() as number);
    result.push(0);
    for (let i = 0; i < divisor.length; i++) result[i] ^= gfMul(divisor[i], factor);
  }
  return result;
}

function utf8Bytes(text: string): number[] {
  const out: number[] = [];
  for (const ch of text) {
    const cp = ch.codePointAt(0) as number;
    if (cp < 0x80) out.push(cp);
    else if (cp < 0x800) out.push(0xc0 | (cp >> 6), 0x80 | (cp & 63));
    else if (cp < 0x10000) out.push(0xe0 | (cp >> 12), 0x80 | ((cp >> 6) & 63), 0x80 | (cp & 63));
    else {
      out.push(0xf0 | (cp >> 18), 0x80 | ((cp >> 12) & 63), 0x80 | ((cp >> 6) & 63), 0x80 | (cp & 63));
    }
  }
  return out;
}

function getBit(x: number, i: number): boolean {
  return ((x >>> i) & 1) !== 0;
}

function alignmentPositions(ver: number, size: number): number[] {
  if (ver === 1) return [];
  const numAlign = Math.floor(ver / 7) + 2;
  const step = Math.floor((ver * 8 + numAlign * 3 + 5) / (numAlign * 4 - 4)) * 2;
  const result = [6];
  for (let pos = size - 7; result.length < numAlign; pos -= step) result.splice(1, 0, pos);
  return result;
}

function maskBit(mask: number, x: number, y: number): boolean {
  switch (mask) {
    case 0: return (x + y) % 2 === 0;
    case 1: return y % 2 === 0;
    case 2: return x % 3 === 0;
    case 3: return (x + y) % 3 === 0;
    case 4: return (Math.floor(x / 3) + Math.floor(y / 2)) % 2 === 0;
    case 5: return ((x * y) % 2) + ((x * y) % 3) === 0;
    case 6: return (((x * y) % 2) + ((x * y) % 3)) % 2 === 0;
    default: return (((x + y) % 2) + ((x * y) % 3)) % 2 === 0;
  }
}

/** Penalty score of a finished symbol (lower scans better). Standard rules
 *  N1 (runs of 5+), N2 (2×2 blocks), N3 (finder-like 1:1:3:1:1 with a
 *  4-module light margin) and N4 (dark/light balance). */
function penalty(m: boolean[][]): number {
  const size = m.length;
  let score = 0;
  // N1 — rows and columns.
  for (let a = 0; a < size; a++) {
    let runRow = 1;
    let runCol = 1;
    for (let b = 1; b < size; b++) {
      if (m[a][b] === m[a][b - 1]) runRow++;
      else {
        if (runRow >= 5) score += 3 + (runRow - 5);
        runRow = 1;
      }
      if (m[b][a] === m[b - 1][a]) runCol++;
      else {
        if (runCol >= 5) score += 3 + (runCol - 5);
        runCol = 1;
      }
    }
    if (runRow >= 5) score += 3 + (runRow - 5);
    if (runCol >= 5) score += 3 + (runCol - 5);
  }
  // N2 — 2×2 same-colour blocks.
  for (let y = 0; y < size - 1; y++) {
    for (let x = 0; x < size - 1; x++) {
      const c = m[y][x];
      if (c === m[y][x + 1] && c === m[y + 1][x] && c === m[y + 1][x + 1]) score += 3;
    }
  }
  // N3 — 1011101 with four light modules on either side, as an 11-module
  // sliding window (0x5D0 = 10111010000, 0x05D = 00001011101).
  for (let a = 0; a < size; a++) {
    let row = 0;
    let col = 0;
    for (let b = 0; b < size; b++) {
      row = ((row << 1) & 0x7ff) | (m[a][b] ? 1 : 0);
      col = ((col << 1) & 0x7ff) | (m[b][a] ? 1 : 0);
      if (b >= 10) {
        if (row === 0x5d0 || row === 0x05d) score += 40;
        if (col === 0x5d0 || col === 0x05d) score += 40;
      }
    }
  }
  // N4 — proportion of dark modules, 10 points per 5% away from 50%.
  let dark = 0;
  for (const r of m) for (const c of r) if (c) dark++;
  const total = size * size;
  const k = Math.ceil(Math.abs(dark * 20 - total * 10) / total) - 1;
  score += Math.max(0, k) * 10;
  return score;
}

/**
 * Encode text as a QR symbol. Returns null when it doesn't fit even
 * version 40 at the requested level.
 */
export function encodeQr(text: string, ecc: QrEcc = 'M', opts: QrOptions = {}): QrMatrix | null {
  const bytes = utf8Bytes(text);
  const o = ECC_ORDINAL[ecc];

  // 1. Smallest version whose capacity fits mode + count + data.
  let ver = Math.max(1, Math.min(40, opts.minVersion ?? 1));
  for (; ver <= 40; ver++) {
    const ccBits = ver <= 9 ? 8 : 16;
    if (4 + ccBits + bytes.length * 8 <= dataCodewords(ver, ecc) * 8) break;
  }
  if (ver > 40) return null;
  const ccBits = ver <= 9 ? 8 : 16;
  const capacityBits = dataCodewords(ver, ecc) * 8;

  // 2. Bit stream: byte mode (0100), character count, data, terminator,
  //    byte alignment, then alternating 0xEC / 0x11 pad codewords.
  const bits: number[] = [];
  const append = (val: number, len: number) => {
    for (let i = len - 1; i >= 0; i--) bits.push((val >>> i) & 1);
  };
  append(0b0100, 4);
  append(bytes.length, ccBits);
  for (const b of bytes) append(b, 8);
  append(0, Math.min(4, capacityBits - bits.length));
  append(0, (8 - (bits.length % 8)) % 8);
  for (let pad = 0xec; bits.length < capacityBits; pad ^= 0xec ^ 0x11) append(pad, 8);
  const data: number[] = [];
  for (let i = 0; i < bits.length; i += 8) {
    let v = 0;
    for (let j = 0; j < 8; j++) v = (v << 1) | bits[i + j];
    data.push(v);
  }

  // 3. Split into blocks, append ECC, interleave.
  const numBlocks = NUM_ECC_BLOCKS[o][ver];
  const blockEccLen = ECC_CODEWORDS_PER_BLOCK[o][ver];
  const rawCodewords = Math.floor(rawDataModules(ver) / 8);
  const numShortBlocks = numBlocks - (rawCodewords % numBlocks);
  const shortBlockLen = Math.floor(rawCodewords / numBlocks);
  const blocks: number[][] = [];
  for (let i = 0, k = 0; i < numBlocks; i++) {
    const dat = data.slice(k, k + shortBlockLen - blockEccLen + (i < numShortBlocks ? 0 : 1));
    k += dat.length;
    const eccWords = rsRemainder(dat, blockEccLen);
    if (i < numShortBlocks) dat.push(0); // placeholder so columns line up
    blocks.push(dat.concat(eccWords));
  }
  const codewords: number[] = [];
  for (let i = 0; i < blocks[0].length; i++) {
    for (let j = 0; j < blocks.length; j++) {
      if (i !== shortBlockLen - blockEccLen || j >= numShortBlocks) codewords.push(blocks[j][i]);
    }
  }

  // 4. Function patterns.
  const size = ver * 4 + 17;
  const modules: boolean[][] = Array.from({ length: size }, () => new Array<boolean>(size).fill(false));
  const isFunction: boolean[][] = Array.from({ length: size }, () => new Array<boolean>(size).fill(false));
  const setFn = (x: number, y: number, dark: boolean) => {
    modules[y][x] = dark;
    isFunction[y][x] = true;
  };
  for (let i = 0; i < size; i++) {
    setFn(6, i, i % 2 === 0);
    setFn(i, 6, i % 2 === 0);
  }
  const finder = (cx: number, cy: number) => {
    for (let dy = -4; dy <= 4; dy++) {
      for (let dx = -4; dx <= 4; dx++) {
        const dist = Math.max(Math.abs(dx), Math.abs(dy));
        const x = cx + dx;
        const y = cy + dy;
        if (x >= 0 && x < size && y >= 0 && y < size) setFn(x, y, dist !== 2 && dist !== 4);
      }
    }
  };
  finder(3, 3);
  finder(size - 4, 3);
  finder(3, size - 4);
  const align = alignmentPositions(ver, size);
  const last = align.length - 1;
  for (let i = 0; i < align.length; i++) {
    for (let j = 0; j < align.length; j++) {
      if ((i === 0 && j === 0) || (i === 0 && j === last) || (i === last && j === 0)) continue;
      for (let dy = -2; dy <= 2; dy++) {
        for (let dx = -2; dx <= 2; dx++) {
          setFn(align[i] + dx, align[j] + dy, Math.max(Math.abs(dx), Math.abs(dy)) !== 1);
        }
      }
    }
  }
  const drawFormat = (mask: number) => {
    const d = (ECC_FORMAT_BITS[ecc] << 3) | mask;
    let rem = d;
    for (let i = 0; i < 10; i++) rem = (rem << 1) ^ ((rem >>> 9) * 0x537);
    const fbits = ((d << 10) | rem) ^ 0x5412;
    for (let i = 0; i <= 5; i++) setFn(8, i, getBit(fbits, i));
    setFn(8, 7, getBit(fbits, 6));
    setFn(8, 8, getBit(fbits, 7));
    setFn(7, 8, getBit(fbits, 8));
    for (let i = 9; i < 15; i++) setFn(14 - i, 8, getBit(fbits, i));
    for (let i = 0; i < 8; i++) setFn(size - 1 - i, 8, getBit(fbits, i));
    for (let i = 8; i < 15; i++) setFn(8, size - 15 + i, getBit(fbits, i));
    setFn(8, size - 8, true); // the dark module
  };
  drawFormat(0); // reserve the area; redrawn with the chosen mask below
  if (ver >= 7) {
    let rem = ver;
    for (let i = 0; i < 12; i++) rem = (rem << 1) ^ ((rem >>> 11) * 0x1f25);
    const vbits = (ver << 12) | rem;
    for (let i = 0; i < 18; i++) {
      const bit = getBit(vbits, i);
      const a = size - 11 + (i % 3);
      const b = Math.floor(i / 3);
      setFn(a, b, bit);
      setFn(b, a, bit);
    }
  }

  // 5. Codewords in the two-column zigzag, skipping the vertical timing line.
  let bitIndex = 0;
  const totalBits = codewords.length * 8;
  for (let right = size - 1; right >= 1; right -= 2) {
    if (right === 6) right = 5;
    for (let vert = 0; vert < size; vert++) {
      for (let j = 0; j < 2; j++) {
        const x = right - j;
        const upward = ((right + 1) & 2) === 0;
        const y = upward ? size - 1 - vert : vert;
        if (!isFunction[y][x] && bitIndex < totalBits) {
          modules[y][x] = getBit(codewords[bitIndex >>> 3], 7 - (bitIndex & 7));
          bitIndex++;
        }
      }
    }
  }

  // 6. Mask: the forced one, or the lowest-penalty of the eight.
  const applyMask = (mask: number) => {
    for (let y = 0; y < size; y++) {
      for (let x = 0; x < size; x++) {
        if (!isFunction[y][x] && maskBit(mask, x, y)) modules[y][x] = !modules[y][x];
      }
    }
  };
  let mask = opts.mask ?? -1;
  if (mask < 0 || mask > 7) {
    let best = Infinity;
    for (let m = 0; m < 8; m++) {
      applyMask(m);
      drawFormat(m);
      const p = penalty(modules);
      if (p < best) {
        best = p;
        mask = m;
      }
      applyMask(m); // XOR again to undo
    }
  }
  applyMask(mask);
  drawFormat(mask);
  return { size, version: ver, mask, modules };
}

/** SVG path data for the dark modules, offset by a quiet zone of `margin`
 *  modules — one `M x,y h1v1h-1z` square per dark module, merged into
 *  horizontal runs to keep the string short. */
export function qrPath(qr: QrMatrix, margin = 4): string {
  const parts: string[] = [];
  for (let y = 0; y < qr.size; y++) {
    let x = 0;
    while (x < qr.size) {
      if (!qr.modules[y][x]) {
        x++;
        continue;
      }
      const start = x;
      while (x < qr.size && qr.modules[y][x]) x++;
      parts.push(`M${start + margin},${y + margin}h${x - start}v1h-${x - start}z`);
    }
  }
  return parts.join('');
}
