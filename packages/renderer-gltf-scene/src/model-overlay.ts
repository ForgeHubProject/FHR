// The diff, painted onto the real model.
//
// Grammar (from the prior-art synthesis in #40 — Autodesk APS's change view is
// the closest working precedent):
//
//   unchanged      desaturated grey, opaque — present but quiet
//   added          saturated blue on the head model
//   modified       saturated orange on the head model
//   removed        translucent purple ghost, drawn from the BASE file at its
//                  base transform: "the ghost of what was"
//   moved          translucent ghost at the old pose + a motion vector to the
//                  new one. Without the vector a double render reads as a
//                  duplicated object rather than a move.
//   ghost base     the whole previous version under the current one, one shared
//                  translucent material, depthWrite off, renderOrder -1. No
//                  alignment work: both files are in the same world space.
//
// Two invariants worth stating because breaking either is silent:
//   * Materials are CLONED before tinting. GLTFLoader shares one material
//     instance across every primitive that references it, so tinting in place
//     recolours strangers elsewhere in the model.
//   * Transparency is used for the *base* layers only. Ghosting the head model
//     as well would leave two translucent layers to depth-sort against each
//     other, which is a sorting soup; unchanged head geometry is desaturated
//     instead, which reads the same way and stays opaque.
//
// three.js is used here for scene assembly and math only — no renderer, no
// canvas — so all of it is exercised in the headless tests.

import {
  Box3,
  BufferGeometry,
  Color,
  ConeGeometry,
  Group,
  Line,
  LineBasicMaterial,
  Mesh,
  MeshBasicMaterial,
  Vector3,
} from "three";
import type { Material, Object3D } from "three";
import type { EntityChange, NodeChange } from "./diff-map.js";
import { hasTransformChange } from "./diff-map.js";
import { KIND_COLOR, NEUTRAL } from "./palette.js";
import { meshesIn, nodeIndexOfObject, objectsByNodeIndex, type AssociatedGltf } from "./associations.js";
import {
  ambiguousNameMessage,
  resolveMaterialPrimitives,
  resolveMeshNodes,
  resolveNodeIndex,
  type NameIndex,
} from "./node-index.js";
import { disposeTree, type DisposeReport } from "./dispose.js";

export type Theme = "light" | "dark";

/** One loaded file: the three.js scene plus the name→index bridge for its JSON. */
export type LoadedSide = { gltf: AssociatedGltf; index: NameIndex };

export type OverlayInput = {
  head: LoadedSide;
  /** The previous version, when it was available and small enough to load. */
  base?: LoadedSide | null;
  changes: NodeChange[];
  /** Changes against `meshes` — painted through the nodes instancing each mesh. */
  meshes?: EntityChange[];
  /** Changes against `materials` — painted through the primitives using each. */
  materials?: EntityChange[];
  /**
   * Changes this view has no way to draw at all (animations today). Not painted,
   * only counted, so the banner can say they exist rather than letting the model
   * imply nothing happened.
   */
  unpaintable?: EntityChange[];
  theme?: Theme;
};

export type OverlayStats = {
  tinted: number;
  desaturated: number;
  removedGhosts: number;
  moveGhosts: number;
  motionVectors: number;
  /** Changed node names that exist in neither file's scene graph. */
  unmatched: number;
  /**
   * Changes that could only ever have been drawn from the previous version,
   * which isn't loaded. Counted apart from `unmatched` because nothing is wrong
   * with the change list — the geometry is simply not in hand.
   */
  needsBase: number;
  /**
   * Changes the model cannot show: an animation edit, a mesh no node instances, a
   * material no primitive references. Counted so the view can say so — a diff
   * with real content that paints nothing must not look like an unchanged file.
   */
  unpaintable: number;
};

