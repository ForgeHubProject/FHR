import { describe, it, expect } from "vitest";
import { decodeGif, parseGif } from "./gif.js";

// Four frames written by Go's image/gif encoder (an independent LZW and
// container writer), on a 6 × 4 canvas with palette [clear, red, green, blue]:
//   0: the whole canvas red but a transparent (5,3); keep it;      5 cs
//   1: green over (2,1)–(4,2), its (3,2) transparent; restore after; 0 cs
//   2: blue over (0,0)–(1,1); clear it to transparent after;       20 cs
//   3: one green pixel at (4,0); keep it;                           1 cs
const GO_FIXTURE =
  "R0lGODlhBgAEAIEAAAAAAP8AAAD/AAAA/yH/C05FVFNDQVBFMi4wAwEAAAAh+QQFBQAAACwAAAAABgAEAAACBYyPqQYFACH5BA0AAAAALAIAAQADAAIAAAIDlAVSACH5BAkUAAAALAAAAAACAAIAAAICnFcAIfkEBQEAAAAsBAAAAAEAAQAAAgJUAQA7";

const bytes = (b64: string) => Uint8Array.from(atob(b64), (c) => c.charCodeAt(0));

/** A frame as rows of letters: R/G/B for the opaque colours, . for transparent. */
function picture(rgba: Uint8ClampedArray, w: number): string[] {
  const rows: string[] = [];
  for (let i = 0, row = ""; i < rgba.length; i += 4) {
    const [r, g, b, a] = [rgba[i], rgba[i + 1], rgba[i + 2], rgba[i + 3]];
    row += a === 0 ? "." : r === 255 && !g && !b ? "R" : g === 255 && !r && !b ? "G" : b === 255 && !r && !g ? "B" : "?";
    if (row.length === w) {
      rows.push(row);
      row = "";
    }
  }
  return rows;
}

