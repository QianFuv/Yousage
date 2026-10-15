/** Relay viewer shell: the four usage pages from a snapshot, without settings or native actions. */
import { lazy, Suspense, useLayoutEffect, useState } from "react";
import {
  ChartNoAxesCombinedIcon,
  FlameIcon,
  GaugeIcon,
  TimerIcon,
} from "lucide-react";
import { useData } from "../usage/controller";
import { UsagePage } from "../usage/UsagePage";
import { NavItem } from "../NavItem";
import { t, useI18n } from "../i18n";

const LossPage = lazy(() =>
  import("../loss/LossPage").then((module) => ({ default: module.LossPage })),
);
const SpeedPage = lazy(() =>
  import("../speed/SpeedPage").then((module) => ({
    default: module.SpeedPage,
  })),
);

/** Pages offered by the viewer, in navigation order. */
type ViewerRoute = "statistics" | "quota" | "loss" | "speed";

/**
 * Format the snapshot time for the banner in the viewer's locale.
 *
 * @param instant - RFC 3339 time the desktop generated the snapshot.
 * @param locale - Viewing device locale.
 * @returns A short date and time, or the raw value when it cannot be parsed.
 */
export function formatSnapshotTime(instant: string, locale: string): string {
  const date = new Date(instant);
  if (Number.isNaN(date.getTime())) return instant;
  return new Intl.DateTimeFormat(locale, {
    month: "numeric",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date);
}

/**
 * Read-only pages rendered from the latest relay snapshot.
 *
 * @param props - Snapshot time and the viewing device locale.
 * @returns The viewer layout.
 */
export function WebViewer({
  generatedAt,
  locale,
}: {
  generatedAt: string;
  locale: string;
}) {
  useI18n();
  const data = useData();
  const [route, setRoute] = useState<ViewerRoute>("statistics");
  const [visited, setVisited] = useState<Set<ViewerRoute>>(() => new Set());
  useLayoutEffect(() => {
    document.documentElement.dataset.density = data.display.value.density;
  }, [data.display.value.density]);
  /** Show a page and keep it mounted afterwards, like the desktop navigation. */
  const navigate = (next: ViewerRoute) => {
    setVisited((current) => new Set([...current, next]));
    setRoute(next);
  };
  return (
    <div className="desktop-layout" data-density={data.display.value.density}>
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
        </div>
      </nav>
      <main tabIndex={-1} aria-label={t("Page content")}>
        <p className="viewer-banner" role="status">
          {t("Snapshot from the desktop app · updated {time}", {
            time: formatSnapshotTime(generatedAt, locale),
          })}
        </p>
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
      </main>
    </div>
  );
}

/**
 * A full-page message shown instead of the viewer or share page.
 *
 * @param props - Title and explanation.
 * @returns The message page.
 */
export function RelayProblem({
  title,
  detail,
}: {
  title: string;
  detail: string;
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
    </div>
  );
}
