/** The data controller follows the desktop scheduler instead of starting per-page refreshes. */
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactElement } from "react";
import * as nativeApi from "../../bindings/yousage/internal/desktop/app";
import { DataProvider, type RefreshState } from "./controller";

const harness = vi.hoisted(() => ({
  listeners: new Map<string, (event: { data: unknown }) => void>(),
  setters: [] as { initial: unknown; set: ReturnType<typeof vi.fn> }[],
}));

vi.mock("react", () => ({
  createContext: () => ({ Provider: "provider" }),
  useContext: () => null,
  useCallback: (callback: unknown) => callback,
  useEffect: (effect: () => void) => effect(),
  useRef: (value: unknown) => ({ current: value }),
  useState: (initial: unknown) => {
    const set = vi.fn();
    harness.setters.push({ initial, set });
    return [initial, set];
  },
}));
vi.mock("@wailsio/runtime", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@wailsio/runtime")>()),
  Events: {
    On: (name: string, listener: (event: { data: unknown }) => void) => {
      harness.listeners.set(name, listener);
      return () => harness.listeners.delete(name);
    },
  },
}));
vi.mock("../notify", () => ({ notifyError: vi.fn() }));
vi.mock("../../bindings/yousage/internal/desktop/app", () => ({
  GetUsage: vi.fn(),
  GetRefreshState: vi.fn(),
  RequestRefresh: vi.fn(),
  RefreshUsage: vi.fn(),
  RefreshLimits: vi.fn(),
  SaveDisplay: vi.fn(),
}));

/** Mount the provider and return its context value. */
function mount() {
  const element = DataProvider({
    initial: {
      revision: 1,
      value: { windowDays: 7, shouldAutoRefresh: true },
    } as never,
    children: null,
  }) as ReactElement<{ value: Record<string, unknown> }>;
  return element.props.value as {
    refresh: () => Promise<void>;
    refreshState: RefreshState;
  };
}

/** The setter of the state that holds the published refresh state. */
function refreshStateSetter() {
  const found = harness.setters.find(
    (entry) =>
      typeof entry.initial === "object" &&
      entry.initial !== null &&
      "lastCompletedAt" in entry.initial,
  );
  if (!found) throw new Error("refresh state is not held by the controller");
  return found.set;
}

beforeEach(() => {
  vi.clearAllMocks();
  harness.listeners.clear();
  harness.setters = [];
  vi.mocked(nativeApi.GetUsage).mockResolvedValue({} as never);
  vi.mocked(nativeApi.GetRefreshState).mockResolvedValue({
    isRefreshing: false,
    lastCompletedAt: "2026-10-30T19:42:49Z",
    lastError: "",
  } as never);
  vi.mocked(nativeApi.RequestRefresh).mockResolvedValue(undefined as never);
});

describe("DataProvider", () => {
  it("leaves automatic refresh to the desktop scheduler", async () => {
    mount();
    await Promise.resolve();
    expect(harness.listeners.has("window-visibility")).toBe(false);
    expect(nativeApi.RefreshUsage).not.toHaveBeenCalled();
    expect(nativeApi.RefreshLimits).not.toHaveBeenCalled();
    expect(nativeApi.GetUsage).toHaveBeenCalledTimes(1);
  });

  it("reloads the shared snapshot whenever the scheduler invalidates usage", async () => {
    mount();
    await vi.waitFor(() => expect(nativeApi.GetUsage).toHaveBeenCalledTimes(1));
    await Promise.resolve();
    harness.listeners.get("usage-invalidated")?.({ data: null });
    await vi.waitFor(() => expect(nativeApi.GetUsage).toHaveBeenCalledTimes(2));
  });

  it("follows published refresh progress and lets it win over the initial read", async () => {
    let resolveInitial: (value: RefreshState) => void = () => {};
    vi.mocked(nativeApi.GetRefreshState).mockReturnValue(
      new Promise((resolve) => {
        resolveInitial = resolve;
      }) as never,
    );
    mount();
    const set = refreshStateSetter();
    const published = {
      isRefreshing: true,
      lastCompletedAt: "",
      lastError: "",
    };
    harness.listeners.get("refresh-state")?.({ data: published });
    resolveInitial({
      isRefreshing: false,
      lastCompletedAt: "2026-10-30T19:12:48Z",
      lastError: "",
    });
    await Promise.resolve();
    await Promise.resolve();
    expect(set.mock.calls).toEqual([[published]]);
  });

  it("applies the initial state when nothing was published yet", async () => {
    mount();
    const set = refreshStateSetter();
    await vi.waitFor(() =>
      expect(set).toHaveBeenCalledWith({
        isRefreshing: false,
        lastCompletedAt: "2026-10-30T19:42:49Z",
        lastError: "",
      }),
    );
  });

  it("turns the refresh shortcut into one global request", async () => {
    await mount().refresh();
    expect(nativeApi.RequestRefresh).toHaveBeenCalledTimes(1);
    expect(nativeApi.RefreshUsage).not.toHaveBeenCalled();
    expect(nativeApi.RefreshLimits).not.toHaveBeenCalled();
  });
});
