# FHR — Forge Handler Repository Specification

> Version: 1.2 · Status: Draft

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

- **Domain groupings** — there is no domain abstraction. Extensions map directly to handlers. Predefined extension sets will be addressed as user-facing **presets** in a future version (a named `.forge-formats` template applied in one command).
- **`.forge/handlers`** — deprecated before implementation. `.forge-formats` is the single per-repo format declaration file.
- **`forge domain` commands** — removed. `forge source` and `forge formats` cover all use cases.

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

**Built-in handler:** `plain-text` is compiled into Forge and is always available as the catch-all fallback. It never needs to be fetched from FHR.

---

## 3. FHR Package Structure

An FHR author publishes **two artifacts** per handler release:

### 3a. Backend handler

Used by the Forge CLI and ForgeHub API. Distributed as:
- **Native binaries** (`forge-handler-<name>_<os>-<arch>`) for the Forge CLI subprocess protocol
- A **WASM module** (`forge-handler-<name>.wasm`) for ForgeHub API server-side execution

Both implement the same stdin/stdout JSON protocol (§7). The backend can be written in **any language**.

### 3b. Frontend renderer

Used by ForgeHub web. Distributed as:
- An **ES module bundle** (`renderer.js`) exporting an `FHRManifest` object (§9)

Must be TypeScript + React — it runs in the browser.

### 3c. Source manifest

A TOML file served at a stable URL, listing extensions and asset download paths (§4).

**Minimal FHR layout:**

```
my-fhr/
├── manifest.toml
├── handlers/
│   └── gltf-scene/1.0.0/
│       ├── forge-handler-gltf-scene_linux-amd64
│       ├── forge-handler-gltf-scene_darwin-arm64
│       ├── forge-handler-gltf-scene_windows-amd64.exe
│       └── forge-handler-gltf-scene.wasm
└── renderers/
    └── gltf-scene/1.0.0/
        └── renderer.js
```

---

## 4. Source Manifest Schema

Forge fetches and caches this on `forge source update` (or on first use of `--source <url>`).

```toml
# Required
name    = "fhr-official"
url     = "https://fhr.forge.io"   # base URL; asset paths are relative to this
version = "1.0"

# Optional
description = "Official Forge Handler Repository"
maintainer  = "ForgeHub Project"

# Flat extension → handler mapping. No domain groupings.
[formats]
".gltf"  = { handler = "gltf-scene", version = "1.0.0" }
".glb"   = { handler = "gltf-scene", version = "1.0.0" }
".obj"   = { handler = "obj-scene",  version = "1.0.0" }
".blend" = { handler = "blender",    version = "1.0.0" }
# plain-text extensions are not listed here — handled by Forge's built-in.

# Asset download paths (relative to `url`).
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
- Extensions map directly to handlers — flat, no hierarchy.
- If two sources claim the same extension, Forge warns and requires `--source` or `forge formats pin` to resolve.
- `plain-text` is never listed; it is built into Forge.

---

## 5. `.forge-formats` File Format

`.forge-formats` is committed to the repo root. It is the single declaration of which extensions Forge applies semantic handling to.

```toml
# .forge-formats

[include]
# Resolve a handler from the default source (fhr-official).
".gltf"  = {}
".glb"   = {}
# Pinned to a specific source name.
".blend" = { source = "fhr-official" }
# Pinned to a raw source URL (uncommon — prefer a named source).
".hip"   = { source = "https://forge.sidefx.com/manifest.toml" }

