# FHR — Forge Handler Repository Specification

> Version: 1.1 · Status: Draft

This document is the authoritative specification for the FHR ecosystem. It covers the full contract between Forge (the CLI), ForgeHub (the web platform), and FHR packages (format handler + renderer bundles).

---

## Table of Contents

1. [Overview](#1-overview)
2. [Ecosystem Architecture](#2-ecosystem-architecture)
3. [FHR Package Structure](#3-fhr-package-structure)
4. [Source Manifest Schema](#4-source-manifest-schema)
5. [`.forge-formats` File Format](#5-forge-formats-file-format)
6. [Forge CLI — Source & Format Commands](#6-forge-cli--source--format-commands)
7. [Backend Handler Contract](#7-backend-handler-contract)
8. [Frontend Renderer Contract](#8-frontend-renderer-contract)
9. [FHRManifest & ForgeHub Loading](#9-fhrmanifest--forgehub-loading)
10. [Handler Loading at Runtime](#10-handler-loading-at-runtime)
11. [Versioning Scheme](#11-versioning-scheme)
12. [fhr-official Bootstrap & Migration](#12-fhr-official-bootstrap--migration)
13. [Publishing a New Handler](#13-publishing-a-new-handler)
14. [Open Questions](#14-open-questions)

---

## 1. Overview

FHR (Forge Handler Repository) is the package registry layer of the Forge ecosystem. It plays the same role that npm registries play for JavaScript packages, or Homebrew taps play for macOS tools — but for **format handlers**: the code that teaches Forge and ForgeHub how to semantically diff, merge, and render hardware artifacts.

The three-repo ecosystem:

| Repo | Role | Analogy |
|------|------|-------- |
| **Forge** | CLI version control tool | git |
| **ForgeHub** | Web collaboration platform | GitHub |
| **FHR** | Handler + renderer registry | npm registry / Homebrew tap |

The pipeline:

```
FHR ──────────────────▶ Forge ──────────────────▶ ForgeHub
(package registry)      (git-like VCS,             (web showcase,
                         pulls + runs FHR            displays what
                         handlers)                   Forge produced)
```

An FHR is an HTTP endpoint that publishes a source manifest listing which file extensions it supports and which handler versions are available. Any server that serves this manifest is a valid FHR — including a GitHub repo with a static TOML file and release assets.

### What is NOT in scope

- **Domain groupings** — there is no domain abstraction in this version. Extensions are mapped directly to handlers; no hierarchy, no family-level fallbacks. If a predefined set of extensions is useful (e.g. "all common 3D formats"), it will be addressed as user-facing **presets** in a future version — a named `.forge-formats` template that users can apply in one command. The implementation complexity of a domain layer is not justified at this stage.
- **`.forge/handlers`** — the domain-level manifest previously discussed. Deprecated before implementation. `.forge-formats` is the single per-repo format declaration file.
- **`forge domain` commands** — replaced by `forge source` and `forge formats`.

---

## 2. Ecosystem Architecture

```
┌──────────────────────────────────────────────────────────────────┐
│  Repository                                                      │
│  .forge-formats  ←── which extensions this repo cares about     │
└────────────────────────────┬─────────────────────────────────────┘
                             │
            ┌────────────────▼────────────────────┐
            │            Forge CLI                 │
            │   forge source · forge formats       │
            └──────┬──────────────────┬────────────┘
                   │                  │
       reads       │                  │  fetches manifests
                   ▼                  ▼
            sources.list        FHR endpoints
            (~/.forge/          (HTTP servers serving
            sources.list)        manifest.toml)
                   │
       ┌───────────▼──────────────────────────┐
       │         Handler Registry              │
       │  extension → handler binary/WASM      │
       │  downloads on first use, caches       │
       └──────────────────────────────────────┘

┌──────────────────────────────────────────────────────────────────┐
│  ForgeHub (web)                                                  │
│  loads FHRManifest (JS/ESM) → mounts SnapshotRenderer,          │
│  DiffRenderer, MergeResolver per file extension                  │
└──────────────────────────────────────────────────────────────────┘
```

**Data flow for a PR diff view:**

1. ForgeHub API calls the backend handler's `diff(base, head)` → returns `StructuredDiff` JSON
2. ForgeHub web looks up the `DiffRenderer` registered for that extension via the loaded `FHRManifest`
3. `DiffRenderer` receives `{ baseSnapshot, targetSnapshot, diffResult }` and renders it in the browser

**Built-in handler:**

The `plain-text` handler is compiled into Forge itself and is always available as the catch-all fallback. It never needs to be fetched from FHR. Any file not claimed by an installed handler falls through to `plain-text`.

---

## 3. FHR Package Structure

An FHR author publishes **two artifacts** per handler release:

### 3a. Backend handler

Used by the Forge CLI and ForgeHub API. Distributed as:
- **Native binaries** (`forge-handler-<name>_linux-amd64`, etc.) for the Forge CLI subprocess protocol
- A **WASM module** (`forge-handler-<name>.wasm`) for ForgeHub API server-side execution (language-agnostic, sandboxed)

Both implement the same stdin/stdout JSON protocol (see §7). The backend can be written in **any language**.

### 3b. Frontend renderer

Used by ForgeHub web. Distributed as:
- An **ES module bundle** (`renderer.js`) exporting an `FHRManifest` object (see §9)

The renderer must be TypeScript + React — it runs in the browser.

### 3c. Source manifest

A TOML file served at a well-known URL listing all supported extensions and asset download paths (see §4).

**Minimal FHR layout (hosted as a static site or GitHub repo):**

```
my-fhr/
├── manifest.toml
├── handlers/
│   └── gltf-scene/
│       └── 1.0.0/
│           ├── forge-handler-gltf-scene_linux-amd64
│           ├── forge-handler-gltf-scene_darwin-arm64
│           ├── forge-handler-gltf-scene_windows-amd64.exe
│           └── forge-handler-gltf-scene.wasm
└── renderers/
    └── gltf-scene/
        └── 1.0.0/
            └── renderer.js
```

---

## 4. Source Manifest Schema

Forge fetches and caches this on `forge source update`. It is a TOML file served at a stable URL.

```toml
# Required
name    = "fhr-official"
url     = "https://fhr.forge.io"   # base URL; asset paths are relative to this
version = "1.0"

# Optional
description = "Official Forge Handler Repository"
maintainer  = "ForgeHub Project"

# Extension → handler mapping.
# Each key is a file extension; value declares which handler version to use.
[formats]
".gltf"  = { handler = "gltf-scene", version = "1.0.0" }
".glb"   = { handler = "gltf-scene", version = "1.0.0" }
".obj"   = { handler = "obj-scene",  version = "1.0.0" }
".blend" = { handler = "blender",    version = "1.0.0" }
# Note: plain-text extensions (.txt, .md, etc.) are handled by Forge's
# built-in text handler and do not need to appear here.

# Asset download paths (relative to `url`).
# Forge constructs: {url}/{path} to download a handler binary or renderer.
[assets.handlers."gltf-scene"."1.0.0"]
"linux-amd64"   = "handlers/gltf-scene/1.0.0/forge-handler-gltf-scene_linux-amd64"
"darwin-amd64"  = "handlers/gltf-scene/1.0.0/forge-handler-gltf-scene_darwin-amd64"
"darwin-arm64"  = "handlers/gltf-scene/1.0.0/forge-handler-gltf-scene_darwin-arm64"
"windows-amd64" = "handlers/gltf-scene/1.0.0/forge-handler-gltf-scene_windows-amd64.exe"
"wasm"          = "handlers/gltf-scene/1.0.0/forge-handler-gltf-scene.wasm"

[assets.renderers."gltf-scene"."1.0.0"]
"esm" = "renderers/gltf-scene/1.0.0/renderer.js"
```

**Rules:**
- There are no domain groupings in the manifest. Each extension is mapped directly.
- If two sources both claim the same extension, Forge emits a conflict warning and the user must pin the extension to a specific source in `.forge-formats`.
- The `plain-text` catch-all is never listed here; it is built into Forge.

---

## 5. `.forge-formats` File Format

`.forge-formats` is a per-repository TOML file committed to the repo root. It is the single declaration of which file extensions Forge applies semantic handling to in this repo.

```toml
# .forge-formats

[include]
# Resolve a handler for this extension from any configured source.
".gltf"  = {}
".glb"   = {}
# Pin to a specific source when two sources conflict.
".blend" = { source = "fhr-official" }

[ignore]
# Track as opaque blob — no handler, no prompt, blob view only in ForgeHub.
".tif"   = {}
".tmp"   = {}
```

### Classification rules

| State | Tracked by Git | Handler resolved | Prompt shown |
|-------|---------------|------------------|--------------|
| `[include]` | yes | yes | no |
| `[ignore]` | yes | no | no |
| unregistered (neither list) | yes | no | yes — classify or skip |

- **Text-based files** are handled by Forge's built-in `plain-text` handler and do not need to appear in `.forge-formats`.
- **Unregistered binary files** prompt the user: `[a]dd to include / [i]gnore / [s]kip`.
- A file in `[include]` with no handler found in any source produces a warning at `forge formats status` and degrades to blob view — it is never a hard error.
- Two sources claiming the same extension without a `source` pin produce a conflict warning until resolved with `forge formats pin`.

### Presets (future)

Predefined `.forge-formats` templates (e.g. a "3D repo starter" that includes `.gltf`, `.glb`, `.obj`, `.blend`) will be introduced as **presets** in a future version. A preset is a named configuration applied with a single command; it expands to explicit extension entries in `.forge-formats`. This replaces the domain shorthand concept and keeps the file format flat and readable.

---

## 6. Forge CLI — Source & Format Commands

### `forge source` — manages `~/.forge/sources.list`

```
forge source add <url>
```
Fetches the manifest at `<url>`, validates it, appends it to `sources.list`. Prints the source name and advertised extensions. Warns immediately if any extension conflicts with an already-configured source.

```
forge source remove <name>
```
Removes the named source. Does not uninstall already-downloaded handlers.

```
forge source list
```
Prints all configured sources with their URL, last-fetched timestamp, and advertised extensions.

```
forge source update
```
Re-fetches all source manifests and updates the local cache. Does not download handler binaries — only refreshes the index. Analogous to `apt-get update`.

**`~/.forge/sources.list` format:**
```toml
[[source]]
name = "fhr-official"
url  = "https://fhr.forge.io/manifest.toml"

[[source]]
name = "community-blend"
url  = "https://handlers.blendercommunity.org/manifest.toml"
```

---

### `forge formats` — manages `.forge-formats` in the current repo

```
forge formats add <extension>
```
Adds the extension to `[include]`. Resolves against configured sources and prints the matched handler. Warns if no handler is found or if two sources conflict (suggests `forge formats pin`).

```
forge formats ignore <extension>
```
Adds the extension to `[ignore]`.

```
forge formats pin <extension> <source>
```
Sets `source = "<source>"` on an existing `[include]` entry. Errors if the extension is not in `[include]` or if `<source>` does not claim it.

```
forge formats status
```
Prints every extension present in the working tree and its resolution state:

```
Extension   Status          Handler              Source
────────────────────────────────────────────────────────────────────
.gltf       resolved        gltf-scene@1.0.0     fhr-official
.blend      resolved        blender@1.0.0        fhr-official  [pinned]
.fbx        warning         —                    — (no handler in any source)
.tif        ignored         —                    —
.llm        unregistered    —                    — (run: forge formats add .llm)
```

**Augmented `forge status` output:**
```
Warnings:
  .blend  → 2 sources claim this extension (fhr-official, community-blend)
             run: forge formats pin .blend <source>
  .fbx    → in .forge-formats [include] but no handler found
             run: forge source update  or  forge source add <url>
```

---

## 7. Backend Handler Contract

All backend handlers implement the same contract regardless of language. The interface is expressed in TypeScript for ForgeHub API (direct import or WASM) and as a stdin/stdout JSON protocol for the Forge CLI subprocess.

### TypeScript interface (ForgeHub API)

```ts
type IngestInput = {
  repoId: string;
  sourceFile: string;
  utf8Text: string;
  label: string | null;
  gitCommitSha: string | null;
};

type MergeResult = {
  blob: Buffer;
  conflicts?: ConflictInfo;
};

type ConflictInfo = {
  conflicts: SemanticConflict[];
};

type SemanticConflict = {
  path: string;   // semantic path, e.g. "nodes[2].translation"
  ours: unknown;
  theirs: unknown;
};

interface ArtifactHandler {
  readonly id: string;  // matches handler name in manifest.toml
  readonly capabilities: {
    semanticCompare: boolean;
    semanticMerge: boolean;
  };
  matchesPath(path: string): boolean;
  ingestFromUtf8Text(input: IngestInput): Promise<string>;
  diff(base: Buffer, head: Buffer): Promise<StructuredDiff>;
  merge?(base: Buffer, ours: Buffer, theirs: Buffer): Promise<MergeResult>;
}
```

### `StructuredDiff` wire format

This is the shared output format. Forge produces it; ForgeHub consumes it.

```ts
type DiffChange = {
  path: string;            // semantic path, e.g. "nodes[2].translation"
  kind: "added" | "removed" | "modified";
  label?: string;          // human-readable label
  before?: unknown;
  after?: unknown;
  children?: DiffChange[]; // for hierarchical formats
};

type StructuredDiff = {
  version: "1.0";
  format: string;          // handler id, e.g. "gltf-scene"
  changes: DiffChange[];
};
```

### Forge CLI subprocess protocol

Forge discovers external handlers as executables named `forge-handler-<name>` in:
1. `~/.forge/plugins/` (installed via `forge source`)
2. Anywhere on `$PATH`
3. *(built-in: `plain-text`, always available)*

Communication is over stdin/stdout JSON:

**`forge-handler-<name> match <filepath>`**
```
stdout: "true" or "false"
exit:   0 always
```

**`forge-handler-<name> diff`**
```
stdin:  { "base": "<base64>", "head": "<base64>" }
stdout: StructuredDiff JSON
exit:   0 on success, 1 on error
```

**`forge-handler-<name> merge`**
```
stdin:  { "base": "<base64>", "ours": "<base64>", "theirs": "<base64>" }
stdout: { "blob": "<base64>", "conflicts": [ SemanticConflict, … ] }
exit:   0 on success, 1 on error
        if merge is not supported: exit 1 with { "error": "not supported" }
        Forge falls back to blob-pick, same as plain git today.
```

**`forge-handler-<name> info`** *(optional but recommended)*
```
stdout: { "id": "gltf-scene", "version": "1.0.0", "formats": [".glb", ".gltf"], "protocol": "1.0" }
exit:   0 always
```

Blobs are base64-encoded to keep the transport pure JSON. The same binary works as both a CLI subprocess and a WASM module — the protocol is identical.

---

## 8. Frontend Renderer Contract

Every FHR renderer must implement three React components (the **floor**). The frontend is always TypeScript + React regardless of what language the backend handler is written in.

### Supporting types

```ts
type Snapshot = {
  id: string;
  commitSha: string;
  filePath: string;
  content: string;    // raw file content, base64-encoded
  entities: unknown;  // parsed IR from ingestFromUtf8Text
};
```

### Floor (required — all three)

```ts
// 1. Single-file viewer — blob page, commit page
type SnapshotRendererProps = {
  snapshot: Snapshot;
};

// 2. Diff view — compare page, PR diff
type DiffRendererProps = {
  baseSnapshot: Snapshot;
  targetSnapshot: Snapshot;
  diffResult: StructuredDiff;
  selectedChangeId?: string;
  onSelectChange: (id: string | null) => void;
};

// 3. Conflict resolution UI — PR merge page
type MergeResolverProps = {
  baseSnapshot: Snapshot;
  oursSnapshot: Snapshot;
  theirsSnapshot: Snapshot;
  diffResult: StructuredDiff;
  onResolve: (entityId: string, field: string | null, side: "base" | "incoming") => void;
};
```

### Ceiling (optional — extended full-page routes)

FHRs may register additional full-page routes mounted under their handler namespace in ForgeHub:

```
/yakup/demo/blob/main/scene.gltf              → SnapshotRenderer (floor)
/yakup/demo/3d-workspace/main/scene.gltf      → GltfWorkspace (ceiling)
```

```ts
type ExtendedRouteProps = {
  token: string;          // caller's auth token for ForgeHub API calls
  repo: { owner: string; name: string };
  currentRef: string;
  filePath: string;
};

type ExtendedRoute = {
  path: string;           // relative to /:owner/:repo/, e.g. "3d-workspace/:ref/*filePath"
  component: React.ComponentType<ExtendedRouteProps>;
  label?: string;         // shown in ForgeHub navigation tabs
};
```

---

## 9. FHRManifest & ForgeHub Loading

The `FHRManifest` is the default export of a renderer bundle (`renderer.js`). ForgeHub imports it to wire up renderers for a file extension.

```ts
interface FHRManifest {
  handlerId: string;      // must match ArtifactHandler.id and manifest.toml handler name
  extensions: string[];
  renderers: {
    snapshot: React.ComponentType<SnapshotRendererProps>;
    diff: React.ComponentType<DiffRendererProps>;
    mergeResolver: React.ComponentType<MergeResolverProps>;
  };
  routes?: ExtendedRoute[];
}

// renderer.js
export default {
  handlerId: "gltf-scene",
  extensions: [".gltf", ".glb"],
  renderers: { snapshot: GltfSnapshotRenderer, diff: GltfDiffRenderer, mergeResolver: GltfMergeResolver },
  routes: [{ path: "3d-workspace/:ref/*filePath", component: GltfWorkspace, label: "3D Workspace" }],
} satisfies FHRManifest;
```

### How ForgeHub loads renderer bundles

- **Official FHRs** (sources the operator has trusted): **Module Federation** — tight React integration, shared dependencies, no double-bundling.
- **Community FHRs** (untrusted sources): **`<iframe>` + postMessage** — sandboxed, the renderer cannot access ForgeHub's DOM or auth tokens.

Where exactly the trust boundary is configured is a **[HUMAN DECISION]** — see §14.

---

## 10. Handler Loading at Runtime

| Context | Format | Notes |
|---------|--------|-------|
| Forge CLI | Native binary | Subprocess protocol; any language |
| ForgeHub API | WASM module | Same protocol; sandboxed; language-agnostic |
| ForgeHub web | ES module | Must be React/TS; runs in browser |

**Forge CLI discovery order:**
1. `~/.forge/plugins/` (downloaded by `forge source`)
2. Anywhere on `$PATH`
3. Built-in: `plain-text` (always, no download needed)

**ForgeHub API WASM caching:**
WASM modules are downloaded on first use and cached at `$FORGE_HANDLER_CACHE` (default `~/.forge/wasm-cache/`) keyed by `{handler-id}/{version}.wasm`.

---

## 11. Versioning Scheme

Backend handler and frontend renderer are **versioned together** per release. One semver covers both the binary/WASM and the renderer bundle.

**Rationale:** Eliminates version skew between the `StructuredDiff` a handler produces and the renderer that consumes it.

- `MAJOR` — breaking change to `StructuredDiff` schema or renderer props contract
- `MINOR` — new capabilities, backwards compatible
- `PATCH` — bug fixes only

Version ranges are not supported in v1 — exact version pinning only.

---

## 12. fhr-official Bootstrap & Migration

The `gltf-scene` handler currently baked into Forge and ForgeHub becomes the first entry in `fhr-official`.

`plain-text` stays compiled into Forge as the built-in catch-all — it will not be an FHR package.

### Migration plan

**Phase 1 — glTF into FHR (current)**
1. Move `gltf.go` from `forgehubproject/forge` into this repo as `packages/handler-gltf-scene/`, wrapped in the subprocess protocol.
2. Add `.gltf`/`.glb` entries to `manifest.toml`.
3. Wire Forge to read `sources.list`, fetch `manifest.toml`, download `forge-handler-gltf-scene`, and invoke it via subprocess.
4. Remove the built-in glTF handler from Forge — `forge diff model.glb` now routes through the FHR binary.
5. Test end-to-end: `forge source add <fhr-url>` → `forge formats add .gltf` → `forge diff model.glb`.

**Phase 2 — ForgeHub loads from FHR**
6. Move the `gltf-scene` TS handler out of ForgeHub API; replace with WASM invocation.
7. ForgeHub web loads `renderer.js` via Module Federation instead of a static import.

**Phase 3 — Community onboarding**
8. Publish types (`@fhr/types`) and `example-handler-native` as the reference for third-party authors.
9. Announce the registry spec.

---

## 13. Publishing a New Handler

### Step 1 — Implement the backend (any language)

The binary must respond to `match`, `diff`, `merge`, `info` over stdin/stdout JSON (§7).

```bash
# Build native binaries
GOOS=linux   GOARCH=amd64 go build -o forge-handler-myformat_linux-amd64   .
GOOS=darwin  GOARCH=arm64 go build -o forge-handler-myformat_darwin-arm64  .
GOOS=windows GOARCH=amd64 go build -o forge-handler-myformat_windows-amd64.exe .

# Build WASM
GOOS=wasip1 GOARCH=wasm  go build -o forge-handler-myformat.wasm .
```

See `packages/example-handler-native/` for a complete Go skeleton.

### Step 2 — Implement the frontend renderer (TypeScript + React)

```ts
import type { FHRManifest } from '@fhr/types';

export default {
  handlerId: 'my-format',
  extensions: ['.myext'],
  renderers: {
    snapshot: MySnapshotRenderer,
    diff: MyDiffRenderer,
    mergeResolver: MyMergeResolver,
  },
} satisfies FHRManifest;
```

Build to a single ESM bundle (`renderer.js`).

### Step 3 — Host assets and write a source manifest

Host binaries and `renderer.js` at stable URLs (GitHub Releases works well). Write `manifest.toml` following §4.

### Step 4 — Open a PR (to be listed in fhr-official)

Review checklist: see `CONTRIBUTING.md`.

To self-host instead:
```bash
forge source add https://your-fhr.example.io/manifest.toml
```

---

## 14. Open Questions

### Resolved

| Question | Resolution |
|----------|------------|
| Domain abstraction | Removed. Direct extension → handler mapping. Predefined extension sets addressed as presets in a future version. |
| `.forge/handlers` domain manifest | Deprecated before implementation. `.forge-formats` is the only per-repo file. |
| `forge domain` commands | Removed. `forge source` and `forge formats` cover all use cases. |
| Backend language | Any language via subprocess protocol / WASM |
| Frontend language | TypeScript + React (runs in browser) |
| Backend + frontend versioned separately or together | Locked together per release |
| Frontend loading — official FHRs | Module Federation |
| Frontend loading — community FHRs | `<iframe>` + postMessage |
| `plain-text` handler | Stays compiled into Forge as built-in catch-all; not an FHR package |
| First handler to migrate | `gltf-scene` (Phase 1) |

### Still open **[HUMAN DECISION]**

1. **Trust boundary for Module Federation vs iframe** — who configures which sources are trusted? Options: per-deployment operator config (recommended for self-hosted), or hosted product trusts only `fhr-official` by default.

2. **`forge source` install behaviour** — when a user runs `forge formats add .gltf`, should Forge automatically download the handler binary, or should that be a separate explicit step (`forge source install`)? Auto-download is more ergonomic; explicit install is more transparent for security.

3. **Semantic merge correctness for complex formats** — for formats like skeletal animation, what is the canonical correct merge when both sides modify the same property? Handler authors will need to define their own conflict resolution strategies.

4. **iframe postMessage protocol** — the typed message contract between ForgeHub and sandboxed community renderers is not yet specified. Needs its own mini-spec before community FHR support ships.
