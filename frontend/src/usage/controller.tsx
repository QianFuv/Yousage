/** Root data controller keeps aggregates and display choices independent of routes and drafts. */
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { Events } from "@wailsio/runtime";
import * as nativeApi from "../../bindings/yousage/app";
import { SavedDisplay } from "../../bindings/yousage/internal/preferences/models";
import { Request } from "../../bindings/yousage/internal/refresh/models";
import type { UsageSnapshot } from "./data";

type DataContextValue = {
  setUsageActive: (isActive: boolean) => void;
  snapshot: UsageSnapshot | null;
  display: SavedDisplay;
  selected: Set<string> | null;
  setSelected: (selected: Set<string> | null) => void;
  error: string;
  isBusy: boolean;
  reload: () => Promise<void>;
  patchDisplay: (patch: Partial<SavedDisplay["value"]>) => Promise<void>;
  refresh: (
    isManual: boolean,
    isUsagePage: boolean,
    afterPending?: boolean,
  ) => Promise<void>;
};
const DATA_CONTEXT = createContext<DataContextValue | null>(null);

/** Share saved view state and last-good summaries without starting periodic timers. */
export function DataProvider({
  initial,
  children,
}: {
  initial: SavedDisplay;
  children: ReactNode;
}) {
  const [display, setDisplay] = useState(initial);
  const displayRef = useRef(display);
  displayRef.current = display;
  const [snapshot, setSnapshot] = useState<UsageSnapshot | null>(null);
  const [selected, setSelected] = useState<Set<string> | null>(null);
  const [error, setError] = useState("");
  const [isBusy, setIsBusy] = useState(false);
  const isRefreshing = useRef(false);
  const isUsageActive = useRef(true);
  const setUsageActive = useCallback((isActive: boolean) => {
    isUsageActive.current = isActive;
  }, []);
  const sequence = useRef(0);
  const pendingRefresh = useRef<null | {
    isManual: boolean;
    isUsagePage: boolean;
    afterPending: boolean;
  }>(null);
  const currentRefresh = useRef<DataContextValue["refresh"]>(async () => {});
  const queue = useRef(Promise.resolve());
  const zone = Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  /** Read cached aggregates without invoking probes or history network requests. */
  const reload = useCallback(async () => {
    const identity = ++sequence.current;
    try {
      const value = await nativeApi.GetUsage(
        displayRef.current.value.windowDays,
        zone,
      );
      if (identity === sequence.current)
        setSnapshot(value as unknown as UsageSnapshot);
    } catch (cause) {
      if (identity === sequence.current) setError(String(cause));
    }
  }, [zone]);
  useEffect(() => {
    void reload();
    const dispose = Events.On("usage-invalidated", () => void reload());
    return () => {
      dispose();
      sequence.current++;
    };
  }, [reload, display.value.windowDays]);
  /** Serialize ordinary preferences using the latest acknowledged revision. */
  const patchDisplay = useCallback((patch: Partial<SavedDisplay["value"]>) => {
    const operation = queue.current
      .catch(() => {})
      .then(async () => {
        try {
          const next = await nativeApi.SaveDisplay(
            SavedDisplay.createFrom(
              JSON.stringify({
                ...displayRef.current,
                value: { ...displayRef.current.value, ...patch },
              }),
            ),
          );
          displayRef.current = next;
          setDisplay(next);
        } catch (cause) {
          setError(String(cause));
          throw cause;
        }
      });
    queue.current = operation;
    return operation;
  }, []);
  /** Gate automatic work on current visibility/route and coalesce explicit refreshes in Go. */
  const refresh = useCallback(
    async (isManual: boolean, isUsagePage: boolean, afterPending = false) => {
      if (!snapshot) return;
      if (isRefreshing.current) {
        pendingRefresh.current = { isManual, isUsagePage, afterPending: true };
        return;
      }
      if (
        !isManual &&
        (document.visibilityState !== "visible" ||
          !isUsagePage ||
          !isUsageActive.current ||
          !displayRef.current.value.shouldAutoRefresh)
      )
        return;
      isRefreshing.current = true;
      setIsBusy(true);
      setError("");
      const targets =
        selected === null
          ? snapshot.environments.map(
              (environment) => environment.environmentId,
            )
          : [...selected];
      const request = Request.createFrom({
        IsManual: isManual,
        IsVisible: document.visibilityState === "visible",
        IsUsagePage: isUsagePage,
        IsAutoEnabled: displayRef.current.value.shouldAutoRefresh,
        AfterPending: afterPending,
      });
      try {
        if (displayRef.current.value.metric === "limits")
          await nativeApi.RefreshLimits(targets, request);
        else
          await nativeApi.RefreshUsage(
            displayRef.current.value.windowDays,
            zone,
            targets,
            request,
          );
      } catch (cause) {
        setError(String(cause));
      } finally {
        await reload();
        isRefreshing.current = false;
        setIsBusy(false);
        const next = pendingRefresh.current;
        pendingRefresh.current = null;
        if (next)
          void currentRefresh.current(
            next.isManual,
            next.isUsagePage,
            next.afterPending,
          );
      }
    },
    [snapshot, selected, zone, reload],
  );
  currentRefresh.current = refresh;
  return (
    <DATA_CONTEXT.Provider
      value={{
        setUsageActive,
        snapshot,
        display,
        selected,
        setSelected,
        error,
        isBusy,
        reload,
        patchDisplay,
        refresh,
      }}
    >
      {children}
    </DATA_CONTEXT.Provider>
  );
}

/** Require the shared route-independent controller. */
export function useData() {
  const value = useContext(DATA_CONTEXT);
  if (!value) throw new Error("Data provider is missing");
  return value;
}
