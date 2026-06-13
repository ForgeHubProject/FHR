# FHR — Forge Handler Repository

> The official registry of format handlers for the Forge ecosystem.

## Dependency direction

```
┌─────────────────────────────────────────────────────────────┐
│  Forge CLI  ────────────────────────────────────────────┐   │
│  (git, but for everything)    subprocess JSON protocol  │   │
│                                                         ▼   │
│                                                    ┌──────┐ │
│                                                    │  FHR │ │
│                                                    │      │ │
│  ForgeHub  ─────────────────────────── @fhr/types ─▶      │ │
│  (GitHub, but for hardware)                        └──────┘ │
└─────────────────────────────────────────────────────────────┘
```

**Forge and FHR are the foundation. ForgeHub builds on top of them — not the other way around.**

- `@fhr/types` is the single source of truth for the handler and renderer contracts
- ForgeHub imports from `@fhr/types`; it does not define its own handler types
- The Forge CLI speaks the FHR subprocess protocol; it does not depend on ForgeHub
- A handler can exist and be used from the Forge CLI without ForgeHub existing at all

## What lives here

```
fhr/
├── manifest.toml                  # live registry index (forge source add <url>)
├── packages/
│   ├── types/                     # @fhr/types — the shared contract
│   ├── example-handler-ts/        # skeleton: TypeScript direct-import handler
│   └── example-handler-native/   # skeleton: language-agnostic subprocess handler
└── CONTRIBUTING.md
```

## How a handler reaches users

```
1. Author implements ArtifactHandler (any language) + FHRManifest (React)
2. Author opens a PR to this repo
3. Maintainers review, merge, publish a release
4. manifest.toml is updated with the new format entry
5. User runs:
     forge source update
     forge formats add .myformat
```

## Two backend paths — same contract

| Path | Used by | Language |
|------|---------|----------|
| Direct TypeScript import | ForgeHub API | TypeScript |
| Subprocess JSON protocol | Forge CLI | **Any** |
| WASM module | ForgeHub API (sandboxed) | **Any** (compile to WASM) |

The frontend renderer (`renderer.tsx`) must be a React component — it runs in the browser. The backend diff/merge logic can be written in any language.

See [SPEC.md](./SPEC.md) for the full contract and [CONTRIBUTING.md](./CONTRIBUTING.md) for the submission workflow.
