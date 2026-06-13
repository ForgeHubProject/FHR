// ─── Wire format (mirrors forge/internal/handler/handler.go) ─────────────────

export type ChangeKind = "added" | "removed" | "modified";

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
