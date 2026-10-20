/** Back closes a viewer dialog through its own entry, and closing one never traverses someone else's history. */
import { describe, expect, it, vi } from "vitest";
import { createDialogHistory, type HistoryScope } from "./dialogHistory";

/** A window whose traversals land later, as in a browser, and whose entries can be inspected. */
interface FakeWindow extends HistoryScope {
  /** Every entry's state, oldest first. */
  readonly entries: unknown[];
  /** Index of the current entry. */
  readonly index: number;
  /** How many times `history.back` was called. */
  readonly backCalls: number;
  /** Press the system Back button: traverse back one entry, later. */
  pressBack(): void;
  /** Land every queued traversal, firing `popstate` for each. */
  land(): void;
}

/**
 * Build a window with asynchronous history traversal.
 *
 * @param initialState - State of the first entry.
 * @returns The window.
 */
function fakeWindow(initialState: unknown = null): FakeWindow {
  const entries: unknown[] = [initialState];
  const listeners = new Set<() => void>();
  let queued: (() => void)[] = [];
  let index = 0;
  let backCalls = 0;
  const traverseBack = () => {
    queued = [
      ...queued,
      () => {
        if (index === 0) return;
        index--;
        for (const listener of [...listeners]) listener();
      },
    ];
  };
  return {
    entries,
    get index() {
      return index;
    },
    get backCalls() {
      return backCalls;
    },
    history: {
      get state() {
        return entries[index];
      },
      pushState(state: unknown) {
        entries.splice(index + 1, entries.length, state);
        index++;
      },
      back() {
        backCalls++;
        traverseBack();
      },
    },
    addEventListener(_type, listener) {
      listeners.add(listener);
    },
    removeEventListener(_type, listener) {
      listeners.delete(listener);
    },
    pressBack: traverseBack,
    land() {
      while (queued.length > 0) {
        const [traversal, ...rest] = queued;
        queued = rest;
        traversal();
      }
    },
  };
}

/**
 * The dialog entry identity of a history state.
 *
 * @param state - A history state.
 * @returns The identity, if the state marks a dialog entry.
 */
function identityOf(state: unknown): unknown {
  return (state as { yousageDialogEntry?: unknown } | null)?.yousageDialogEntry;
}

describe("dialog history entry", () => {
  it("adds one marked entry that keeps the existing state, and Back closes the dialog without another traversal", () => {
    const scope = fakeWindow({ route: "usage" });
    const dialogs = createDialogHistory(scope);
    const onBack = vi.fn();
    const other = vi.fn();
    dialogs.listen(other);
    const close = dialogs.open(onBack);

    expect(scope.entries).toHaveLength(2);
    expect(scope.entries[0]).toEqual({ route: "usage" });
    expect(scope.history.state).toMatchObject({ route: "usage" });
    expect(identityOf(scope.history.state)).toEqual(expect.any(String));

    scope.pressBack();
    scope.land();
    expect(onBack).toHaveBeenCalledTimes(1);
    expect(other).not.toHaveBeenCalled();

    close();
    expect(scope.backCalls).toBe(0);
    expect(scope.index).toBe(0);
  });

  it("removes its own entry exactly once when closed by the close button, backdrop, Escape or unmount", () => {
    const scope = fakeWindow();
    const dialogs = createDialogHistory(scope);
    const onBack = vi.fn();
    const other = vi.fn();
    dialogs.listen(other);
    const close = dialogs.open(onBack);

    close();
    close();
    scope.land();

    expect(scope.backCalls).toBe(1);
    expect(scope.index).toBe(0);
    expect(onBack).not.toHaveBeenCalled();
    expect(other).not.toHaveBeenCalled();
  });

  it("leaves history alone when another entry was added on top of the dialog", () => {
    const scope = fakeWindow();
    const dialogs = createDialogHistory(scope);
    const close = dialogs.open(() => {});
    scope.history.pushState(null, "");

    close();
    scope.land();

    expect(scope.backCalls).toBe(0);
    expect(scope.index).toBe(2);
  });

  it("adds a newly opened model's entry only after the closed one's traversal lands, with its own identity", () => {
    const scope = fakeWindow();
    const dialogs = createDialogHistory(scope);
    const firstBack = vi.fn();
    const secondBack = vi.fn();
    const closeFirst = dialogs.open(firstBack);
    const firstIdentity = identityOf(scope.history.state);

    closeFirst();
    dialogs.open(secondBack);
    expect(scope.entries).toHaveLength(2);
    scope.land();

    expect(scope.entries).toHaveLength(2);
    expect(scope.index).toBe(1);
    const secondIdentity = identityOf(scope.history.state);
    expect(secondIdentity).toEqual(expect.any(String));
    expect(secondIdentity).not.toBe(firstIdentity);
    expect(secondBack).not.toHaveBeenCalled();

    scope.pressBack();
    scope.land();
    expect(secondBack).toHaveBeenCalledTimes(1);
    expect(firstBack).not.toHaveBeenCalled();
    expect(scope.index).toBe(0);
  });

  it("never adds the entry of a dialog that closed while waiting for an earlier traversal", () => {
    const scope = fakeWindow();
    const dialogs = createDialogHistory(scope);
    dialogs.open(() => {})();
    const closeWaiting = dialogs.open(() => {});

    closeWaiting();
    scope.land();

    expect(scope.entries).toHaveLength(2);
    expect(scope.index).toBe(0);
    expect(scope.backCalls).toBe(1);
  });

  it("passes a Back after the dialog has closed on to other history users", () => {
    const scope = fakeWindow();
    scope.history.pushState({ page: "earlier" }, "");
    const dialogs = createDialogHistory(scope);
    const onBack = vi.fn();
    const other = vi.fn();
    dialogs.listen(other);
    dialogs.open(onBack)();
    scope.land();

    scope.pressBack();
    scope.land();

    expect(onBack).not.toHaveBeenCalled();
    expect(other).toHaveBeenCalledTimes(1);
    expect(scope.index).toBe(0);
  });

  it("stops notifying a listener once it stops listening", () => {
    const scope = fakeWindow();
    scope.history.pushState(null, "");
    const dialogs = createDialogHistory(scope);
    const other = vi.fn();
    dialogs.listen(other)();

    scope.pressBack();
    scope.land();

    expect(other).not.toHaveBeenCalled();
  });
});

