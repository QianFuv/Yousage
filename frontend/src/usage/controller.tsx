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
import * as nativeApi from "../../bindings/yousage/internal/desktop/app";
import { SavedDisplay } from "../../bindings/yousage/internal/preferences/models";
import type { UsageSnapshot } from "./data";
import { notifyError } from "../notify";

/** Global refresh progress published by the desktop scheduler. */
export type RefreshState = {
  /** Whether a global refresh cycle is running. */
  isRefreshing: boolean;
  /** RFC 3339 completion time of the last cycle, or empty before the first one. */
  lastCompletedAt: string;
  /** Sanitized failure of the last cycle, or empty when it fully succeeded. */
  lastError: string;
};
const IDLE_REFRESH: RefreshState = {
  isRefreshing: false,
  lastCompletedAt: "",
  lastError: "",
};

type DataContextValue = {
  snapshot: UsageSnapshot | null;
  display: SavedDisplay;
  selected: Set<string> | null;
  setSelected: (selected: Set<string> | null) => void;
  error: string;
  isBusy: boolean;
  refreshState: RefreshState;
  isSavingDisplay: boolean;
  reload: () => Promise<void>;
  patchDisplay: (patch: Partial<SavedDisplay["value"]>) => Promise<void>;
  refresh: () => Promise<void>;
};
const DATA_CONTEXT = createContext<DataContextValue | null>(null);

/**
 * Share saved view state and last-good summaries. The desktop scheduler owns every
 * automatic refresh; each page only reloads on `usage-invalidated`.
 *
 * @param props - The saved display and the routes that consume the shared data.
 * @returns The data provider.
 */
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
  const hasSnapshot = useRef(false);
  const [isSavingDisplay, setIsSavingDisplay] = useState(false);
  const preferenceWrites = useRef(0);
  const [refreshState, setRefreshState] = useState(IDLE_REFRESH);
  const sequence = useRef(0);
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
          ) {
            hasSnapshot.current = true;
            setSnapshot(value as unknown as UsageSnapshot);
            setError("");
          }
        } catch (cause) {
          if (identity !== sequence.current) continue;
          if (hasSnapshot.current) notifyError(cause);
          else setError(String(cause));
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
    return () => {
      dispose();
      sequence.current++;
      shouldReload.current = false;
    };
  }, [reload, display.value.windowDays]);
  /** Follow the scheduler's progress; a published change supersedes the initial read. */
  useEffect(() => {
    let hasEvent = false;
    let isMounted = true;
    const dispose = Events.On("refresh-state", (event) => {
      hasEvent = true;
      setRefreshState({ ...IDLE_REFRESH, ...(event.data as RefreshState) });
    });
    void nativeApi
      .GetRefreshState()
      .then((value) => {
        if (isMounted && !hasEvent && value)
          setRefreshState({ ...IDLE_REFRESH, ...value });
      })
      .catch(() => {});
    return () => {
      isMounted = false;
      dispose();
    };
  }, []);
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
          notifyError(cause);
          throw cause;
        } finally {
          preferenceWrites.current--;
          setIsSavingDisplay(preferenceWrites.current > 0);
        }
      });
    queue.current = operation;
    return operation;
  }, []);
  /** Ask the scheduler for one coalesced cycle now; results arrive as invalidations. */
  const refresh = useCallback(async () => {
    try {
      await nativeApi.RequestRefresh();
    } catch (cause) {
      notifyError(cause);
    }
  }, []);
  return (
    <DATA_CONTEXT.Provider
      value={{
        snapshot,
        display,
        selected,
        setSelected,
        error,
        isBusy: refreshState.isRefreshing,
        refreshState,
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
