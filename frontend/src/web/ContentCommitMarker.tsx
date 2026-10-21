/** Acknowledge actionable error content independently of accepted report rendering. */
import { useEffect } from "react";

/**
 * Notify after the error content containing this marker commits.
 *
 * @param props - A callback scoped to the current rendering generation.
 * @returns No visible element.
 */
export function ContentCommitMarker({ onCommit }: { onCommit?: () => void }) {
  useEffect(() => onCommit?.(), [onCommit]);
  return null;
}
