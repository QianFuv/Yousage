/** Answer desktop binding calls from a decrypted snapshot instead of the Go process. */
import { objectNames, type RuntimeTransport } from "@wailsio/runtime";
import * as nativeApi from "../../bindings/yousage/internal/desktop/app";
import type { PrivateSnapshot } from "./payloads";
import { t } from "../i18n";

/** Bindings the viewer answers, by the name used in the generated module. */
type ViewerMethod =
  | "GetUsage"
  | "GetLossReport"
  | "GetSpeedReport"
  | "GetReceipt"
  | "GetDisplay"
  | "SaveDisplay"
  | "GetAppearance"
  | "GetBootstrap"
  | "RefreshUsage"
  | "RefreshLimits"
  | "RequestRefresh"
  | "GetRefreshState"
  | "OpenExternal"
  | "FrontendReady";

/** Calls that record each binding's generated method ID instead of reaching a backend. */
const PROBES: Record<ViewerMethod, () => Promise<unknown>> = {
  GetUsage: () => nativeApi.GetUsage(1, ""),
  GetLossReport: () => nativeApi.GetLossReport(1, ""),
  GetSpeedReport: () => nativeApi.GetSpeedReport(1, ""),
  GetReceipt: () => nativeApi.GetReceipt(1, ""),
  GetDisplay: () => nativeApi.GetDisplay(),
  SaveDisplay: () => nativeApi.SaveDisplay({} as never),
  GetAppearance: () => nativeApi.GetAppearance(),
  GetBootstrap: () => nativeApi.GetBootstrap(),
  RefreshUsage: () => nativeApi.RefreshUsage(1, "", [], {} as never),
  RefreshLimits: () => nativeApi.RefreshLimits([], {} as never),
  RequestRefresh: () => nativeApi.RequestRefresh(),
  GetRefreshState: () => nativeApi.GetRefreshState(),
  OpenExternal: () => nativeApi.OpenExternal(""),
  FrontendReady: () => nativeApi.FrontendReady(),
};

/** What the viewer needs from its host page. */
export type SnapshotSource = {
  /** The snapshot currently shown. */
  current: () => PrivateSnapshot;
  /** Download a newer snapshot when the relay has one. */
  refetch: () => Promise<void>;
  /** The viewing device's own color scheme and locale. */
  device: { isDark: () => boolean; locale: string };
  /** Open a destination chosen by the user. */
  open: (url: string) => void;
  /** Host transport for calls the snapshot does not answer; without one they are rejected. */
  fallback?: RuntimeTransport | null;
};

/** Thrown by a capturing transport to stop a probe before it reaches the network. */
class ProbeStop extends Error {}

/**
 * Record the generated method ID of every binding the viewer answers.
 *
 * @param setTransport - The runtime's transport setter.
 * @param previous - Transport to restore afterwards, such as a native bridge.
 * @returns Binding names keyed by method ID.
 */
export async function captureMethodIds(
  setTransport: (transport: RuntimeTransport | null) => void,
  previous: RuntimeTransport | null = null,
): Promise<Map<number, ViewerMethod>> {
  const names = new Map<number, ViewerMethod>();
  let current: ViewerMethod = "GetUsage";
  setTransport({
    call(objectID, _method, _windowName, args) {
      if (objectID === objectNames.Call)
        names.set((args as { methodID: number }).methodID, current);
      return Promise.reject(new ProbeStop());
    },
  });
  for (const [name, probe] of Object.entries(PROBES) as [
    ViewerMethod,
    () => Promise<unknown>,
  ][]) {
    current = name;
    await probe().catch(() => {});
  }
  setTransport(previous);
  return names;
}

/**
 * Build a transport that serves reads from the snapshot and keeps display changes in memory.
 *
 * Every other call goes to the fallback transport when there is one and is rejected
 * otherwise, so desktop-only actions cannot run from a viewer.
 *
 * @param names - Binding names keyed by method ID.
 * @param source - The snapshot and the viewing device.
 * @returns The runtime transport.
 */
export function createSnapshotTransport(
  names: ReadonlyMap<number, ViewerMethod>,
  source: SnapshotSource,
): RuntimeTransport {
  let display: PrivateSnapshot["display"] | null = null;
  /** The snapshot's reports for the requested window, falling back to 30 days. */
  const period = (days: unknown) => {
    const periods = source.current().periods;
    return periods[String(days)] ?? periods["30"];
  };
  /** Answer one binding call; generated model constructors mutate what they receive. */
  const answer = async (request: { methodID: number; args: unknown[] }) => {
    const name = names.get(request.methodID);
    const snapshot = source.current();
    switch (name) {
      case "GetUsage":
        return period(request.args[0]).usage;
      case "GetLossReport":
        return period(request.args[0]).loss;
      case "GetSpeedReport":
        return period(request.args[0]).speed;
      case "GetReceipt":
        return period(request.args[0]).receipt;
      case "GetDisplay":
        return display ?? snapshot.display;
      case "SaveDisplay": {
        const saved = request.args[0] as PrivateSnapshot["display"];
        display = { revision: saved.revision + 1, value: saved.value };
        return display;
      }
      case "GetAppearance":
        return snapshot.appearance;
      case "GetBootstrap":
        return {
          isSystemDark: source.device.isDark(),
          systemLocale: source.device.locale,
        };
      case "RefreshUsage":
      case "RefreshLimits":
      case "RequestRefresh":
        await source.refetch();
        return null;
      case "GetRefreshState":
        return {
          isRefreshing: false,
          lastCompletedAt: snapshot.generatedAt,
          lastError: "",
        };
      case "OpenExternal":
        source.open(String(request.args[0]));
        return null;
      case "FrontendReady":
        return null;
      default:
        throw new Error(t("This action is only available in the desktop app."));
    }
  };
  return {
    async call(objectID, method, windowName, args) {
      const request = args as { methodID: number; args: unknown[] };
      if (
        source.fallback &&
        (objectID !== objectNames.Call || !names.has(request.methodID))
      )
        return source.fallback.call(objectID, method, windowName, args);
      if (objectID !== objectNames.Call) return null;
      return structuredClone(await answer(request));
    },
  };
}
