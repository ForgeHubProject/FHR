// Per-vertex deviation: current mesh vs previous surface, computed in slices.
//
// The measurement itself is one line — closest point on the base surface, then
// the distance to it (closest-point.ts). Everything else here is about *when*
// it runs. A 100k-vertex mesh is ~0.4 s of arithmetic on this build; done in one
// go on the main thread that is 25 dropped frames while the reviewer is holding
// the mouse, and the model appears frozen at the exact moment they asked it a
// question. So the loop yields every few thousand vertices and the picture keeps
// moving while the answer arrives.
//
// Not a worker: the CSP the hosts serve has no `worker-src` for a blob URL and
// neither host serves a same-origin worker script for this bundle (#40's
// non-goals). Chunking on the main thread is what is actually available.
//
// Pure numbers — no three.js, no DOM.

import type { SurfaceIndex } from "./closest-point.js";

/**
 * Vertices measured between yields. Sized so one slice is a few milliseconds on
 * the machines a review happens on: small enough not to drop a frame, large
 * enough that the yield overhead stays under a percent of the total.
 */
export const CHUNK_VERTICES = 4096;

export type DeviationResult = {
  /** Distance per vertex, in the geometry's own units, in vertex order. */
  values: Float32Array;
  min: number;
  max: number;
};

/** Yield control so the frame loop can run; awaited between slices. */
export type Yielder = () => Promise<void>;

/**
 * The default yielder: `requestIdleCallback` where the browser has it, a
 * macrotask otherwise. A macrotask and not a microtask — `queueMicrotask` and
 * bare `await` both run before the next frame, so they yield to nothing that
 * matters and the model still freezes.
 */
export const idleYield: Yielder = () =>
  new Promise<void>((resolve) => {
    const idle = (globalThis as { requestIdleCallback?: (cb: () => void, o?: unknown) => void })
      .requestIdleCallback;
    if (typeof idle === "function") idle(() => resolve(), { timeout: 100 });
    else setTimeout(resolve, 0);
  });

/** Measure `positions` (3 floats per vertex) against `surface`, all at once. */
export function deviationSync(positions: ArrayLike<number>, surface: SurfaceIndex): DeviationResult {
  const count = Math.floor(positions.length / 3);
  const values = new Float32Array(count);
  measure(positions, surface, values, 0, count);
  return withRange(values);
}

/**
 * The same measurement, in slices, awaiting `yieldTo` between them.
 *
 * `signal.cancelled` is checked at every slice boundary: the reviewer can switch
 * the heatmap off — or leave overlay mode entirely — while a big mesh is still
 * being measured, and a computation that ignored that would go on burning the
 * main thread for a picture nobody is going to see. Returns null when cancelled.
 */
export async function deviationChunked(
  positions: ArrayLike<number>,
  surface: SurfaceIndex,
  yieldTo: Yielder,
  signal?: { cancelled: boolean },
  chunk: number = CHUNK_VERTICES,
): Promise<DeviationResult | null> {
  const count = Math.floor(positions.length / 3);
  const values = new Float32Array(count);
  for (let from = 0; from < count; from += chunk) {
    if (signal?.cancelled) return null;
    measure(positions, surface, values, from, Math.min(from + chunk, count));
    if (from + chunk < count) await yieldTo();
  }
  if (signal?.cancelled) return null;
  return withRange(values);
}

function measure(
  positions: ArrayLike<number>,
  surface: SurfaceIndex,
  out: Float32Array,
  from: number,
  to: number,
): void {
  for (let v = from; v < to; v++) {
    const at = v * 3;
    out[v] = Math.sqrt(
      surface.closestDistanceSquared(positions[at] ?? 0, positions[at + 1] ?? 0, positions[at + 2] ?? 0),
    );
  }
}

function withRange(values: Float32Array): DeviationResult {
  let min = Infinity;
  let max = 0;
  for (let i = 0; i < values.length; i++) {
    const v = values[i]!;
    if (v < min) min = v;
    if (v > max) max = v;
  }
  return { values, min: Number.isFinite(min) ? min : 0, max };
}

/**
 * A deviation as a reviewer would say it. glTF's unit is the metre (spec §3.3),
 * so a millimetre-scale edit on a car body has to be reported in millimetres or
 * the headline number reads "0.000 m" — which is how "max deviation 12 mm", the
 * sentence this whole slice exists to produce, turns into nothing.
 */
export function formatDeviation(metres: number): string {
  if (!Number.isFinite(metres)) return "—";
  const mm = metres * 1000;
  if (mm < 1) return `${mm.toPrecision(2)} mm`;
  if (mm < 1000) return `${mm < 10 ? mm.toFixed(2) : mm.toFixed(1)} mm`;
  return `${metres.toFixed(metres < 10 ? 2 : 1)} m`;
}
