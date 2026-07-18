// The gltf-scene renderer's OWN 3D scene. three.js is this renderer's private
// choice for drawing its picture — it is NOT a shared FHR contract and nothing
// here is reused by other formats' renderers. The only FHR contracts are
// mount() and StructuredDiff; everything in this file lives inside this one
// bundle. A different 3D format's renderer is free to draw however it likes.

import * as THREE from "three";
import { OrbitControls } from "three/examples/jsm/controls/OrbitControls.js";
import type { SceneNode } from "./scene-graph.js";

type Theme = "light" | "dark";

export type SceneHandle = { dispose(): void };

const deg2rad = (d: number): number => (d * Math.PI) / 180;

/**
 * Imperative: mount a three.js scene of the given nodes into `container`. Each
 * node is a unit box placed by its transform and tinted by its change kind;
 * removed nodes render translucent. Lighting is a simple ambient+directional
 * rig — no external HDR, so it stays within the strict CSP both hosts enforce.
 * Returns a handle that stops the animation loop and releases GPU resources.
 * Requires a DOM + WebGL context (i.e. a real browser).
 */
export function mountScene(container: HTMLElement, nodes: SceneNode[], theme: Theme = "light"): SceneHandle {
  const width = container.clientWidth || 640;
  const height = container.clientHeight || 420;

  const scene = new THREE.Scene();
  scene.background = new THREE.Color(theme === "dark" ? 0x0d1117 : 0xf6f8fa);

  const camera = new THREE.PerspectiveCamera(50, width / height, 0.1, 5000);

  const renderer = new THREE.WebGLRenderer({ antialias: true });
  renderer.setPixelRatio(Math.min(globalThis.devicePixelRatio || 1, 2));
  renderer.setSize(width, height);
  container.appendChild(renderer.domElement);

  scene.add(new THREE.AmbientLight(0xffffff, 0.75));
  const key = new THREE.DirectionalLight(0xffffff, 0.85);
  key.position.set(6, 10, 8);
  scene.add(key);

  scene.add(
    new THREE.GridHelper(
      40,
      40,
      theme === "dark" ? 0x30363d : 0xd0d7de,
      theme === "dark" ? 0x21262d : 0xe6edf3,
    ),
  );

  const boxGeom = new THREE.BoxGeometry(1, 1, 1);
  const materials: THREE.Material[] = [];
  const group = new THREE.Group();
  const bounds = new THREE.Box3();

  for (const n of nodes) {
    const removed = n.kind === "removed";
    const mat = new THREE.MeshStandardMaterial({
      color: n.color,
      metalness: 0.1,
      roughness: 0.7,
      transparent: removed,
      opacity: removed ? 0.45 : 1,
    });
    materials.push(mat);
    const mesh = new THREE.Mesh(boxGeom, mat);
    mesh.position.set(n.position[0], n.position[1], n.position[2]);
    mesh.rotation.set(deg2rad(n.rotationEulerDeg[0]), deg2rad(n.rotationEulerDeg[1]), deg2rad(n.rotationEulerDeg[2]));
    mesh.scale.set(n.scale[0], n.scale[1], n.scale[2]);
    group.add(mesh);
    bounds.expandByObject(mesh);
  }
  scene.add(group);

  // Frame the camera on the content.
  const center = new THREE.Vector3();
  const size = new THREE.Vector3(2, 2, 2);
  if (!bounds.isEmpty()) {
    bounds.getCenter(center);
    bounds.getSize(size);
  }
  const radius = Math.max(size.x, size.y, size.z, 1);
  camera.position.set(center.x + radius * 2.2, center.y + radius * 1.8, center.z + radius * 2.2);
  camera.lookAt(center);

  const controls = new OrbitControls(camera, renderer.domElement);
  controls.target.copy(center);
  controls.enableDamping = true;
  controls.update();

  let raf = 0;
  let alive = true;
  const tick = (): void => {
    if (!alive) return;
    raf = requestAnimationFrame(tick);
    controls.update();
    renderer.render(scene, camera);
  };
  tick();

  const onResize = (): void => {
    const w = container.clientWidth || width;
    const h = container.clientHeight || height;
    camera.aspect = w / h;
    camera.updateProjectionMatrix();
    renderer.setSize(w, h);
  };
  globalThis.addEventListener?.("resize", onResize);

  return {
    dispose() {
      alive = false;
      cancelAnimationFrame(raf);
      globalThis.removeEventListener?.("resize", onResize);
      controls.dispose();
      boxGeom.dispose();
      for (const m of materials) m.dispose();
      renderer.dispose();
      renderer.domElement.parentNode?.removeChild(renderer.domElement);
    },
  };
}
