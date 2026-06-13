# FHR — Forge Handler Repository

> The official registry of format handlers for the Forge ecosystem.

## The pipeline

```
FHR  ────────────────────▶  Forge  ────────────────────▶  ForgeHub
(package registry)        (git-like VCS,             (web showcase,
                           pulls + runs FHR            displays what
                           handlers)                   Forge produced)
```

**FHR** defines what a format *is* — how to diff it, merge it, and render it.

**Forge** is the tunnel: it pulls handlers from FHR and makes their knowledge
operational inside version control. `forge diff model.glb` works because Forge
fetched the `gltf-scene` handler from FHR and ran it.

**ForgeHub** is the showcase: it presents the repos and diffs that Forge produced.
It pulls only the *frontend renderers* from FHR — to display a `StructuredDiff`
in the browser. The diff computation itself already happened inside Forge.

## Dependency arrows

```
FHR
 │
 ├── Forge CLI pulls handler binaries/WASM from FHR, runs them as subprocesses
 │       forge diff model.glb  →  forge-handler-gltf-scene  →  StructuredDiff
 │
 └── ForgeHub web pulls renderer bundles from FHR, mounts them as React components
         /pr/42/diff  →  GltfDiffRenderer(diffResult)  →  3D diff in browser
```

Neither Forge nor ForgeHub defines its own handler types.
`@fhr/types` is the single source of truth for the contract both consume.

## What lives here

```
fhr/
├── manifest.toml                    # registry index (forge source add <url>)
├── packages/
│   ├── types/                       # @fhr/types — the shared contract
│   ├── example-handler-ts/          # skeleton: TypeScript handler (direct import)
│   └── example-handler-native/      # skeleton: any-language handler (subprocess)
├── CONTRIBUTING.md
└── SPEC.md
```

## How a handler reaches users

```
1. Author implements ArtifactHandler (any language) + FHRManifest (React)
2. Author opens a PR to this repo
3. Maintainers review, merge, publish a release
4. manifest.toml is updated with the new format entry + asset URLs
5. Users pick it up:
     forge source update
     forge formats add .myformat
```

From that point on, `forge diff file.myformat` produces a semantic diff,
and ForgeHub renders it in the browser using the matching renderer bundle.

## Backend: language agnostic

The handler backend (diff/merge logic) can be written in any language.
Forge speaks to it over the subprocess JSON protocol — stdin/stdout JSON.
ForgeHub runs it as a WASM module server-side.

| Path | Used by | Language constraint |
|------|---------|--------------------|
| Subprocess binary | Forge CLI | None — any language |
| WASM module | ForgeHub API | None — compile anything to WASM |
| TypeScript direct import | ForgeHub API | TypeScript only |

## Frontend: always TypeScript + React

The renderer runs in the browser. It must be a React component bundle.
Your backend can be Rust; your renderer will still be `.tsx`.

See [SPEC.md](./SPEC.md) for the full contract and [CONTRIBUTING.md](./CONTRIBUTING.md) for the submission workflow.
