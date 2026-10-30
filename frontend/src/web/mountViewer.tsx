/** Mount the read-only usage pages over a snapshot-backed transport. */
import type { Root } from "react-dom/client";
import {
  objectNames,
  setTransport,
  type RuntimeTransport,
} from "@wailsio/runtime";
import { RefreshCwIcon } from "lucide-react";
import { Button } from "../../../tools/parity/upstream/apps/web/src/components/ui/button";
import * as nativeApi from "../../bindings/yousage/internal/desktop/app";
import { DataProvider } from "../usage/controller";
import { AppearanceProvider, applySavedAppearance } from "../theme/appearance";
import { msg, resolveLanguage, setLanguage, t } from "../i18n";
import { configureTimestampLocale } from "../../vendor/timestampFormat";
import type { PrivateSnapshot } from "./payloads";
import { captureMethodIds, createSnapshotTransport } from "./snapshotTransport";
import {
  followSnapshots,
  type FollowStatus,
  type SnapshotFollower,
} from "./follow";
import { DISPLAY_CHANGES_EVENT, displayChanges } from "./displaySync";
import { ReadOnlyContext } from "./readOnly";
import { RelayProblem, WebViewer } from "./WebViewer";
import type { ViewerConnection } from "./ViewerSettings";
import { PROBLEMS } from "./load";
import {
  createSnapshotSession,
  type SnapshotCandidate,
} from "./snapshotSession";
import { ViewerBoundary } from "./ViewerBoundary";
import { ContentCommitMarker } from "./ContentCommitMarker";
import {
  createRenderAcknowledger,
  VIEWER_COMMIT_CONTEXT,
} from "./renderAcknowledgment";
import { clearNotifications, Toaster } from "../notify";
import type { DiagnosticReporter } from "../diagnostics";

const DARK_QUERY = "(prefers-color-scheme: dark)";

/** Bindings that read a shown snapshot's reports, which its pages render once answered. */
const REPORT_READS = new Set([
  "GetUsage",
  "GetLossReport",
  "GetSpeedReport",
  "GetReceipt",
]);

/** Why a downloaded snapshot was not kept, as shown over the snapshot kept instead. */
const REJECTION_MESSAGES = {
  render: PROBLEMS.data.title,
  save: msg("The received data could not be saved on this device"),
} as const;

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
  /** The first snapshot: one kept earlier, or a download kept only once it shows. */
  initial: SnapshotCandidate<PrivateSnapshot>;
  /** The first snapshot was kept on this device earlier, so refresh it at once with visible progress. */
  isSaved?: boolean;
  /** Download a snapshot newer than the entity tag; `null` when unchanged, an error with a message on failure. */
  download: (
    etag: string,
  ) => Promise<SnapshotCandidate<PrivateSnapshot> | null>;
  /** Start over with a fresh download after the viewer could not show anything. */
  recover: () => void;
  /** Open a destination chosen by the user. */
  open: (url: string) => void;
  /** Viewing device locale. */
  locale: string;
  /** Transport for calls the snapshot does not answer, such as a native bridge. */
  fallback?: RuntimeTransport | null;
  /** The relay shown on the settings page, with an optional way to replace the link. */
  connection?: ViewerConnection;
  /** Local diagnostics intake told about each render failure; the relay web page has none. */
  reportDiagnostic?: DiagnosticReporter;
  /** Open the host's local diagnostics from the settings page, when the host has them. */
  showDiagnostics?: () => void;
  /** Report fresh accepted rendering, or that loading, failure or teardown hides it. */
  reportViewerShown?: (isShown: boolean) => void;
  /** Replace startup with an actionable standalone error if no snapshot has ever been shown. */
  onInitialFailure?: () => void;
  /** A current actionable error committed, even if native startup has not yet uncovered the viewer. */
  reportErrorShown?: () => void;
};

/**
 * Route binding calls to the snapshot and render the viewer.
 *
 * A newer snapshot replaces the shown one only after the active page rendered its answered
 * reports without failing and the host saved it; otherwise the kept snapshot is shown again.
 * Cached and restored snapshots report visibility only after their own fresh render acknowledgement.
 *
 * @param host - Snapshot source and page integration.
 * @returns A function that stops following the relay and device color scheme and restores the fallback transport.
 * @throws When the viewer cannot be mounted; the fallback transport is restored and nothing keeps running.
 */