export type Overlay = {
  /** Add this to the scene; everything below hangs off it. */
  root: Group;
  /** The current version (blink state A). */
  headGroup: Group;
  /** The previous version with its own materials, hidden (blink state B). */
  baseSolidGroup: Group | null;
  /** The previous version as a translucent underlay. */
  baseGhostGroup: Group | null;
  /** Ghosts of removed geometry, drawn from the base file (null when none). */
  removedGroup: Group | null;
  /** Ghosts of moved geometry at its old pose, plus motion vectors. */
  movedGroup: Group | null;
  /** Union of everything the diff touches — what the camera should frame. */
  changeBox: Box3;
  /** Whole-scene bounds, for the initial framing. */
  sceneBox: Box3;
  /** Per-change bounding box, so a change list can fly the camera to one (#45). */
  boxByChangeName: Map<string, Box3>;
  /** Per-change objects, for selection/highlight in #45. */
  objectsByChangeName: Map<string, Object3D[]>;
  /**
   * Every object this overlay painted → the change it was painted for. The
   * raycast-picking direction (#45): a click walks up from the Mesh it hit until
   * it finds an entry here. It covers the ghost clones too, which appear in no
   * glTF association because the loader never made them.
   */
  changeNameByObject: Map<Object3D, string>;
  /** Head-model node index → change name, the fallback when nothing was painted. */
  changeNameByNodeIndex: Map<number, string>;
  /** The head model's glTF node for an object (associations.ts), or null. */
  nodeIndexOfObject(object: Object3D): number | null;
  stats: OverlayStats;
  /** Plain-language notes for the banner list (ambiguity, unmatched names). */
  notes: string[];
  dispose(): DisposeReport;
};

const GHOST_BASE_OPACITY = 0.22;
const REMOVED_OPACITY = 0.5;
const MOVED_OPACITY = 0.32;
/** Wireframe lines are thin, so the old pose needs more alpha than a solid would. */
const MOVED_WIREFRAME_OPACITY = 0.85;
/** Arrowhead size as a fraction of the travel distance — scales with the move. */
const ARROWHEAD_LENGTH_RATIO = 0.22;
const ARROWHEAD_RADIUS_RATIO = 0.08;
/** ConeGeometry's own axis, the "from" of every arrowhead rotation. */
const CONE_AXIS = new Vector3(0, 1, 0);
/** World-space distance below which a "move" is too small to draw an arrow for. */
const MOTION_EPSILON = 1e-3;

