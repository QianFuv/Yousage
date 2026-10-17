/** Keep a mounted viewer on the relay's latest snapshot while it is on screen. */

/** How often a visible viewer asks the relay for a newer snapshot; unchanged ones cost a 304. */
export const FOLLOW_INTERVAL_MS = 30_000;

/** Whether the shown snapshot is being refreshed, could not be refreshed, or is up to date. */
export type FollowStatus =
  | { state: "current" }
  | { state: "refreshing" }
  | { state: "failed"; problem: string };

/** A running follower. */
export type SnapshotFollower = {
  /**
   * Fetch now, sharing a request already in flight; rejects when the fetch fails.
   *
   * @param isShown - Report progress and failure through the status, as on opening the app.
   */
  refetch: (isShown?: boolean) => Promise<void>;
  /** Stop timers and listeners and ignore fetches still in flight. */
  stop: () => void;
};

/**
 * Poll while the page is visible and fetch again as soon as it returns to the foreground.
 *
 * Returning to the foreground shows its progress; periodic polls stay quiet unless one
 * clears an earlier failure.
 *
 * @param fetchNewer - Download a newer snapshot; `null` when unchanged.
 * @param onNewer - Receives each newer snapshot while the follower runs.
 * @param onStatus - Receives each change of the shown status.
 * @returns The follower.
 */
export function followSnapshots<T>(
  fetchNewer: () => Promise<T | null>,
  onNewer: (next: T) => void,
  onStatus: (status: FollowStatus) => void = () => {},
): SnapshotFollower {
  let isRunning = true;
  let isFlightShown = false;
  let status: FollowStatus = { state: "current" };
  let flight: Promise<void> | null = null;
  /** Report a status change while running. */
  const report = (next: FollowStatus) => {
    if (!isRunning) return;
    status = next;
    onStatus(next);
  };
  const refetch = (isShown = false) => {
    if (isShown && !isFlightShown) {
      isFlightShown = true;
      report({ state: "refreshing" });
    }
    return (flight ??= (async () => {
      try {
        const next = await fetchNewer();
        if (next && isRunning) onNewer(next);
        if (isFlightShown || status.state === "failed")
          report({ state: "current" });
      } catch (cause) {
        if (isFlightShown)
          report({
            state: "failed",
            problem: cause instanceof Error ? cause.message : String(cause),
          });
        throw cause;
      } finally {
        flight = null;
        isFlightShown = false;
      }
    })());
  };
  /** Automatic attempts never reject; the next one retries. */
  const follow = (isShown: boolean) => {
    if (isRunning && document.visibilityState === "visible")
      void refetch(isShown).catch(() => {});
  };
  const poll = () => follow(false);
  const resume = () => follow(true);
  const timer = setInterval(poll, FOLLOW_INTERVAL_MS);
  document.addEventListener("visibilitychange", resume);
  return {
    refetch,
    stop() {
      isRunning = false;
      clearInterval(timer);
      document.removeEventListener("visibilitychange", resume);
    },
  };
}
