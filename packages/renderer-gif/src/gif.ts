// GIF frame decoding for the per-frame compare: every frame of an animated GIF
// composited onto the full canvas, as a browser would show it.
//
// Memory stays near the file's own size, however long the animation: parsing
// keeps each frame's header and the offset of its compressed data, and a frame
// is decompressed only when asked for. Compositing walks forward from the
// nearest checkpoint — a saved copy of the canvas every few frames — so playing
// in order costs one frame's work per frame, and a jump costs at most an
// interval's worth.
//
// Compositing follows what browsers do: the canvas starts transparent (the
// background colour index is ignored), a frame's transparent index leaves the
// pixel under it, and disposal 2 clears the frame's rectangle to transparent
// while 3 restores what was under it. A delay of 0 or 1 centisecond plays as
// 100 ms. Truncated data draws as much of the frame as arrived.

import type { AnimatedImage } from "@fhr/renderer-sdk/image";

/** A header may claim 65535 × 65535; refuse canvases or frames past this. */
const MAX_PIXELS = 50_000_000;
const MAX_FRAMES = 10_000;
/** Memory for checkpoints; the interval between them grows to stay within it. */
const CHECKPOINT_BYTES = 64_000_000;

type RawFrame = {
  x: number;
  y: number;
  w: number;
  h: number;
  interlaced: boolean;
  palette: Uint8Array | null;
  /** The transparent colour index, or -1. */
  transparent: number;
  /** 0–1 keep, 2 clear to transparent, 3 restore the previous canvas. */
  disposal: number;
  delayMs: number;
  minCodeSize: number;
  /** Offset of the first data sub-block's length byte. */
  data: number;
};

export type ParsedGif = { width: number; height: number; frames: RawFrame[] };

/** Read a GIF's structure without decompressing any frame. Null when it isn't one. */
export function parseGif(bytes: Uint8Array): ParsedGif | null {
  const len = bytes.length;
  if (len < 13) return null;
  const sig = String.fromCharCode(...bytes.subarray(0, 6));
  if (sig !== "GIF87a" && sig !== "GIF89a") return null;
  const u16 = (at: number): number => bytes[at]! | (bytes[at + 1]! << 8);
  let width = u16(6);
  let height = u16(8);
  const packed = bytes[10]!;
  let p = 13;
  let global: Uint8Array | null = null;
  if (packed & 0x80) {
    const n = 3 << ((packed & 7) + 1);
    global = bytes.subarray(p, Math.min(len, p + n));
    p += n;
  }

  const frames: RawFrame[] = [];
  let control = { disposal: 0, delayCs: 0, transparent: -1 };
  while (p < len) {
    const block = bytes[p++]!;
    if (block === 0x3b) break; // trailer
    if (block === 0x21) {
      const label = bytes[p++];
      if (label === 0xf9 && p + 5 <= len && bytes[p]! >= 4) {
        const flags = bytes[p + 1]!;
        control = { disposal: (flags >> 2) & 7, delayCs: u16(p + 2), transparent: flags & 1 ? bytes[p + 4]! : -1 };
      }
      p = skipSubBlocks(bytes, p);
      continue;
    }
    if (block !== 0x2c || p + 9 > len) break; // garbage or truncated: keep what was read
    const x = u16(p);
    const y = u16(p + 2);
    const w = u16(p + 4);
    const h = u16(p + 6);
    const flags = bytes[p + 8]!;
    p += 9;
    let palette = global;
    if (flags & 0x80) {
      const n = 3 << ((flags & 7) + 1);
      palette = bytes.subarray(p, Math.min(len, p + n));
      p += n;
    }
    if (p >= len) break;
    const minCodeSize = bytes[p++]!;
    frames.push({
      x,
      y,
      w,
      h,
      interlaced: (flags & 0x40) !== 0,
      palette,
      transparent: control.transparent,
      disposal: control.disposal,
      delayMs: control.delayCs <= 1 ? 100 : control.delayCs * 10,
      minCodeSize,
      data: p,
    });
    if (frames.length > MAX_FRAMES || w * h > MAX_PIXELS) return null;
    p = skipSubBlocks(bytes, p);
    control = { disposal: 0, delayCs: 0, transparent: -1 }; // a control block covers one image
  }
  if (!frames.length) return null;
  // A zero-sized logical screen: take the first frame's extent, as browsers do.
  if (!width || !height) {
    width = frames[0]!.x + frames[0]!.w;
    height = frames[0]!.y + frames[0]!.h;
  }
  if (!width || !height || width * height > MAX_PIXELS) return null;
  return { width, height, frames };
}