[ignore]
# Track as opaque blob — no handler, no prompt, blob view only.
".tif"   = {}
".tmp"   = {}
```

### Classification rules

| State | Tracked by Git | Handler resolved | Prompt shown |
|-------|---------------|------------------|--------------|
| `[include]` | yes | yes | no |
| `[ignore]` | yes | no | no |
| unregistered | yes | no | yes — classify or skip |

- **Text-based files** are handled by the built-in `plain-text` handler — no entry needed.
- **Unregistered binary files** prompt: `[a]dd to include / [i]gnore / [s]kip`.
- `[include]` with no handler found → warning at `forge formats status`, blob view fallback.
- Two sources, no pin → conflict warning until resolved.

### Presets (future)

Predefined named templates (e.g. a "3D repo starter") will be introduced as **presets** in a future version. A preset expands to explicit extension entries in `.forge-formats`. This replaces the domain shorthand concept while keeping the file flat and readable.

---

## 6. Forge CLI — Source & Format Commands

### `forge source` — manages `~/.forge/sources.list`

```
forge source add <url> [--name <name>]
```
Fetches the manifest at `<url>`, validates it, and appends it to `sources.list`.
- `--name <name>` — override the source name from the manifest. Useful when the manifest's `name` field would conflict with an existing source, or when you want a shorter local alias.
- If omitted, the name is taken from the manifest's `name` field.
- Warns immediately if any extension in the new source conflicts with an already-configured source.

```
forge source remove <name>
```
Removes the named source. Does not uninstall already-downloaded handlers.

```
forge source list
```
Prints all configured sources with their URL, last-fetched timestamp, and advertised extensions.

```
forge source update [--source <name>]
```
Re-fetches source manifests and updates the local cache. Does not download handler binaries.
- `--source <name>` — update only the named source. If omitted, updates all.

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
forge formats add <extension> [--source <name-or-url>]
```
Adds the extension to `[include]` in `.forge-formats`.
- `--source <name>` — resolve the handler from this named source (must be in `sources.list`). Written as `source = "<name>"` in `.forge-formats`.
- `--source <url>` — resolve from a raw manifest URL. Forge fetches the manifest on the fly (without permanently adding it to `sources.list`), finds the handler, and writes `source = "<url>"` in `.forge-formats`. Other users who clone the repo and run `forge formats status` will see the URL and can add it to their own `sources.list` if they want.
- If `--source` is omitted, Forge resolves from `fhr-official` by default. If `fhr-official` is not configured, Forge errors with an install hint.
- Warns if two sources both claim the extension and no `--source` flag was given.

```bash
# Examples
forge formats add .gltf                                  # from fhr-official (default)
forge formats add .blend --source fhr-official           # explicit source name
forge formats add .blend --source community-blend        # another registered source
forge formats add .hip   --source https://forge.sidefx.com/manifest.toml  # raw URL
```

```
forge formats ignore <extension>
```
Adds the extension to `[ignore]`.

```
forge formats pin <extension> --source <name-or-url>
```
Sets (or updates) the `source` field on an existing `[include]` entry. Use this to resolve a conflict after the fact, or to switch a pinned extension to a different source.
- `--source` is required for `pin`.
- Errors if the extension is not already in `[include]` or if the given source does not claim it.

```
forge formats remove <extension>
```
Removes the extension from whichever section it is in (`[include]` or `[ignore]`). Does not uninstall the handler binary.

```
forge formats status
```
Prints every extension detected in the working tree and its resolution state:

```
Extension   State           Handler              Source
─────────────────────────────────────────────────────────────────────
.gltf       resolved        gltf-scene@1.0.0     fhr-official
.blend      resolved        blender@1.0.0        fhr-official  [pinned]
.hip        resolved        houdini@2.1.0        https://forge.sidefx.com/manifest.toml  [pinned]
.fbx        warning         —                    —  (no handler in any source)
.tif        ignored
.llm        unregistered                              (run: forge formats add .llm)
```

**Augmented `forge status` output when warnings exist:**
```
Warnings:
  .blend  → 2 sources claim this extension (fhr-official, community-blend)
             run: forge formats pin .blend --source <name>
  .fbx    → in [include] but no handler found in any source
             run: forge source update  or  forge source add <url>
```

---

## 7. Backend Handler Contract

All backend handlers implement the same contract regardless of language. Expressed as TypeScript for ForgeHub API (direct import or WASM) and as a stdin/stdout JSON protocol for the Forge CLI.

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
  path: string;   // e.g. "nodes[2].translation"
  ours: unknown;
  theirs: unknown;
};

interface ArtifactHandler {
  readonly id: string;   // matches handler name in manifest.toml
  readonly capabilities: { semanticCompare: boolean; semanticMerge: boolean };
  matchesPath(path: string): boolean;
  ingestFromUtf8Text(input: IngestInput): Promise<string>;
  diff(base: Buffer, head: Buffer): Promise<StructuredDiff>;
  merge?(base: Buffer, ours: Buffer, theirs: Buffer): Promise<MergeResult>;
}
```

### `StructuredDiff` wire format

```ts
type DiffChange = {
  path: string;
  kind: "added" | "removed" | "modified";
  label?: string;
  before?: unknown;
  after?: unknown;
  children?: DiffChange[];
};

