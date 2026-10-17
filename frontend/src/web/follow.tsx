/** Keep a mounted viewer on the relay's latest snapshot while it is on screen. */

/** How often a visible viewer asks the relay for a newer snapshot; unchanged ones cost a 304. */
export const FOLLOW_INTERVAL_MS = 30_000;

/** A running follower. */
export type SnapshotFollower = {
  /** Fetch now, sharing a request already in flight; rejects when the fetch fails. */
  refetch: () => Promise<void>;
  /** Stop timers and listeners and ignore fetches still in flight. */
  stop: () => void;
};

/**
 * Poll while the page is visible and fetch again as soon as it returns to the foreground.
 *
 * @param fetchNewer - Download a newer snapshot; `null` when unchanged.
 * @param onNewer - Receives each newer snapshot while the follower runs.
 * @returns The follower.
 */
export function followSnapshots<T>(
  fetchNewer: () => Promise<T | null>,
  onNewer: (next: T) => void,
): SnapshotFollower {
  let isRunning = true;
  let flight: Promise<void> | null = null;
  const refetch = () =>
    (flight ??= (async () => {
      try {
        const next = await fetchNewer();
        if (next && isRunning) onNewer(next);
      } finally {
        flight = null;
      }
    })());
  /** Automatic attempts stay quiet; the next one retries. */
  const follow = () => {
    if (isRunning && document.visibilityState === "visible")
      void refetch().catch(() => {});
  };
  const timer = setInterval(follow, FOLLOW_INTERVAL_MS);
  document.addEventListener("visibilitychange", follow);
  return {
    refetch,
    stop() {
      isRunning = false;
      clearInterval(timer);
      document.removeEventListener("visibilitychange", follow);
    },
  };
}
