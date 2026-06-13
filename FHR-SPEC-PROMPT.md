# FHR Ecosystem Spec — Session Bootstrap

We are building a hardware version control ecosystem consisting of three repositories:

- **Forge** — our core Git-based VCS CLI (like git, but for hardware artifacts)
- **ForgeHub** — our collaboration platform (like GitHub, but for hardware teams)
- **FHR (Forge-Handler-Repository)** — the official repository of format handlers and renderers

Your task in this session is to write a full spec document (`SPEC.md`) for the FHR repository, covering the entire ecosystem contract. Here is the full design we have agreed on.

---

## Background

ForgeHub currently has handlers for `.gltf` (gltf-scene) and plain text baked in. The goal is to extract all concrete handlers out of ForgeHub/Forge and into FHR (and eventually community registries), making the system extensible by third parties.

The unified diff wire format (`StructuredDiff`) is already implemented:

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
  format: string;
  changes: DiffChange[];
};
```

The backend handler interface is already defined:

```ts
type ArtifactHandler = {
  id: string;
  capabilities: { semanticCompare: boolean; semanticMerge: boolean };
  matchesPath(path: string): boolean;
  ingestFromUtf8Text(input: IngestInput): Promise<string>;
  diff(base: Buffer, head: Buffer): Promise<StructuredDiff>;
  merge?(base: Buffer, ours: Buffer, theirs: Buffer): Promise<MergeResult>;
};
```

---

## Ecosystem Architecture

```
sources.list      -> where Forge looks for FHRs (registries)
.forge-formats    -> which extensions a repo cares about
FHR               -> provides handler (backend) + renderer (frontend) per format
ForgeHub          -> shell + contract, loads what .forge-formats declares
```

---

## Forge CLI — Source and Format Commands

**Source management** (manipulates `sources.list`):

```
forge source add <url>       # fetch source manifest, append to sources.list
forge source remove <name>
forge source list            # show all sources + advertised formats
forge source update          # re-fetch all source manifests (like apt-get update)
```

**Format management** (manipulates `.forge-formats`):

```
forge formats add .blend              # add to include, warn on conflicts
forge formats ignore .tif             # add to ignore list
forge formats pin .blend <source>     # resolve ambiguity when 2 sources claim same extension
forge formats status                  # show all extensions in repo + handler resolution status
```

**`forge status` augmented output** when conflicts or missing handlers exist:

```
Warnings:
  .blend  -> 2 handlers found (fhr-official, community-3d) - ambiguous
            run: forge formats pin .blend <source>
  .llm    -> listed in .forge-formats but no handler found in any source
```

---

## `.forge-formats` File Format (TOML)

```toml
[include]
".gltf"  = {}                           # any source
".blend" = { source = "fhr-official" }  # pinned to specific source
"domain:3d" = {}                        # shorthand, expands to all 3d formats from source

[ignore]
".tif"     = {}
".garbage" = {}
```

**Rules:**

- Text-based files are handled by default — no need to list them
- `include` = resolve handler + renderer, preload on repo open, semantic diff/merge available
- `ignore` = tracked by Git/Forge, stored as opaque blob, no handler resolution, no prompt
- Unregistered extension (not in either list) = prompt user to classify it
- Format in `include` with no handler in any source = warning at `forge status` time, graceful fallback to blob view
- Two sources claiming the same extension = conflict warning, user must run `forge formats pin`

---

## Source Manifest Schema

Each FHR must publish a manifest at a well-known URL. Forge fetches and caches this on `forge source update`:

```toml
name    = "fhr-official"
url     = "https://fhr.example.io"
version = "1.0"

[formats]
".gltf"  = { handler = "gltf-scene", version = "1.2.0" }
".obj"   = { handler = "obj-scene",  version = "1.0.1" }
".blend" = { handler = "blender",    version = "0.9.0" }

[domains]
"3d" = [".gltf", ".obj", ".blend", ".fbx"]
```

Domains are named shorthand groups defined by the source, not by Forge itself.

---

## ForgeHub Frontend Contract

### The Floor (required — every FHR renderer must implement)

```ts
// 1. Snapshot renderer — viewing a single file at a commit
type SnapshotRendererProps = {
  snapshot: Snapshot;
};

// 2. Diff renderer — compare view / PR diff
type DiffRendererProps = {
  baseSnapshot: Snapshot;
  targetSnapshot: Snapshot;
  diffResult: DiffResult;
  selectedChangeId?: string;
  onSelectChange: (id: string | null) => void;
};

// 3. Merge resolver — PR conflict resolution
type MergeResolverProps = {
  baseSnapshot: Snapshot;
  oursSnapshot: Snapshot;
  theirsSnapshot: Snapshot;
  diffResult: DiffResult;
  onResolve: (entityId: string, field: string | null, side: "base" | "incoming") => void;
};
```

### The Ceiling (optional — FHR extended pages)

FHRs may register their own full pages. ForgeHub mounts these under the handler namespace and wraps them in its chrome (header, auth, navigation):

```
/yakup/demo/blob/main/scene.blend         -> baseline: ForgeHub uses FHR's SnapshotRenderer
/yakup/demo/blend-workspace/scene.blend   -> extended: FHR owns the page, ForgeHub wraps chrome
```

Extended route registration:

```ts
type FHRManifest = {
  handlerId: string;
  extensions: string[];
  renderers: {
    snapshot: SnapshotRenderer;
    diff: DiffRenderer;
    mergeResolver: MergeResolver;
  };
  routes?: Array<{
    path: string;
    component: React.ComponentType<ExtendedRouteProps>;
    label?: string;
  }>;
};

type ExtendedRouteProps = {
  token: string;
  repo: Repo;
  currentRef: string;
  filePath: string;
};
```

---

## Open Questions to Resolve

1. **Artifact format** — how does Forge/ForgeHub load and execute handler code at runtime?
   - JS/ES modules (simplest for frontend renderers, native to browser)
   - WASM (language-agnostic, sandboxed — good for backend diff/merge)
   - Native binaries (Forge CLI only, not browser)
   - Likely split: JS modules for renderers, WASM or native for CLI-side diff/merge

2. **Frontend loading mechanism** — how does ForgeHub load FHR renderer bundles?
   - Module Federation (Vite/webpack) — dynamic remote JS, tight integration, requires trust
   - `<iframe>` + postMessage — sandboxed, safer for third-party FHRs, limits UI integration
   - Recommendation: Module Federation for official FHRs, iframe option for untrusted community FHRs

3. **Handler versioning** — should backend handler version and frontend renderer version be independently versioned or locked together per FHR release?

4. **`fhr-official` bootstrap** — the current `gltf-scene` and `plain-text` handlers baked into ForgeHub become the first entries in `fhr-official`. Define the migration path.

---

Please write a comprehensive `SPEC.md` covering all of the above. Resolve the open questions with reasoned recommendations where possible, and flag the ones that genuinely need a human decision. The document should serve as the authoritative reference for anyone implementing Forge source management, `.forge-formats` parsing, the FHR publish protocol, and the ForgeHub renderer contract.
