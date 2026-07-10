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

await build({
  entryPoints: ["src/index.ts"],
  bundle: true,
  format: "esm",
  platform: "browser",
  target: "es2020",
  outfile: "dist/renderer.js",
  define: { __BUILD__: JSON.stringify(sha) },
  minify: true,
  sourcemap: false,
});

console.log(`built dist/renderer.js (build ${sha})`);
