// ─── Wire format (mirrors forge/internal/handler/handler.go) ─────────────────

/**
 * "renamed" is additive: `fhrVersion` stays 1, and consumers are required to
 * carry a kind they do not recognise rather than drop it (see the SDK's
 * `countKinds`), so a renderer built before this kind existed still counts and
 * shows it. A renamed change's label and path use the entity's *new* name, with
 * `before`/`after` carrying old → new; anything else that changed at the same
 * time hangs off it as a child.
 */
export type ChangeKind = "added" | "removed" | "modified" | "renamed";

export type DiffChange = {
  path: string;
  kind: ChangeKind;
  /** Human-readable label, e.g. "Armature bone: spine_01" */
  label?: string;
  before?: unknown;
  after?: unknown;
  /** Nested changes for hierarchical formats */
  children?: DiffChange[];
};

export type StructuredDiff = {
  version: "1.0";
  /** Handler id that produced this diff, e.g. "gltf-scene" */
  format: string;
  changes: DiffChange[];
};

export type SemanticConflict = {
  /** Semantic path, e.g. "nodes[2].translation" */
  path: string;
  ours: unknown;
  theirs: unknown;
};

export type ConflictInfo = {
  conflicts: SemanticConflict[];
};

export type MergeResult = {
  blob: Buffer;
  conflicts?: ConflictInfo;
};

// ─── Backend handler contract ─────────────────────────────────────────────────
// Implemented by each handler package (handler-gltf-scene, handler-plain-text, …)
// Used by: ForgeHub API (direct import or WASM), Forge CLI (subprocess protocol)

export type HandlerCapabilities = {
  semanticCompare: boolean;
  semanticMerge: boolean;
};

export type IngestInput = {
  repoId: string;
  sourceFile: string;
  utf8Text: string;
  label: string | null;
  gitCommitSha: string | null;
};

export interface ArtifactHandler {
  /** Unique identifier. Must match the handler name in manifest.toml. */
  readonly id: string;
  readonly capabilities: HandlerCapabilities;

  /** Return true if this handler should process the given file path. */
  matchesPath(path: string): boolean;

  /** Ingest a raw file into ForgeHub's canonical intermediate representation. */
  ingestFromUtf8Text(input: IngestInput): Promise<string>;

  /** Produce a structured semantic diff between two raw file blobs. */
  diff(base: Buffer, head: Buffer): Promise<StructuredDiff>;

  /**
   * Attempt a 3-way semantic merge.
   * Omit (or return conflicts) if format cannot merge cleanly.
   * ForgeHub falls back to blob pick if this is not implemented.
   */
  merge?(base: Buffer, ours: Buffer, theirs: Buffer): Promise<MergeResult>;
}

// ─── Frontend renderer contract ───────────────────────────────────────────────
// Implemented by each renderer package (renderer-gltf-scene, renderer-plain-text, …)
// Used by: ForgeHub web (loaded via Module Federation or iframe postMessage)

import type { ComponentType } from "react";

export type Snapshot = {
  id: string;
  commitSha: string;
  filePath: string;
  /** Raw file content, base64-encoded */
  content: string;
  /** Parsed intermediate representation (format-specific, from ingestFromUtf8Text) */
  entities: unknown;
};

/** 1. Single-file viewer — blob page, commit page */
export type SnapshotRendererProps = {
  snapshot: Snapshot;
};

/** 2. Diff view — compare page, PR diff */
export type DiffRendererProps = {
  baseSnapshot: Snapshot;
  targetSnapshot: Snapshot;
  diffResult: StructuredDiff;
  selectedChangeId?: string;
  onSelectChange: (id: string | null) => void;
};

/** 3. Conflict resolution UI — PR merge page */
export type MergeResolverProps = {
  baseSnapshot: Snapshot;
  oursSnapshot: Snapshot;
  theirsSnapshot: Snapshot;
  diffResult: StructuredDiff;
  onResolve: (entityId: string, field: string | null, side: "base" | "incoming") => void;
};

/** Props passed to optional extended full-page routes (ceiling) */
export type ExtendedRouteProps = {
  /** Caller's auth token for API calls back to ForgeHub */
  token: string;
  repo: { owner: string; name: string };
  currentRef: string;
  filePath: string;
};

