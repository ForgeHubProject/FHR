import type { ArtifactHandler, IngestInput, StructuredDiff, MergeResult } from "@fhr/types";

/**
 * TypeScript direct-import path.
 * ForgeHub API imports this module directly at build time.
 *
 * Use this path when:
 * - Your handler logic is straightforward TypeScript
 * - You don't need a separate native binary
 *
 * For other languages, see packages/example-handler-native/ instead.
 */
export const exampleHandler: ArtifactHandler = {
  id: "example",

  capabilities: {
    semanticCompare: true,
    semanticMerge: false,
  },

  matchesPath(path: string): boolean {
    return path.endsWith(".example");
  },

  async ingestFromUtf8Text(input: IngestInput): Promise<string> {
    // Parse the raw file into your canonical IR.
    // Return a JSON string — ForgeHub stores this as the snapshot's entity blob.
    return JSON.stringify({ raw: input.utf8Text });
  },

  async diff(base: Buffer, head: Buffer): Promise<StructuredDiff> {
    void base;
    void head;
    return {
      version: "1.0",
      format: "example",
      changes: [],
    };
  },

  // async merge(base: Buffer, ours: Buffer, theirs: Buffer): Promise<MergeResult> {
  //   return { blob: ours };
  // },
};
