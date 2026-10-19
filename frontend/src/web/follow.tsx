/** Keep a mounted viewer on the relay's latest snapshot while it is on screen. */
import { SnapshotRejectedError } from "./snapshotSession";

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
   * A retry that joins a request which is not one is tried once more as a retry when that
   * request fails, before the shared request settles.
   *
   * @param isShown - Report progress and failure through the status, as on opening the app.
   * @param isRetry - The person asked to try again, so a snapshot rejected earlier is tried again.
   */
  refetch: (isShown?: boolean, isRetry?: boolean) => Promise<void>;
  /** Stop timers and listeners and ignore fetches still in flight. */
  stop: () => void;
};

/**
 * Poll while the page is visible and fetch again as soon as it returns to the foreground.
 *
 * Returning to the foreground shows its progress; periodic polls stay quiet unless one
 * clears an earlier failure or reaches a snapshot the viewer rejected.
 *
 * @param fetchNewer - Download a newer snapshot; `null` when unchanged. Receives whether
 * the person asked to try again.
 * @param onNewer - Applies each newer snapshot while the follower runs; a rejection fails the fetch.
 * @param onStatus - Receives each change of the shown status.
 * @returns The follower.
 */
export function followSnapshots<T>(
  fetchNewer: (isRetry: boolean) => Promise<T | null>,
  onNewer: (next: T) => void | Promise<void>,
  onStatus: (status: FollowStatus) => void = () => {},
): SnapshotFollower {
  let isRunning = true;
  let isFlightShown = false;
  /** Whether the attempt in flight is a retry. */
  let isFlightRetry = false;
  /** A retry joined an attempt that is not one, so a failed attempt is tried again as a retry. */
  let shouldRetry = false;
  let status: FollowStatus = { state: "current" };
  let flight: Promise<void> | null = null;
  /** Report a status change while running. */
  const report = (next: FollowStatus) => {
    if (!isRunning) return;
    status = next;
    onStatus(next);
  };
  /**
   * Download and apply once, then once more as a retry if one joined a failed attempt.
   *
   * @param isRetry - Whether the first attempt is a retry.
   */
  const attempt = async (isRetry: boolean) => {
    isFlightRetry = isRetry;
    try {
      const next = await fetchNewer(isRetry);
      if (next && isRunning) await onNewer(next);
    } catch (cause) {
      if (!shouldRetry || !isRunning) throw cause;
      shouldRetry = false;
      return attempt(true);
    }
  };
  const refetch = (isShown = false, isRetry = false) => {
    if (isShown && !isFlightShown) {
      isFlightShown = true;
      report({ state: "refreshing" });
    }
    if (flight && isRetry && !isFlightRetry) shouldRetry = true;
    return (flight ??= (async () => {
      try {
        await attempt(isRetry);
        if (isFlightShown || status.state === "failed")
          report({ state: "current" });
      } catch (cause) {
        if (isFlightShown || cause instanceof SnapshotRejectedError)
          report({
            state: "failed",
            problem: cause instanceof Error ? cause.message : String(cause),
          });
        throw cause;
      } finally {
        flight = null;
        isFlightShown = false;
        isFlightRetry = false;
        shouldRetry = false;
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