type StructuredDiff = {
  version: "1.0";
  format: string;   // handler id, e.g. "gltf-scene"
  changes: DiffChange[];
};
```

### Forge CLI subprocess protocol

Forge discovers external handlers as executables named `forge-handler-<name>` in:
1. `~/.forge/plugins/` (installed via `forge source`)
2. Anywhere on `$PATH`
3. Built-in: `plain-text` (always available)

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
exit:   0 on success
        1 with { "error": "not supported" } if merge is not implemented
           → Forge falls back to blob-pick, same as plain git
```

**`forge-handler-<name> info`** *(optional but recommended)*
```
stdout: { "id": "gltf-scene", "version": "1.0.0", "formats": [".glb", ".gltf"], "protocol": "1.0" }
exit:   0 always
```

Blobs are base64-encoded to keep the transport pure JSON. The same binary works as both a CLI subprocess and a WASM module.

---

## 8. Frontend Renderer Contract

Every FHR renderer must implement three React components (the **floor**). Always TypeScript + React regardless of the backend language.

### Supporting types

```ts
type Snapshot = {
  id: string;
  commitSha: string;
  filePath: string;
  content: string;    // raw file, base64-encoded
  entities: unknown;  // parsed IR from ingestFromUtf8Text
};
```

### Floor (all three required)

```ts
// 1. Single-file viewer — blob page, commit page
type SnapshotRendererProps = { snapshot: Snapshot };

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

```ts
type ExtendedRouteProps = {
  token: string;   // caller's auth token for ForgeHub API calls
  repo: { owner: string; name: string };
  currentRef: string;
  filePath: string;
};

type ExtendedRoute = {
  path: string;    // relative to /:owner/:repo/, e.g. "3d-workspace/:ref/*filePath"
  component: React.ComponentType<ExtendedRouteProps>;
  label?: string;
};
```

---

## 9. FHRManifest & ForgeHub Loading

```ts
interface FHRManifest {
  handlerId: string;   // must match ArtifactHandler.id and manifest.toml handler name
  extensions: string[];
  renderers: {
    snapshot: React.ComponentType<SnapshotRendererProps>;
    diff: React.ComponentType<DiffRendererProps>;
    mergeResolver: React.ComponentType<MergeResolverProps>;
  };
  routes?: ExtendedRoute[];
}

