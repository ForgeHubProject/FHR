// A PSD preview (handler-psd, preview.rs): the flattened image as a PNG, with
// a tEXt chunk saying where each layer is on it (image pixels, keyed by
// Photoshop's layer id).

/** The tEXt keyword of the layers' boxes. */
export const CHUNK_KEYWORD = "fhr-psd";

export type Box = [x: number, y: number, w: number, h: number];

export type Layout = { v: number; w: number; h: number; scale: number; layers: Record<string, Box> };

/** The layout chunk of a preview PNG; null when it has none (or isn't a PNG). */
export function readLayout(png: Uint8Array): Layout | null {
  const SIGNATURE = [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a];
  if (png.length < 8 || SIGNATURE.some((b, i) => png[i] !== b)) return null;
  let i = 8;
  while (i + 8 <= png.length) {
    const len = ((png[i]! << 24) | (png[i + 1]! << 16) | (png[i + 2]! << 8) | png[i + 3]!) >>> 0;
    const type = latin1(png.subarray(i + 4, i + 8));
    const start = i + 8;
    const end = start + len;
    if (end > png.length) return null;
    if (type === "tEXt") {
      const data = png.subarray(start, end);
      const nul = data.indexOf(0);
      if (nul > 0 && latin1(data.subarray(0, nul)) === CHUNK_KEYWORD) {
        try {
          const v: unknown = JSON.parse(latin1(data.subarray(nul + 1)));
          return isLayout(v) ? v : null;
        } catch {
          return null;
        }
      }
    }
    if (type === "IEND") break;
    i = end + 4; // the CRC
  }
  return null;
}

function latin1(bytes: Uint8Array): string {
  let s = "";
  for (let i = 0; i < bytes.length; i += 8192) s += String.fromCharCode(...bytes.subarray(i, i + 8192));
  return s;
}

function isLayout(v: unknown): v is Layout {
  const l = v as Partial<Layout> | null;
  return (
    typeof l === "object" &&
    l !== null &&
    typeof l.w === "number" &&
    typeof l.h === "number" &&
    typeof l.layers === "object" &&
    l.layers !== null
  );
}
