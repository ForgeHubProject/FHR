// Isolate-on-select. Scene traversal only — no renderer, no canvas — so the real
// module runs here against real three.js objects.

import { describe, it, expect } from "vitest";
import { BoxGeometry, Group, Mesh, MeshBasicMaterial } from "three";
import { createIsolator } from "./isolate.js";

const mesh = (name: string): Mesh => {
  const m = new Mesh(new BoxGeometry(), new MeshBasicMaterial());
  m.name = name;
  return m;
};

function scene() {
  const root = new Group();
  const wheel = new Group();
  wheel.name = "wheel-node";
  const wheelPrimitive = mesh("wheel-primitive");
  wheel.add(wheelPrimitive);
  const body = mesh("body");
  const ghost = mesh("ghost");
  const baseSolid = new Group();
  const baseMesh = mesh("base-copy");
  baseSolid.add(baseMesh);
  baseSolid.visible = false;
  root.add(wheel, body, ghost, baseSolid);
  return { root, wheel, wheelPrimitive, body, ghost, baseSolid, baseMesh };
}

describe("createIsolator", () => {
  it("hides everything except the selected subtree", () => {
    const s = scene();
    const isolator = createIsolator(s.root);
    isolator.isolate([s.wheel]);
    expect(s.wheelPrimitive.visible).toBe(true);
    expect(s.body.visible).toBe(false);
    expect(s.ghost.visible).toBe(false);
    expect(isolator.active).toBe(true);
  });

  // Hiding a group hides everything under it — including the one object the
  // reviewer asked to see, if it happens to be a descendant.
  it("never touches groups, only leaf meshes", () => {
    const s = scene();
    createIsolator(s.root).isolate([s.wheelPrimitive]);
    expect(s.wheel.visible).toBe(true);
    expect(s.wheelPrimitive.visible).toBe(true);
  });

  it("puts everything back exactly as it was", () => {
    const s = scene();
    s.ghost.visible = false; // already hidden for its own reasons
    const isolator = createIsolator(s.root);
    isolator.isolate([s.wheel]);
    isolator.clear();
    expect(s.body.visible).toBe(true);
    expect(s.ghost.visible).toBe(false);
    expect(isolator.active).toBe(false);
  });

  it("moves cleanly from one selection to the next", () => {
    const s = scene();
    const isolator = createIsolator(s.root);
    isolator.isolate([s.wheel]);
    isolator.isolate([s.body]);
    expect(s.body.visible).toBe(true);
    expect(s.wheelPrimitive.visible).toBe(false);
    isolator.clear();
    expect(s.wheelPrimitive.visible).toBe(true);
  });

  // The A/B blink is a whole-model comparison; it must survive an isolation.
  it("leaves skipped subtrees alone, so the blink keeps working", () => {
    const s = scene();
    const isolator = createIsolator(s.root, { skip: [s.baseSolid] });
    isolator.isolate([s.wheel]);
    expect(s.baseMesh.visible).toBe(true);
    s.baseSolid.visible = true;
    expect(s.baseMesh.visible).toBe(true);
  });

  it("does nothing for an empty selection", () => {
    const s = scene();
    const isolator = createIsolator(s.root);
    isolator.isolate([]);
    expect(s.body.visible).toBe(true);
    expect(isolator.active).toBe(false);
  });

  it("does nothing for a selection with no meshes in it", () => {
    const s = scene();
    const isolator = createIsolator(s.root);
    isolator.isolate([new Group()]);
    expect(s.body.visible).toBe(true);
    expect(isolator.active).toBe(false);
  });
});
