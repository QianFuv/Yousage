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
  setUsageActive: (
    isActive: boolean,
    metric: "cost" | "tokens" | "limits",
  ) => void;
  snapshot: UsageSnapshot | null;
  display: SavedDisplay;
  selected: Set<string> | null;
  setSelected: (selected: Set<string> | null) => void;
  error: string;
  isBusy: boolean;
  isSavingDisplay: boolean;
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
  const [isSavingDisplay, setIsSavingDisplay] = useState(false);
  const preferenceWrites = useRef(0);
  const [isBusy, setIsBusy] = useState(false);
  const isRefreshing = useRef(false);
  const isUsageActive = useRef(true);
  const isNativeVisible = useRef(false);
  const usageMetric = useRef<"cost" | "tokens" | "limits">("limits");
  const setUsageActive = useCallback(
    (isActive: boolean, metric: "cost" | "tokens" | "limits") => {
      isUsageActive.current = isActive;
      if (isActive) usageMetric.current = metric;
    },
    [],
  );
  const sequence = useRef(0);
  const pendingRefresh = useRef<null | {
    isManual: boolean;
    isUsagePage: boolean;
    afterPending: boolean;
  }>(null);
  const currentRefresh = useRef<DataContextValue["refresh"]>(async () => {});
  const queue = useRef(Promise.resolve());
  const zone = Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  const reloadFlight = useRef<Promise<void> | null>(null);
  const shouldReload = useRef(false);
  /** Serialize invalidations and read the latest window after changes during a pending read. */
  const reload = useCallback(() => {
    shouldReload.current = true;
    if (reloadFlight.current) return reloadFlight.current;
    const operation = (async () => {
      while (shouldReload.current) {
        shouldReload.current = false;
        const identity = ++sequence.current;
        const days = displayRef.current.value.windowDays;
        try {
          const value = await nativeApi.GetUsage(days, zone);
          if (
            identity === sequence.current &&
            days === displayRef.current.value.windowDays
          )
            setSnapshot(value as unknown as UsageSnapshot);
        } catch (cause) {
          if (identity === sequence.current) setError(String(cause));
        }
      }
    })().finally(() => {
      reloadFlight.current = null;
    });
    reloadFlight.current = operation;
    return operation;
  }, [zone]);
  useEffect(() => {
    void reload();
    const dispose = Events.On("usage-invalidated", () => void reload());
    const disposeVisibility = Events.On("window-visibility", (event) => {
      isNativeVisible.current = event.data === true;
      if (isNativeVisible.current)
        void currentRefresh.current(false, isUsageActive.current, true);
    });
    return () => {
      dispose();
      disposeVisibility();
      sequence.current++;
      shouldReload.current = false;
    };
  }, [reload, display.value.windowDays]);
  /** Serialize ordinary preferences using the latest acknowledged revision. */
  const patchDisplay = useCallback((patch: Partial<SavedDisplay["value"]>) => {
    preferenceWrites.current++;
    setIsSavingDisplay(true);
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
        } finally {
          preferenceWrites.current--;
          setIsSavingDisplay(preferenceWrites.current > 0);
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
          !isNativeVisible.current ||
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
        IsVisible:
          document.visibilityState === "visible" && isNativeVisible.current,
        IsUsagePage: isUsagePage,
        IsAutoEnabled: displayRef.current.value.shouldAutoRefresh,
        AfterPending: afterPending,
      });
      try {
        if (usageMetric.current === "limits")
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
        isSavingDisplay,
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
