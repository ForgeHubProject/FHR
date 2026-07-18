import { build } from "esbuild";
import { execSync } from "node:child_process";

// Stamp the bundle with the same short commit SHA the release workflow uses,
// so RendererBundle.build matches the binary + wasm build for one release.
let sha = process.env["BUILD_SHA"];
if (!sha) {
  try {
    sha = execSync("git rev-parse --short HEAD").toString().trim();
  } catch {
    sha = "dev";
  }
}

const common = {
  bundle: true,
  format: "esm",
  platform: "browser",
  target: "es2020",
  define: { __BUILD__: JSON.stringify(sha) },
  minify: true,
  sourcemap: false,
};

// Lite bundle — the change-tree view, kept tiny. It dynamic-imports the 3D
// chunk (below) at runtime only when the viewer opens the scene.
await build({ ...common, entryPoints: ["src/index.ts"], outfile: "dist/renderer.js" });

// Heavy 3D chunk — inlines three.js. Published alongside the lite bundle as
// renderer-gltf-scene-3d.js; the lite bundle resolves it as a sibling.
await build({ ...common, entryPoints: ["src/index-3d.ts"], outfile: "dist/renderer-3d.js" });

console.log(`built dist/renderer.js + dist/renderer-3d.js (build ${sha})`);
