// Budgets the 3D view enforces on itself. Pure numbers + predicates so the
// gating is testable and stated in one place instead of inline magic numbers.

/**
 * Largest base blob we'll download and keep resident for the ghost overlay and
 * the A/B blink. Both features hold a *second* full model in memory, so the cap
 * is about the reviewer's machine, not the network: past this size the diff
 * paint on the head model is still shown, and a banner says what was skipped.
 * `BlobRef.size` lets us decide before spending the download.
 */
export const GHOST_BASE_MAX_BYTES = 32 * 1024 * 1024;

/** Human-readable megabytes for banners: 33554432 → "32 MB". */
export function formatMb(bytes: number): string {
  const mb = bytes / (1024 * 1024);
  return `${mb >= 10 ? Math.round(mb) : Math.round(mb * 10) / 10} MB`;
}

/**
 * May we load the base model alongside the head one? Unknown/absent sizes are
 * allowed (a host that doesn't report size shouldn't lose the feature); zero and
 * negative sizes mean "no bytes there".
 */
export function allowGhostBase(size: number | undefined, cap: number = GHOST_BASE_MAX_BYTES): boolean {
  if (size === undefined) return true;
  if (!Number.isFinite(size) || size <= 0) return false;
  return size <= cap;
}

/** The banner shown when the base model was skipped for being too large. */
export function ghostBaseSkippedMessage(size: number, cap: number = GHOST_BASE_MAX_BYTES): string {
  return (
    `The previous version is ${formatMb(size)} (over the ${formatMb(cap)} the 3D view keeps in memory), ` +
    `so the ghost overlay, the A/B blink and removed-part ghosts are turned off. ` +
    `Changes on the current version are still painted below.`
  );
}