/**
 * The Android change-link flow, using history exactly as `android.tsx` does.
 *
 * @param scope - The window.
 * @returns The flow's screen and actions.
 */
function changeLinkFlow(scope: FakeWindow) {
  const dialogs = createDialogHistory(scope);
  const flow = {
    dialogs,
    screen: "viewer" as "viewer" | "pairing",
    isChangingLink: false,
    reopened: 0,
    changeLink() {
      dialogs.whenSettled(() => {
        if (!flow.isChangingLink) {
          scope.history.pushState(null, "");
          flow.isChangingLink = true;
        }
        flow.screen = "pairing";
      });
    },
  };
  dialogs.listen(() => {
    if (!flow.isChangingLink) return;
    flow.isChangingLink = false;
    flow.screen = "viewer";
    flow.reopened++;
  });
  return flow;
}

describe("dialog history with the change-link screen", () => {
  it("keeps the change-link screen when a closed dialog's traversal lands after Change link", () => {
    const scope = fakeWindow();
    const flow = changeLinkFlow(scope);
    const onBack = vi.fn();
    const close = flow.dialogs.open(onBack);

    close();
    flow.changeLink();
    scope.land();

    expect(flow.screen).toBe("pairing");
    expect(flow.reopened).toBe(0);
    expect(onBack).not.toHaveBeenCalled();
    expect(scope.entries).toEqual([null, null]);
    expect(scope.index).toBe(1);

    scope.pressBack();
    scope.land();
    expect(flow.screen).toBe("viewer");
    expect(flow.reopened).toBe(1);
    expect(scope.index).toBe(0);

    scope.pressBack();
    scope.land();
    expect(flow.reopened).toBe(1);
    expect(scope.index).toBe(0);
  });

  it("keeps the change-link screen when the viewer unmounted its dialog just before Use another link", () => {
    const scope = fakeWindow();
    const flow = changeLinkFlow(scope);
    const close = flow.dialogs.open(() => {});
    scope.pressBack();
    scope.land();
    const reopen = flow.dialogs.open(() => {});

    reopen();
    flow.changeLink();
    flow.changeLink();
    scope.land();

    expect(flow.screen).toBe("pairing");
    expect(flow.reopened).toBe(0);
    expect(scope.entries).toEqual([null, null]);
    close();
    expect(scope.backCalls).toBe(1);
  });

  it("opens the change-link screen at once when no dialog traversal is pending", () => {
    const scope = fakeWindow();
    const flow = changeLinkFlow(scope);

    flow.changeLink();

    expect(flow.screen).toBe("pairing");
    expect(scope.index).toBe(1);
  });

  it("does not hand a dialog's Back to the change-link listener", () => {
    const scope = fakeWindow();
    const flow = changeLinkFlow(scope);
    flow.isChangingLink = true;
    const onBack = vi.fn();
    flow.dialogs.open(onBack);

    scope.pressBack();
    scope.land();

    expect(onBack).toHaveBeenCalledTimes(1);
    expect(flow.reopened).toBe(0);
  });
});