/** Build the overlay described at the top of this file. */
export function buildOverlay(input: OverlayInput): Overlay {
  const { head, changes } = input;
  const base = input.base ?? null;
  const dark = (input.theme ?? "light") === "dark";

  const root = new Group();
  root.name = "fhr-overlay";

  head.gltf.scene.updateMatrixWorld(true);
  base?.gltf.scene.updateMatrixWorld(true);

  const headGroup = new Group();
  headGroup.name = "fhr-head";
  headGroup.add(head.gltf.scene);

  // Materials this overlay creates and therefore owns. They end up attached to
  // objects under `root`, so disposal finds them by walking the tree; the
  // originals they *displace* are the ones that need tracking (`orphaned`).
  const ghostBaseMaterial = new MeshBasicMaterial({
    color: dark ? 0x8b98a5 : 0x57606a,
    transparent: true,
    opacity: GHOST_BASE_OPACITY,
    depthWrite: false,
  });
  const removedMaterial = new MeshBasicMaterial({
    color: KIND_COLOR["removed"],
    transparent: true,
    opacity: REMOVED_OPACITY,
    depthWrite: false,
  });
  // The old pose is drawn as a WIREFRAME, not a fainter solid.
  //
  // Both poses of a moved node are the same shape in the same hue, so the only
  // thing distinguishing them was opacity — and that cue does not survive
  // contact with reality. The ghost is unlit (MeshBasicMaterial) while the head
  // copy is lit and tinted, so at a glancing angle or on a face turned away from
  // the light the *current* position can read darker than the *previous* one, and
  // "faded means old" silently inverts. A reviewer then cannot answer the only
  // question the picture exists to answer: which way did it go.
  //
  // Wireframe vs solid is categorical rather than tonal. It cannot invert under
  // lighting, it survives any camera angle, and it carries no hue information, so
  // it stays legible for colour-blind viewers — who are exactly the reason this
  // palette avoids red/green in the first place.
  const movedMaterial = new MeshBasicMaterial({
    color: KIND_COLOR["modified"],
    wireframe: true,
    transparent: true,
    opacity: MOVED_WIREFRAME_OPACITY,
    depthWrite: false,
  });
  const motionMaterial = new LineBasicMaterial({ color: KIND_COLOR["modified"] });
  // Opaque, unlike the wireframe old pose: the head marks where the object
  // actually is now, and a translucent marker for the current state would put it
  // back in the same visual register as the thing it distinguishes it from.
  const arrowheadMaterial = new MeshBasicMaterial({ color: KIND_COLOR["modified"] });

  /** Originals we replaced; nothing references them any more, so they must go. */
  const orphaned = new Set<Material>();
  const tintCache = new Map<string, Material>();

  const headObjects = objectsByNodeIndex(head.gltf);
  const baseObjects = base ? objectsByNodeIndex(base.gltf) : new Map<number, Object3D[]>();

  const notes: string[] = [];
  const stats: OverlayStats = {
    tinted: 0,
    desaturated: 0,
    removedGhosts: 0,
    moveGhosts: 0,
    motionVectors: 0,
    unmatched: 0,
    needsBase: 0,
    unpaintable: 0,
  };
  const changeBox = new Box3();
  const boxByChangeName = new Map<string, Box3>();
  const objectsByChangeName = new Map<string, Object3D[]>();
  const changeNameByObject = new Map<Object3D, string>();
  const changeNameByNodeIndex = new Map<number, string>();
  const paintedHeadMeshes = new Set<Object3D>();
  /** Base nodes that get their own grammar, so the plain ghost skips them. */
  const baseNodesWithOwnGrammar = new Set<number>();
  /** Base nodes the diff actually names — the only ones the ghost has news about. */
  const baseNodesInDiff = new Set<number>();

  const removedGroup = new Group();
  removedGroup.name = "fhr-removed";
  const movedGroup = new Group();
  movedGroup.name = "fhr-moved";

  const noteAmbiguity = (name: string, count: number): void => {
    const message = ambiguousNameMessage(name, count);
    if (!notes.includes(message)) notes.push(message);
  };

  for (const change of changes) {
    const inHead = resolveNodeIndex(head.index, change.name);
    // A renamed node is called something else in the previous version, so the
    // base file has to be looked up under the old name — otherwise the ghost and
    // the motion vector for "renamed and moved" silently find nothing.
    const inBase = base ? resolveNodeIndex(base.index, change.oldName ?? change.name) : null;
    if (inHead.ambiguous) noteAmbiguity(change.name, inHead.all.length);

    const baseNodeIndex = inBase?.index ?? null;
    if (baseNodeIndex !== null) baseNodesInDiff.add(baseNodeIndex);
    const headTargets = inHead.index === null ? [] : headObjects.get(inHead.index) ?? [];
    const baseTargets = baseNodeIndex === null ? [] : baseObjects.get(baseNodeIndex) ?? [];

    if (headTargets.length === 0 && baseTargets.length === 0) {
      // A removed node exists only in the previous version. When that version
      // isn't loaded, its absence here says nothing about the change list, and
      // reporting it as "in neither file" sends a reviewer hunting for a bug in
      // the handler that produced a perfectly correct diff.
      if (!base && change.kind === "removed") stats.needsBase++;
      else stats.unmatched++;
      continue;
    }

    const box = new Box3();
    const painted: Object3D[] = [];

    // Present in the current version → tint it in place on the head model.
    if (headTargets.length > 0 && change.kind !== "removed") {
      const color = KIND_COLOR[change.kind] ?? NEUTRAL;
      for (const target of headTargets) {
        for (const mesh of meshesIn(target)) {
          replaceMaterial(mesh, (material) => tint(material, color, tintCache), orphaned);
          paintedHeadMeshes.add(mesh);
          stats.tinted++;
        }
        painted.push(target);
        box.union(worldBox(target));
      }
    }

    // Gone from the current version → draw it from the base file, as a ghost.
    if (change.kind === "removed" && baseTargets.length > 0) {
      for (const target of baseTargets) {
        const ghost = ghostCloneAt(target, removedMaterial);
        removedGroup.add(ghost);
        painted.push(ghost);
        box.union(worldBox(target));
        stats.removedGhosts++;
      }
      if (baseNodeIndex !== null) baseNodesWithOwnGrammar.add(baseNodeIndex);
    }

    // Moved/rotated/scaled → ghost at the old pose plus a motion vector, so the
    // pair reads as one object that moved rather than as two objects.
    if (hasTransformChange(change) && baseTargets.length > 0 && headTargets.length > 0) {
      for (const target of baseTargets) {
        const ghost = ghostCloneAt(target, movedMaterial);
        movedGroup.add(ghost);
        painted.push(ghost);
        box.union(worldBox(target));
        stats.moveGhosts++;
      }
      if (baseNodeIndex !== null) baseNodesWithOwnGrammar.add(baseNodeIndex);

      const from = worldBox(baseTargets[0]!).getCenter(new Vector3());
      const to = worldBox(headTargets[0]!).getCenter(new Vector3());
      if (from.distanceTo(to) > MOTION_EPSILON) {
        movedGroup.add(motionVector(from, to, motionMaterial));
        const head = motionArrowhead(from, to, arrowheadMaterial);
        if (head) movedGroup.add(head);
        stats.motionVectors++;
      }
    }

    if (!box.isEmpty()) {
      changeBox.union(box);
      boxByChangeName.set(change.name, box);
    }
    if (painted.length > 0) {
      objectsByChangeName.set(change.name, painted);
      for (const object of painted) changeNameByObject.set(object, change.name);
    }
    if (inHead.index !== null && !changeNameByNodeIndex.has(inHead.index)) {
      changeNameByNodeIndex.set(inHead.index, change.name);
    }
  }

  /**
   * Paint some of a node's primitives and record the result under `name`.
   *
   * `ordinals` empty means every primitive of the node. GLTFLoader emits one Mesh
   * per primitive in primitive order, so an ordinal indexes straight into the
   * node's meshes; an ordinal past the end is a diff describing a primitive this
   * file doesn't have, which is skipped rather than thrown.
   */
  const paintNodePrimitives = (
    nodeIndex: number,
    ordinals: readonly number[],
    kind: string,
    name: string,
  ): Object3D[] => {
    const color = KIND_COLOR[kind] ?? NEUTRAL;
    const hit: Object3D[] = [];
    for (const target of headObjects.get(nodeIndex) ?? []) {
      const primitives = meshesIn(target);
      const chosen = ordinals.length === 0 ? primitives : ordinals.map((o) => primitives[o]);
      for (const mesh of chosen) {
        if (!mesh) continue;
        replaceMaterial(mesh, (material) => tint(material, color, tintCache), orphaned);
        paintedHeadMeshes.add(mesh);
        stats.tinted++;
        hit.push(mesh);
      }
      if (!changeNameByNodeIndex.has(nodeIndex)) changeNameByNodeIndex.set(nodeIndex, name);
    }
    return hit;
  };

  const recordEntityPaint = (change: EntityChange, painted: Object3D[]): void => {
    if (painted.length === 0) {
      // The key resolved to nothing this file draws — an unreferenced mesh, a
      // material no primitive uses. Counted so the banner can own it.
      stats.unpaintable++;
      return;
    }
    const box = new Box3();
    for (const object of painted) box.union(worldBox(object));
    if (!box.isEmpty()) {
      changeBox.union(box);
      boxByChangeName.set(change.name, box);
    }
    objectsByChangeName.set(change.name, painted);
    for (const object of painted) changeNameByObject.set(object, change.name);
  };

  // A mesh is drawn once per node instancing it, so one geometry edit can have
  // several places on screen — four wheels sharing one WheelMesh is the ordinary
  // case, and painting just the first would be a lie about where the change is.
  for (const change of input.meshes ?? []) {
    const painted: Object3D[] = [];
    for (const nodeIndex of resolveMeshNodes(head.index, change.name)) {
      painted.push(...paintNodePrimitives(nodeIndex, change.primitives, change.kind, change.name));
    }
    recordEntityPaint(change, painted);
  }

  // A material reaches geometry only through the primitives referencing it, so
  // this paints those primitives and not the nodes that merely contain them: a
  // recoloured trim material on one primitive of a ten-primitive mesh lights up
  // the trim, not the whole part.
  for (const change of input.materials ?? []) {
    const painted: Object3D[] = [];
    const byNode = new Map<number, number[]>();
    for (const ref of resolveMaterialPrimitives(head.index, change.name)) {
      const list = byNode.get(ref.node);
      if (list) list.push(ref.primitive);
      else byNode.set(ref.node, [ref.primitive]);
    }
    for (const [nodeIndex, ordinals] of byNode) {
      painted.push(...paintNodePrimitives(nodeIndex, ordinals, change.kind, change.name));
    }
    recordEntityPaint(change, painted);
  }

  // Changes with no representation on a static model at all. Never painted, only
  // counted — the alternative is a view that looks like an unchanged file.
  stats.unpaintable += (input.unpaintable ?? []).length;

  const paintApplied = stats.tinted > 0 || stats.removedGhosts > 0 || stats.moveGhosts > 0;

  // Quiet everything the diff didn't touch — but only once something *is* loud,
  // or a model with no locatable changes would go uniformly grey for no reason.
  if (paintApplied) {
    for (const mesh of meshesIn(head.gltf.scene)) {
      if (paintedHeadMeshes.has(mesh)) continue;
      replaceMaterial(mesh, (material) => desaturate(material, tintCache), orphaned);
      stats.desaturated++;
    }
  }

  root.add(headGroup);
  const hasRemoved = removedGroup.children.length > 0;
  const hasMoved = movedGroup.children.length > 0;
  if (hasRemoved) root.add(removedGroup);
  if (hasMoved) root.add(movedGroup);

  // The previous version: solid (hidden, for the blink) and ghosted (visible).
  let baseSolidGroup: Group | null = null;
  let baseGhostGroup: Group | null = null;
  if (base) {
    baseSolidGroup = new Group();
    baseSolidGroup.name = "fhr-base-solid";
    baseSolidGroup.add(base.gltf.scene);
    baseSolidGroup.visible = false;
    root.add(baseSolidGroup);

    baseGhostGroup = new Group();
    baseGhostGroup.name = "fhr-base-ghost";
    const ghost = base.gltf.scene.clone(true);
    // Two exclusions, for two different reasons.
    //
    // Nodes drawn under their own grammar (removed, moved) are hidden so the same
    // geometry isn't painted twice in two different translucent hues.
    //
    // Nodes the diff never mentions are hidden because their head counterpart is
    // identical and sits in exactly the same world position. A translucent copy
    // laid over an opaque twin conveys nothing — but it does put two coincident
    // surfaces in the pipeline, and three.js draws transparency after the opaque
    // pass with `depthWrite` off and an equal-depth test that *passes*, so the
    // ghost blends over its own twin wherever float depth happens to tie. The
    // result is faces washing out and winking in and out as the camera moves,
    // which reads as broken geometry. Unchanged parts are already accounted for:
    // the head model shows them desaturated, and the blink shows the whole
    // previous version on demand. The ghost's job is what *differs*.
    const ghostHidden = new Set<number>(baseNodesWithOwnGrammar);
    for (const index of baseObjects.keys()) {
      if (!baseNodesInDiff.has(index)) ghostHidden.add(index);
    }
    hideNodes(base.gltf.scene, ghost, ghostHidden, baseObjects);
    for (const mesh of meshesIn(ghost)) {
      (mesh as { material?: Material }).material = ghostBaseMaterial;
      mesh.renderOrder = -1;
    }
    baseGhostGroup.add(ghost);
    root.add(baseGhostGroup);
  }

  if (stats.unmatched > 0) {
    notes.push(
      `${stats.unmatched} changed ${stats.unmatched === 1 ? "node" : "nodes"} in the change list ` +
        `${stats.unmatched === 1 ? "isn't" : "aren't"} in either file's scene graph, so ${stats.unmatched === 1 ? "it isn't" : "they aren't"} highlighted here.`,
    );
  }

  if (stats.unpaintable > 0) {
    const one = stats.unpaintable === 1;
    notes.push(
      `${stats.unpaintable} ${one ? "change" : "changes"} in the list ${one ? "has" : "have"} no place on the ` +
        `model — an animation edit, or geometry nothing in the scene draws. ${one ? "It is" : "They are"} ` +
        `listed but not highlighted here.`,
    );
  }

  if (stats.needsBase > 0) {
    const one = stats.needsBase === 1;
    notes.push(
      `${stats.needsBase} removed ${one ? "node is" : "nodes are"} listed below but ${one ? "isn't" : "aren't"} ` +
        `drawn here: removed geometry can only come from the previous version, which isn't loaded. ` +
        `The change list is correct — the model just can't show what is no longer in it.`,
    );
  }

  const sceneBox = worldBox(root);

  return {
    root,
    headGroup,
    baseSolidGroup,
    baseGhostGroup,
    removedGroup: hasRemoved ? removedGroup : null,
    movedGroup: hasMoved ? movedGroup : null,
    changeBox,
    sceneBox,
    boxByChangeName,
    objectsByChangeName,
    changeNameByObject,
    changeNameByNodeIndex,
    nodeIndexOfObject: (object: Object3D): number | null => nodeIndexOfObject(object, head.gltf),
    stats,
    notes,
    dispose(): DisposeReport {
      // The shared overlay materials are attached to objects in the tree, so the
      // walk finds them; `orphaned` carries the originals tinting swapped out.
      return disposeTree(root, orphaned);
    },
  };
}

