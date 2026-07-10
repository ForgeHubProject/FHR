# FHR Rendering & Compute Tiers Specification

**Status: DRAFT — for discussion.** Companion to [SPEC.md](./SPEC.md). This
document extends §8 (Frontend Renderer Contract) with a distribution and
serving model, adds WASM handler builds as a platform asset, and defines a
tiered model for *where* diffs are computed and rendered. Where this document
and SPEC.md §11 disagree on versioning, the content-hash rolling-release
scheme (FHR#4, forge#21/#23) is authoritative.

Implementation is intentionally out of scope; see the tracking issues:

- FHR: renderer distribution + WASM builds (this repo)
- forge: `forge diff --web` local renderer serving
- ForgeHub: tiered rendering + client-side render toggle

---

## 1. Motivation

Three observations from project review:

1. **The FHR → ForgeHub renderer handover is too direct.** ForgeHub is the
   only consumer of renderers, so the renderer contract never has to be truly
   consumer-agnostic, and forge — the tool that actually computes semantic
   diffs — has the weakest way to display them (terminal text).
2. **Forge should expose renderers itself.** The `git difftool` analogy:
   when a GUI is wanted, forge should serve the format's renderer to a local
   browser instead of expecting a desktop differ. Renderers are TS/JS; the
   browser is their natural runtime, and forge already owns the distribution
   pipeline (manifest → assets → `~/.forge/`).
3. **Server cost and client capability pull in opposite directions.**
   Client-side compute reduces server load but forces full blob downloads
   and repeats work on every viewer's machine — including weak ones.
   Server-side compute is cacheable and canonical but can be slow or costly.
   Neither should be hardwired.

Design goal: **where computation and rendering happen becomes a deployment /
runtime decision, not an architecture decision.** The enabler is a renderer
contract that consumes a `StructuredDiff` without caring who computed it.

## 2. Renderer bundle distribution

### 2a. Authoring floor (unchanged)

Handler authors keep writing the SPEC.md §8 floor: three React/TypeScript
components (`SnapshotRenderer`, `DiffRenderer`, `MergeResolver`) exported via
an `FHRManifest` object.

### 2b. Build output: self-contained ESM with a `mount()` wrapper

The package build step wraps the React floor in a framework-agnostic entry
point and inlines all dependencies (React, three.js, etc.):

```ts
// renderer.js — generated wrapper, single self-contained ESM file
export default {
  fhrVersion: 1,
  handlerId: "gltf-scene",
  build: "20260709-f1dd134",          // same content-hash build as the binaries
  mount(el: HTMLElement, props: MountProps): { update(p: MountProps): void; unmount(): void };
};

type MountProps = {
  mode: "view" | "diff" | "merge";
  diff?: StructuredDiff;              // computed by ANY tier (§4)
  blobs?: { base?: BlobRef; head?: BlobRef; ours?: BlobRef; theirs?: BlobRef };
  theme?: "light" | "dark";
  onEvent?: (e: RendererEvent) => void;   // e.g. merge resolution produced
};

type BlobRef = { url: string; size: number };  // consumer-served, same-origin
```

Rationale:

- **Framework-agnostic consumers.** forge's local shell page (§3b) must not
  need to be a React app; ForgeHub must not be pinned to the React version a
  bundle was built against. Only the wrapper boundary is standardized.
- **Self-contained = no runtime network.** The bundle makes no external
  fetches; blobs and diff JSON are provided by the consumer. This is what
  makes the same bundle work on ForgeHub, on `localhost` from forge, and
  offline.
- Bundle size is the accepted cost. Guidance: warn in CI above 3 MB gzipped;
  three.js-class dependencies are expected for 3D formats.

### 2c. Manifest schema additions

Two additions to the §4 manifest, both following the post-FHR#4 flat shape:

```toml
# WASM build of the backend handler — one more platform key, same table.
[assets.handlers."gltf-scene"]
"linux-amd64" = "https://github.com/forgehubproject/fhr/releases/download/gltf-scene-latest/forge-handler-gltf-scene-linux-amd64"
# ... existing platforms ...
"wasm"        = "https://github.com/forgehubproject/fhr/releases/download/gltf-scene-latest/forge-handler-gltf-scene.wasm"

# Renderer bundle — not platform-keyed; one ESM target.
[assets.renderers]
"gltf-scene" = "https://github.com/forgehubproject/fhr/releases/download/gltf-scene-latest/renderer-gltf-scene.js"
```

The existing rolling-release workflow builds and attaches both artifacts on
every push to the handler package; the `update-manifest` job stamps the same
build SHA into binaries, WASM, and renderer. **Backend, WASM, and renderer
are always released together under one content-hash build** — this preserves
the "no version skew between StructuredDiff producer and consumer" guarantee
of SPEC.md §11 without semver.

WASM builds come from the same Go source (`GOOS=js GOARCH=wasm`, or TinyGo if
size demands it). A handler package MAY omit the `wasm` key; consumers then
simply cannot offer in-browser compute for that format (§4, Tier B).

### 2d. Installation and pinning

`forge formats add <ext>` additionally downloads the renderer bundle to
`~/.forge/renderers/<handlerID>.js` (+ `.json` sidecar meta, mirroring
`~/.forge/plugins/`). The per-repo `.forge/handlers` lockfile entry pins one
build for binary + WASM + renderer jointly — no separate renderer pin.

## 3. Consumers

### 3a. ForgeHub web

Dynamically imports the renderer bundle for a file's handler (URL from the
repo's pinned build, cached by content hash) and calls `mount()`. Over time
this **replaces the hardcoded viewers** in `apps/web`; the built-in gltf/text
viewers become the first two published bundles. Sandboxing for community
(non-official) bundles remains an open question (§7, SPEC.md open question 5).

### 3b. forge local web UI — `forge diff --web`

```
forge diff <file> [<base>..<head>] --web
```

1. Resolves the handler + renderer for the file (per-repo scope via
   `.forge/formats`, pinned build via `.forge/handlers`).
2. Computes the `StructuredDiff` via the installed subprocess handler.
3. Starts a loopback-only HTTP server serving: a minimal static shell page,
   the renderer bundle from `~/.forge/renderers/`, the diff JSON, and blob
   endpoints backed by the git object database / working tree.
4. Opens the default browser; exits on Ctrl-C.

No blob ever leaves the machine; no network access is required. A later
`forge mergetool --web` reuses the same shell with `mode: "merge"`, writing
the resolution back through the existing mergetool path.

## 4. Compute tiers

| Tier | Diff computed by | Crosses the wire | Role |
|---|---|---|---|
| **S — Server** (default) | ForgeHub API handler, cached in `diffCache` | small `StructuredDiff` JSON | **Canonical.** The record for review/approval. Works on any client. |
| **B — Browser** (opt-in) | WASM handler build in the viewer's browser | both raw blobs + wasm + renderer | Offloads server CPU. Viewing convenience only. |
| **L — Local** (opt-in) | Local forge binary via `forge diff --web` | nothing (blobs already in the clone) | Cheapest for large assets; zero server involvement. |

Invariants:

- **Tier S remains canonical.** Client-computed diffs are a viewing
  convenience; anything review-shaped (approvals, merge decisions recorded by
  the server) refers to the server-computed diff.
- **Build pinning gives consistency.** Tiers B and L use the build pinned in
  the repo's `.forge/handlers`. If the executing build differs from the
  server's, the UI must say so rather than silently render a different diff.
- **Tier availability is capability-detected**, never assumed: Tier B
  requires a `wasm` asset in the manifest and acceptable blob sizes; Tier L
  requires a local clone.

## 5. Mode selection UX (ForgeHub)

- **Quiet by default.** A small mode indicator on the diff/merge header
  ("rendered on server · switch"). No machinery until there is friction.
- **Reactive nudge.** If the Tier-S request exceeds a latency threshold (or
  the payload is known-large up front), replace the spinner with an offer:
  "taking longer than usual — render on your machine instead?"
- **Honest costs.** The Tier-B option displays the download it implies
  ("Render in browser — downloads 2 × 84 MB"); it is hidden entirely when
  capability detection fails. Tier L is offered as **"Open in forge"** — a
  copyable `forge diff --web <file> <base>..<head>` command (deep link
  protocol optional, later).
- **Sticky preference.** The chosen mode persists per format (and optionally
  per repo/device); a global "prefer client-side rendering" lives in user
  settings. Toggle usage is itself a signal of where Tier S is too slow.
- **Merge is phased separately.** Client-side merge *resolution* uploads a
  resolved blob for server validation — a bigger contract than read-only
  diff viewing. Not in the first iteration.

## 6. Security model

- Renderer bundles and WASM builds ride the **same trust chain as handler
  binaries** (already executed natively today): content-hash builds, hashes
  recorded in installed metadata, pinned per-repo via `.forge/handlers`.
- Bundles are self-contained; the forge local shell serves everything from
  loopback and sets a CSP that blocks all external requests, so a bundle
  cannot exfiltrate blob data.
- ForgeHub executing *official* bundles is equivalent to today's vendored
  viewers. Community bundles need the iframe/postMessage sandbox already
  listed as SPEC.md open question 5 — unchanged by this document.

## 7. Phasing

1. **P1 — Contract + first artifacts:** finalize `mount()` wrapper types in
   `@fhr/types`; release workflow additionally builds + attaches
   `renderer-gltf-scene.js` (port of ForgeHub's viewer) and the `wasm` build;
   manifest gains §2c keys.
2. **P2 — forge:** `forge formats add` installs renderers; `forge diff --web`.
3. **P3 — ForgeHub adoption:** web app loads FHR bundles instead of
   hardcoded viewers (Tier S unchanged otherwise).
4. **P4 — Tier B:** in-browser WASM compute behind the §5 toggle.
5. **P5 — Merge:** `forge mergetool --web` and ForgeHub client-side merge
   resolution.

## 8. Open questions

1. `mount()` wrapper generation: hand-written per package vs. a small
   `@fhr/renderer-sdk` build helper (recommended: SDK helper, keeps the floor
   authoring experience identical to SPEC.md §8).
2. Go `wasm` binary size (js/wasm runtime is multi-MB): acceptable, or does
   TinyGo become a requirement for Tier B viability?
3. Blob-size ceiling for offering Tier B at all (proposal: hide above
   200 MB combined).
4. Deep-link protocol (`forge://…`) vs. copyable command for "Open in forge"
   (proposal: copyable command first; protocol handlers are per-OS pain).
5. Does ForgeHub server ever adopt the WASM builds itself (ForgeHub#59
   Option B via WASM instead of subprocesses)? Out of scope here, but the
   `wasm` asset makes it possible without new FHR work.
