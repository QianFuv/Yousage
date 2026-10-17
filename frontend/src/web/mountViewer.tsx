/** Mount the read-only usage pages over a snapshot-backed transport. */
import type { Root } from "react-dom/client";
import { setTransport, type RuntimeTransport } from "@wailsio/runtime";
import * as nativeApi from "../../bindings/yousage/internal/desktop/app";
import { DataProvider } from "../usage/controller";
import { AppearanceProvider, applySavedAppearance } from "../theme/appearance";
import { resolveLanguage, setLanguage } from "../i18n";
import { configureTimestampLocale } from "../../vendor/timestampFormat";
import type { PrivateSnapshot } from "./payloads";
import { captureMethodIds, createSnapshotTransport } from "./snapshotTransport";
import { followSnapshots, type FollowStatus } from "./follow";
import { DISPLAY_CHANGES_EVENT, displayChanges } from "./displaySync";
import { ReadOnlyContext } from "./readOnly";
import { WebViewer } from "./WebViewer";
import type { ViewerConnection } from "./ViewerSettings";
import { clearNotifications, Toaster } from "../notify";

const DARK_QUERY = "(prefers-color-scheme: dark)";

/**
 * Deliver an event to runtime listeners as if the native side had sent it.
 *
 * @param name - Event name.
 * @param data - Event payload.
 */
function dispatchEvent(name: string, data: unknown) {
  (
    window as unknown as {
      _wails?: {
        dispatchWailsEvent: (event: { name: string; data: unknown }) => void;
      };
    }
  )._wails?.dispatchWailsEvent({ name, data });
}

/** What a page hosting the viewer provides. */
export type ViewerHost = {
  /** React root the viewer renders into. */
  root: Root;
  /** The first snapshot to show. */
  initial: PrivateSnapshot;
  /** The first snapshot was saved on this device earlier, so refresh it at once with visible progress. */
  isSaved?: boolean;
  /** Download a newer snapshot; `null` when unchanged, an error with a message on failure. */
  fetchNewer: () => Promise<PrivateSnapshot | null>;
  /** Open a destination chosen by the user. */
  open: (url: string) => void;
  /** Viewing device locale. */
  locale: string;
  /** Transport for calls the snapshot does not answer, such as a native bridge. */
  fallback?: RuntimeTransport | null;
  /** The relay shown on the settings page, with an optional way to replace the link. */
  connection?: ViewerConnection;
};

/**
 * Route binding calls to the snapshot and render the viewer.
 *
 * @param host - Snapshot source and page integration.
 * @returns A function that stops following the relay and device color scheme and restores the fallback transport.
 */
export async function mountViewer(host: ViewerHost): Promise<() => void> {
  const fallback = host.fallback ?? null;
  let snapshot = host.initial;
  const names = await captureMethodIds(setTransport, fallback);
  const darkQuery = matchMedia(DARK_QUERY);
  let status: FollowStatus = { state: "current" };
  /** Render the shell again so it shows the latest snapshot time and refresh status. */
  let render: () => void = () => {};
  /** Hand each newer snapshot's theme and reports to the mounted pages. */
  const follower = followSnapshots(
    host.fetchNewer,
    (next) => {
      const changes = displayChanges(
        snapshot.display.value,
        next.display.value,
      );
      snapshot = next;
      render();
      dispatchEvent("appearance-snapshot", snapshot.appearance);
      if (Object.keys(changes).length)
        dispatchEvent(DISPLAY_CHANGES_EVENT, changes);
      dispatchEvent("usage-invalidated", null);
    },
    (next) => {
      status = next;
      render();
    },
  );
  /** Refresh with visible progress; failures stay in the status. */
  const refreshShown = () => void follower.refetch(true).catch(() => {});
  setTransport(
    createSnapshotTransport(names, {
      current: () => snapshot,
      /** A failure the status already shows is not reported again by the pages. */
      refetch: () =>
        follower.refetch().catch((cause: unknown) => {
          if (status.state !== "failed") throw cause;
        }),
      device: { isDark: () => darkQuery.matches, locale: host.locale },
      open: host.open,
      fallback,
    }),
  );
  /** Tell the theme controller that the device color scheme changed. */
  const onSchemeChange = (event: MediaQueryListEvent) =>
    dispatchEvent("system-appearance", event.matches);
  darkQuery.addEventListener("change", onSchemeChange);
  const [bootstrap, appearance, display] = await Promise.all([
    nativeApi.GetBootstrap(),
    nativeApi.GetAppearance(),
    nativeApi.GetDisplay(),
  ]);
  setLanguage(resolveLanguage(display.value.language, host.locale));
  configureTimestampLocale(host.locale);
  applySavedAppearance(appearance, bootstrap.isSystemDark);
  render = () =>
    host.root.render(
      <ReadOnlyContext.Provider value>
        <AppearanceProvider
          initial={appearance}
          isSystemDark={bootstrap.isSystemDark}
        >
          <DataProvider initial={display}>
            <WebViewer
              generatedAt={snapshot.generatedAt}
              appVersion={snapshot.appVersion}
              locale={host.locale}
              connection={host.connection}
              refresh={{ status, onRetry: refreshShown }}
            />
          </DataProvider>
          <Toaster />
        </AppearanceProvider>
      </ReadOnlyContext.Provider>,
    );
  render();
  if (host.isSaved) refreshShown();
  return () => {
    follower.stop();
    clearNotifications();
    darkQuery.removeEventListener("change", onSchemeChange);
    setTransport(fallback);
  };
}