/** World-space bounds of a subtree (empty box for an empty subtree). */
function worldBox(object: Object3D): Box3 {
  return new Box3().setFromObject(object);
}

/** Swap a mesh's material(s) through `make`, remembering what was displaced. */
function replaceMaterial(
  mesh: Object3D,
  make: (material: Material) => Material,
  orphaned: Set<Material>,
): void {
  const holder = mesh as { material?: Material | Material[] };
  const current = holder.material;
  if (!current) return;
  if (Array.isArray(current)) {
    holder.material = current.map((m) => {
      orphaned.add(m);
      return make(m);
    });
    return;
  }
  orphaned.add(current);
  holder.material = make(current);
}

type Tintable = {
  color?: Color;
  emissive?: Color;
  emissiveIntensity?: number;
  metalness?: number;
  roughness?: number;
};

/**
 * A clone of `source` pulled towards `color`, with a little emissive so the
 * change stays legible in shadow. Cached per (material, colour) pair, because a
 * material shared by 500 primitives should yield one clone, not 500.
 */
function tint(source: Material, color: number, cache: Map<string, Material>): Material {
  const key = `${source.uuid}:${color.toString(16)}`;
  const cached = cache.get(key);
  if (cached) return cached;

  const clone = source.clone();
  const tintable = clone as unknown as Tintable;
  const target = new Color(color);
  if (tintable.color) tintable.color.lerp(target, 0.7);
  if (tintable.emissive) {
    tintable.emissive.copy(target);
    tintable.emissiveIntensity = 0.18;
  }
  cache.set(key, clone);
  return clone;
}

