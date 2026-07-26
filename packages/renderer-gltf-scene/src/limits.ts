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
 * May we load the base model alongside the head one?
 *
 * This refuses one thing only: a size we *know* to be over the cap. Every other
 * value — absent, zero, negative, NaN — means the host told us nothing useful,
 * and the answer is to try the fetch and report whatever actually happens.
 *
 * Treating zero as "no bytes there" is the tempting reading and it was wrong.
 * A host that simply hasn't filled the field in sends zero, which is
 * indistinguishable from a genuinely empty blob, so the inference silently
 * disabled the ghost overlay, the A/B blink, removed-part ghosts and old-pose
 * motion vectors against a host that was serving both versions perfectly well.
 * Worse, refusing here on a zero size produced the *only* skip with no banner,
 * because the banner names a size and there was no size worth naming — so the
 * one case that most needed an explanation was the one case that gave none.
 *
 * If the bytes really are absent, the fetch says so and that path already
 * reports it honestly. Guessing from a sentinel buys nothing and costs the whole
 * feature.
 */
export function allowGhostBase(size: number | undefined, cap: number = GHOST_BASE_MAX_BYTES): boolean {
  if (size === undefined || !Number.isFinite(size) || size <= 0) return true;
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