/** Decode a GIF for the compare; null when the bytes aren't a GIF this can read. */
export function decodeGif(bytes: Uint8Array): AnimatedImage | null {
  const gif = parseGif(bytes);
  return gif ? new GifAnimation(bytes, gif) : null;
}

class GifAnimation implements AnimatedImage {
  readonly width: number;
  readonly height: number;
  private readonly frames: RawFrame[];
  private readonly interval: number;
  /** The canvas before frame `next` is drawn. */
  private state: Uint8ClampedArray;
  private next = 0;
  private readonly out: Uint8ClampedArray;
  private outIndex = -1;
  private readonly checkpoints = new Map<number, Uint8ClampedArray>();
  private readonly indices: Uint8Array;

  constructor(private readonly bytes: Uint8Array, gif: ParsedGif) {
    this.width = gif.width;
    this.height = gif.height;
    this.frames = gif.frames;
    const canvas = gif.width * gif.height * 4;
    this.state = new Uint8ClampedArray(canvas);
    this.out = new Uint8ClampedArray(canvas);
    this.interval = Math.max(16, Math.ceil((gif.frames.length * canvas) / CHECKPOINT_BYTES));
    this.indices = new Uint8Array(Math.max(1, ...gif.frames.map((f) => f.w * f.h)));
  }

  get frameCount(): number {
    return this.frames.length;
  }

  delayMs(index: number): number {
    return this.frames[index]?.delayMs ?? 100;
  }

  frame(index: number): Uint8ClampedArray {
    if (index === this.outIndex) return this.out;
    if (index < this.next) this.rewind(index);
    while (this.next < index) this.step();
    const f = this.frames[index]!;
    if (f.disposal === 2 || f.disposal === 3) {
      this.out.set(this.state);
      this.draw(f, this.out);
    } else {
      // Nothing to undo afterwards: the canvas after this frame is the frame.
      this.step();
      this.out.set(this.state);
    }
    this.outIndex = index;
    return this.out;
  }

  private rewind(index: number): void {
    let at = Math.floor(index / this.interval) * this.interval;
    while (at > 0 && !this.checkpoints.has(at)) at -= this.interval;
    if (at > 0) this.state.set(this.checkpoints.get(at)!);
    else this.state.fill(0);
    this.next = at;
  }

  /** Draw frame `next` onto the canvas and dispose of it. */
  private step(): void {
    const f = this.frames[this.next]!;
    const under = f.disposal === 3 ? this.copyRect(f) : null;
    this.draw(f, this.state);
    if (f.disposal === 2) this.clearRect(f);
    else if (under) this.pasteRect(f, under);
    this.next++;
    if (this.next % this.interval === 0 && !this.checkpoints.has(this.next)) {
      this.checkpoints.set(this.next, this.state.slice());
    }
  }

  private draw(f: RawFrame, target: Uint8ClampedArray): void {
    const pal = f.palette;
    if (!pal || !f.w || !f.h) return;
    const n = lzwDecode(this.bytes, f.data, f.minCodeSize, this.indices, f.w * f.h);
    const rows = f.interlaced ? interlacedRows(f.h) : null;
    const W = this.width;
    const H = this.height;
    const idx = this.indices;
    for (let r = 0, p = 0; r < f.h && p < n; r++) {
      const y = f.y + (rows ? rows[r]! : r);
      if (y >= H) {
        p += f.w;
        continue;
      }
      for (let c = 0; c < f.w && p < n; c++, p++) {
        const x = f.x + c;
        const i = idx[p]!;
        if (x >= W || i === f.transparent) continue;
        const pi = i * 3;
        if (pi + 2 >= pal.length) continue;
        const o = (y * W + x) * 4;
        target[o] = pal[pi]!;
        target[o + 1] = pal[pi + 1]!;
        target[o + 2] = pal[pi + 2]!;
        target[o + 3] = 255;
      }
    }
  }

