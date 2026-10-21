/** Tell the viewer when a shown snapshot's pages have actually rendered its data. */
import { createContext, useContext, useEffect } from "react";

/** How long a shown snapshot may take to render before it is given up as not shown. */
export const ACKNOWLEDGE_TIMEOUT_MS = 30_000;

/** What a commit marker reports to: the confirmation it renders with and where to report. */
type ViewerCommit = {
  /** Confirmation the viewer last rendered for. */
  token: number;
  /** Report that the marker committed while rendering the given confirmation. */
  onCommit: (token: number) => void;
};

/** Provided by the viewer; absent in the desktop app, where markers do nothing. */
export const VIEWER_COMMIT_CONTEXT = createContext<ViewerCommit | null>(null);

/**
 * Report each commit of the content it is placed after.
 *
 * Place it after the active page's content inside that content's `Suspense` boundary, so
 * it commits only once the content does rather than a loading fallback, and its effect
 * runs after the effects of that content.
 *
 * @returns Nothing visible.
 */
export function ViewerCommitMarker() {
  const commit = useContext(VIEWER_COMMIT_CONTEXT);
  useEffect(() => commit?.onCommit(commit.token), [commit]);
  return null;
}

/** Collects the reads, commits and failures that decide whether a shown snapshot rendered. */
export type RenderAcknowledger = {
  /**
   * Follow a report read; a failed read means its snapshot cannot be shown.
   *
   * @param generation - The shown snapshot the read answers from.
   * @param read - The read.
   * @returns The same read.
   */
  track: <T>(generation: number, read: Promise<T>) => Promise<T>;
  /** The confirmation token for the next render of the viewer. */
  token: () => number;
  /**
   * Record a commit marker's commit.
   *
   * @param token - The confirmation it rendered with.
   */
  committed: (token: number) => void;
  /**
   * Record that a shown snapshot's pages failed to render.
   *
   * @param generation - The shown snapshot whose rendering failed.
   */
  failed: (generation: number) => void;
  /**
   * Wait until the shown snapshot's active page commits with all its reads answered.
   *
   * @param generation - The shown snapshot.
   * @returns Resolves once it rendered; rejects on a failure, cancellation or the timeout.
   */
  wait: (generation: number) => Promise<void>;
  /** Give up the snapshot being waited for, as when another is shown or the viewer closes. */
  cancel: () => void;
};

/**
 * Acknowledge a shown snapshot only after its data rendered in the active page.
 *
 * Once every report read has been answered, the viewer renders again with a new
 * confirmation token. A marker committing with that token, with no read started since and
 * no failure of that snapshot, acknowledges it. Commits of loading fallbacks, of data from
 * before the reads were answered, or of another snapshot's pages acknowledge nothing.
 * Unanswered reads from an obsolete generation never delay the current snapshot.
 *
 * @param confirm - Render the viewer again with the current {@link RenderAcknowledger.token}.
 * @param timeoutMs - How long to wait before giving up.
 * @returns The acknowledger.
 */
export function createRenderAcknowledger(
  confirm: () => void,
  timeoutMs = ACKNOWLEDGE_TIMEOUT_MS,
): RenderAcknowledger {
  /** Count reads per snapshot so an obsolete request cannot block its replacement. */
  const reads = new Map<number, { pending: number; count: number }>();
  let token = 0;
  let confirmation: { token: number; readCount: number } | null = null;
  let confirmationTimer: ReturnType<typeof setTimeout> | null = null;
  const failures = new Set<number>();
  let pending: {
    generation: number;
    resolve: () => void;
    reject: (cause: Error) => void;
    timer: ReturnType<typeof setTimeout>;
  } | null = null;
  /**
   * Stop waiting.
   *
   * @param cause - Why the snapshot was not shown, or nothing when it was.
   */
  const settle = (cause?: Error) => {
    if (!pending) return;
    const { resolve, reject, timer } = pending;
    pending = null;
    confirmation = null;
    if (confirmationTimer !== null) clearTimeout(confirmationTimer);
    confirmationTimer = null;
    clearTimeout(timer);
    if (cause) reject(cause);
    else resolve();
  };
  /**
   * Remember a snapshot's failure and stop waiting for it.
   *
   * @param generation - The shown snapshot whose rendering failed.
   */
  const fail = (generation: number) => {
    failures.add(generation);
    if (pending?.generation === generation)
      settle(new Error("The snapshot failed to render."));
  };
  /** Render a confirmation once the reads answered and their updates were scheduled. */
  const scheduleConfirmation = () => {
    if (
      !pending ||
      (reads.get(pending.generation)?.pending ?? 0) > 0 ||
      confirmationTimer !== null
    )
      return;
    confirmationTimer = setTimeout(() => {
      confirmationTimer = null;
      if (!pending) return;
      const current = reads.get(pending.generation);
      if ((current?.pending ?? 0) > 0) return;
      confirmation = { token: ++token, readCount: current?.count ?? 0 };
      confirm();
    });
  };
  return {
    /** Track only this snapshot's reads; late completion cannot alter its replacement. */
    track(generation, read) {
      const current = reads.get(generation) ?? { pending: 0, count: 0 };
      reads.set(generation, current);
      current.pending++;
      current.count++;
      return read.then(
        (value) => {
          current.pending--;
          if (pending?.generation === generation) scheduleConfirmation();
          return value;
        },
        (cause: unknown) => {
          current.pending--;
          fail(generation);
          if (pending?.generation === generation) scheduleConfirmation();
          throw cause;
        },
      );
    },
    token: () => token,
    /** Accept a confirmation only if its own snapshot started no further report reads. */
    committed(committedToken) {
      if (
        pending &&
        (reads.get(pending.generation)?.pending ?? 0) === 0 &&
        confirmation?.token === committedToken &&
        confirmation.readCount === (reads.get(pending.generation)?.count ?? 0)
      )
        settle();
    },
    failed: fail,
    /** Supersede an earlier wait and discard its read counters before confirming this snapshot. */
    wait(generation) {
      settle(new Error("Another snapshot is shown."));
      for (const previous of reads.keys())
        if (previous < generation) reads.delete(previous);
      for (const failed of failures)
        if (failed < generation) failures.delete(failed);
      if (failures.has(generation))
        return Promise.reject(new Error("The snapshot failed to render."));
      return new Promise<void>((resolve, reject) => {
        pending = {
          generation,
          resolve,
          reject,
          timer: setTimeout(
            () => settle(new Error("The snapshot did not render in time.")),
            timeoutMs,
          ),
        };
        scheduleConfirmation();
      });
    },
    cancel: () => settle(new Error("The snapshot is no longer shown.")),
  };
}
