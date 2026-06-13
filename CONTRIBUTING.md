# Contributing a Handler to FHR

## Architecture principle

Forge and FHR are the foundation. ForgeHub builds on top of them.

```
Forge CLI  ──(subprocess JSON protocol)──▶  your handler binary (any language)
ForgeHub   ──(@fhr/types)────────────────▶  your handler (TS import or WASM)
                                            your renderer (always React/TS)
```

A handler can be used from the Forge CLI without ForgeHub. ForgeHub is a
consumer of the FHR contract — it does not own it.

## Two backend paths

### Path A — TypeScript (direct import)

Use `packages/example-handler-ts` as your starting point.

ForgeHub API imports your handler directly as a Node module. Simplest path
for handlers whose logic is naturally TypeScript (e.g. JSON, Markdown, SVG).

### Path B — Native / any language (subprocess protocol)

Use `packages/example-handler-native` as your starting point.

Your handler is a standalone binary (`forge-handler-<name>`) that speaks JSON
over stdin/stdout. Forge CLI calls it as a subprocess. ForgeHub API runs it
as a WASM module. The language is completely up to you.

Both paths produce the same `StructuredDiff` output. The frontend renderer
consumes that output — it doesn't know or care which path the backend used.

## The frontend renderer (always TypeScript + React)

The renderer runs in the browser. It must be a React component bundle.
Your backend can be in Rust; your renderer will still be `.tsx`.

Import all types from `@fhr/types` — that is the single source of truth
for the handler and renderer contracts.

## Workflow

### 1. Scaffold

```bash
# TypeScript handler
cp -r packages/example-handler-ts  packages/handler-myformat

# Or native handler (any language)
cp -r packages/example-handler-native  packages/handler-myformat
```

### 2. Implement

**Backend** — implement these in your chosen language:
- `match <filepath>` → `"true"` or `"false"`
- `diff` → `StructuredDiff` JSON
- `merge` → `{ blob, conflicts }` JSON *(optional)*
- `info` → `{ id, version, formats, protocol }` JSON

**Frontend** — implement these React components in `renderer.tsx`:
- `SnapshotRenderer` — single-file viewer (blob/commit page)
- `DiffRenderer` — diff view (compare/PR page)
- `MergeResolver` — conflict resolution UI (PR merge page)

Export an `FHRManifest` as the default export.

### 3. Open a pull request

Review checklist:

- [ ] `handler.id` / `info.id` matches the package directory name
- [ ] `manifest.handlerId` matches the handler id
- [ ] `matchesPath` covers all extensions listed in the PR description
- [ ] `diff` returns a valid `StructuredDiff` (version `"1.0"`)
- [ ] All three renderer components are implemented (non-empty)
- [ ] `npm run typecheck` passes on the renderer
- [ ] For native handlers: binary builds cleanly for at least one platform

### 4. After merge

Maintainers will:
1. Publish a versioned release with binaries + `renderer.js`
2. Add format entries to `manifest.toml`
3. Update `[assets]` with download URLs

Users then get your handler with:
```bash
forge source update
forge formats add .myformat
```

## Naming conventions

| Thing | Convention | Example |
|-------|------------|---------|
| Package dir | `packages/handler-<format>` | `packages/handler-gltf-scene` |
| npm name | `@fhr/handler-<format>` | `@fhr/handler-gltf-scene` |
| `handler.id` / `info.id` | `<format>` (kebab) | `gltf-scene` |
| Binary name | `forge-handler-<format>_<os>-<arch>` | `forge-handler-gltf-scene_linux-amd64` |
| `manifest.toml` key | same as handler id | `".gltf" = { handler = "gltf-scene" }` |