  /** The frame's rectangle, clipped to the canvas, as [start, end) offsets per row. */
  private *rowSpans(f: RawFrame): Generator<[number, number]> {
    const x0 = Math.min(f.x, this.width);
    const x1 = Math.min(f.x + f.w, this.width);
    const y1 = Math.min(f.y + f.h, this.height);
    for (let y = f.y; y < y1; y++) yield [(y * this.width + x0) * 4, (y * this.width + x1) * 4];
  }

  private copyRect(f: RawFrame): Uint8ClampedArray[] {
    return [...this.rowSpans(f)].map(([a, b]) => this.state.slice(a, b));
  }

  private pasteRect(f: RawFrame, rows: Uint8ClampedArray[]): void {
    let i = 0;
    for (const [a] of this.rowSpans(f)) this.state.set(rows[i++]!, a);
  }

  private clearRect(f: RawFrame): void {
    for (const [a, b] of this.rowSpans(f)) this.state.fill(0, a, b);
  }
}

function skipSubBlocks(bytes: Uint8Array, p: number): number {
  while (p < bytes.length) {
    const n = bytes[p++]!;
    if (n === 0) break;
    p += n;
  }
  return p;
}

/** Display row of each row in an interlaced image's data order. */
function interlacedRows(h: number): Uint32Array {
  const rows = new Uint32Array(h);
  let i = 0;
  for (const [start, step] of [[0, 8], [4, 8], [2, 4], [1, 2]] as const) {
    for (let y = start; y < h; y += step) rows[i++] = y;
  }
  return rows;
}

// LZW tables, shared: decoding is synchronous and never re-entered.
const prefix = new Uint16Array(4096);
const suffix = new Uint8Array(4096);
const stack = new Uint8Array(4097);

/**
 * Decompress GIF image data (sub-blocks starting at `at`) into `out`, at most
 * `count` indices. Returns how many it produced: fewer when the data is
 * truncated or corrupt.
 */
export function lzwDecode(bytes: Uint8Array, at: number, minCodeSize: number, out: Uint8Array, count: number): number {
  if (minCodeSize < 1 || minCodeSize > 11) return 0;
  const clear = 1 << minCodeSize;
  const eoi = clear + 1;
  for (let i = 0; i < clear; i++) suffix[i] = i;
  let size = minCodeSize + 1;
  let mask = (1 << size) - 1;
  let avail = clear + 2;
  let old = -1;
  let first = 0;
  let datum = 0;
  let bits = 0;
  let produced = 0;
  let pos = at;
  let end = at; // end of the current sub-block
  const len = bytes.length;

  decode: while (produced < count) {
    while (bits < size) {
      if (pos >= end) {
        if (end >= len) break decode;
        const n = bytes[end]!;
        if (n === 0) break decode;
        pos = end + 1;
        end = Math.min(len, pos + n);
        if (pos >= end) break decode;
      }
      datum |= bytes[pos++]! << bits;
      bits += 8;
    }
    let code = datum & mask;
    datum >>>= size;
    bits -= size;

    if (code === clear) {
      size = minCodeSize + 1;
      mask = (1 << size) - 1;
      avail = clear + 2;
      old = -1;
      continue;
    }
    if (code === eoi) break;
    if (old === -1) {
      if (code > clear) break;
      out[produced++] = code;
      old = first = code;
      continue;
    }
    const incoming = code;
    let sp = 0;
    if (code >= avail) {
      if (code > avail) break; // a code that can't exist yet: corrupt
      stack[sp++] = first;
      code = old;
    }
    while (code > eoi) {
      stack[sp++] = suffix[code]!;
      code = prefix[code]!;
    }
    first = code;
    stack[sp++] = first;
    if (avail < 4096) {
      prefix[avail] = old;
      suffix[avail] = first;
      avail++;
      if (avail === mask + 1 && size < 12) {
        size++;
        mask = (1 << size) - 1;
      }
    }
    old = incoming;
    while (sp > 0 && produced < count) out[produced++] = stack[--sp]!;
  }
  return produced;
}