describe("decodeGif", () => {
  it("composites each frame as a browser shows it: keep, restore, clear, transparency", () => {
    const gif = decodeGif(bytes(GO_FIXTURE))!;
    expect([gif.width, gif.height, gif.frameCount]).toEqual([6, 4, 4]);
    expect(picture(gif.frame(0), 6)).toEqual(["RRRRRR", "RRRRRR", "RRRRRR", "RRRRR."]);
    expect(picture(gif.frame(1), 6)).toEqual(["RRRRRR", "RRGGGR", "RRGRGR", "RRRRR."]);
    // Frame 1 restored what was under it, so frame 2 lands on frame 0.
    expect(picture(gif.frame(2), 6)).toEqual(["BBRRRR", "BBRRRR", "RRRRRR", "RRRRR."]);
    // Frame 2 cleared its rectangle to transparent, not to a background colour.
    expect(picture(gif.frame(3), 6)).toEqual(["..RRGR", "..RRRR", "RRRRRR", "RRRRR."]);
  });

  it("plays 0 and 1 centisecond delays at 100 ms, as browsers do", () => {
    const gif = decodeGif(bytes(GO_FIXTURE))!;
    expect([0, 1, 2, 3].map((i) => gif.delayMs(i))).toEqual([50, 100, 200, 100]);
  });

  it("gives the same frame whatever order frames are asked for in", () => {
    const inOrder = decodeGif(bytes(GO_FIXTURE))!;
    const expected = [0, 1, 2, 3].map((i) => picture(inOrder.frame(i), 6));
    const jumping = decodeGif(bytes(GO_FIXTURE))!;
    for (const i of [3, 1, 1, 0, 2, 3, 0]) expect(picture(jumping.frame(i), 6)).toEqual(expected[i]);
  });

  it("rewinds through checkpoints in a long animation", () => {
    // 70 frames (checkpoints every 16) cycling all four disposals.
    const frames: FrameSpec[] = Array.from({ length: 70 }, (_, i) => ({
      x: i % 5,
      y: i % 3,
      w: 3,
      h: 2,
      indices: [1 + (i % 3), 0, 2, 3, 1 + ((i + 1) % 3), 0],
      transparent: 0,
      disposal: i % 4,
    }));
    const gif = build(8, 5, PALETTE, frames);
    const seq = decodeGif(gif)!;
    const expected = frames.map((_, i) => Array.from(seq.frame(i)));
    const jumping = decodeGif(gif)!;
    for (const i of [69, 3, 40, 16, 15, 32, 0, 68, 17, 48, 47, 64]) expect(Array.from(jumping.frame(i))).toEqual(expected[i]);
  });

  it("decodes a full code table, with a clear or with the clear deferred", () => {
    const random = generator(1);
    const indices = Array.from({ length: 120 * 90 }, () => random(256));
    const palette = Array.from({ length: 256 }, (_, i) => [i, 255 - i, (i * 7) % 256]);
    for (const deferClear of [false, true]) {
      const gif = decodeGif(build(120, 90, palette, [{ w: 120, h: 90, indices, deferClear }]))!;
      const rgba = gif.frame(0);
      const wrong = indices.findIndex((k, p) => rgba[p * 4] !== k || rgba[p * 4 + 1] !== 255 - k || rgba[p * 4 + 3] !== 255);
      expect(wrong).toBe(-1);
    }
  });

  it("reads every entry of a full table, the last one too", () => {
    // With the clear deferred, a full table stays in use — and its last entry,
    // 4095, is the one an off-by-one drops.
    // (Which data reuses that entry is luck of the parse; this seed does, 14 times.)
    const random = generator(2);
    const indices = Array.from({ length: 300 * 300 }, () => random(4));
    const stats = { last: 0 };
    const gif = decodeGif(build(300, 300, PALETTE, [{ w: 300, h: 300, indices, deferClear: true, stats }]))!;
    expect(stats.last).toBeGreaterThan(0);
    const rgba = gif.frame(0);
    const wrong = indices.findIndex((k, p) => [0, 1, 2].some((c) => rgba[p * 4 + c] !== PALETTE[k]![c]) || rgba[p * 4 + 3] !== 255);
    expect(wrong).toBe(-1);
  });

  it("puts an interlaced image's rows back in display order", () => {
    const indices = Array.from({ length: 2 * 11 }, (_, p) => 1 + (Math.floor(p / 2) % 3));
    const gif = decodeGif(build(2, 11, PALETTE, [{ w: 2, h: 11, indices, interlaced: true }]))!;
    expect(picture(gif.frame(0), 2)).toEqual(["RR", "GG", "BB", "RR", "GG", "BB", "RR", "GG", "BB", "RR", "GG"]);
  });

  it("uses a frame's own palette over the global one", () => {
    const gif = decodeGif(
      build(2, 1, PALETTE, [
        { w: 2, h: 1, indices: [1, 2] },
        { w: 2, h: 1, indices: [1, 2], palette: [[0, 0, 0], [0, 0, 255], [255, 0, 0], [0, 0, 0]] },
      ]),
    )!;
    expect(picture(gif.frame(0), 2)).toEqual(["RG"]);
    expect(picture(gif.frame(1), 2)).toEqual(["BR"]);
  });

  it("draws as much of a truncated frame as arrived", () => {
    const whole = build(10, 10, PALETTE, [{ w: 10, h: 10, indices: Array(100).fill(1) }]);
    // Cut inside the image data: header + palette + descriptor + a few data bytes.
    const cut = whole.subarray(0, whole.length - 8);
    const gif = decodeGif(cut)!;
    const rows = picture(gif.frame(0), 10);
    expect(rows[0]).toBe("RRRRRRRRRR");
    expect(rows[9]).toBe("..........");
  });

  it("refuses what isn't a GIF, or claims a canvas too large to hold", () => {
    expect(decodeGif(new TextEncoder().encode("PNG not a GIF at all"))).toBeNull();
    expect(decodeGif(new Uint8Array(0))).toBeNull();
    const huge = build(1, 1, PALETTE, [{ w: 1, h: 1, indices: [1] }]);
    huge.set([0xff, 0xff, 0xff, 0xff], 6); // 65535 × 65535
    expect(decodeGif(huge)).toBeNull();
  });

  it("sizes a zero-sized canvas from the first frame", () => {
    const gif = build(0, 0, PALETTE, [{ x: 1, y: 1, w: 3, h: 2, indices: Array(6).fill(2) }]);
    expect(parseGif(gif)).toMatchObject({ width: 4, height: 3 });
  });
});

// ---- a small GIF writer for the cases above ----

/** A seeded LCG; its high bits, since an LCG's low bits repeat with a short period. */
function generator(seed: number): (m: number) => number {
  return (m) => ((seed = (seed * 1103515245 + 12345) % 2 ** 31) >>> 16) % m;
}

