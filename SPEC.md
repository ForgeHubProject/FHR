# FHR — Forge Handler Repository: Ecosystem Specification

**Version:** 1.0-draft  
**Status:** Authoritative design reference  
**Last updated:** 2026-06-13

---

## Table of Contents

1. [Overview and Purpose](#1-overview-and-purpose)
2. [Ecosystem Architecture](#2-ecosystem-architecture)
3. [FHR Package Structure](#3-fhr-package-structure)
4. [Source Manifest Schema](#4-source-manifest-schema)
5. [`.forge-formats` File Format](#5-forge-formats-file-format)
6. [Forge CLI Commands](#6-forge-cli-commands)
7. [Backend Handler Contract](#7-backend-handler-contract)
8. [Frontend Renderer Contract](#8-frontend-renderer-contract)
9. [FHRManifest Type and ForgeHub Loading](#9-fhrmanifest-type-and-forgehub-loading)
10. [Handler Loading at Runtime](#10-handler-loading-at-runtime)
11. [ForgeHub Loading Mechanism](#11-forgehub-loading-mechanism)
12. [Versioning Scheme](#12-versioning-scheme)
13. [`fhr-official` Bootstrap and Migration Plan](#13-fhr-official-bootstrap-and-migration-plan)
14. [Publishing a New FHR (Author Guide)](#14-publishing-a-new-fhr-author-guide)
15. [Open Questions Requiring Human Decision](#15-open-questions-requiring-human-decision)

---

## 1. Overview and Purpose

**FHR (Forge Handler Repository)** is the official registry for format handlers and renderers in the Forge ecosystem. It plays the same role for Forge that npm plays for Node.js — a trusted, versioned registry where format authors publish packages and where Forge/ForgeHub resolves which code to use for each file format.

### Why FHR exists

Forge is "git but for everything" — it tracks binary and structured files (3D models, PCBs, CAD designs, ML model weights, etc.) that plain-text diff tools cannot meaningfully compare. But Forge itself is format-agnostic. It needs a plugin mechanism to delegate format-specific diff, merge, and rendering to specialized code.

Historically, ForgeHub shipped with a small set of baked-in handlers (`gltf-scene`, `plain-text`). This does not scale. FHR externalizes the handler/renderer registry so:

- Third parties can publish handlers for new formats without modifying ForgeHub.
- Teams can run private FHR instances for proprietary formats.
- Forge CLI and ForgeHub stay thin shells that load behavior dynamically.
- The ecosystem can evolve independently of the core platform.

### What FHR provides, per format

Every entry in an FHR registry provides two things:

| Layer | Who uses it | What it does |
|---|---|---|
| **Handler** (backend) | Forge CLI, ForgeHub server | Ingests files, produces `StructuredDiff`, optionally merges |
| **Renderer** (frontend) | ForgeHub browser UI | Renders snapshots, diffs, merge conflict resolution |

These two layers are versioned and released together as a single FHR package.

---

## 2. Ecosystem Architecture

### Component map

```
┌─────────────────────────────────────────────────────────────────┐
│  Developer machine                                              │
│                                                                 │
│  ┌──────────────┐    reads     ┌──────────────────────────┐    │
│  │  Forge CLI   │◄────────────►│  sources.list            │    │
│  │  (Go)        │              │  (global, per-user)      │    │
│  └──────┬───────┘              └──────────────────────────┘    │
│         │ reads                                                  │
│         ▼                                                        │
│  ┌──────────────┐                                               │
│  │ .forge-formats│  (per-repo, committed)                       │
│  └──────┬───────┘                                               │
│         │ resolves handler via sources.list                     │
│         ▼                                                        │
│  ┌──────────────┐    fetches   ┌──────────────────────────┐    │
│  │  FHR source  │◄────────────►│  Source manifest         │    │
│  │  registry    │              │  (TOML, at well-known URL)│    │
│  └──────┬───────┘              └──────────────────────────┘    │
│         │ downloads                                             │
│         ▼                                                        │
│  ┌──────────────┐                                               │
│  │Handler binary│  (WASM or native, cached locally)            │
│  └──────────────┘                                               │
└─────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────┐
│  ForgeHub (browser + server)                                    │
│                                                                 │
│  ┌──────────────┐    reads     ┌──────────────────────────┐    │
│  │  ForgeHub    │◄────────────►│  .forge-formats           │    │
│  │  server      │              │  (from repo metadata)    │    │
│  └──────┬───────┘              └──────────────────────────┘    │
│         │ resolves, fetches FHRManifest                         │
│         ▼                                                        │
│  ┌──────────────┐    loads     ┌──────────────────────────┐    │
│  │  ForgeHub    │◄────────────►│  FHR renderer bundle     │    │
│  │  React shell │              │  (JS/ES module from FHR) │    │
│  └──────┬───────┘              └──────────────────────────┘    │
│         │ mounts                                                 │
│         ▼                                                        │
│  ┌──────────────────────────────────────────────────┐          │
│  │  SnapshotRenderer │ DiffRenderer │ MergeResolver │          │
│  └──────────────────────────────────────────────────┘          │
└─────────────────────────────────────────────────────────────────┘
```

### Data flow summary

1. A repo's `.forge-formats` declares which extensions the team cares about and which source to use (or leaves it open).
2. `forge source update` fetches and caches the manifest from each source in `sources.list`.
3. When Forge CLI performs a diff, it resolves the extension → handler from cached manifests, downloads the handler binary (WASM or native), and runs it.
4. When ForgeHub displays a file, it reads `.forge-formats`, resolves the renderer bundle URL from the source manifest, and dynamically loads the JS module.
5. ForgeHub mounts the FHR-provided React components inside its chrome.

### Sources of truth

| File | Location | Controls |
|---|---|---|
| `sources.list` | Global user config (`~/.forge/sources.list`) | Which FHR registries Forge trusts |
| `.forge-formats` | Repo root, committed | Which formats this repo uses and how to resolve them |
| Source manifest | Hosted by each FHR at a well-known URL | Which handlers/renderers each format resolves to |

---

## 3. FHR Package Structure

An FHR is a registry (server + manifest). Each format in the registry is backed by a **handler package** — a versioned bundle that an FHR author publishes.

### Handler package contents

```
my-handler-1.2.0/
├── manifest.json          # machine-readable metadata (see below)
├── handler.wasm           # backend diff/merge logic (WASM)
├── handler-native-linux-amd64   # optional: native binary for Forge CLI
├── handler-native-darwin-arm64  # optional: native binary for Forge CLI
├── renderer/
│   ├── remoteEntry.js     # Module Federation entry point (official FHRs)
│   └── ...                # renderer bundle assets
└── README.md              # human-readable documentation
```

### `manifest.json` schema

```json
{
  "id": "gltf-scene",
  "version": "1.2.0",
  "extensions": [".gltf", ".glb"],
  "capabilities": {
    "semanticCompare": true,
    "semanticMerge": false
  },
  "backend": {
    "wasm": "handler.wasm",
    "native": {
      "linux-amd64": "handler-native-linux-amd64",
      "darwin-arm64": "handler-native-darwin-arm64",
      "windows-amd64": "handler-native-windows-amd64.exe"
    }
  },
  "frontend": {
    "remoteEntry": "renderer/remoteEntry.js",
    "exposedModule": "./FHRManifest"
  },
  "minForgeVersion": "0.5.0",
  "minForgeHubVersion": "0.3.0"
}
```

### Field definitions

| Field | Type | Required | Description |
|---|---|---|---|
| `id` | string | yes | Stable handler identifier. Must be globally unique within a registry. Kebab-case. |
| `version` | string | yes | Semver. Backend and frontend are versioned together. |
| `extensions` | string[] | yes | File extensions this handler claims (e.g. `[".gltf", ".glb"]`). |
| `capabilities.semanticCompare` | bool | yes | Whether handler can produce a `StructuredDiff`. |
| `capabilities.semanticMerge` | bool | yes | Whether handler implements `merge()`. |
| `backend.wasm` | string | yes | Relative path to WASM binary within package. |
| `backend.native` | object | no | Platform-specific native binaries (for Forge CLI performance). |
| `frontend.remoteEntry` | string | yes | Path to Module Federation remote entry JS file. |
| `frontend.exposedModule` | string | yes | The exposed module name (must export `FHRManifest`). |
| `minForgeVersion` | string | no | Minimum Forge CLI version required. |
| `minForgeHubVersion` | string | no | Minimum ForgeHub version required. |

---

## 4. Source Manifest Schema

Each FHR registry must publish a **source manifest** at a well-known path: `<registry-url>/manifest.toml`.

Forge fetches and caches this file on `forge source update`. The manifest advertises which formats and domains the registry provides, and at what versions.

### Full TOML spec

```toml
# ── Identity ──────────────────────────────────────────────────────────────────
name    = "fhr-official"
url     = "https://fhr.example.io"
version = "1.0"           # manifest schema version, not registry version

# ── Format entries ────────────────────────────────────────────────────────────
# Each key is a file extension. The value specifies which handler package
# in this registry handles it, and at what version.
[formats]
".gltf"  = { handler = "gltf-scene",  version = "1.2.0" }
".glb"   = { handler = "gltf-scene",  version = "1.2.0" }  # same handler, two extensions
".obj"   = { handler = "obj-scene",   version = "1.0.1" }
".blend" = { handler = "blender",     version = "0.9.0" }
".fbx"   = { handler = "fbx-scene",   version = "0.4.2" }
".txt"   = { handler = "plain-text",  version = "2.0.0" }
".md"    = { handler = "plain-text",  version = "2.0.0" }

# ── Domain definitions ────────────────────────────────────────────────────────
# Domains are named shorthand groups defined by the registry.
# `domain:3d` in a .forge-formats file expands to all extensions listed here.
# Domains are defined by registries, not by Forge itself.
[domains]
"3d"   = [".gltf", ".glb", ".obj", ".blend", ".fbx", ".stl", ".step"]
"text" = [".txt", ".md", ".rst", ".csv"]
```

### Field definitions

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes | Short identifier for this registry. Used in `.forge-formats` `source =` pins and `forge source` commands. Must be unique in `sources.list`. |
| `url` | string | yes | Base URL of the registry. Forge appends `/manifest.toml` to fetch the manifest and `/packages/<handler>/<version>/` to fetch packages. |
| `version` | string | yes | Schema version of the manifest format itself. Current: `"1.0"`. |
| `[formats]` | table | yes | Map of file extension → handler reference. |
| `[formats].<ext>.handler` | string | yes | Handler package ID within this registry. |
| `[formats].<ext>.version` | string | yes | Exact semver version of the handler package to use. |
| `[domains]` | table | no | Named groups of extensions. Each domain name maps to a list of extensions. Domains may reference extensions not in `[formats]` (e.g., extensions handled by a different registry). |

### URL conventions

Given a source manifest at `https://fhr.example.io/manifest.toml`, Forge expects:

```
https://fhr.example.io/manifest.toml                        # source manifest
https://fhr.example.io/packages/gltf-scene/1.2.0/manifest.json   # package manifest
https://fhr.example.io/packages/gltf-scene/1.2.0/handler.wasm    # WASM binary
https://fhr.example.io/packages/gltf-scene/1.2.0/renderer/remoteEntry.js  # renderer bundle
```

### Caching

Forge caches source manifests locally at `~/.forge/sources/<name>/manifest.toml`. Handler binaries are cached at `~/.forge/cache/<name>/<handler>/<version>/`. Caches are invalidated only on `forge source update`.

---

## 5. `.forge-formats` File Format

`.forge-formats` is a TOML file committed at the root of every Forge repository. It declares which file formats the team wants active handler resolution for.

### Full TOML spec

```toml
# ── Include section ───────────────────────────────────────────────────────────
# Extensions listed here will have handler + renderer resolved.
# Forge preloads handlers for these extensions on repo open.
# Semantic diff and merge are available.
[include]

# Any source — Forge picks the first match across all sources in sources.list
".gltf" = {}

# Pinned to a specific source — use this when two registries both claim the extension
".blend" = { source = "fhr-official" }

# Domain shorthand — expands to all extensions in the "3d" domain from any source
"domain:3d" = {}

# Domain shorthand pinned to a specific source
"domain:cad" = { source = "company-internal" }

# ── Ignore section ────────────────────────────────────────────────────────────
# Extensions listed here are tracked by Forge as opaque blobs.
# No handler resolution, no prompt, no semantic diff.
[ignore]
".tif"     = {}
".garbage" = {}
".tmp"     = {}
```

### Extension resolution rules

| Situation | Behavior |
|---|---|---|
| Extension in `[include]`, one handler found | Handler resolved, semantic diff/merge available |
| Extension in `[include]`, no handler in any source | Warning at `forge status` time; file tracked as blob, diff shows binary change |
| Extension in `[include]`, two+ handlers from different sources | Conflict warning at `forge status`; user must run `forge formats pin <ext> <source>` to resolve |
| Extension in `[include]`, pinned to source | Only that source's handler is used; no conflict possible |
| Extension in `[ignore]` | Tracked as opaque blob, no handler, no prompt |
| Extension in neither list | User is prompted on first encounter: "Add to include, ignore, or skip?" |
| Text-based extension (`.txt`, `.md`, etc.) | Default handler (`plain-text`) is available without explicit inclusion |

### Domain expansion

When Forge encounters `"domain:3d" = {}` in `[include]`:

1. It looks up the domain `"3d"` in every source in `sources.list`.
2. It collects the union of all extensions from all `"3d"` domain definitions.
3. Each collected extension is treated as if individually listed in `[include]`.
4. If `source = "fhr-official"` is specified, only that source's domain definition is used.

### Conflict resolution

A conflict arises when two sources in `sources.list` both claim the same extension in their `[formats]` tables. Conflicts do not prevent Forge from working — they produce warnings and fall back to the first-listed source. The user resolves conflicts by pinning:

```toml
# Before pinning — warning at forge status:
".blend" = {}

# After pinning — no warning:
".blend" = { source = "fhr-official" }
```

---

## 6. Forge CLI Commands

### 6.1 `forge source` — source registry management

Manipulates `~/.forge/sources.list`.

#### `forge source add <url>`

Fetches the manifest at `<url>/manifest.toml`, validates it, and appends the source to `sources.list`.

```
$ forge source add https://fhr.example.io
Fetching manifest from https://fhr.example.io/manifest.toml ...
Found registry: fhr-official (v1.0)
Formats: .gltf, .glb, .obj, .blend, .fbx, .txt, .md
Domains: 3d, text
Added fhr-official to sources.list
```

**Errors:**
- URL unreachable: print error, do not modify `sources.list`.
- Manifest invalid: print validation errors, do not modify `sources.list`.
- Name conflict (a source with that `name` already exists): print error, suggest `forge source remove <name>` first.

#### `forge source remove <name>`

Removes the named source from `sources.list`. Does not delete cached manifests or handler binaries.

```
$ forge source remove community-3d
Removed community-3d from sources.list
Note: cached packages remain at ~/.forge/cache/community-3d/
```

**Errors:**
- Source not found: print error, list available sources.
- Source is the pinned source for one or more extensions in `.forge-formats`: print warning listing affected extensions, require `--force` to proceed.

#### `forge source list`

Lists all sources with status and advertised formats.

```
$ forge source list
SOURCE          URL                          FORMATS                  LAST UPDATED
fhr-official    https://fhr.example.io       .gltf .glb .obj .blend   2026-06-13 09:00
community-3d    https://community.fhr.dev    .blend .fbx .zpr         2026-06-12 14:22

Conflicts detected:
  .blend -> claimed by fhr-official AND community-3d
  Run: forge formats pin .blend <source>
```

**Flags:**
- `--json`: output as JSON array.
- `--verbose`: also show domain definitions per source.

#### `forge source update`

Re-fetches all source manifests. Equivalent to `apt-get update`.

```
$ forge source update
Updating fhr-official ...       ok (5 new formats, 0 removed)
Updating community-3d ...       ok (no changes)
Update complete. Run `forge source list` to review.
```

**Flags:**
- `--source <name>`: update only one source.
- `--prune`: also purge cached handler binaries for versions no longer referenced by any manifest.

---

### 6.2 `forge formats` — per-repo format management

Manipulates `.forge-formats` in the current repo.

#### `forge formats add <extension>`

Adds the extension to the `[include]` section of `.forge-formats`.

```
$ forge formats add .blend
Added .blend to [include] in .forge-formats
Handler resolved: fhr-official / blender v0.9.0
```

**Warnings:**
- Two sources claim the extension: print conflict warning and suggest `forge formats pin`.
- No source has a handler for the extension: print warning, still add to include.

#### `forge formats ignore <extension>`

Adds the extension to the `[ignore]` section.

```
$ forge formats ignore .tif
Added .tif to [ignore] in .forge-formats
```

**Errors:**
- Extension already in `[include]`: print error, require explicit removal first.

#### `forge formats pin <extension> <source>`

Sets `source = "<source>"` on an existing `[include]` entry, resolving ambiguity when two sources claim the same extension.

```
$ forge formats pin .blend fhr-official
Pinned .blend to fhr-official in .forge-formats
Conflict resolved. Handler: fhr-official / blender v0.9.0
```

**Errors:**
- Extension not in `[include]`: print error, suggest `forge formats add` first.
- Named source not in `sources.list`: print error.
- Named source does not have a handler for the extension: print error.

#### `forge formats status`

Shows all extensions tracked in the repo and their handler resolution status.

```
$ forge formats status
EXTENSION    STATUS       HANDLER                    SOURCE
.gltf        resolved     gltf-scene v1.2.0          fhr-official
.blend       CONFLICT     blender v0.9.0 / ?         fhr-official, community-3d
.fbx         resolved     fbx-scene v0.4.2           fhr-official
.llm         MISSING      -                          (no handler in any source)
.tif         ignored      -                          -

Warnings:
  .blend  -> 2 handlers found (fhr-official, community-3d) - ambiguous
             run: forge formats pin .blend <source>
  .llm    -> listed in .forge-formats but no handler found in any source
             Semantic diff unavailable; file will be diffed as blob
```

**Flags:**
- `--json`: output as JSON.
- `--all`: also show extensions Forge has encountered in the repo that are not in `.forge-formats`.

---

### Augmented `forge status` output

When `.forge-formats` conflicts or missing handlers exist, `forge status` appends a warnings section:

```
On branch main
Changes not staged for commit:
  modified:   scene.blend
  modified:   texture.tif

Warnings:
  .blend  -> 2 handlers found (fhr-official, community-3d) - ambiguous
             run: forge formats pin .blend <source>
  .llm    -> listed in .forge-formats but no handler found in any source
```

---

## 7. Backend Handler Contract

The backend handler is the code that Forge CLI (and the ForgeHub server) runs to produce structured diffs and perform merges. It runs outside the browser.

### Wire types

```ts
// A single semantic change within a diff
type DiffChange = {
  path: string;          // dot-notation path to the changed element, e.g. "meshes.0.vertices"
  kind: "added" | "removed" | "modified";
  label?: string;        // human-readable label for this change, e.g. "Vertex count"
  before?: unknown;      // value before change (for "modified" and "removed")
  after?: unknown;       // value after change (for "modified" and "added")
  children?: DiffChange[]; // nested changes for hierarchical formats
};

// The top-level diff output produced by a handler
type StructuredDiff = {
  version: "1.0";        // wire format version; always "1.0" for now
  format: string;        // handler ID that produced this diff, e.g. "gltf-scene"
  changes: DiffChange[];
};

// Input to ingestFromUtf8Text
type IngestInput = {
  text: string;          // raw UTF-8 file content
  path: string;          // file path (for extension-based hints)
};

// Output of a merge operation
type MergeResult =
  | { ok: true; merged: Buffer }  // clean merge
  | { ok: false; conflicts: MergeConflict[] };  // merge has unresolved conflicts

type MergeConflict = {
  path: string;          // path to the conflicting field
  base: unknown;
  ours: unknown;
  theirs: unknown;
};
```

### `ArtifactHandler` interface

```ts
type ArtifactHandler = {
  // Stable identifier matching the FHR package ID (e.g. "gltf-scene")
  id: string;

  // What this handler can do
  capabilities: {
    semanticCompare: boolean; // can produce StructuredDiff
    semanticMerge: boolean;   // implements merge()
  };

  // Returns true if this handler should be used for the given file path.
  // Handlers typically check the file extension.
  // Called by Forge to confirm the manifest-level routing before invoking diff/merge.
  matchesPath(path: string): boolean;

  // Converts a UTF-8 text representation into the handler's canonical internal format.
  // Used when importing text-based serializations of binary formats.
  ingestFromUtf8Text(input: IngestInput): Promise<string>;

  // Produces a StructuredDiff between two file versions.
  // `base` is the older version, `head` is the newer version.
  // Must be implemented if capabilities.semanticCompare is true.
  diff(base: Buffer, head: Buffer): Promise<StructuredDiff>;

  // Performs a three-way merge.
  // Optional — only implement if capabilities.semanticMerge is true.
  // `base` is the common ancestor, `ours` is the current branch, `theirs` is the incoming branch.
  merge?(base: Buffer, ours: Buffer, theirs: Buffer): Promise<MergeResult>;
};
```

### Method contracts

#### `matchesPath(path)`

- Must be pure and synchronous (though typed as sync here; do not make it async).
- Should return `true` for all extensions listed in the source manifest's `[formats]` table for this handler.
- May also return `true` for sub-paths (e.g., files inside a `.blend` package directory).
- Forge calls this as a secondary confirmation after manifest-level routing; it should not throw.

#### `ingestFromUtf8Text(input)`

- Used for text-based formats that have a UTF-8 wire form (e.g., GLTF JSON).
- Returns the canonical internal representation as a string (often re-serialized JSON/binary).
- If the format is purely binary and has no text form, this method may throw with a clear error message.

#### `diff(base, head)`

- Both `base` and `head` are raw file bytes as `Buffer`.
- Returns a `StructuredDiff` with `version: "1.0"` and `format` set to the handler's `id`.
- Must not mutate its inputs.
- If the file is identical, return `{ version: "1.0", format: id, changes: [] }`.
- On error, throw with a descriptive message; Forge will fall back to binary diff.

#### `merge(base, ours, theirs)`

- Returns `MergeResult`. If merge is clean, `ok: true` and `merged` contains the result bytes.
- If there are conflicts, `ok: false` and `conflicts` lists each conflicting field.
- The `conflicts` array is forwarded to the frontend `MergeResolver` component.
- Handlers that do not implement semantic merge omit this method entirely; Forge falls back to Git's text merge.

---

## 8. Frontend Renderer Contract

The frontend renderer is the React code ForgeHub loads in the browser to display and interact with format-specific content.

### Supporting types

```ts
// A single file at a specific commit
type Snapshot = {
  content: ArrayBuffer;  // raw file bytes
  path: string;          // file path within the repo
  ref: string;           // commit SHA or branch name
  mimeType?: string;     // optional MIME hint
};

// The structured diff produced by the backend handler
type DiffResult = StructuredDiff; // same as backend wire type
```

### The Floor (required)

Every FHR renderer **must** implement all three of these component types. ForgeHub will not load a renderer bundle that does not export all three.

#### 1. `SnapshotRenderer` — viewing a single file at a commit

```ts
type SnapshotRendererProps = {
  snapshot: Snapshot;
};

type SnapshotRenderer = React.ComponentType<SnapshotRendererProps>;
```

Used at: `/yakup/demo/blob/main/scene.gltf`

Responsibilities:
- Display the file content in a meaningful, format-specific way.
- Should be read-only.
- Should handle loading states internally (e.g., while parsing `snapshot.content`).

#### 2. `DiffRenderer` — compare view and PR diff

```ts
type DiffRendererProps = {
  baseSnapshot: Snapshot;      // older version
  targetSnapshot: Snapshot;    // newer version
  diffResult: DiffResult;      // StructuredDiff from backend handler
  selectedChangeId?: string;   // currently highlighted change (from sidebar)
  onSelectChange: (id: string | null) => void; // user clicked a change
};

type DiffRenderer = React.ComponentType<DiffRendererProps>;
```

Used at: `/yakup/demo/compare/main...feature-branch`

Responsibilities:
- Visually represent the diff between two file versions.
- Respond to `selectedChangeId` by highlighting or focusing the relevant element.
- Call `onSelectChange` when the user clicks a change in the renderer's own UI (e.g., clicking a changed mesh in a 3D viewer).
- ForgeHub's change sidebar drives `selectedChangeId`; the renderer and sidebar are kept in sync.

#### 3. `MergeResolver` — PR conflict resolution

```ts
type MergeResolverProps = {
  baseSnapshot: Snapshot;      // common ancestor
  oursSnapshot: Snapshot;      // current branch
  theirsSnapshot: Snapshot;    // incoming branch
  diffResult: DiffResult;      // diff from backend handler
  onResolve: (
    entityId: string,           // ID of the conflicting entity
    field: string | null,       // specific field, or null for the whole entity
    side: "base" | "incoming"   // which version to keep
  ) => void;
};

type MergeResolver = React.ComponentType<MergeResolverProps>;
```

Used at: `/yakup/demo/merge/feature-branch`

Responsibilities:
- Display the three-way conflict state.
- Allow the user to resolve conflicts field-by-field or entity-by-entity.
- Call `onResolve` for each user decision.
- ForgeHub collects all `onResolve` calls and assembles the final merged file.

---

### The Ceiling (optional)

FHRs may register additional full-page routes. ForgeHub mounts these under the handler's namespace and wraps them in its chrome (header, auth, navigation). This allows FHRs to provide rich, format-specific UIs beyond the standard diff/merge views.

#### Extended route URL pattern

```
# Baseline (ForgeHub uses FHR's SnapshotRenderer in its own layout)
/yakup/demo/blob/main/scene.blend

# Extended (FHR owns the page body, ForgeHub wraps chrome)
/yakup/demo/blend-workspace/scene.blend
/yakup/demo/blend-workspace/models/chair.blend
```

The second segment after the repo (e.g., `blend-workspace`) is the handler's chosen namespace, declared in its routes array.

#### Extended route props

```ts
type ExtendedRouteProps = {
  token: string;       // ForgeHub auth token for making API calls
  repo: Repo;          // repo metadata
  currentRef: string;  // current branch or commit
  filePath: string;    // path to the file being viewed
};

type Repo = {
  owner: string;
  name: string;
  defaultBranch: string;
};
```

---

## 9. FHRManifest Type and ForgeHub Loading

### `FHRManifest` type

The renderer bundle's `remoteEntry.js` must expose a module (at the path declared in `manifest.json`'s `frontend.exposedModule`) that default-exports an `FHRManifest`:

```ts
type FHRManifest = {
  // Must match the handler ID in the source manifest and package manifest.json
  handlerId: string;

  // Extensions this renderer handles (must match backend handler's extensions)
  extensions: string[];

  // The three required renderer components
  renderers: {
    snapshot: SnapshotRenderer;
    diff: DiffRenderer;
    mergeResolver: MergeResolver;
  };

  // Optional: additional full-page routes
  routes?: Array<{
    // URL segment appended after the repo (e.g., "blend-workspace")
    // ForgeHub mounts this at /<owner>/<repo>/<path>/*
    path: string;

    // The page component ForgeHub renders for this route
    component: React.ComponentType<ExtendedRouteProps>;

    // Human-readable label for navigation (e.g., "Blender Workspace")
    label?: string;
  }>;
};
```

### Example

```ts
// In the renderer bundle, exposed as "./FHRManifest"
import { GLTFSnapshot, GLTFDiff, GLTFMerge } from "./components";

const manifest: FHRManifest = {
  handlerId: "gltf-scene",
  extensions: [".gltf", ".glb"],
  renderers: {
    snapshot: GLTFSnapshot,
    diff: GLTFDiff,
    mergeResolver: GLTFMerge,
  },
};

export default manifest;
```

### How ForgeHub loads FHRManifest

1. When a user navigates to a file in a repo, ForgeHub reads `.forge-formats` from the repo metadata.
2. It looks up the extension in `.forge-formats` to determine the source and handler ID.
3. It fetches the source manifest (cached) to resolve the renderer bundle URL.
4. It dynamically loads the renderer bundle (see [Section 11](#11-forgehub-loading-mechanism)).
5. It calls the default export of the exposed module and validates that it is a valid `FHRManifest`.
6. It mounts the appropriate renderer component.

### Validation

ForgeHub validates the `FHRManifest` before mounting:
- `handlerId` must match the expected handler ID from the source manifest.
- `extensions` must include the current file's extension.
- `renderers.snapshot`, `renderers.diff`, and `renderers.mergeResolver` must all be present and be functions (React components).
- If validation fails, ForgeHub falls back to a raw byte viewer with an error banner.

---

## 10. Handler Loading at Runtime

**Resolved recommendation: Split model.** Backend and frontend use different artifact formats suited to their runtime environments.

### Split model overview

| Layer | Runtime | Format | Rationale |
|---|---|---|---|
| Backend diff/merge | Forge CLI (Go), ForgeHub server | **WASM** (preferred) or native binary | WASM is language-agnostic and sandboxed. Native binaries are optional for performance-critical cases. |
| Frontend renderer | Browser (ForgeHub React app) | **ES module / JS bundle** | Native to the browser. No WASM overhead for UI code. |

### Backend: WASM

- Handler authors compile their diff/merge logic to WASM.
- Forge CLI uses a WASM runtime (e.g., Wasmtime) to execute the handler.
- WASM is sandboxed: the handler cannot access the filesystem or network beyond what Forge explicitly allows.
- WASM interface: the handler exports `diff(base_ptr, base_len, head_ptr, head_len) -> result_ptr` and (optionally) `merge(...)`. Forge passes file bytes via WASM memory and reads back the `StructuredDiff` as JSON.
- Handlers written in any language that compiles to WASM are supported (Rust, C, Go, AssemblyScript, etc.).

### Backend: Native binaries (optional)

- FHRs may optionally ship native binaries per platform (`linux-amd64`, `darwin-arm64`, `windows-amd64`).
- Forge CLI prefers native if available (better performance for large files), falls back to WASM.
- Native binaries are only used by Forge CLI, never by ForgeHub server or browser.
- Native binaries communicate via stdin/stdout with a line-delimited JSON protocol matching the same `StructuredDiff` wire format.

### Frontend: ES modules

- Renderer bundles are standard JavaScript ES modules.
- ForgeHub loads them dynamically using the browser's native module system or Module Federation (see [Section 11](#11-forgehub-loading-mechanism)).
- No WASM for frontend renderers — browser JS is sufficient and avoids cross-language bridge complexity.

### Security model

- WASM handlers run in a sandboxed environment. They cannot escape to the host filesystem.
- JS renderer bundles run in the browser's JS engine. Their security depends on the loading mechanism (Module Federation vs. iframe — see Section 11).
- ForgeHub verifies package integrity using a SHA-256 hash included in the source manifest (future: manifest signing via a registry keypair).

---

## 11. ForgeHub Loading Mechanism

**Resolved recommendation: Tiered trust model** — Module Federation for official/trusted FHRs, `<iframe>` + postMessage for community/untrusted FHRs.

### Tier 1: Module Federation (official and trusted FHRs)

ForgeHub uses Webpack/Vite Module Federation to dynamically load renderer bundles from trusted sources.

**How it works:**

1. ForgeHub's webpack/Vite config declares remote slots.
2. At runtime, ForgeHub uses `__webpack_init_sharing__` / dynamic `import()` to load `remoteEntry.js` from the handler's URL.
3. The remote module is initialized into ForgeHub's shared React instance (same React version, shared context).
4. ForgeHub mounts the renderer components directly into its React tree.

**Advantages:**
- Full React context sharing (auth, theme, routing).
- Tight UI integration — renderer feels native.
- Best developer experience for FHR authors.

**Trust requirement:**
- The JS bundle runs in ForgeHub's origin with full DOM and API access.
- Only sources explicitly listed in ForgeHub's trusted registry list qualify for Module Federation.
- `fhr-official` is always trusted. Community registries must be reviewed and allowlisted by the ForgeHub operator.

**Example config:**
```js
// vite.config.ts (ForgeHub)
federation({
  remotes: {
    // Populated dynamically at runtime based on resolved FHR sources
  },
  shared: ["react", "react-dom"],
})
```

### Tier 2: `<iframe>` + postMessage (community/untrusted FHRs)

For FHRs from sources not in the trusted list, ForgeHub loads the renderer inside a sandboxed `<iframe>`.

**How it works:**

1. ForgeHub creates a sandboxed `<iframe>` with `sandbox="allow-scripts"` (no `allow-same-origin`).
2. ForgeHub sends props to the iframe via `postMessage` (serialized snapshot bytes, diff result, etc.).
3. The iframe renders the FHR UI and sends events back via `postMessage` (e.g., `onSelectChange`, `onResolve`).
4. ForgeHub receives events and updates its own state accordingly.

**Advantages:**
- Strong security boundary: iframe cannot access ForgeHub's DOM, cookies, or local storage.
- Community FHRs cannot exfiltrate user data or hijack the ForgeHub session.

**Limitations:**
- Cannot share React context (theme, auth must be passed explicitly via postMessage).
- UI integration is less seamless (iframe boundaries visible).
- Extended routes (ceiling) are not available for untrusted FHRs.

### Trust boundary decision

**This is a human decision.** The spec recommends the tiered model above, but the exact criteria for graduating a community FHR to trusted (Module Federation) status — review process, signing requirements, audit cadence — must be decided by the ForgeHub team before launch.

Suggested criteria for trust graduation:
- Source URL is HTTPS.
- Registry operator has signed a contributor agreement.
- Package content is audited (manually or via automated static analysis).
- Packages are signed with a registry keypair, and ForgeHub verifies signatures before loading.

---

## 12. Versioning Scheme

**Resolved recommendation: Lock backend and frontend together per FHR release.**

### One version number per handler release

Each FHR package (handler + renderer) has a single semver version number. When the author publishes `gltf-scene@1.3.0`, both the WASM backend and the JS renderer bundle are updated together.

**Rationale:**
- Eliminates version skew bugs where a renderer expects fields in `StructuredDiff` that the handler does not produce.
- Simpler for FHR authors — one version to manage, one release to cut.
- ForgeHub always loads the renderer version that matches the backend handler version in the source manifest.
- If an author only changes the renderer (e.g., UI bug fix), they still bump the version and publish a new package. The backend WASM is included unchanged.

### Semver semantics

| Change type | Version bump |
|---|---|
| Breaking change to `StructuredDiff` output schema | Major |
| New fields in `StructuredDiff`, new renderer features | Minor |
| Bug fixes to handler or renderer | Patch |
| Breaking change to `ArtifactHandler` interface (rare) | Major + ForgeHub compat update required |

### Source manifest pinning

Source manifests always pin to exact versions:

```toml
[formats]
".gltf" = { handler = "gltf-scene", version = "1.2.0" }  # exact, not "^1.2.0"
```

This ensures reproducibility: a repo's `.forge-formats` resolves to exactly the same handler/renderer version for all team members, regardless of when they run `forge source update`.

### Upgrading

- Source maintainers update their manifest (bump version) and run the registry's publish pipeline.
- Users run `forge source update` to fetch the new manifest.
- The new version is cached on next use.
- Existing repos continue using the version specified in the source manifest until the manifest is updated.

---

## 13. `fhr-official` Bootstrap and Migration Plan

### Current state

ForgeHub currently ships with two handlers baked in:
- `gltf-scene` — handles `.gltf` and `.glb` files
- `plain-text` — handles `.txt`, `.md`, and other text-based files

These are implemented as internal modules in the ForgeHub codebase and are not versioned or externalized.

### Target state

Both handlers are extracted into `fhr-official` v1.0. ForgeHub ships with `fhr-official` pre-configured in its default `sources.list`. Existing repos continue to work without any change.

### Migration steps

#### Step 1: Extract handlers into FHR packages

1. Extract `gltf-scene` logic from ForgeHub into a standalone package.
   - Compile diff/merge logic to WASM.
   - Package renderer React components into a Module Federation bundle.
   - Publish as `fhr-official/gltf-scene@1.0.0`.
2. Repeat for `plain-text` → `fhr-official/plain-text@1.0.0`.
3. Publish `fhr-official` registry with manifest listing both handlers.

#### Step 2: Update ForgeHub

1. Add `fhr-official` to ForgeHub's default `sources.list` (shipped with installer).
2. Replace internal handler lookup with FHR resolution path.
3. Keep internal handler code as a fallback for repos with no `.forge-formats` and no sources configured (graceful degradation).

#### Step 3: Repos with no `.forge-formats`

Existing repos that pre-date FHR have no `.forge-formats` file. Forge handles this as follows:

- On first `forge status` or file view in ForgeHub, Forge detects the missing file.
- If `fhr-official` is in `sources.list`, Forge generates a default `.forge-formats` based on detected extensions in the repo.
- User is prompted to review and commit the generated file.
- No breaking change — existing repos degrade gracefully to blob view until `.forge-formats` is committed.

#### Step 4: Deprecation of baked-in handlers

- ForgeHub v(N+1): baked-in handlers are soft-deprecated (warning if FHR resolution is unavailable).
- ForgeHub v(N+2): baked-in handlers removed. FHR resolution is required.
- Timeline to be determined by the ForgeHub team.

### No breaking change guarantee

Existing repos that have `.forge-formats` entries using the old handler IDs (`gltf-scene`, `plain-text`) work without modification because `fhr-official`'s manifest uses the same handler IDs.

---

## 14. Publishing a New FHR (Author Guide)

This section is a step-by-step guide for authors who want to publish a new format handler.

### Prerequisites

- Familiarity with the `ArtifactHandler` interface and `FHRManifest` type.
- A file format you want to support (e.g., `.pcb` for PCB design files).
- Access to an FHR registry (either `fhr-official` or a private/community registry).
- A WASM toolchain for your implementation language (Rust + `wasm-pack`, Go + TinyGo, etc.).
- Node.js and a bundler (Vite or Webpack) for the renderer.

### Step 1: Implement the backend handler

Create a library in your chosen language that implements the `ArtifactHandler` interface and exposes it as a WASM module.

```rust
// Example: Rust WASM handler skeleton
#[no_mangle]
pub extern "C" fn diff(base_ptr: *const u8, base_len: usize,
                       head_ptr: *const u8, head_len: usize) -> *mut u8 {
    let base = unsafe { std::slice::from_raw_parts(base_ptr, base_len) };
    let head = unsafe { std::slice::from_raw_parts(head_ptr, head_len) };
    let result = compute_diff(base, head); // your format-specific logic
    let json = serde_json::to_vec(&result).unwrap();
    // Write length-prefixed result to WASM memory and return pointer
    // ...
}
```

Your `diff` output must serialize to valid `StructuredDiff` JSON.

### Step 2: Implement the frontend renderer

Create React components for `SnapshotRenderer`, `DiffRenderer`, and `MergeResolver`.

```tsx
// snapshot.tsx
import { SnapshotRendererProps } from "@forge/fhr-types";

export const PCBSnapshot: React.FC<SnapshotRendererProps> = ({ snapshot }) => {
  // Parse snapshot.content and render a PCB viewer
  return <PCBViewer data={snapshot.content} />;
};
```

### Step 3: Create the FHRManifest

```ts
// manifest.ts
import { FHRManifest } from "@forge/fhr-types";
import { PCBSnapshot } from "./snapshot";
import { PCBDiff } from "./diff";
import { PCBMerge } from "./merge";

const manifest: FHRManifest = {
  handlerId: "pcb-design",
  extensions: [".pcb", ".kicad_pcb"],
  renderers: {
    snapshot: PCBSnapshot,
    diff: PCBDiff,
    mergeResolver: PCBMerge,
  },
};

export default manifest;
```

### Step 4: Build and package

```bash
# Build WASM
wasm-pack build --target web --out-dir dist/

# Build renderer bundle (Vite Module Federation)
vite build

# Assemble the package
mkdir -p package/renderer
cp dist/handler.wasm package/
cp dist/remoteEntry.js package/renderer/
cat > package/manifest.json <<EOF
{
  "id": "pcb-design",
  "version": "1.0.0",
  "extensions": [".pcb", ".kicad_pcb"],
  "capabilities": { "semanticCompare": true, "semanticMerge": false },
  "backend": { "wasm": "handler.wasm" },
  "frontend": { "remoteEntry": "renderer/remoteEntry.js", "exposedModule": "./FHRManifest" }
}
EOF
```

### Step 5: Publish to a registry

**For `fhr-official`:**
1. Open a PR to the `fhr-official` repository with your package in `packages/pcb-design/1.0.0/`.
2. The FHR maintainers review the handler (automated security scan + manual review).
3. On merge, the registry CI publishes the package and updates `manifest.toml`.

**For a private/community registry:**
1. Stand up an FHR-compatible registry server (reference implementation TBD).
2. Publish directly via the registry's API.
3. Users add your registry with `forge source add <your-registry-url>`.

### Step 6: Add to a source manifest

The registry maintainer (or you, for a private registry) updates `manifest.toml`:

```toml
[formats]
".pcb"        = { handler = "pcb-design", version = "1.0.0" }
".kicad_pcb"  = { handler = "pcb-design", version = "1.0.0" }
```

### Step 7: Test end-to-end

```bash
# Add your registry
forge source add https://my-registry.example.io

# In a test repo with a .pcb file:
forge formats add .pcb
forge formats status
# Expected: .pcb -> resolved -> pcb-design v1.0.0 -> my-registry

forge diff scene.pcb  # should produce semantic diff output
```

---

## 15. Open Questions Requiring Human Decision

The following questions have reasoned recommendations but require explicit human sign-off before implementation begins.

---

### Q1: Trust boundary for Module Federation

**Question:** What are the exact criteria for a community FHR to graduate from `<iframe>` loading to Module Federation (trusted) loading in ForgeHub?

**Recommendation:** Require HTTPS source URL, signed contributor agreement, manual or automated code audit, and package signing. But the review process, SLAs, and audit tooling need to be defined.

**Why human decision needed:** This is a security policy decision with legal and operational implications. Getting it wrong means either (a) locking out legitimate community FHRs forever, or (b) shipping untrusted JS into ForgeHub's origin.

**Decision needed:** Define the trust graduation criteria and the review/audit process.

---

### Q2: Extended routes for untrusted FHRs

**Question:** Should community FHRs (loaded via `<iframe>`) be allowed to register extended routes (ceiling features), or only floor components?

**Recommendation:** Allow iframe-based extended routes by making ForgeHub navigate to an iframe-full-page view for those routes. But this requires designing a full-page iframe protocol, which is non-trivial.

**Why human decision needed:** This is a product scope decision. The simplest answer is "no extended routes for untrusted FHRs" — but that may unacceptably limit the ecosystem.

**Decision needed:** Define the product scope for untrusted FHR extended routes.

---

### Q3: Native binary security model

**Question:** Should Forge CLI execute native binaries from FHR registries, and if so, what sandboxing is required?

**Recommendation:** Default to WASM only. Require an explicit user opt-in (`forge source add --allow-native`) to enable native binaries from a source, with a clear warning about execution privileges.

**Why human decision needed:** Executing native binaries from a registry is a significant security surface. The opt-in mechanism and user-facing warnings need product and security team review.

**Decision needed:** Confirm or change the native binary opt-in model and warning language.

---

### Q4: Registry reference implementation

**Question:** Does the Forge team publish an open-source reference implementation of an FHR-compatible registry server?

**Recommendation:** Yes — a minimal Go or Node.js server that serves the expected URL structure (`/manifest.toml`, `/packages/<id>/<version>/`) so community teams can self-host.

**Why human decision needed:** This is a resourcing decision. The reference implementation is important for ecosystem health but requires ongoing maintenance.

**Decision needed:** Commit to building and maintaining a reference registry server, and choose the implementation language.

---

### Q5: Package signing and integrity verification

**Question:** Should FHR packages be signed, and should ForgeHub/Forge verify signatures before loading?

**Recommendation:** Yes, signing is required for any FHR used in production. The registry publishes a public key; packages include a detached signature. Forge and ForgeHub verify before executing/loading. For `fhr-official`, Anthropic/ForgeHub team holds the signing key.

**Why human decision needed:** Key management, revocation, and the signature format need security team input. This also affects the timeline for when community FHRs can launch.

**Decision needed:** Approve signing scheme and define key management process.

---

*End of FHR Ecosystem Specification v1.0-draft*