/** A clone of `source` drained of colour: present, but not competing. */
function desaturate(source: Material, cache: Map<string, Material>): Material {
  const key = `${source.uuid}:neutral`;
  const cached = cache.get(key);
  if (cached) return cached;

  const clone = source.clone();
  const tintable = clone as unknown as Tintable;
  if (tintable.color) tintable.color.lerp(new Color(NEUTRAL), 0.8);
  if (tintable.emissive) {
    tintable.emissive.setHex(0x000000);
    tintable.emissiveIntensity = 0;
  }
  if (typeof tintable.metalness === "number") tintable.metalness = Math.min(tintable.metalness, 0.1);
  if (typeof tintable.roughness === "number") tintable.roughness = Math.max(tintable.roughness, 0.75);
  cache.set(key, clone);
  return clone;
}

/**
 * A clone of a base subtree, frozen at that subtree's *world* pose (it is being
 * re-parented out from under its ancestors) and painted with one shared ghost
 * material. Geometry is shared with the original, not copied.
 */
function ghostCloneAt(source: Object3D, material: Material): Object3D {
  const clone = source.clone(true);
  clone.matrixAutoUpdate = false;
  clone.matrix.copy(source.matrixWorld);
  clone.matrixWorldNeedsUpdate = true;
  for (const mesh of meshesIn(clone)) {
    (mesh as { material?: Material }).material = material;
    mesh.renderOrder = -1;
  }
  // The clone itself may be the mesh.
  if ((clone as { isMesh?: boolean }).isMesh === true) {
    (clone as { material?: Material }).material = material;
    clone.renderOrder = -1;
  }
  return clone;
}

