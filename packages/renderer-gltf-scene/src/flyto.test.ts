import { describe, it, expect } from "vitest";
import { Box3, PerspectiveCamera, Vector3 } from "three";
import { createFlyTo, DEFAULT_FLY_MS, frameBox, smoothstep, type OrbitLike } from "./flyto.js";

/** OrbitControls' surface as far as the tween is concerned. */
function fakeControls(): OrbitLike & { updates: number } {
  return { target: new Vector3(), updates: 0, update(): void { this.updates++; } };
}

const boxAt = (center: Vector3, size = 2): Box3 =>
  new Box3().setFromCenterAndSize(center, new Vector3(size, size, size));

describe("smoothstep", () => {
  it("is clamped and eased at both ends", () => {
    expect(smoothstep(-1)).toBe(0);
    expect(smoothstep(0)).toBe(0);
    expect(smoothstep(0.5)).toBeCloseTo(0.5);
    expect(smoothstep(1)).toBe(1);
    expect(smoothstep(2)).toBe(1);
  });

  it("starts and ends slowly (the point of using it)", () => {
    expect(smoothstep(0.1)).toBeLessThan(0.1);
    expect(smoothstep(0.9)).toBeGreaterThan(0.9);
  });
});

describe("frameBox", () => {
  it("centres the orbit target on the box", () => {
    const camera = new PerspectiveCamera(50, 1.5, 0.1, 5000);
    camera.position.set(0, 0, 100);
    const framing = frameBox(boxAt(new Vector3(5, 1, -2)), camera);
    expect(framing.target.toArray()).toEqual([5, 1, -2]);
  });

  it("backs off far enough to fit the box in the field of view", () => {
    const camera = new PerspectiveCamera(50, 1.5, 0.1, 5000);
    camera.position.set(0, 0, 10);
    const near = frameBox(boxAt(new Vector3(), 2), camera);
    const far = frameBox(boxAt(new Vector3(), 20), camera);
    const distance = (p: Vector3): number => p.length();
    expect(distance(far.position)).toBeGreaterThan(distance(near.position) * 5);
  });

  it("keeps the current viewing direction, so a fly-to isn't a teleport", () => {
    const camera = new PerspectiveCamera(50, 1.5, 0.1, 5000);
    camera.position.set(0, 0, 50); // looking down +Z at the origin
    const framing = frameBox(boxAt(new Vector3()), camera);
    expect(framing.position.x).toBeCloseTo(0);
    expect(framing.position.y).toBeCloseTo(0);
    expect(framing.position.z).toBeGreaterThan(0);
  });

  it("uses a three-quarter view when the camera has no direction yet", () => {
    const camera = new PerspectiveCamera(50, 1.5, 0.1, 5000);
    const framing = frameBox(boxAt(new Vector3()), camera);
    expect(framing.position.x).toBeGreaterThan(0);
    expect(framing.position.y).toBeGreaterThan(0);
    expect(framing.position.z).toBeGreaterThan(0);
  });

  it("leaves the camera alone for an empty box", () => {
    const camera = new PerspectiveCamera();
    camera.position.set(1, 2, 3);
    expect(frameBox(new Box3(), camera).position.toArray()).toEqual([1, 2, 3]);
  });
});

describe("createFlyTo", () => {
  it("snaps without a tween", () => {
    const camera = new PerspectiveCamera(50, 1.5, 0.1, 5000);
    const controls = fakeControls();
    const flyTo = createFlyTo(camera, controls);

    flyTo.snap(boxAt(new Vector3(10, 0, 0)));
    expect(controls.target.x).toBeCloseTo(10);
    expect(flyTo.active).toBe(false);
    expect(controls.updates).toBeGreaterThan(0);
  });

  it("eases the camera and the orbit target to the framing, then finishes", () => {
    const camera = new PerspectiveCamera(50, 1.5, 0.1, 5000);
    camera.position.set(0, 0, 40);
    const controls = fakeControls();
    const flyTo = createFlyTo(camera, controls);

    flyTo.to(boxAt(new Vector3(20, 0, 0)));
    expect(flyTo.active).toBe(true);

    expect(flyTo.update(1000)).toBe(true); // first frame sets the clock
    expect(controls.target.x).toBeCloseTo(0); // t = 0
    flyTo.update(1000 + DEFAULT_FLY_MS / 2);
    const halfway = controls.target.x;
    expect(halfway).toBeGreaterThan(0);
    expect(halfway).toBeLessThan(20);

    expect(flyTo.update(1000 + DEFAULT_FLY_MS)).toBe(false); // done
    expect(controls.target.x).toBeCloseTo(20);
    expect(flyTo.active).toBe(false);
  });

  it("does nothing for an empty box, and updates are no-ops when idle", () => {
    const camera = new PerspectiveCamera(50, 1.5, 0.1, 5000);
    camera.position.set(0, 0, 5);
    const flyTo = createFlyTo(camera, fakeControls());
    flyTo.to(new Box3());
    expect(flyTo.active).toBe(false);
    expect(flyTo.update(0)).toBe(false);
    expect(camera.position.toArray()).toEqual([0, 0, 5]);
  });

  it("a cancelled flight stops where it is — it does not snap to the target", () => {
    const camera = new PerspectiveCamera(50, 1.5, 0.1, 5000);
    camera.position.set(0, 0, 40);
    const controls = fakeControls();
    const flyTo = createFlyTo(camera, controls);

    flyTo.to(boxAt(new Vector3(20, 0, 0)));
    flyTo.update(0);
    flyTo.update(DEFAULT_FLY_MS / 2);
    const interrupted = controls.target.x;
    flyTo.cancel();
    flyTo.update(DEFAULT_FLY_MS);
    expect(controls.target.x).toBe(interrupted);
    expect(flyTo.active).toBe(false);
  });

  it("a new flight starts from wherever the camera is now", () => {
    const camera = new PerspectiveCamera(50, 1.5, 0.1, 5000);
    camera.position.set(0, 0, 40);
    const controls = fakeControls();
    const flyTo = createFlyTo(camera, controls);

    flyTo.to(boxAt(new Vector3(20, 0, 0)));
    flyTo.update(0);
    flyTo.update(DEFAULT_FLY_MS / 3);
    const midX = controls.target.x;

    flyTo.to(boxAt(new Vector3(-20, 0, 0)));
    flyTo.update(1000);
    expect(controls.target.x).toBeCloseTo(midX); // continues, doesn't jump
    flyTo.update(1000 + DEFAULT_FLY_MS);
    expect(controls.target.x).toBeCloseTo(-20);
  });

  it("a zero-duration flight applies immediately", () => {
    const camera = new PerspectiveCamera(50, 1.5, 0.1, 5000);
    const controls = fakeControls();
    const flyTo = createFlyTo(camera, controls);
    flyTo.to(boxAt(new Vector3(3, 0, 0)), 0);
    expect(controls.target.x).toBeCloseTo(3);
    expect(flyTo.active).toBe(false);
  });
});