export type ExtendedRoute = {
  /**
   * Path pattern relative to /:owner/:repo/
   * e.g. "3d-workspace/:ref/*filePath"
   * ForgeHub mounts it at /:owner/:repo/3d-workspace/...
   */
  path: string;
  component: ComponentType<ExtendedRouteProps>;
  /** Tab label shown in ForgeHub navigation */
  label?: string;
};

/**
 * The default export of every renderer bundle (renderer.js).
 * ForgeHub imports this to wire up renderers for a file extension.
 */
export interface FHRManifest {
  /** Must match ArtifactHandler.id and the handler name in manifest.toml */
  handlerId: string;
  extensions: string[];
  renderers: {
    snapshot: ComponentType<SnapshotRendererProps>;
    diff: ComponentType<DiffRendererProps>;
    mergeResolver: ComponentType<MergeResolverProps>;
  };
  /** Optional extended pages (ceiling). Omit if not needed. */
  routes?: ExtendedRoute[];
}

// ─── Renderer mount() contract (framework-agnostic bundle boundary) ───────────
// See SPEC-RENDERING.md §2. Where FHRManifest above is the *authoring* floor
// (React components), a built renderer.js bundle's default export is a
// RendererBundle: consumers (ForgeHub web, forge's local `--web` shell) call
// mount() without needing to know the bundle uses React — or anything — inside.
//
// The key difference from the FHRManifest floor: mount() consumes a
// StructuredDiff plus raw blob references, which are available in *every*
// compute tier (server, in-browser WASM, or local forge). The floor's
// Snapshot-shaped props assume server-side ingestion and stay ForgeHub-only.

export type RendererMode = "view" | "diff" | "merge";

/** A blob the consumer serves (same-origin URL) plus its byte size, so a host
 *  can show honest download costs before choosing a client-side tier. */
export type BlobRef = { url: string; size: number };

export type RendererBlobs = {
  base?: BlobRef;
  head?: BlobRef;
  ours?: BlobRef;
  theirs?: BlobRef;
};

/** Events a renderer emits back to its host. */
export type RendererEvent =
  | { type: "select"; changePath: string | null }
  /** A merge resolution the renderer produced, as a base64-encoded blob. */
  | { type: "resolved"; blobBase64: string }
  | { type: "error"; message: string };

export type MountProps = {
  mode: RendererMode;
  /** The semantic diff — produced by any tier; the renderer does not care which. */
  diff?: StructuredDiff;
  /** Raw blob references, served same-origin by the consumer. */
  blobs?: RendererBlobs;
  theme?: "light" | "dark";
  /**
   * The change the host wants selected, as a `DiffChange.path`. The inbound half
   * of the selection round trip whose outbound half is `RendererEvent.select`:
   * a host that highlights a row in its own UI pushes the same key back in here,
   * and the renderer highlights it (and, for a 3D renderer, frames it).
   * Undefined means "the host isn't driving selection"; null means "nothing".
   */
  selectedChangePath?: string | null;
  /**
   * What the handler that produced `diff` declares about itself — the same
   * `capabilities` object it exposes on `ArtifactHandler`, surfaced to the
   * renderer so a presentation can be chosen honestly.
   *
   * The one that matters today is `semanticCompare`. A renderer with several
   * presentations (SPEC-RENDERING §2e) opens on a structural one when semantic
   * comparison is meaningful for this format, and on a see-for-yourself one
   * (side-by-side) when it is not — instead of showing a structural diff that
   * says "everything changed" because the topology was regenerated.
   *
   * Optional, and a renderer MUST have a sensible default without it: a host
   * that doesn't plumb the handler's declaration through is not an error.
   */
  capabilities?: HandlerCapabilities;
  /** Host callback for renderer-emitted events (selection, resolution, error). */
  onEvent?: (e: RendererEvent) => void;
};

/** Live handle returned by mount(); lets the host push new props or tear down. */
export type RendererInstance = {
  update(props: MountProps): void;
  unmount(): void;
};

/** The default export of every built renderer.js bundle. */
export interface RendererBundle {
  /** Contract version this bundle targets. Bump on breaking mount() changes. */
  readonly fhrVersion: 1;
  /** Must match ArtifactHandler.id and the handler name in manifest.toml. */
  readonly handlerId: string;
  readonly extensions: string[];
  /** Content-hash build this bundle was released under (matches binary + wasm). */
  readonly build?: string;
  mount(el: HTMLElement, props: MountProps): RendererInstance;
}
