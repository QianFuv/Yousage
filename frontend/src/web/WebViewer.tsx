/** Relay viewer shell: the four usage pages from a snapshot plus a viewer settings page, without native actions. */
import {
  lazy,
  Suspense,
  useEffect,
  useLayoutEffect,
  useState,
  type ReactNode,
} from "react";
import {
  ChartNoAxesCombinedIcon,
  FlameIcon,
  GaugeIcon,
  RefreshCwIcon,
  Settings2Icon,
  TimerIcon,
} from "lucide-react";
import { Button } from "../../../tools/parity/upstream/apps/web/src/components/ui/button";
import { useData } from "../usage/controller";
import { UsagePage } from "../usage/UsagePage";
import { NavItem } from "../NavItem";
import { Events } from "@wailsio/runtime";
import { resolveLanguage, setLanguage, t, useI18n } from "../i18n";
import { ViewerSettings, type ViewerConnection } from "./ViewerSettings";
import { RefreshStatus } from "./RefreshStatus";
import type { FollowStatus } from "./follow";
import { DISPLAY_CHANGES_EVENT } from "./displaySync";
import { PROBLEMS } from "./load";
import { ViewerBoundary } from "./ViewerBoundary";
import { ViewerCommitMarker } from "./renderAcknowledgment";

/**
 * Pages loaded only once visited. A failed load stays failed in its lazy component,
 * so retrying after a failure loads them again through new ones.
 *
 * @returns The lazily loaded pages.
 */
function lazyPages() {
  return {
    LossPage: lazy(() =>
      import("../loss/LossPage").then((module) => ({
        default: module.LossPage,
      })),
    ),
    SpeedPage: lazy(() =>
      import("../speed/SpeedPage").then((module) => ({
        default: module.SpeedPage,
      })),
    ),
  };
}

/** Pages offered by the viewer, in navigation order. */
type ViewerRoute = "statistics" | "quota" | "loss" | "speed" | "settings";

/**
 * Explain that a page failed to render, with a way to render it again.
 *
 * @param props - The retry action.
 * @returns The message in place of the page.
 */
function PageProblem({ onRetry }: { onRetry: () => void }) {
  return (
    <RelayProblem
      title={t(PROBLEMS.display.title)}
      detail={t(PROBLEMS.display.detail)}
    >
      <Button onClick={onRetry}>
        <RefreshCwIcon aria-hidden />
        {t("Try again")}
      </Button>
    </RelayProblem>
  );
}

/**
 * Read-only pages rendered from the latest relay snapshot.
 *
 * @param props - Snapshot time and version, the viewing device locale, the relay connection,
 * refresh status, the shown snapshot's render key and a listener for pages that fail to render.
 * @returns The viewer layout.
 */