/** A two-point line: where a moved node was → where it is now. */
function motionVector(from: Vector3, to: Vector3, material: LineBasicMaterial): Line {
  const geometry = new BufferGeometry().setFromPoints([from, to]);
  const line = new Line(geometry, material);
  line.name = "fhr-motion";
  line.renderOrder = 1;
  return line;
}

/**
 * The cone at the destination end of a motion vector.
 *
 * A bare segment between two copies of one object states that they are related
 * but not which came first — the reviewer has to infer direction from the ghost
 * treatment, which means the arrow adds nothing the ghost didn't already say. A
 * head makes the reading unambiguous and local: the point sits on the current
 * position, so "it ended up here" is answered without cross-referencing anything.
 *
 * Returned as a separate sibling of the line rather than folded into it, so the
 * line stays a `Line` whose geometry callers already read.
 */
function motionArrowhead(from: Vector3, to: Vector3, material: Material): Mesh | null {
  const direction = new Vector3().subVectors(to, from);
  const distance = direction.length();
  if (distance <= MOTION_EPSILON) return null;
  direction.divideScalar(distance);

  // Proportional to the travel, so a millimetre nudge doesn't get a cone larger
  // than the move it describes, and a ten-metre move still gets a visible one.
  const length = distance * ARROWHEAD_LENGTH_RATIO;
  const radius = distance * ARROWHEAD_RADIUS_RATIO;

  const cone = new Mesh(new ConeGeometry(radius, length, 12), material);
  cone.name = "fhr-motion-head";
  cone.renderOrder = 1;
  // ConeGeometry points along +Y; aim it down the travel direction and pull it
  // back by half its length so the tip — not the centre — lands on `to`.
  cone.quaternion.setFromUnitVectors(CONE_AXIS, direction);
  cone.position.copy(to).addScaledVector(direction, -length / 2);
  return cone;
}

/**
 * Hide, in `clone`, the counterparts of the given base node indices. Original and
 * clone are walked in lockstep — `Object3D.clone` preserves child order, so the
 * n-th visited node of one is the n-th of the other.
 */
function hideNodes(
  original: Object3D,
  clone: Object3D,
  nodeIndices: Set<number>,
  objectsByIndex: Map<number, Object3D[]>,
): void {
  if (nodeIndices.size === 0) return;
  const wanted = new Set<Object3D>();
  for (const index of nodeIndices) {
    for (const object of objectsByIndex.get(index) ?? []) wanted.add(object);
  }
  if (wanted.size === 0) return;

  const originals: Object3D[] = [];
  original.traverse((o) => originals.push(o));
  const clones: Object3D[] = [];
  clone.traverse((o) => clones.push(o));
  for (let i = 0; i < originals.length && i < clones.length; i++) {
    if (wanted.has(originals[i]!)) clones[i]!.visible = false;
  }
}