const PALETTE = [[0, 0, 0], [255, 0, 0], [0, 255, 0], [0, 0, 255]];

type FrameSpec = {
  x?: number;
  y?: number;
  w: number;
  h: number;
  indices: number[];
  palette?: number[][];
  transparent?: number;
  disposal?: number;
  delayCs?: number;
  interlaced?: boolean;
  deferClear?: boolean;
  /** Counts how often the encoder wrote code 4095, the table's last entry. */
  stats?: { last: number };
};

function build(width: number, height: number, global: number[][], frames: FrameSpec[]): Uint8Array {
  const out: number[] = [];
  const u16 = (n: number) => out.push(n & 0xff, n >> 8);
  const table = (pal: number[][]) => {
    const bits = Math.max(1, Math.ceil(Math.log2(pal.length)));
    for (let i = 0; i < 1 << bits; i++) out.push(...(pal[i] ?? [0, 0, 0]));
    return bits;
  };
  const bitsFor = (pal: number[][]) => Math.max(1, Math.ceil(Math.log2(pal.length)));
  out.push(..."GIF89a".split("").map((c) => c.charCodeAt(0)));
  u16(width);
  u16(height);
  out.push(0x80 | (bitsFor(global) - 1), 0, 0);
  table(global);
  for (const f of frames) {
    out.push(0x21, 0xf9, 4, ((f.disposal ?? 0) << 2) | (f.transparent !== undefined ? 1 : 0));
    u16(f.delayCs ?? 10);
    out.push(f.transparent ?? 0, 0);
    out.push(0x2c);
    u16(f.x ?? 0);
    u16(f.y ?? 0);
    u16(f.w);
    u16(f.h);
    const pal = f.palette ?? global;
    out.push((f.palette ? 0x80 | (bitsFor(pal) - 1) : 0) | (f.interlaced ? 0x40 : 0));
    if (f.palette) table(f.palette);
    let indices = f.indices;
    if (f.interlaced) {
      const rows: number[] = [];
      for (const [start, step] of [[0, 8], [4, 8], [2, 4], [1, 2]]) for (let y = start!; y < f.h; y += step!) rows.push(y);
      indices = rows.flatMap((y) => f.indices.slice(y * f.w, (y + 1) * f.w));
    }
    const minCodeSize = Math.max(2, bitsFor(pal));
    out.push(minCodeSize);
    const data = lzwEncode(indices, minCodeSize, f.deferClear ?? false, f.stats);
    for (let i = 0; i < data.length; i += 255) {
      const chunk = data.slice(i, i + 255);
      out.push(chunk.length, ...chunk);
    }
    out.push(0);
  }
  out.push(0x3b);
  return Uint8Array.from(out);
}

/**
 * The textbook GIF LZW encoder: codes widen as the table grows; a full table
 * is reset with a clear code, or with `deferClear` kept as it is while 12-bit
 * codes carry on (both are valid GIF, and both appear in the wild).
 */
function lzwEncode(indices: number[], minCodeSize: number, deferClear: boolean, stats?: { last: number }): number[] {
  const clear = 1 << minCodeSize;
  const out: number[] = [];
  let acc = 0;
  let nbits = 0;
  let size = minCodeSize + 1;
  const emit = (code: number) => {
    if (code === 4095 && stats) stats.last++;
    acc |= code << nbits;
    nbits += size;
    while (nbits >= 8) {
      out.push(acc & 0xff);
      acc >>>= 8;
      nbits -= 8;
    }
  };
  let dict = new Map<number, number>();
  let next = clear + 2;
  emit(clear);
  let prefix = indices[0]!;
  for (let i = 1; i < indices.length; i++) {
    const k = indices[i]!;
    const key = prefix * 256 + k;
    const known = dict.get(key);
    if (known !== undefined) {
      prefix = known;
      continue;
    }
    emit(prefix);
    if (next < 4096) {
      dict.set(key, next++);
      if (next > 1 << size && size < 12) size++;
    } else if (!deferClear) {
      emit(clear);
      dict = new Map();
      next = clear + 2;
      size = minCodeSize + 1;
    }
    prefix = k;
  }
  emit(prefix);
  emit(clear + 1);
  if (nbits > 0) out.push(acc & 0xff);
  return out;
}
