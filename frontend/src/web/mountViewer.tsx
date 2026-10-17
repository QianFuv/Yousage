/** Mount the read-only usage pages over a snapshot-backed transport. */
import type { ReactNode } from "react";
import type { Root } from "react-dom/client";
import { setTransport, type RuntimeTransport } from "@wailsio/runtime";
import * as nativeApi from "../../bindings/yousage/internal/desktop/app";
import { DataProvider } from "../usage/controller";
import { AppearanceProvider, applySavedAppearance } from "../theme/appearance";
import { resolveLanguage, setLanguage } from "../i18n";
import { configureTimestampLocale } from "../../vendor/timestampFormat";
import type { PrivateSnapshot } from "./payloads";
import { captureMethodIds, createSnapshotTransport } from "./snapshotTransport";
import { ReadOnlyContext } from "./readOnly";
import { WebViewer } from "./WebViewer";
import { clearNotifications, Toaster } from "../notify";

const DARK_QUERY = "(prefers-color-scheme: dark)";

/** What a page hosting the viewer provides. */
export type ViewerHost = {
  /** React root the viewer renders into. */
  root: Root;
  /** The first snapshot to show. */
  initial: PrivateSnapshot;
  /** Download a newer snapshot; `null` when unchanged, an error with a message on failure. */
  fetchNewer: () => Promise<PrivateSnapshot | null>;
  /** Open a destination chosen by the user. */
  open: (url: string) => void;
  /** Viewing device locale. */
  locale: string;
  /** Transport for calls the snapshot does not answer, such as a native bridge. */
  fallback?: RuntimeTransport | null;
  /** Extra control shown in the snapshot banner. */
  bannerAction?: ReactNode;
};

/**
 * Route binding calls to the snapshot and render the viewer.
 *
 * @param host - Snapshot source and page integration.
 * @returns A function that stops following the device color scheme and restores the fallback transport.
 */
export async function mountViewer(host: ViewerHost): Promise<() => void> {
  const fallback = host.fallback ?? null;
  let snapshot = host.initial;
  const names = await captureMethodIds(setTransport, fallback);
  const darkQuery = matchMedia(DARK_QUERY);
  /** Render the shell again so the banner shows the latest snapshot time. */
  let renderTime: (generatedAt: string) => void = () => {};
  setTransport(
    createSnapshotTransport(names, {
      current: () => snapshot,
      async refetch() {
        const next = await host.fetchNewer();
        if (next) {
          snapshot = next;
          renderTime(snapshot.generatedAt);
        }
      },
      device: { isDark: () => darkQuery.matches, locale: host.locale },
      open: host.open,
      fallback,
    }),
  );
  /** Tell the theme controller that the device color scheme changed. */
  const onSchemeChange = (event: MediaQueryListEvent) =>
    (
      window as unknown as {
        _wails?: {
          dispatchWailsEvent: (event: { name: string; data: boolean }) => void;
        };
      }
    )._wails?.dispatchWailsEvent({
      name: "system-appearance",
      data: event.matches,
    });
  darkQuery.addEventListener("change", onSchemeChange);
  const [bootstrap, appearance, display] = await Promise.all([
    nativeApi.GetBootstrap(),
    nativeApi.GetAppearance(),
    nativeApi.GetDisplay(),
  ]);
  setLanguage(resolveLanguage(display.value.language, host.locale));
  configureTimestampLocale(host.locale);
  applySavedAppearance(appearance, bootstrap.isSystemDark);
  renderTime = (generatedAt) =>
    host.root.render(
      <ReadOnlyContext.Provider value>
        <AppearanceProvider
          initial={appearance}
          isSystemDark={bootstrap.isSystemDark}
        >
          <DataProvider initial={display}>
            <WebViewer
              generatedAt={generatedAt}
              locale={host.locale}
              bannerAction={host.bannerAction}
            />
          </DataProvider>
          <Toaster />
        </AppearanceProvider>
      </ReadOnlyContext.Provider>,
    );
  renderTime(snapshot.generatedAt);
  return () => {
    clearNotifications();
    darkQuery.removeEventListener("change", onSchemeChange);
    setTransport(fallback);
  };
}
