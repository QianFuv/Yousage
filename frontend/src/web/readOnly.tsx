/** Read-only presentation flag for pages rendered from a relay snapshot. */
import { createContext, useContext } from "react";

/** True when pages render a snapshot that cannot be changed from this device. */
export const ReadOnlyContext = createContext(false);

/**
 * Whether the current page renders a read-only snapshot.
 *
 * @returns `true` inside the relay viewer, `false` in the desktop app.
 */
export function useIsReadOnly(): boolean {
  return useContext(ReadOnlyContext);
}
