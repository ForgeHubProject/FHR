import { describe, it, expect } from "vitest";
import { CHUNK_KEYWORD, readLayout } from "./layout.js";

/** A PNG with nothing but a signature and one tEXt chunk (the reader needs no more). */
function png(keyword: string, text: string): Uint8Array {
  const data = new TextEncoder().encode(`${keyword}\0${text}`);
  const out = new Uint8Array(8 + 12 + data.length);
  out.set([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]);
  new DataView(out.buffer).setUint32(8, data.length);
  out.set(new TextEncoder().encode("tEXt"), 12);
  out.set(data, 16);
  return out;
}

describe("readLayout", () => {
  it("reads the layers' boxes from the preview's chunk", () => {
    const layout = readLayout(png(CHUNK_KEYWORD, '{"v":1,"w":400,"h":300,"scale":1,"layers":{"5":[0,0,400,300]}}'));
    expect(layout?.layers["5"]).toEqual([0, 0, 400, 300]);
  });

  it("is null for another chunk, damaged JSON or not a PNG", () => {
    expect(readLayout(png("fhr-figcraft", '{"v":1}'))).toBeNull();
    expect(readLayout(png(CHUNK_KEYWORD, "{nope"))).toBeNull();
    expect(readLayout(new Uint8Array([1, 2, 3]))).toBeNull();
  });
});
