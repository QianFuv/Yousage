/** Carry display preferences changed on the desktop into a viewer without undoing local choices. */

/** Event the viewer listens to for display preferences the desktop changed. */
export const DISPLAY_CHANGES_EVENT = "display-changes";

/**
 * List the display preferences whose value differs between two snapshots.
 *
 * @param previous - Display preferences in the snapshot shown so far.
 * @param next - Display preferences in the newer snapshot.
 * @returns Only the changed preferences with their new values; empty when nothing changed.
 */
export function displayChanges(
  previous: Record<string, unknown>,
  next: Record<string, unknown>,
): Record<string, unknown> {
  return Object.fromEntries(
    Object.entries(next).filter(
      ([name, value]) =>
        JSON.stringify(value) !== JSON.stringify(previous[name]),
    ),
  );
}