// renderer.js default export
export default {
  handlerId: "gltf-scene",
  extensions: [".gltf", ".glb"],
  renderers: { snapshot: GltfSnapshotRenderer, diff: GltfDiffRenderer, mergeResolver: GltfMergeResolver },
  routes: [{ path: "3d-workspace/:ref/*filePath", component: GltfWorkspace, label: "3D Workspace" }],
} satisfies FHRManifest;
```

### Loading mechanism

- **Official / operator-trusted sources**: **Module Federation** — full React context sharing, shared deps.
- **Untrusted community sources**: **`<iframe>` + postMessage** — sandboxed, cannot access ForgeHub's DOM or auth tokens.

Trust boundary configuration is a **[HUMAN DECISION]** — see §14.

---

## 10. Handler Loading at Runtime

| Context | Format | Notes |
|---------|--------|-------|
| Forge CLI | Native binary | Subprocess protocol; any language |
| ForgeHub API | WASM module | Same protocol; sandboxed |
| ForgeHub web | ES module | React/TS; runs in browser |

**CLI discovery order:**
1. `~/.forge/plugins/` (downloaded by `forge source`)
2. `$PATH`
3. Built-in `plain-text`

**WASM caching:** modules cached at `$FORGE_HANDLER_CACHE` (default `~/.forge/wasm-cache/`) keyed by `{handler-id}/{version}.wasm`.

---

## 11. Versioning Scheme

Backend and frontend are **versioned together** per release. One semver covers both the binary/WASM and the renderer bundle. Eliminates version skew between `StructuredDiff` producer and consumer.

- `MAJOR` — breaking change to `StructuredDiff` schema or renderer props
- `MINOR` — new capabilities, backwards compatible
- `PATCH` — bug fixes

Exact version pinning only in v1 (no ranges).

---

## 12. fhr-official Bootstrap & Migration

`plain-text` stays compiled into Forge as the built-in catch-all — not an FHR package.

`gltf-scene` is the first handler to migrate.

### Migration plan

**Phase 1 — glTF into FHR (current)**
1. Move `gltf.go` from `forgehubproject/forge` into `packages/handler-gltf-scene/`, wrapped in the subprocess protocol.
2. Add `.gltf`/`.glb` entries to `manifest.toml` with asset download URLs.
3. Wire Forge to read `sources.list`, fetch `manifest.toml`, download `forge-handler-gltf-scene`, invoke via subprocess.
4. Remove the built-in glTF handler from Forge.
5. End-to-end test: `forge source add <fhr-url>` → `forge formats add .gltf` → `forge diff model.glb`.

**Phase 2 — ForgeHub loads from FHR**
6. Replace ForgeHub API's direct TS import of gltf-scene with WASM invocation.
7. ForgeHub web loads `renderer.js` via Module Federation instead of a static import.

**Phase 3 — Community onboarding**
8. Publish `@fhr/types` and `example-handler-native` as the reference for third-party authors.
9. Announce the registry spec.

---

## 13. Publishing a New Handler

### Step 1 — Backend (any language)

Implement the subprocess protocol (§7). See `packages/example-handler-native/` for a Go skeleton.

```bash
# Native binaries
GOOS=linux   GOARCH=amd64 go build -o forge-handler-myformat_linux-amd64 .
GOOS=darwin  GOARCH=arm64 go build -o forge-handler-myformat_darwin-arm64 .
GOOS=windows GOARCH=amd64 go build -o forge-handler-myformat_windows-amd64.exe .
# WASM
GOOS=wasip1 GOARCH=wasm  go build -o forge-handler-myformat.wasm .
```

### Step 2 — Frontend renderer (TypeScript + React)

```ts
import type { FHRManifest } from '@fhr/types';
export default {
  handlerId: 'my-format',
  extensions: ['.myext'],
  renderers: { snapshot: MySnapshotRenderer, diff: MyDiffRenderer, mergeResolver: MyMergeResolver },
} satisfies FHRManifest;
```

Build to `renderer.js` (single ESM bundle).

### Step 3 — Host and write `manifest.toml`

Host binaries and `renderer.js` at stable URLs. Write `manifest.toml` following §4.

### Step 4 — Open a PR or self-host

To list in `fhr-official`: open a PR here (see `CONTRIBUTING.md`).

To self-host:
```bash
forge source add https://your-fhr.example.io/manifest.toml --name my-org
forge formats add .myext --source my-org
```

---

## 14. Open Questions

### Resolved

| Question | Resolution |
|----------|------------|
| Domain abstraction | Removed. Flat extension → handler mapping. Presets as future UX sugar. |
| `.forge/handlers` | Deprecated before implementation. |
| `forge domain` commands | Removed. Superseded by `forge source` / `forge formats`. |
| `--source` flag | `forge formats add .ext --source <name-or-url>`. Default: `fhr-official`. Raw URL resolves on the fly without permanently adding to `sources.list`. |
| `--name` flag on `forge source add` | Overrides the source name from the manifest for a local alias. |
| Backend language | Any language via subprocess / WASM |
| Frontend language | TypeScript + React |
| Backend + frontend versioned | Locked together per release |
| Official FHR loading | Module Federation |
| Community FHR loading | `<iframe>` + postMessage |
| `plain-text` | Built into Forge; not an FHR package |
| First migration | `gltf-scene` (Phase 1) |

### Still open **[HUMAN DECISION]**

1. **Trust boundary** — who configures which sources use Module Federation vs iframe? Recommendation: operator-level for self-hosted; hosted product trusts only `fhr-official` by default.

2. **Auto-download on `forge formats add`** — should adding an extension automatically download the handler binary, or should that be an explicit separate step? Auto-download is more ergonomic; explicit is more transparent for security.

3. **Raw `--source <url>` and `sources.list`** — when a raw URL is used, should Forge prompt to permanently add it to `sources.list`, or always treat it as one-shot? One-shot is safer; prompting is more convenient for teams.

4. **Semantic merge correctness** — for complex formats (skeletal animation, shader graphs), what is the canonical correct merge when both sides modify the same property? Handler authors define their own strategies; Forge falls back to blob-pick.

5. **iframe postMessage protocol** — the typed message contract between ForgeHub and sandboxed community renderers is not yet specified.
