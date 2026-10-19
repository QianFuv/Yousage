/** Keep the last good snapshot until a newer one has been shown and, on a device, saved. */

/** A downloaded snapshot that is kept only once the viewer has shown it. */
export type SnapshotCandidate<T> = {
  /** The validated snapshot. */
  payload: T;
  /** Entity tag the relay served it with; empty when unknown. */
  etag: string;
  /** Its content, so a snapshot the viewer could not show is recognized when served again. */
  identity: string;
  /** Save it on the device as the snapshot to open next time; absent when nothing is saved. */
  commit?: () => Promise<void>;
};

/** The snapshot the viewer shows and keeps, with the entity tag to refresh it by. */
export type AcceptedSnapshot<T> = Omit<SnapshotCandidate<T>, "commit">;

/** Why a downloaded snapshot was not kept. */
export type RejectionReason = "render" | "save";

/** A refresh that reached a snapshot this viewer will not keep; reported even by quiet refreshes. */
export class SnapshotRejectedError extends Error {}

/** What a session needs from the viewer and its host. */
export type SnapshotSessionOptions<T> = {
  /** The snapshot kept already, or `null` when the first one is still a candidate. */
  accepted: AcceptedSnapshot<T> | null;
  /** Download a snapshot newer than the entity tag; `null` when unchanged. */
  download: (etag: string) => Promise<SnapshotCandidate<T> | null>;
  /** Show a snapshot; rejects when the pages cannot render it. */
  show: (payload: T) => Promise<void>;
  /** Show the kept snapshot again after a candidate failed. */
  restore: (payload: T) => void;
  /** Explain to the viewer why a candidate was not kept. */
  describe: (reason: RejectionReason) => string;
};

/** The accepted snapshot and the steps that replace it. */
export type SnapshotSession<T> = {
  /** The snapshot kept so far, or `null` before the first one is kept. */
  accepted: () => AcceptedSnapshot<T> | null;
  /**
   * Download a newer snapshot by the accepted entity tag.
   *
   * @param isRetry - The person asked to try again, so a snapshot rejected earlier is shown again.
   * @returns The candidate, or `null` when unchanged.
   */
  fetch: (isRetry?: boolean) => Promise<SnapshotCandidate<T> | null>;
  /**
   * Show a candidate and save it; on failure show the accepted snapshot again.
   *
   * @param candidate - A candidate from {@link SnapshotSession.fetch}, or the first snapshot.
   */
  accept: (candidate: SnapshotCandidate<T>) => Promise<void>;
};

/**
 * Track the accepted snapshot so its entity tag advances only after a newer one is shown
 * and saved, and a snapshot that failed is not shown again by automatic refreshes.
 *
 * @param options - The accepted snapshot, download, display and messages.
 * @returns The session.
 */
export function createSnapshotSession<T>(
  options: SnapshotSessionOptions<T>,
): SnapshotSession<T> {
  let accepted = options.accepted;
  let rejected: { identity: string; reason: RejectionReason } | null = null;
  return {
    accepted: () => accepted,
    async fetch(isRetry = false) {
      const candidate = await options.download(accepted?.etag ?? "");
      if (candidate && !isRetry && candidate.identity === rejected?.identity)
        throw new SnapshotRejectedError(options.describe(rejected.reason));
      return candidate;
    },
    async accept(candidate) {
      const isShown = candidate.identity === accepted?.identity;
      let reason: RejectionReason = "render";
      try {
        if (!isShown) await options.show(candidate.payload);
        reason = "save";
        await candidate.commit?.();
      } catch {
        rejected = { identity: candidate.identity, reason };
        if (!isShown && accepted) options.restore(accepted.payload);
        throw new SnapshotRejectedError(options.describe(reason));
      }
      accepted = {
        payload: candidate.payload,
        etag: candidate.etag,
        identity: candidate.identity,
      };
      rejected = null;
    },
  };
}
