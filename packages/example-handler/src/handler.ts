import type { ArtifactHandler, IngestInput, StructuredDiff, MergeResult } from "@fhr/types";

/**
 * Replace "example" with your format name throughout.
 * This file is the backend contract — used by ForgeHub API and the Forge CLI
 * subprocess protocol.
 */
export const exampleHandler: ArtifactHandler = {
  id: "example",

  capabilities: {
    semanticCompare: true,
    semanticMerge: false, // set true and implement merge() when ready
  },

  matchesPath(path: string): boolean {
    return path.endsWith(".example");
  },

  async ingestFromUtf8Text(input: IngestInput): Promise<string> {
    // Parse the raw file into your canonical intermediate representation.
    // Return a JSON string — ForgeHub stores this as the snapshot's entity blob.
    return JSON.stringify({ raw: input.utf8Text });
  },

  async diff(base: Buffer, head: Buffer): Promise<StructuredDiff> {
    // Compare base and head blobs at the semantic level.
    // Return a StructuredDiff describing what changed.
    //
    // Example: compare field by field, emit one DiffChange per changed property.
    void base;
    void head;
    return {
      version: "1.0",
      format: "example",
      changes: [
        // { path: "field.name", kind: "modified", before: "old", after: "new" }
      ],
    };
  },

  // Uncomment and implement when semanticMerge = true:
  // async merge(base: Buffer, ours: Buffer, theirs: Buffer): Promise<MergeResult> {
  //   return { blob: ours, conflicts: [] };
  // },
};
