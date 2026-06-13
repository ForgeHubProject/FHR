import type {
  FHRManifest,
  SnapshotRendererProps,
  DiffRendererProps,
  MergeResolverProps,
} from "@fhr/types";

/**
 * The three renderer components are the floor — every FHR must implement all three.
 * Replace the placeholder UI with your real format-specific viewer.
 */

function SnapshotRenderer({ snapshot }: SnapshotRendererProps) {
  return (
    <div>
      <pre>{snapshot.filePath}</pre>
      {/* Render the file here */}
    </div>
  );
}

function DiffRenderer({ diffResult, onSelectChange }: DiffRendererProps) {
  return (
    <div>
      {diffResult.changes.map((change) => (
        <div
          key={change.path}
          onClick={() => onSelectChange(change.path)}
        >
          [{change.kind}] {change.label ?? change.path}
        </div>
      ))}
    </div>
  );
}

function MergeResolver({ diffResult, onResolve }: MergeResolverProps) {
  return (
    <div>
      {diffResult.changes.map((change) => (
        <div key={change.path}>
          <span>{change.label ?? change.path}</span>
          <button onClick={() => onResolve(change.path, null, "base")}>Keep ours</button>
          <button onClick={() => onResolve(change.path, null, "incoming")}>Take incoming</button>
        </div>
      ))}
    </div>
  );
}

/**
 * Default export — ForgeHub imports this from renderer.js to wire up
 * all renderers for this format's extensions.
 */
const manifest: FHRManifest = {
  handlerId: "example",
  extensions: [".example"],
  renderers: {
    snapshot: SnapshotRenderer,
    diff: DiffRenderer,
    mergeResolver: MergeResolver,
  },
  // routes: [{ path: "example-workspace/:ref/*filePath", component: ExampleWorkspace, label: "Workspace" }],
};

export default manifest;
