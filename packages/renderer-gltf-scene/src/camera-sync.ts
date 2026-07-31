// Locking the two side-by-side panes to one camera.
//
// Synchronised cameras are make-or-break for the mode. If the two views can
// drift, every apparent difference might be parallax, and the picture is worse
// than useless because it *looks* authoritative.
//
// So there is no synchronisation: there is ONE rig — one PerspectiveCamera and
// one OrbitControls — drawn twice into two scissor rects. Two rigs kept in step
// by copying state each frame is an approximation with a bug in it eventually;
// one rig cannot drift from itself.
//
// What that leaves to get wrong is the *projection*. Each pane has its own
// aspect ratio, and a PerspectiveCamera bakes aspect into its projection matrix,
// so the aspect has to change between the two draws and be put back afterwards:
// OrbitControls computes panning against `camera.aspect`, and a rig left holding
// half-width geometry pans at half speed for the rest of the session.
//
// Pure math on three's own types — no renderer, no canvas — so it is exercised
// headlessly.

import type { PerspectiveCamera, Vector3 } from "three";

/** The orbit state the panes share: where the eye is, and what it looks at. */
export type CameraState = {
  position: [number, number, number];
  target: [number, number, number];
};

/** The bits of OrbitControls this module touches. */
export type OrbitLike = { target: Vector3; update(): void };

/** Read the rig's state as plain numbers — what a test can compare exactly. */
export function cameraState(camera: PerspectiveCamera, controls: OrbitLike): CameraState {
  return {
    position: [camera.position.x, camera.position.y, camera.position.z],
    target: [controls.target.x, controls.target.y, controls.target.z],
  };
}

/** Put a rig back where `state` says. Exact copy, never a lerp. */
export function applyCameraState(
  camera: PerspectiveCamera,
  controls: OrbitLike,
  state: CameraState,
): void {
  camera.position.set(state.position[0], state.position[1], state.position[2]);
  controls.target.set(state.target[0], state.target[1], state.target[2]);
  camera.lookAt(controls.target);
}

/** The camera fields a pane pass writes. Narrow, so the tests need no WebGL. */
export type ProjectionRig = { aspect: number; updateProjectionMatrix(): void };

/**
 * Draw one pane: give the camera that pane's aspect, run `draw`, then restore
 * the aspect the interactive rig had. The restore is in a `finally` because a
 * draw that throws must not leave the controls computing against a pane's aspect
 * for the rest of the session.
 *
 * Position and target are untouched — that is what "locked" means here.
 */
export function withPaneAspect(camera: ProjectionRig, aspect: number, draw: () => void): void {
  const previous = camera.aspect;
  camera.aspect = aspect > 0 ? aspect : previous;
  camera.updateProjectionMatrix();
  try {
    draw();
  } finally {
    camera.aspect = previous;
    camera.updateProjectionMatrix();
  }
}
