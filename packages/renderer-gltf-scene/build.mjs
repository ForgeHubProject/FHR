import { build } from "esbuild";
import { execSync } from "node:child_process";
import { gzipSync } from "node:zlib";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";

const packageRoot = path.dirname(fileURLToPath(import.meta.url));
const pkg = JSON.parse(readFileSync(path.join(packageRoot, "package.json"), "utf8"));

/**
 * Gzip budgets per output file, from package.json's `fhr.bundleBudgetGzipBytes`
 * so the build and the test that enforces it can't drift apart. SPEC-RENDERING's
 * 3 MB is a *ceiling*, not a target: the 3D chunk is lazily loaded but it is
 * still loaded over someone's connection during a review, so it gets a budget
 * with room for one more slice, not for three.js's worth of surprises.
 */
export const BUDGETS = pkg.fhr.bundleBudgetGzipBytes;

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
  absWorkingDir: packageRoot,
};

// Lite bundle — the change-tree view, kept tiny. It dynamic-imports the 3D
// chunk (below) at runtime only when the viewer opens the scene.
await build({ ...common, entryPoints: ["src/index.ts"], outfile: "dist/renderer.js" });

// Heavy 3D chunk — inlines three.js. Published alongside the lite bundle as
// renderer-gltf-scene-3d.js; the lite bundle resolves it as a sibling.
await build({ ...common, entryPoints: ["src/index-3d.ts"], outfile: "dist/renderer-3d.js" });

/** Gzipped size of a built file, in bytes — what a host actually ships. */
export function gzipSize(file) {
  return gzipSync(readFileSync(path.join(packageRoot, "dist", file)), { level: 9 }).byteLength;
}

const over = [];
const report = [];
for (const [file, budget] of Object.entries(BUDGETS)) {
  const size = gzipSize(file);
  const percent = Math.round((size / budget) * 100);
  report.push(`  ${file.padEnd(16)} ${fmtKb(size).padStart(9)} gzip  (${percent}% of ${fmtKb(budget)})`);
  if (size > budget) over.push(`${file} is ${fmtKb(size)} gzip, over its ${fmtKb(budget)} budget`);
}

console.log(`built dist/renderer.js + dist/renderer-3d.js (build ${sha})`);
console.log(report.join("\n"));

function fmtKb(bytes) {
  return `${(bytes / 1024).toFixed(1)} KB`;
}

if (over.length > 0) {
  console.error(`\nbundle budget exceeded:\n  ${over.join("\n  ")}`);
  // Fail the build, not just the test suite: the release workflow builds the
  // bundle without running vitest.
  process.exitCode = 1;
}
