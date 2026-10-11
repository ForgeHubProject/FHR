// Camera framing and the fly-to-a-change tween.
//
// Thirty-odd lines of smoothstep instead of a tween library: the ease is one
// polynomial, the state is two Vector3 pairs, and the whole thing is driven from
// the render loop that already exists. Pure math on three's own types, so it is
// testable without a renderer — no WebGL context is created anywhere here.

import { Box3, MathUtils, Vector3, type PerspectiveCamera } from "three";

/** Ken Perlin's smoothstep: eased at both ends, zero derivative at t=0 and t=1. */
export function smoothstep(t: number): number {
  const x = MathUtils.clamp(t, 0, 1);
  return x * x * (3 - 2 * x);
}

/** Where the camera and its orbit target must be to frame `box`. */
export type Framing = { position: Vector3; target: Vector3 };

/**
 * The direction a scene opens on: a three-quarter view from above and in front
 * (+X, +Y, +Z in glTF's Y-up frame). It sees all three principal planes at an
 * angle, so a flat part — a plate, a decal, one face — is never edge-on, which
 * is how it would otherwise vanish.
 */
export const OPENING_DIRECTION: Readonly<Vector3> = new Vector3(1, 0.8, 1).normalize();

/**
 * Frame a box: keep the camera's current viewing *direction* (so a fly-to reads
 * as moving closer to the same model, not as teleporting to a new one) and back
 * off far enough that the box's bounding sphere fits the vertical field of view.
 * `direction`, when given, replaces the current one — the opening view.
 */
export function frameBox(box: Box3, camera: PerspectiveCamera, margin = 1.35, direction?: Vector3): Framing {
  const target = new Vector3();
  const size = new Vector3();
  if (box.isEmpty()) {
    return { position: camera.position.clone(), target: target.clone() };
  }
  box.getCenter(target);
  box.getSize(size);

  const radius = Math.max(size.length() / 2, 1e-4);
  const halfFov = MathUtils.degToRad(camera.fov) / 2;
  const distance = (radius / Math.max(Math.sin(halfFov), 1e-4)) * margin;

  // The given direction, else the current one from the box — and the opening
  // view if the camera sits on the target and so has no direction at all.
  const dir = direction ? direction.clone() : camera.position.clone().sub(target);
  if (dir.lengthSq() < 1e-8) dir.copy(OPENING_DIRECTION);
  dir.normalize();

  return { position: target.clone().addScaledVector(dir, distance), target };
}

/** The bits of OrbitControls the tween touches. */
export type OrbitLike = { target: Vector3; update(): void };

export type FlyTo = {
  /** Start a tween to frame `box`. An empty box is ignored. */
  to(box: Box3, durationMs?: number): void;
  /** Jump straight to framing `box` from the opening direction, no tween (initial framing). */
  snap(box: Box3): void;
  /** Advance the tween; returns true while it is still running. */
  update(nowMs: number): boolean;
  /** Stop the tween where it is (e.g. the user grabbed the mouse). */
  cancel(): void;
  readonly active: boolean;
};

export const DEFAULT_FLY_MS = 650;

/**
 * A single-slot camera tween. Starting a new flight from wherever the camera
 * currently is means an interrupted flight never snaps.
 */
export function createFlyTo(camera: PerspectiveCamera, controls: OrbitLike): FlyTo {
  let fromPosition: Vector3 | null = null;
  let fromTarget: Vector3 | null = null;
  let to: Framing | null = null;
  let startMs = 0;
  let durationMs = DEFAULT_FLY_MS;

  const apply = (framing: Framing): void => {
    camera.position.copy(framing.position);
    controls.target.copy(framing.target);
    camera.lookAt(framing.target);
    controls.update();
  };

  return {
    to(box: Box3, ms: number = DEFAULT_FLY_MS): void {
      if (box.isEmpty()) return;
      const framing = frameBox(box, camera);
      if (ms <= 0) {
        apply(framing);
        return;
      }
      fromPosition = camera.position.clone();
      fromTarget = controls.target.clone();
      to = framing;
      durationMs = ms;
      startMs = Number.NaN; // set on the first update, so queuing costs nothing
    },
    snap(box: Box3): void {
      if (box.isEmpty()) return;
      // Not the camera's current direction: before the first frame the camera
      // sits at the origin, so "current" would be the direction from the model
      // back to the origin — top-down for a model above it, edge-on along X for
      // one beside it — an accident of where the model was authored.
      apply(frameBox(box, camera, undefined, OPENING_DIRECTION.clone()));
    },
    update(nowMs: number): boolean {
      if (!to || !fromPosition || !fromTarget) return false;
      if (Number.isNaN(startMs)) startMs = nowMs;
      const t = smoothstep((nowMs - startMs) / durationMs);
      camera.position.lerpVectors(fromPosition, to.position, t);
      controls.target.lerpVectors(fromTarget, to.target, t);
      camera.lookAt(controls.target);
      if (t >= 1) {
        to = null;
        fromPosition = null;
        fromTarget = null;
        return false;
      }
      return true;
    },
    cancel(): void {
      to = null;
      fromPosition = null;
      fromTarget = null;
    },
    get active(): boolean {
      return to !== null;
    },
  };
}
