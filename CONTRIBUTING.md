# Contributing a Handler to FHR

This is the official Forge Handler Repository. Each package under `packages/`
is one format handler — a backend diff/merge implementation and a frontend
react renderer, versioned and published together.

## Workflow

### 1. Fork and scaffold

Fork this repo, then copy the example package:

```bash
cp -r packages/example-handler packages/handler-myformat
```

Rename everything from `example` to your format name.

### 2. Implement the backend handler

Edit `packages/handler-myformat/src/handler.ts`. Implement:

- `matchesPath(path)` — return `true` for your file extensions
- `ingestFromUtf8Text(input)` — parse the raw file into your canonical IR
- `diff(base, head)` — return a `StructuredDiff` describing semantic changes
- `merge(base, ours, theirs)` *(optional)* — attempt a 3-way merge

All types are in `@fhr/types`. See `SPEC.md` for the full contract.

### 3. Implement the frontend renderer

Edit `packages/handler-myformat/src/renderer.tsx`. Implement the three
React components and export a valid `FHRManifest` as the default export:

- `SnapshotRenderer` — single-file viewer (blob/commit page)
- `DiffRenderer` — side-by-side or inline diff (compare/PR page)
- `MergeResolver` — conflict resolution UI (PR merge page)

### 4. Open a pull request

Open a PR to this repo. The review checklist:

- [ ] `handler.id` matches the package directory name
- [ ] `manifest.handlerId` matches `handler.id`
- [ ] `matchesPath` covers all extensions listed in the PR description
- [ ] `diff()` returns a valid `StructuredDiff` (version `"1.0"`)
- [ ] Both `SnapshotRenderer` and `DiffRenderer` are implemented (non-empty)
- [ ] `MergeResolver` is implemented (may show a "not supported" message if `merge()` is absent)
- [ ] `npm run typecheck` passes

### 5. After merge

Once merged, the maintainers will:

1. Publish the handler and renderer to a versioned release
2. Add the format entries to `manifest.toml`
3. Update `[assets]` with the download URLs

Users can then pull your handler with:

```bash
forge source update
forge formats add .myformat
```

## Local development

```bash
npm install
npm run build        # build all packages
npm run typecheck    # type-check all packages
```

## Package naming conventions

| Thing | Convention | Example |
|-------|------------|---------|
| Package dir | `packages/handler-<format>` | `packages/handler-gltf-scene` |
| npm package name | `@fhr/handler-<format>` | `@fhr/handler-gltf-scene` |
| `handler.id` | `<format>` (kebab) | `gltf-scene` |
| `manifest.toml` key | same as `handler.id` | `[formats.".gltf"]` |
