// A FigCraft preview (handler-figcraft, preview.rs): one PNG of every screen,
// with a tEXt chunk saying where each screen is on the sheet and where each of
// its layers is on the screen (image pixels, keyed by FigCraft's layer id).

/** The tEXt keyword of the sheet's layout. */
export const CHUNK_KEYWORD = "fhr-figcraft";

export type Box = [x: number, y: number, w: number, h: number];

export type SheetScreen = {
  id: number;
  name: string;
  page: number;
  pageName: string;
  /** Where the screen is on the sheet. */
  x: number;
  y: number;
  w: number;
  h: number;
  /** Every layer on the screen (the screen too), by id: its box on the screen's image. */
  layers: Record<string, Box>;
};

export type Sheet = { v: number; scale: number; screens: SheetScreen[] };

/** The layout chunk of a preview PNG; null when it has none (or isn't a PNG). */
export function readSheet(png: Uint8Array): Sheet | null {
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
          return isSheet(v) ? v : null;
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

function isSheet(v: unknown): v is Sheet {
  const s = v as Partial<Sheet> | null;
  return (
    typeof s === "object" &&
    s !== null &&
    typeof s.scale === "number" &&
    Array.isArray(s.screens) &&
    s.screens.every(
      (x) =>
        typeof x === "object" &&
        x !== null &&
        typeof x.id === "number" &&
        typeof x.x === "number" &&
        typeof x.y === "number" &&
        typeof x.w === "number" &&
        typeof x.h === "number" &&
        typeof x.layers === "object",
    )
  );
}
