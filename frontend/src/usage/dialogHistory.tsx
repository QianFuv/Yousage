/**
 * History entries owned by read-only viewer dialogs, so Back closes the open
 * dialog first and a closing dialog's own traversal never reaches other
 * history listeners such as the Android change-link screen.
 */
import { useEffect, useEffectEvent } from "react";

/** Key of the history state that marks an entry as a dialog's own. */
const ENTRY_KEY = "yousageDialogEntry";

/** The parts of a window that dialog history uses. */
export interface HistoryScope {
  readonly history: Pick<History, "state" | "pushState" | "back">;
  addEventListener(type: "popstate", listener: () => void): void;
  removeEventListener(type: "popstate", listener: () => void): void;
}

/** History coordination shared by viewer dialogs and other history users. */
export interface DialogHistory {
  /**
   * Add a history entry for an opened dialog.
   *
   * @param onBack - Called once when Back leaves the dialog's entry.
   * @returns Closes the entry; it removes its own entry only while it is current.
   */
  open(onBack: () => void): () => void;
  /**
   * Listen to history traversals that no dialog started or consumed.
   *
   * @param listener - Called for each such traversal.
   * @returns Stops listening.
   */
  listen(listener: () => void): () => void;
  /**
   * Run a history change once every traversal started by a closing dialog has
   * landed, so the change cannot be undone by that traversal.
   *
   * @param action - The history change; runs at once when nothing is pending.
   */
  whenSettled(action: () => void): void;
}

/** One dialog's entry, pushed once earlier traversals have landed. */
interface OpenEntry {
  readonly onBack: () => void;
  isPushed: boolean;
}

/**
 * The dialog entry identity in a history state.
 *
 * @param state - A history state.
 * @returns The entry identity, or `null` for other entries.
 */
function entryIdentity(state: unknown): string | null {
  if (typeof state !== "object" || state === null) return null;
  const identity = (state as Record<string, unknown>)[ENTRY_KEY];
  return typeof identity === "string" ? identity : null;
}

/**
 * Coordinate dialog history entries in one window.
 *
 * @param scope - The window whose history is used.
 * @returns The coordinator; it listens to the window for its lifetime.
 */
export function createDialogHistory(scope: HistoryScope): DialogHistory {
  const loadIdentity = `${Date.now().toString(36)}.${Math.random().toString(36).slice(2)}`;
  const entries = new Map<string, OpenEntry>();
  const listeners = new Set<() => void>();
  let deferred: (() => void)[] = [];
  let entryCount = 0;
  let pendingTraversals = 0;

  const flush = () => {
    while (pendingTraversals === 0 && deferred.length > 0) {
      const [action, ...rest] = deferred;
      deferred = rest;
      action();
    }
  };

  const whenSettled = (action: () => void) => {
    deferred = [...deferred, action];
    flush();
  };

  scope.addEventListener("popstate", () => {
    if (pendingTraversals > 0) {
      pendingTraversals--;
      flush();
      return;
    }
    const current = entryIdentity(scope.history.state);
    const left = [...entries].filter(
      ([identity, entry]) => entry.isPushed && identity !== current,
    );
    for (const [identity, entry] of left) {
      entries.delete(identity);
      entry.onBack();
    }
    if (left.length > 0) return;
    for (const listener of [...listeners]) listener();
  });

  return {
    open(onBack) {
      const identity = `${loadIdentity}:${++entryCount}`;
      entries.set(identity, { onBack, isPushed: false });
      whenSettled(() => {
        const entry = entries.get(identity);
        if (entry === undefined) return;
        const state = scope.history.state as unknown;
        scope.history.pushState(
          {
            ...(typeof state === "object" && state !== null ? state : {}),
            [ENTRY_KEY]: identity,
          },
          "",
        );
        entry.isPushed = true;
      });
      return () => {
        const entry = entries.get(identity);
        if (entry === undefined) return;
        entries.delete(identity);
        if (!entry.isPushed) return;
        if (entryIdentity(scope.history.state) !== identity) return;
        pendingTraversals++;
        scope.history.back();
      };
    },
    listen(listener) {
      const owned = () => listener();
      listeners.add(owned);
      return () => {
        listeners.delete(owned);
      };
    },
    whenSettled,
  };
}

/** The coordinator of this window, created on first use. */
let windowHistory: DialogHistory | null = null;

/**
 * Dialog history of the current window.
 *
 * @returns The window's single coordinator.
 */
export function viewerHistory(): DialogHistory {
  windowHistory ??= createDialogHistory(window);
  return windowHistory;
}

/**
 * Give an open dialog its own history entry, so Back closes it.
 *
 * @param isEnabled - Whether the dialog uses history; the desktop app does not.
 * @param onBack - Closes the dialog after Back.
 */
export function useDialogHistoryEntry(
  isEnabled: boolean,
  onBack: () => void,
): void {
  const handleBack = useEffectEvent(onBack);
  useEffect(() => {
    if (!isEnabled) return;
    return viewerHistory().open(() => handleBack());
  }, [isEnabled]);
}