export async function mountViewer(host: ViewerHost): Promise<() => void> {
  const fallback = host.fallback ?? null;
  let snapshot = host.initial.payload;
  let isMounted = true;
  /** Counts shown snapshots, so failures and boundaries belong to one of them. */
  let generation = 0;
  let acknowledgedGeneration: number | null = null;
  let failedGeneration: number | null = null;
  let hasShownSnapshot = false;
  let hasReportedInitialFailure = false;
  let acknowledgmentAttempt = 0;
  let status: FollowStatus = { state: "current" };
  /** Render the shell again so it shows the latest snapshot time and refresh status. */
  let render: () => void = () => {};
  const acknowledger = createRenderAcknowledger(() => render());
  let follower: SnapshotFollower | null = null;
  const darkQuery = matchMedia(DARK_QUERY);
  /** Tell the theme controller that the device color scheme changed. */
  const onSchemeChange = (event: MediaQueryListEvent) =>
    dispatchEvent("system-appearance", event.matches);
  /** Notify the host without letting a detached bridge affect snapshot acceptance. */
  const reportViewerShown = (isShown: boolean) => {
    if (isShown) hasShownSnapshot = true;
    try {
      host.reportViewerShown?.(isShown);
    } catch {
      /* Visibility is independent of diagnostics and snapshot storage. */
    }
  };
  /** Defer host root replacement until after the current React commit or promise callback. */
  const reportInitialFailure = () => {
    if (!host.onInitialFailure || hasShownSnapshot || hasReportedInitialFailure)
      return;
    hasReportedInitialFailure = true;
    queueMicrotask(() => {
      if (isMounted && !hasShownSnapshot) host.onInitialFailure?.();
    });
  };
  /** Stop all viewer work and reveal the host's standalone diagnostics entry. */
  const unmount = () => {
    if (!isMounted) return;
    isMounted = false;
    reportViewerShown(false);
    follower?.stop();
    acknowledger.cancel();
    clearNotifications();
    darkQuery.removeEventListener("change", onSchemeChange);
    setTransport(fallback);
  };
  /** Render another snapshot and tell the mounted pages about its theme and reports. */
  const present = (next: PrivateSnapshot) => {
    if (!isMounted || next === snapshot) return;
    const changes = displayChanges(snapshot.display.value, next.display.value);
    snapshot = next;
    generation++;
    acknowledgedGeneration = null;
    acknowledger.cancel();
    reportViewerShown(false);
    render();
    dispatchEvent("appearance-snapshot", snapshot.appearance);
    if (Object.keys(changes).length)
      dispatchEvent(DISPLAY_CHANGES_EVENT, changes);
    dispatchEvent("usage-invalidated", null);
    dispatchEvent("refresh-state", {
      isRefreshing: false,
      lastCompletedAt: snapshot.generatedAt,
      lastError: "",
    });
  };
  /** Wait for this generation's page commit without accepting obsolete render evidence. */
  const acknowledge = async () => {
    if (!isMounted) throw new Error("The viewer was closed.");
    const rendered = generation;
    const attempt = ++acknowledgmentAttempt;
    try {
      await acknowledger.wait(rendered);
    } catch (cause) {
      if (
        isMounted &&
        rendered === generation &&
        attempt === acknowledgmentAttempt
      )
        reportInitialFailure();
      throw cause;
    }
    if (!isMounted || rendered !== generation || failedGeneration === rendered)
      throw new Error("The snapshot is no longer shown.");
    acknowledgedGeneration = rendered;
  };
  /** Freshly acknowledge already accepted cached or restored data, independently of networking. */
  const acknowledgeAccepted = () => {
    const rendered = generation;
    void acknowledge().then(
      () => {
        if (
          isMounted &&
          rendered === generation &&
          acknowledgedGeneration === rendered
        )
          reportViewerShown(true);
      },
      () => {},
    );
  };
  /** Candidates the first refresh applies before downloading anything newer. */
  let pendingInitial = host.isSaved ? null : host.initial;
  const session = createSnapshotSession<PrivateSnapshot>({
    accepted: host.isSaved ? host.initial : null,
    download: host.download,
    /** Keep candidate acceptance pending until the current generation actually renders. */
    async show(next) {
      present(next);
      await acknowledge();
    },
    /** Restore accepted data with a fresh acknowledgement rather than reusing its old commit. */
    restore(next) {
      present(next);
      if (isMounted) acknowledgeAccepted();
    },
    describe: (reason) => t(REJECTION_MESSAGES[reason]),
  });
  /** Refresh with visible progress; failures stay in the status. */
  const refreshShown = (isRetry = false) =>
    void follower?.refetch(true, isRetry).catch(() => {});
  reportViewerShown(false);
  try {
    const names = await captureMethodIds(setTransport, fallback);
    const transport = createSnapshotTransport(names, {
      current: () => snapshot,
      /** A failure the status already shows is not reported again by the pages. */
      refetch: async () => {
        await follower?.refetch().catch((cause: unknown) => {
          if (status.state !== "failed") throw cause;
        });
      },
      device: { isDark: () => darkQuery.matches, locale: host.locale },
      open: host.open,
      fallback,
    });
    setTransport({
      /** Report reads count toward the shown snapshot's acknowledgment. */
      call(objectID, method, windowName, args) {
        const read = transport.call(objectID, method, windowName, args);
        const name =
          objectID === objectNames.Call
            ? names.get((args as { methodID: number }).methodID)
            : undefined;
        return name && REPORT_READS.has(name)
          ? acknowledger.track(generation, read)
          : read;
      },
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
    follower = followSnapshots(
      async (isRetry) => {
        const initial = pendingInitial;
        pendingInitial = null;
        return initial ?? session.fetch(isRetry);
      },
      async (candidate) => {
        try {
          await session.accept(candidate);
        } catch (cause) {
          if (isMounted && session.accepted() === null) reportInitialFailure();
          throw cause;
        }
        if (isMounted && acknowledgedGeneration === generation)
          reportViewerShown(true);
      },
      (next) => {
        status = next;
        render();
      },
    );
    /** Render this generation with callbacks that cannot affect a later or closed viewer. */
    render = () => {
      if (!isMounted) return;
      const rendered = generation;
      /** Acknowledge only the current mounted page's confirmation token. */
      const onCommit = (token: number) => {
        if (isMounted && rendered === generation) acknowledger.committed(token);
      };
      /** A failure belongs to the snapshot this render showed, even when reported later. */
      const onRenderError = () => {
        if (!isMounted || rendered !== generation) return;
        acknowledgedGeneration = null;
        failedGeneration = rendered;
        acknowledger.failed(rendered);
        reportViewerShown(false);
        reportInitialFailure();
        try {
          host.reportDiagnostic?.("render");
        } catch {
          /* Diagnostics never change how the viewer recovers from the failure. */
        }
      };
      /** A committed error can complete startup without falsely accepting its failed snapshot. */
      const onErrorShown = () => {
        if (
          isMounted &&
          rendered === generation &&
          failedGeneration === rendered
        )
          host.reportErrorShown?.();
      };
      host.root.render(
        <ViewerBoundary
          resetKey={rendered}
          onError={onRenderError}
          fallback={() => (
            <RelayProblem
              title={t(PROBLEMS.display.title)}
              detail={t(PROBLEMS.display.detail)}
            >
              <Button onClick={host.recover}>
                <RefreshCwIcon aria-hidden />
                {t("Try again")}
              </Button>
              <ContentCommitMarker onCommit={onErrorShown} />
            </RelayProblem>
          )}
        >
          <VIEWER_COMMIT_CONTEXT.Provider
            value={{ token: acknowledger.token(), onCommit }}
          >
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
                    onShowDiagnostics={host.showDiagnostics}
                    refresh={{ status, onRetry: () => refreshShown(true) }}
                    renderKey={rendered}
                    onRenderError={onRenderError}
                    onErrorShown={onErrorShown}
                  />
                </DataProvider>
                <Toaster />
              </AppearanceProvider>
            </ReadOnlyContext.Provider>
          </VIEWER_COMMIT_CONTEXT.Provider>
        </ViewerBoundary>,
      );
    };
    render();
    if (host.isSaved) acknowledgeAccepted();
  } catch (cause) {
    unmount();
    throw cause;
  }
  if (host.isSaved) refreshShown();
  else void follower.refetch().catch(() => {});
  return unmount;
}
