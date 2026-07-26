// Click → change resolution. The raycast itself is three.js's (and is exercised
// here against a real Raycaster on real geometry, which needs no WebGL); what
// this file is really about is the walk from the Mesh that got hit to the change
// the diff is talking about.

import { describe, it, expect } from "vitest";
import { BoxGeometry, Group, Mesh, MeshBasicMaterial, Object3D, PerspectiveCamera, Raycaster, Vector2 } from "three";
import { changeAtHits, changeAtObject, isClickGesture, isVisibleInTree, ndcFromPointer } from "./pick.js";

const rect = { left: 0, top: 0, width: 200, height: 100 };

describe("ndcFromPointer", () => {
  it("puts the centre at the origin and the corners at ±1", () => {
    expect(ndcFromPointer({ clientX: 100, clientY: 50 }, rect)).toEqual({ x: 0, y: 0 });
    expect(ndcFromPointer({ clientX: 0, clientY: 0 }, rect)).toEqual({ x: -1, y: 1 });
    expect(ndcFromPointer({ clientX: 200, clientY: 100 }, rect)).toEqual({ x: 1, y: -1 });
  });

  it("measures from the canvas, not the page", () => {
    expect(ndcFromPointer({ clientX: 140, clientY: 70 }, { left: 40, top: 20, width: 200, height: 100 })).toEqual({
      x: 0,
      y: 0,
    });
  });

  it("has nowhere to point in a zero-sized viewport", () => {
    expect(ndcFromPointer({ clientX: 5, clientY: 5 }, { left: 0, top: 0, width: 0, height: 0 })).toEqual({ x: 0, y: 0 });
  });
});

describe("changeAtObject", () => {
  /** node → Group → Mesh: the shape GLTFLoader builds for a multi-primitive mesh. */
  function tree() {
    const nodeGroup = new Group();
    const primitive = new Mesh(new BoxGeometry(), new MeshBasicMaterial());
    nodeGroup.add(primitive);
    return { nodeGroup, primitive };
  }

  it("walks up from the mesh that was hit to the painted change", () => {
    const { nodeGroup, primitive } = tree();
    const lookup = { changeNameByObject: new Map<Object3D, string>([[nodeGroup, "Wheel_FL"]]) };
    expect(changeAtObject(primitive, lookup)).toBe("Wheel_FL");
  });

  it("resolves an object that is itself the painted one", () => {
    const { primitive } = tree();
    const lookup = { changeNameByObject: new Map<Object3D, string>([[primitive, "Paint"]]) };
    expect(changeAtObject(primitive, lookup)).toBe("Paint");
  });

  it("is null for geometry no change was painted on", () => {
    const { primitive } = tree();
    expect(changeAtObject(primitive, { changeNameByObject: new Map() })).toBeNull();
    expect(changeAtObject(null, { changeNameByObject: new Map() })).toBeNull();
  });

  it("falls back to the loader's node association", () => {
    const { primitive } = tree();
    expect(
      changeAtObject(primitive, {
        changeNameByObject: new Map(),
        nodeIndexOf: () => 7,
        changeNameByNodeIndex: new Map([[7, "Hood"]]),
      }),
    ).toBe("Hood");
  });

  it("prefers the painted map over the association", () => {
    const { nodeGroup, primitive } = tree();
    expect(
      changeAtObject(primitive, {
        changeNameByObject: new Map<Object3D, string>([[nodeGroup, "Wheel_FL"]]),
        nodeIndexOf: () => 7,
        changeNameByNodeIndex: new Map([[7, "Hood"]]),
      }),
    ).toBe("Wheel_FL");
  });
});

describe("changeAtHits", () => {
  const meshFor = (name: string): { mesh: Mesh; lookupEntry: [Object3D, string] } => {
    const mesh = new Mesh(new BoxGeometry(), new MeshBasicMaterial());
    return { mesh, lookupEntry: [mesh as Object3D, name] };
  };

  it("takes the frontmost hit that resolves", () => {
    const front = meshFor("Front");
    const back = meshFor("Back");
    const lookup = { changeNameByObject: new Map<Object3D, string>([front.lookupEntry, back.lookupEntry]) };
    expect(changeAtHits([{ object: front.mesh }, { object: back.mesh }], lookup)).toBe("Front");
  });

  it("looks past hits on unchanged geometry", () => {
    const unchanged = new Mesh(new BoxGeometry(), new MeshBasicMaterial());
    const changed = meshFor("Wheel_FL");
    const lookup = { changeNameByObject: new Map<Object3D, string>([changed.lookupEntry]) };
    expect(changeAtHits([{ object: unchanged }, { object: changed.mesh }], lookup)).toBe("Wheel_FL");
  });

  // three.js's raycaster tests layers, not visibility, so an isolated view still
  // has every hidden mesh in the ray's path.
  it("ignores hits on hidden geometry", () => {
    const hidden = meshFor("Hidden");
    hidden.mesh.visible = false;
    const shown = meshFor("Shown");
    const lookup = { changeNameByObject: new Map<Object3D, string>([hidden.lookupEntry, shown.lookupEntry]) };
    expect(changeAtHits([{ object: hidden.mesh }, { object: shown.mesh }], lookup)).toBe("Shown");
  });

  it("ignores hits under a hidden ancestor", () => {
    const group = new Group();
    group.visible = false;
    const child = meshFor("Inside");
    group.add(child.mesh);
    const lookup = { changeNameByObject: new Map<Object3D, string>([child.lookupEntry]) };
    expect(changeAtHits([{ object: child.mesh }], lookup)).toBeNull();
  });

  it("is null when nothing was hit", () => {
    expect(changeAtHits([], { changeNameByObject: new Map() })).toBeNull();
  });

  // The whole path, with three.js's own raycaster doing the geometry: no WebGL is
  // involved in casting a ray, so this runs headlessly.
  it("resolves a real raycast against real geometry", () => {
    const camera = new PerspectiveCamera(50, 1, 0.1, 100);
    camera.position.set(0, 0, 5);
    camera.lookAt(0, 0, 0);
    camera.updateMatrixWorld(true);

    const nodeGroup = new Group();
    nodeGroup.add(new Mesh(new BoxGeometry(1, 1, 1), new MeshBasicMaterial()));
    nodeGroup.updateMatrixWorld(true);

    const raycaster = new Raycaster();
    raycaster.setFromCamera(new Vector2(0, 0), camera);
    const hits = raycaster.intersectObjects([nodeGroup], true);
    expect(hits.length).toBeGreaterThan(0);
    expect(
      changeAtHits(hits, { changeNameByObject: new Map<Object3D, string>([[nodeGroup, "Wheel_FL"]]) }),
    ).toBe("Wheel_FL");
  });
});

describe("isVisibleInTree", () => {
  it("needs the whole chain visible", () => {
    const parent = new Group();
    const child = new Group();
    parent.add(child);
    expect(isVisibleInTree(child)).toBe(true);
    parent.visible = false;
    expect(isVisibleInTree(child)).toBe(false);
  });
});

describe("isClickGesture", () => {
  it("accepts a press that didn't travel", () => {
    expect(isClickGesture({ x: 10, y: 10, t: 0 }, { x: 12, y: 11, t: 120 })).toBe(true);
  });

  it("rejects an orbit drag — that belongs to the controls", () => {
    expect(isClickGesture({ x: 10, y: 10, t: 0 }, { x: 60, y: 40, t: 200 })).toBe(false);
  });

  it("rejects a long press, which is not a click either", () => {
    expect(isClickGesture({ x: 10, y: 10, t: 0 }, { x: 10, y: 10, t: 2000 })).toBe(false);
  });
});