export function WebViewer({
  generatedAt,
  appVersion,
  locale,
  connection,
  refresh,
  renderKey = 0,
  onRenderError,
}: {
  generatedAt: string;
  appVersion: string;
  locale: string;
  connection?: ViewerConnection;
  refresh?: { status: FollowStatus; onRetry: () => void };
  renderKey?: number;
  onRenderError?: (error: unknown) => void;
}) {
  useI18n();
  const data = useData();
  const [route, setRoute] = useState<ViewerRoute>("statistics");
  const [visited, setVisited] = useState<Set<ViewerRoute>>(() => new Set());
  const [{ LossPage, SpeedPage }, setPages] = useState(lazyPages);
  useLayoutEffect(
    () => setLanguage(resolveLanguage(data.display.value.language, locale)),
    [data.display.value.language, locale],
  );
  /** Apply preferences the desktop changed, keeping choices made only in this viewer. */
  useEffect(
    () =>
      Events.On(DISPLAY_CHANGES_EVENT, (event) => {
        void data
          .patchDisplay(event.data as Partial<typeof data.display.value>)
          .catch(() => {});
      }),
    [data.patchDisplay],
  );
  /** Show a page and keep it mounted afterwards, like the desktop navigation. */
  const navigate = (next: ViewerRoute) => {
    setVisited((current) => new Set([...current, next]));
    setRoute(next);
  };
  return (
    <div className="desktop-layout">
      <nav className="app-sidebar" aria-label={t("Application")}>
        <div className="sidebar-brand">
          <strong className="sidebar-wordmark" aria-label="Yousage">
            <span aria-hidden>Y</span>
            <span className="sidebar-wordmark-tail" aria-hidden>
              ou
            </span>
            <span className="sidebar-wordmark-tail" aria-hidden>
              sage
            </span>
          </strong>
        </div>
        <div className="sidebar-group">
          <NavItem
            icon={ChartNoAxesCombinedIcon}
            label={t("Usage")}
            isCurrent={route === "statistics"}
            onClick={() => navigate("statistics")}
          />
          <NavItem
            icon={GaugeIcon}
            label={t("Quota")}
            isCurrent={route === "quota"}
            onClick={() => navigate("quota")}
          />
          <NavItem
            icon={FlameIcon}
            label={t("Loss")}
            isCurrent={route === "loss"}
            onClick={() => navigate("loss")}
          />
          <NavItem
            icon={TimerIcon}
            label={t("Speed")}
            isCurrent={route === "speed"}
            onClick={() => navigate("speed")}
          />
          <NavItem
            icon={Settings2Icon}
            label={t("Settings")}
            isCurrent={route === "settings"}
            onClick={() => navigate("settings")}
          />
        </div>
      </nav>
      <main tabIndex={-1} aria-label={t("Page content")}>
        <div hidden={route !== "statistics" && route !== "quota"}>
          <UsagePage
            isActive={route === "statistics" || route === "quota"}
            view={route === "statistics" ? "statistics" : "limits"}
            onSettings={() => {}}
          />
        </div>
        {visited.has("loss") && (
          <div hidden={route !== "loss"}>
            <ViewerBoundary
              resetKey={renderKey}
              onError={onRenderError}
              onRetry={() => setPages(lazyPages())}
              fallback={(retry) => <PageProblem onRetry={retry} />}
            >
              <Suspense fallback={null}>
                <LossPage isActive={route === "loss"} />
                {route === "loss" && <ViewerCommitMarker />}
              </Suspense>
            </ViewerBoundary>
          </div>
        )}
        {visited.has("speed") && (
          <div hidden={route !== "speed"}>
            <ViewerBoundary
              resetKey={renderKey}
              onError={onRenderError}
              onRetry={() => setPages(lazyPages())}
              fallback={(retry) => <PageProblem onRetry={retry} />}
            >
              <Suspense fallback={null}>
                <SpeedPage isActive={route === "speed"} />
                {route === "speed" && <ViewerCommitMarker />}
              </Suspense>
            </ViewerBoundary>
          </div>
        )}
        {route === "settings" && (
          <>
            <ViewerSettings
              generatedAt={generatedAt}
              appVersion={appVersion}
              locale={locale}
              connection={connection}
            />
            <ViewerCommitMarker />
          </>
        )}
      </main>
      {refresh && (
        <RefreshStatus
          status={refresh.status}
          generatedAt={generatedAt}
          locale={locale}
          onRetry={refresh.onRetry}
        />
      )}
    </div>
  );
}

/**
 * A full-page message shown instead of the viewer or share page.
 *
 * @param props - Title, explanation and optional actions.
 * @returns The message page.
 */
export function RelayProblem({
  title,
  detail,
  children,
}: {
  title: string;
  detail: string;
  children?: ReactNode;
}) {
  return (
    <div className="relay-problem" role="alert">
      <strong className="sidebar-wordmark" aria-label="Yousage">
        <span aria-hidden>Y</span>
        <span className="sidebar-wordmark-tail" aria-hidden>
          ou
        </span>
        <span className="sidebar-wordmark-tail" aria-hidden>
          sage
        </span>
      </strong>
      <h1>{title}</h1>
      <p>{detail}</p>
      {children}
    </div>
  );
}
