# FHR — Forge Handler Repository Specification

> Version: 1.0 · Status: Draft

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
13. [Publishing a New FHR](#13-publishing-a-new-fhr)
14. [Open Questions](#14-open-questions)

---

## 1. Overview

FHR (Forge Handler Repository) is the package registry layer of the Forge ecosystem. It plays the same role that npm registries play for JavaScript packages, or Homebrew taps play for macOS tools — but for **format handlers**: the code that teaches Forge and ForgeHub how to semantically diff, merge, and render hardware artifacts.

The three-repo ecosystem:

| Repo | Role | Analogy |
|------|------|---------|
| **Forge** | CLI version control tool | git |
| **ForgeHub** | Web collaboration platform | GitHub |
| **FHR** | Handler + renderer registry | npm registry / Homebrew tap |

An FHR is an HTTP endpoint that publishes a source manifest listing which file extensions it supports and which handler versions are available. Any server that serves this manifest is a valid FHR — including a GitHub repo with a static JSON/TOML file and release assets.

---

## 2. Ecosystem Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│  Repository                                                     │
│  .forge-formats  ←── which extensions this repo cares about    │
└───────────────────────────────┬─────────────────────────────────┘
                                │
               ┌────────────────▼──────────────────┐
               │           Forge CLI                │
               │  forge source · forge formats      │
               └──────┬───────────────┬─────────────┘
                      │               │
          reads       │               │  fetches manifests from
                      ▼               ▼
               sources.list      FHR endpoints
               (~/.forge/         (HTTP servers)
               sources.list)
                      │
          ┌───────────▼───────────────────────┐
          │        Handler Registry            │
          │  resolves extension → handler      │
          │  downloads binary / WASM / JS      │
          └───────────────────────────────────┘

┌────────────────────────────────────────────────────────────────┐
│  ForgeHub (web)                                                │
│  loads FHRManifest (JS/ESM) → mounts SnapshotRenderer,        │
│  DiffRenderer, MergeResolver per file extension                │
└────────────────────────────────────────────────────────────────┘
```

**Data flow for a PR diff view:**

1. ForgeHub API calls the backend handler's `diff(base, head)` → returns `StructuredDiff` JSON
2. ForgeHub web checks the file extension → looks up the registered `DiffRenderer` from the loaded `FHRManifest`
3. `DiffRenderer` receives `{ baseSnapshot, targetSnapshot, diffResult }` and renders it in the browser

---

## 3. FHR Package Structure

An FHR author publishes **two artifacts** per handler release:

### 3a. Backend handler

Used by the Forge CLI and ForgeHub API. Distributed as:
- A **native binary** (`forge-handler-<name>_linux-amd64`, etc.) for the Forge CLI subprocess protocol
- A **WASM module** (`forge-handler-<name>.wasm`) for ForgeHub API server-side execution (language-agnostic, sandboxed)

Both must implement the same stdin/stdout JSON protocol (see §7).

### 3b. Frontend renderer

Used by ForgeHub web. Distributed as:
- An **ES module bundle** (`renderer.js`) exporting an `FHRManifest` object (see §9)

### 3c. Source manifest

A TOML file served at a well-known URL that lists all formats and available versions (see §4).

**Minimal FHR directory layout (hosted as a static site or GitHub repo):**

```
my-fhr/
├── manifest.toml               # source manifest (fetched by forge source update)
├── handlers/
│   └── gltf-scene/
│       └── 1.2.0/
│           ├── forge-handler-gltf-scene_linux-amd64
│           ├── forge-handler-gltf-scene_darwin-arm64
│           ├── forge-handler-gltf-scene_windows-amd64.exe
│           └── forge-handler-gltf-scene.wasm
└── renderers/
    └── gltf-scene/
        └── 1.2.0/
            └── renderer.js
```

---

## 4. Source Manifest Schema

Forge fetches and caches this on `forge source update`. It is a TOML file served at a URL configured in `sources.list`.

```toml
# Required fields
name    = "fhr-official"          # unique identifier for this source
url     = "https://fhr.forge.io"  # base URL; all asset paths are relative to this
version = "1.0"                   # manifest format version

# Optional
description = "Official Forge handler registry"
maintainer  = "ForgeHub Project <registry@forge.io>"

# Formats this source provides handlers for
[formats]
".gltf"  = { handler = "gltf-scene",  version = "1.2.0" }
".glb"   = { handler = "gltf-scene",  version = "1.2.0" }
".obj"   = { handler = "obj-scene",   version = "1.0.1" }
".blend" = { handler = "blender",     version = "0.9.0" }
".txt"   = { handler = "plain-text",  version = "2.0.0" }
".md"    = { handler = "plain-text",  version = "2.0.0" }

# Domain shorthands: named groups of extensions
# 'forge formats add domain:3d' expands to all extensions in this group
[domains]
"3d"    = [".gltf", ".glb", ".obj", ".blend", ".fbx"]
"text"  = [".txt", ".md", ".rst", ".yaml", ".json"]
"image" = [".png", ".jpg", ".tif", ".exr"]

# Per-handler asset download paths (relative to manifest url)
# Forge constructs the download URL as: {url}/{assets.handlers.{name}.{version}.{platform}}
[assets.handlers."gltf-scene"."1.2.0"]
"linux-amd64"    = "handlers/gltf-scene/1.2.0/forge-handler-gltf-scene_linux-amd64"
"darwin-amd64"   = "handlers/gltf-scene/1.2.0/forge-handler-gltf-scene_darwin-amd64"
"darwin-arm64"   = "handlers/gltf-scene/1.2.0/forge-handler-gltf-scene_darwin-arm64"
"windows-amd64"  = "handlers/gltf-scene/1.2.0/forge-handler-gltf-scene_windows-amd64.exe"
"wasm"           = "handlers/gltf-scene/1.2.0/forge-handler-gltf-scene.wasm"

[assets.renderers."gltf-scene"."1.2.0"]
"esm" = "renderers/gltf-scene/1.2.0/renderer.js"
```

**Resolution rules:**
- If two sources both claim the same extension, Forge emits a conflict warning at `forge formats status` time. The user must run `forge formats pin .ext <source>` to resolve it.
- Domains are defined per-source. A domain shorthand from source A only expands to extensions that source A claims.

---

## 5. `.forge-formats` File Format

`.forge-formats` is a per-repository TOML file committed to the repo root. It declares which file extensions Forge should apply semantic handling to.

```toml
# .forge-formats

[include]
# Any source that claims this extension
".gltf"  = {}
".glb"   = {}
# Pinned to a specific source (resolves ambiguity)
".blend" = { source = "fhr-official" }
# Domain shorthand — expands to all extensions in the '3d' domain from any source
"domain:3d" = {}

[ignore]
# Tracked by Git/Forge as opaque blobs — no handler resolution, no prompt
".tif"     = {}
".tmp"     = {}
```

### Classification rules

| File type | Behaviour |
|-----------|----------|
| Text-based (`.txt`, `.md`, `.json`, …) | Handled by the built-in `plain-text` handler by default — no need to list in `.forge-formats` |
| Listed in `[include]` | Handler resolved from sources; semantic diff/merge available; renderer loaded in ForgeHub |
| Listed in `[ignore]` | Tracked as opaque blob; no handler; blob view only in ForgeHub; no prompt to classify |
| **Unregistered** (binary, not in either list) | User is prompted to classify: `add to [include] / [ignore] / remind me later` |
| In `[include]` but no handler found in any source | Warning at `forge formats status`; graceful fallback to blob view |
| In `[include]`, two sources claim it, no `source` pin | Conflict warning; user must run `forge formats pin` |

### Domain shorthand expansion

`"domain:3d" = {}` is syntactic sugar. At runtime Forge expands it to every extension listed under the `3d` domain in any configured source. If a `source` field is provided (`"domain:3d" = { source = "fhr-official" }`), expansion is limited to that source's domain definition.

---

## 6. Forge CLI — Source & Format Commands

### `forge source` — manages `~/.forge/sources.list`

```
forge source add <url>
```
Fetches the source manifest at `<url>`, validates its schema, and appends an entry to `~/.forge/sources.list`. Prints the source `name` and list of advertised formats on success.

```
forge source remove <name>
```
Removes the named source from `sources.list`. Does not uninstall already-downloaded handlers.

```
forge source list
```
Prints all configured sources with their URL, last-updated timestamp, and the list of formats they advertise.

```
forge source update
```
Re-fetches all source manifests and updates the local cache. Analogous to `apt-get update`. Does not download handlers — only updates the index.

**`~/.forge/sources.list` format:**
```toml
[[source]]
name = "fhr-official"
url  = "https://fhr.forge.io/manifest.toml"

[[source]]
name = "community-3d"
url  = "https://handlers.blendercommunity.org/manifest.toml"
```

---

### `forge formats` — manages `.forge-formats` in the current repo

```
forge formats add <extension-or-domain>
```
Adds an extension (`.blend`) or domain shorthand (`domain:3d`) to the `[include]` section of `.forge-formats`. Warns if another source already claims that extension and suggests `forge formats pin`.

```
forge formats ignore <extension>
```
Adds the extension to the `[ignore]` section.

```
forge formats pin <extension> <source>
```
Sets `source = "<source>"` on an existing `[include]` entry, resolving an ambiguity. Errors if the extension is not already in `[include]` or if `<source>` does not claim the extension.

```
forge formats status
```
Prints a table of all extensions in the repo (detected from the working tree + `.forge-formats`) and their resolution status:

```
Extension   Status          Handler             Source
─────────────────────────────────────────────────────────────────
.gltf       resolved        gltf-scene@1.2.0    fhr-official
.blend      resolved        blender@0.9.0       fhr-official  [pinned]
.fbx        warning         —                   — (no handler in any source)
.tif        ignored         —                   —
.llm        unregistered    —                   — (run: forge formats add .llm or forge formats ignore .llm)
```

**Augmented `forge status` output** when warnings exist:
```
Warnings:
  .blend  → 2 sources claim this extension (fhr-official, community-3d)
             run: forge formats pin .blend <source>
  .fbx    → listed in .forge-formats [include] but no handler found
             run: forge source update  or  forge source add <url>
```

---

## 7. Backend Handler Contract

All backend handlers implement `ArtifactHandler`. This interface is used by both the Forge CLI (via subprocess) and the ForgeHub API (via WASM or direct import).

### TypeScript interface (ForgeHub API)

```ts
type IngestInput = {
  filePath: string;
  content: string; // UTF-8 text content
};

type MergeResult = {
  blob: Buffer;
  conflicts?: SemanticConflict[];
};

type SemanticConflict = {
  path: string;  // semantic path, e.g. "nodes[2].translation"
  ours: unknown;
  theirs: unknown;
};

type ArtifactHandler = {
  /** Unique identifier, matches the handler name in the source manifest */
  id: string;

  capabilities: {
    semanticCompare: boolean;
    semanticMerge: boolean;
  };

  /** Return true if this handler should process the given file path */
  matchesPath(path: string): boolean;

  /** Ingest a text-based artifact into the canonical IR stored in ForgeHub */
  ingestFromUtf8Text(input: IngestInput): Promise<string>;

  /** Produce a structured semantic diff between two blobs */
  diff(base: Buffer, head: Buffer): Promise<StructuredDiff>;

  /** Attempt a 3-way merge. Optional — omit if semanticMerge is false */
  merge?(base: Buffer, ours: Buffer, theirs: Buffer): Promise<MergeResult>;
};
```

### `StructuredDiff` wire format

```ts
type DiffChange = {
  path: string;          // semantic path, e.g. "nodes[2].translation"
  kind: "added" | "removed" | "modified";
  label?: string;        // human-readable label, e.g. "Armature bone: spine_01"
  before?: unknown;      // previous value (omitted for 'added')
  after?: unknown;       // new value (omitted for 'removed')
  children?: DiffChange[]; // nested changes for hierarchical formats
};

type StructuredDiff = {
  version: "1.0";
  format: string;        // handler id, e.g. "gltf-scene"
  changes: DiffChange[];
};
```

### Forge CLI subprocess protocol

Forge CLI discovers external handlers as executables named `forge-handler-<name>` in `~/.forge/plugins/` or `$PATH`. It communicates via stdin/stdout JSON:

**`forge-handler-<name> match <filepath>`**
```
stdout: "true" or "false"
exit 0 always
```

**`forge-handler-<name> diff`**
```
stdin:  { "base": "<base64>", "head": "<base64>" }
stdout: StructuredDiff JSON
exit 0 on success, 1 on error
```

**`forge-handler-<name> merge`**
```
stdin:  { "base": "<base64>", "ours": "<base64>", "theirs": "<base64>" }
stdout: { "blob": "<base64>", "conflicts": [ SemanticConflict, … ] }
exit 0 on success, 1 on error ("not supported" if merge not implemented)
```

**`forge-handler-<name> info`** (optional)
```
stdout: { "id": "gltf-scene", "version": "1.2.0", "formats": [".glb", ".gltf"], "protocol": "1.0" }
```

Binary blobs are base64-encoded to keep the transport pure JSON.

---

## 8. Frontend Renderer Contract

ForgeHub expects every FHR to provide three React components. These are the **floor** — the minimum required to be a valid FHR renderer.

### Supporting types

```ts
type Snapshot = {
  id: string;
  commitSha: string;
  filePath: string;
  /** Raw file content as a base64 string */
  content: string;
  /** Parsed intermediate representation (format-specific) */
  entities: unknown;
};

type DiffResult = StructuredDiff; // same wire format as the backend
```

### Floor (required)

```ts
/** 1. Snapshot renderer — viewing a single file at a commit */
type SnapshotRendererProps = {
  snapshot: Snapshot;
};
type SnapshotRenderer = React.ComponentType<SnapshotRendererProps>;

/** 2. Diff renderer — compare view / PR diff */
type DiffRendererProps = {
  baseSnapshot: Snapshot;
  targetSnapshot: Snapshot;
  diffResult: DiffResult;
  selectedChangeId?: string;
  onSelectChange: (id: string | null) => void;
};
type DiffRenderer = React.ComponentType<DiffRendererProps>;

/** 3. Merge resolver — PR conflict resolution UI */
type MergeResolverProps = {
  baseSnapshot: Snapshot;
  oursSnapshot: Snapshot;
  theirsSnapshot: Snapshot;
  diffResult: DiffResult;
  onResolve: (entityId: string, field: string | null, side: "base" | "incoming") => void;
};
type MergeResolver = React.ComponentType<MergeResolverProps>;
```

### Ceiling (optional — extended pages)

FHRs may register their own full-page routes. ForgeHub mounts these under the handler namespace and wraps them in its chrome (header, auth, navigation).

```
/yakup/demo/blob/main/scene.blend                  → SnapshotRenderer (floor)
/yakup/demo/blend-workspace/main/scene.blend       → FHR extended route (ceiling)
```

```ts
type ExtendedRouteProps = {
  token: string;       // caller's auth token, for API calls back to ForgeHub
  repo: {
    owner: string;
    name: string;
  };
  currentRef: string;  // branch or commit SHA
  filePath: string;
};

type ExtendedRoute = {
  /** Path pattern relative to /owner/repo/, e.g. "blend-workspace/:ref/*filePath" */
  path: string;
  component: React.ComponentType<ExtendedRouteProps>;
  /** Label shown in ForgeHub navigation tabs */
  label?: string;
};
```

---

## 9. FHRManifest & ForgeHub Loading

The `FHRManifest` is the single export of a renderer bundle (`renderer.js`). ForgeHub imports it at runtime to wire up the renderers for a given handler.

```ts
type FHRManifest = {
  /** Must match the handler id in the source manifest */
  handlerId: string;

  /** File extensions this renderer handles */
  extensions: string[];

  renderers: {
    snapshot: SnapshotRenderer;
    diff: DiffRenderer;
    mergeResolver: MergeResolver;
  };

  /** Optional extended routes (ceiling) */
  routes?: ExtendedRoute[];
};

// renderer.js default export
export default {
  handlerId: "gltf-scene",
  extensions: [".gltf", ".glb"],
  renderers: { snapshot: GltfSnapshotRenderer, diff: GltfDiffRenderer, mergeResolver: GltfMergeResolver },
  routes: [
    { path: "3d-workspace/:ref/*filePath", component: GltfWorkspace, label: "3D Workspace" }
  ],
} satisfies FHRManifest;
```

### How ForgeHub loads FHR renderer bundles

**Official FHRs (trusted — from `fhr-official` or sources the operator has explicitly trusted):**

Use **Module Federation** (Vite). The renderer bundle is loaded as a remote Vite/webpack module federation entry. This gives full React context sharing, shared dependencies (avoids double-bundling React), and tight UI integration.

**Community FHRs (untrusted — from user-added sources):**

Use **`<iframe>` + `postMessage`**. ForgeHub renders an iframe pointing to the FHR-hosted renderer URL. The iframe and ForgeHub communicate via a typed postMessage protocol. This provides OS-level isolation — a malicious community renderer cannot access ForgeHub's DOM, auth tokens, or React tree.

> **Human decision required:** Where exactly is the trust boundary drawn? Options:
> - Operator-level: the ForgeHub deployment operator configures a trusted-sources list; all other sources use iframe.
> - User-level: each ForgeHub user can mark sources as trusted in their settings.
> - Recommendation: **operator-level** for self-hosted ForgeHub; for the hosted product (forge.io), only `fhr-official` is Module Federation; all community sources use iframe.

---

## 10. Handler Loading at Runtime

### Split model

| Context | Format | Rationale |
|---------|--------|-----------|
| Forge CLI | Native binary | Full OS access, best performance, already the subprocess model |
| ForgeHub API | WASM | Language-agnostic, sandboxed, no native binary deployment per platform |
| ForgeHub web | ES module (JS) | Native to browser, tree-shakeable, no WASM overhead for pure rendering |

### Forge CLI handler discovery order

1. Built-in handlers compiled into the `forge` binary (`plain-text`, `gltf-scene` — always available as baseline)
2. `~/.forge/plugins/` (installed via `forge handler install`)
3. Anywhere on `$PATH`

### WASM loading in ForgeHub API

ForgeHub API downloads and caches the `.wasm` asset from the source manifest's asset URLs on first use. The WASM module is instantiated per-request with a WASI runtime. Input/output use the same stdin/stdout JSON protocol as the CLI subprocess — the same handler binary works in both contexts.

Cached WASM modules are stored at `$FORGE_HANDLER_CACHE` (default: `~/.forge/wasm-cache/`) keyed by `{handler-id}/{version}.wasm`.

---

## 11. Versioning Scheme

Backend handler and frontend renderer are **versioned together** per FHR release. A single version number covers both the binary/WASM and the renderer bundle.

**Rationale:** Keeping versions locked together avoids the class of bugs where a renderer tries to interpret a `StructuredDiff` produced by a different handler version. Handler authors cut one release; consumers pin one version.

**Version format:** Semantic Versioning (`MAJOR.MINOR.PATCH`).

- `MAJOR` bump: breaking change to the `StructuredDiff` schema this handler produces, or breaking change to the renderer props contract.
- `MINOR` bump: new capabilities added (new diff paths, new renderer features) — backwards compatible.
- `PATCH` bump: bug fixes only.

Forge and ForgeHub pin to a specific version in `.forge-formats` (via the source manifest). Version ranges are not supported in v1 — exact pinning only.

---

## 12. fhr-official Bootstrap & Migration

The `gltf-scene` and `plain-text` handlers currently baked into ForgeHub become the first entries in the `fhr-official` registry at version `1.0.0`.

### Migration plan

**Phase 1 — Extract without breaking (current sprint)**
1. Move the `gltf-scene` and `plain-text` handler code from `forgehubproject/forgehub` into this repo (`fhr`).
2. Publish them as `fhr-official` v1.0.0 with the source manifest above.
3. ForgeHub ships with `fhr-official` pre-configured in its default `sources.list` — no user action required.
4. ForgeHub's handler registry falls back to the bundled handlers if the FHR source is unreachable, preserving current behaviour.

**Phase 2 — Load from FHR**
5. ForgeHub loads the renderer bundle dynamically via Module Federation instead of importing it at build time.
6. The bundled fallback copies are removed from ForgeHub's build.

**Phase 3 — Community onboarding**
7. Publish the `FHRManifest` TypeScript types and the `plain-text` handler as a reference implementation / starter template.
8. Announce the community registry spec so third-party FHR authors can publish.

**No breaking change for existing repos:** repos that don't have a `.forge-formats` file continue to work — text files get `plain-text`, everything else gets blob view, same as today.

---

## 13. Publishing a New FHR

Step-by-step guide for a handler author:

### Step 1 — Implement the backend handler

Implement `ArtifactHandler` in any language that can read/write the subprocess JSON protocol. The `plain-text` handler in this repo is the canonical reference implementation.

```
forge-handler-myformat match <filepath>   # prints true/false
forge-handler-myformat diff               # stdin: {base, head} → StructuredDiff
forge-handler-myformat merge              # stdin: {base, ours, theirs} → {blob, conflicts}
forge-handler-myformat info               # {id, version, formats, protocol}
```

Compile to:
- Native binaries for each platform (`linux-amd64`, `darwin-arm64`, `windows-amd64`)
- WASM module (`forge-handler-myformat.wasm`)

### Step 2 — Implement the frontend renderer

Create a React component bundle exporting an `FHRManifest`. Install the types:

```bash
npm install @forgehub/fhr-types
```

```ts
import type { FHRManifest } from '@forgehub/fhr-types';

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

Build to a single ESM bundle: `renderer.js`.

### Step 3 — Publish assets

Host all binaries and the renderer bundle at stable URLs (GitHub Releases works well).

### Step 4 — Write a source manifest

Create `manifest.toml` following §4. Host it at a stable URL.

### Step 5 — Register (optional)

To be listed in `fhr-official`, open a PR to this repo adding your handler entry to the official manifest. The ForgeHub team reviews for quality and security before merging.

To self-host, users run:
```bash
forge source add https://your-fhr-host.io/manifest.toml
```

---

## 14. Open Questions

The following questions have been resolved with recommendations in this spec. Items marked **[HUMAN DECISION]** still require explicit sign-off before implementation.

### Resolved

| Question | Resolution |
|----------|-----------|
| Artifact format for backend handlers | Split: native binary for CLI, WASM for ForgeHub API |
| Artifact format for frontend renderers | ES modules (native to browser) |
| Frontend loading mechanism for official FHRs | Module Federation |
| Frontend loading mechanism for community FHRs | `<iframe>` + postMessage |
| Backend + frontend versioned together or independently | Locked together per release |
| `fhr-official` bootstrap | Extract existing handlers; ForgeHub ships default `sources.list` entry; phased removal of bundled copies |

### Still open **[HUMAN DECISION]**

1. **Trust boundary for Module Federation vs iframe** — where exactly does the ForgeHub operator configure which sources are trusted for Module Federation vs sandboxed in an iframe? Options: per-deployment config, per-user setting, or a curated allowlist maintained by the ForgeHub team. Recommendation: operator-level config for self-hosted; hosted product trusts only `fhr-official` by default.

2. **Semantic merge correctness for complex formats** — for formats like skeletal animation or shader graphs, what is the canonical "correct" merge when both sides modify the same node? This may require handler authors to define conflict resolution strategies rather than a single universal answer.

3. **Handler sandboxing for CLI** — community handlers run native code on the user's machine. The subprocess/stdio protocol isolates them from Forge's process, but OS-level sandboxing (seccomp, WASI) is a longer-term concern for untrusted community sources. Defer to M4 or later.

4. **iframe postMessage protocol** — the typed message contract between ForgeHub and sandboxed community renderers is not yet specified. Needs its own mini-spec before community FHR support ships.
