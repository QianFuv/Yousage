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
  Settings2Icon,
  TimerIcon,
} from "lucide-react";
import { useData } from "../usage/controller";
import { UsagePage } from "../usage/UsagePage";
import { NavItem } from "../NavItem";
import { Events } from "@wailsio/runtime";
import { resolveLanguage, setLanguage, t, useI18n } from "../i18n";
import { ViewerSettings, type ViewerConnection } from "./ViewerSettings";
import { RefreshStatus } from "./RefreshStatus";
import type { FollowStatus } from "./follow";
import { DISPLAY_CHANGES_EVENT } from "./displaySync";

const LossPage = lazy(() =>
  import("../loss/LossPage").then((module) => ({ default: module.LossPage })),
);
const SpeedPage = lazy(() =>
  import("../speed/SpeedPage").then((module) => ({
    default: module.SpeedPage,
  })),
);

/** Pages offered by the viewer, in navigation order. */
type ViewerRoute = "statistics" | "quota" | "loss" | "speed" | "settings";

/**
 * Read-only pages rendered from the latest relay snapshot.
 *
 * @param props - Snapshot time and version, the viewing device locale, the relay connection and refresh status.
 * @returns The viewer layout.
 */
export function WebViewer({
  generatedAt,
  appVersion,
  locale,
  connection,
  refresh,
}: {
  generatedAt: string;
  appVersion: string;
  locale: string;
  connection?: ViewerConnection;
  refresh?: { status: FollowStatus; onRetry: () => void };
}) {
  useI18n();
  const data = useData();
  const [route, setRoute] = useState<ViewerRoute>("statistics");
  const [visited, setVisited] = useState<Set<ViewerRoute>>(() => new Set());
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
        <Suspense fallback={null}>
          {visited.has("loss") && (
            <div hidden={route !== "loss"}>
              <LossPage isActive={route === "loss"} />
            </div>
          )}
          {visited.has("speed") && (
            <div hidden={route !== "speed"}>
              <SpeedPage isActive={route === "speed"} />
            </div>
          )}
        </Suspense>
        {route === "settings" && (
          <ViewerSettings
            generatedAt={generatedAt}
            appVersion={appVersion}
            locale={locale}
            connection={connection}
          />
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
