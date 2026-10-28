import { describe, it, expect } from "vitest";
import { readSheet, CHUNK_KEYWORD } from "./sheet.js";

/** A PNG's chunk list, CRCs zeroed (readSheet doesn't check them). */
function png(chunks: [string, Uint8Array][]): Uint8Array {
  const parts: number[] = [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a];
  for (const [type, data] of chunks) {
    const n = data.length;
    parts.push((n >>> 24) & 255, (n >>> 16) & 255, (n >>> 8) & 255, n & 255);
    for (const c of type) parts.push(c.charCodeAt(0));
    parts.push(...data, 0, 0, 0, 0);
  }
  return new Uint8Array(parts);
}
const text = (keyword: string, body: string) => new Uint8Array([...keyword].map((c) => c.charCodeAt(0)).concat([0], [...body].map((c) => c.charCodeAt(0))));

const layout = {
  v: 1,
  scale: 1,
  screens: [{ id: 2, name: "1 Welcome", page: 1, pageName: "Onboarding", x: 0, y: 0, w: 393, h: 852, layers: { "18": [28, 526, 287, 36] } }],
};

describe("the preview's layout chunk", () => {
  it("reads the screens and layer boxes out of the PNG", () => {
    const bytes = png([["IHDR", new Uint8Array(13)], ["tEXt", text(CHUNK_KEYWORD, JSON.stringify(layout))], ["IDAT", new Uint8Array(4)], ["IEND", new Uint8Array(0)]]);
    expect(readSheet(bytes)).toEqual(layout);
  });

  it("reads escaped names back as they were", () => {
    // The handler escapes non-ASCII: a tEXt chunk is Latin-1.
    const escaped = JSON.stringify({ ...layout, screens: [{ ...layout.screens[0], name: "Gr\\u00fc\\u00dfe" }] }).replace(/\\\\u/g, "\\u");
    const sheet = readSheet(png([["IHDR", new Uint8Array(13)], ["tEXt", text(CHUNK_KEYWORD, escaped)], ["IEND", new Uint8Array(0)]]));
    expect(sheet?.screens[0]?.name).toBe("Grüße");
  });

  it("is null for a PNG without it, another tool's text, garbage and non-PNGs", () => {
    expect(readSheet(png([["IHDR", new Uint8Array(13)], ["IEND", new Uint8Array(0)]]))).toBeNull();
    expect(readSheet(png([["tEXt", text("Software", "something")], ["IEND", new Uint8Array(0)]]))).toBeNull();
    expect(readSheet(png([["tEXt", text(CHUNK_KEYWORD, "{not json")], ["IEND", new Uint8Array(0)]]))).toBeNull();
    expect(readSheet(png([["tEXt", text(CHUNK_KEYWORD, '{"screens": 3}')], ["IEND", new Uint8Array(0)]]))).toBeNull();
    expect(readSheet(new Uint8Array([1, 2, 3]))).toBeNull();
  });
});
