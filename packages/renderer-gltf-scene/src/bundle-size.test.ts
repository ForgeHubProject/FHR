// CI-enforced bundle budgets.
//
// Two things are being protected. The 3D chunk has a 250 KB gzip budget, well
// under SPEC-RENDERING's 3 MB ceiling, because it is downloaded during a review
// on someone else's connection — a ceiling is not a target. And the lite bundle
// must stay a few KB: it is what every reviewer pays for the change tree, so a
// stray `import * as THREE` in a shared module (a palette, a type helper) has to
// fail here rather than quietly multiply it by fifty.
//
// The build runs for real, so this also proves both entries still bundle.

import { describe, it, expect, beforeAll } from "vitest";
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { gzipSync } from "node:zlib";
import path from "node:path";
import { fileURLToPath } from "node:url";

const packageRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const budgets: Record<string, number> = JSON.parse(
  readFileSync(path.join(packageRoot, "package.json"), "utf8"),
).fhr.bundleBudgetGzipBytes;

const read = (file: string): string =>
  readFileSync(path.join(packageRoot, "dist", file), "utf8");
const gzipBytes = (file: string): number =>
  gzipSync(readFileSync(path.join(packageRoot, "dist", file)), { level: 9 }).byteLength;
const kb = (bytes: number): string => `${(bytes / 1024).toFixed(1)} KB`;

describe("bundle budgets", () => {
  beforeAll(() => {
    execFileSync("node", ["build.mjs"], { cwd: packageRoot, stdio: "pipe" });
  }, 180_000);

  it("declares a budget for every published bundle", () => {
    expect(Object.keys(budgets).sort()).toEqual(["renderer-3d.js", "renderer.js"]);
  });

  it("keeps the 3D chunk under its gzip budget", () => {
    const size = gzipBytes("renderer-3d.js");
    const budget = budgets["renderer-3d.js"]!;
    expect(size, `renderer-3d.js is ${kb(size)} gzip, budget ${kb(budget)}`).toBeLessThanOrEqual(budget);
  });

  it("keeps the lite bundle tiny", () => {
    const size = gzipBytes("renderer.js");
    const budget = budgets["renderer.js"]!;
    expect(size, `renderer.js is ${kb(size)} gzip, budget ${kb(budget)}`).toBeLessThanOrEqual(budget);
  });

  it("keeps three.js out of the lite bundle entirely", () => {
    const lite = read("renderer.js");
    // Class names survive minification inside three's own error strings.
    for (const marker of ["WebGLRenderer", "BufferGeometry", "GLTFLoader", "OrbitControls"]) {
      expect(lite, `three.js leaked into the lite bundle (${marker})`).not.toContain(marker);
    }
  });

  it("puts the real-model loader in the 3D chunk", () => {
    const chunk = read("renderer-3d.js");
    expect(chunk).toContain("GLTFLoader");
  });
});
