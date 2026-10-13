/** Usage navigation presents observations; pricing, source and display configuration live in Settings. */
import { useEffect, useRef, useState, useMemo, lazy, Suspense } from "react";
import {
  enumerateDays,
  enumerateHourStarts,
} from "@t3tools/shared/usageFormat";
import {
  CHATGPT_USAGE_URL,
  usesChatGptSharing,
  type LimitPresentations,
} from "@t3tools/shared/usageLimits";
const UsageLimitsPooled = lazy(() =>
  import("../../vendor/UsageLimitsPooled").then((module) => ({
    default: module.UsageLimitsPooled,
  })),
);
const UsageStatsPanel = lazy(() =>
  import("./UsageStatsPanel").then((module) => ({
    default: module.UsageStatsPanel,
  })),
);
import { RefreshCwIcon } from "lucide-react";
import { EmptyState, Notice, PageBody, PageHeader } from "../ui";
import { cn } from "../../../tools/parity/src/bridges/utils";
import { Button } from "../../../tools/parity/upstream/apps/web/src/components/ui/button";
import {
  Toggle,
  ToggleGroup,
} from "../../../tools/parity/upstream/apps/web/src/components/ui/toggle-group";
import { mergeAnsweredUsage } from "./data";
import { indexCycleValues } from "./CycleValue";
import { t } from "../i18n";
import { useData } from "./controller";
import { PeriodToggle } from "./PeriodToggle";
import * as nativeApi from "../../bindings/yousage/internal/desktop/app";

/** Preserve null All and explicit empty selection while exposing coverage inside the environment filter. */
export function UsagePage({
  isActive,
  view,
  onSettings,
}: {
  isActive: boolean;
  view: "limits" | "statistics";
  onSettings: (section: "pricing" | "sources", model?: string) => void;
}) {
  const data = useData();
  const metric =
    view === "limits"
      ? "limits"
      : data.display.value.metric === "tokens"
        ? "tokens"
        : "cost";
  useEffect(
    () => data.setUsageActive(isActive, metric),
    [data.setUsageActive, isActive, metric],
  );
  const refreshRef = useRef(data.refresh);
  refreshRef.current = data.refresh;
  const [limitsNow, setLimitsNow] = useState(Date.now);
  const environmentKey =
    data.snapshot?.environments
      .filter(
        (value) =>
          data.selected === null || data.selected.has(value.environmentId),
      )
      .map((value) => `${value.environmentId}:${value.connected}`)
      .join(";") ?? "";
  const selectionKey =
    data.selected === null ? "all" : [...data.selected].sort().join(";");
  useEffect(() => {
    if (!isActive || !environmentKey) return;
    setLimitsNow(Date.now());
    void refreshRef.current(false, true);
    /** Refresh on actual foreground activity; hidden/minimized settings never start automatic scans. */
    const activity = () => {
      if (document.visibilityState === "visible")
        void refreshRef.current(false, true);
    };
    window.addEventListener("focus", activity);
    document.addEventListener("visibilitychange", activity);
    return () => {
      window.removeEventListener("focus", activity);
      document.removeEventListener("visibilitychange", activity);
    };
  }, [
    isActive,
    metric,
    data.display.value.windowDays,
    selectionKey,
    environmentKey,
    data.display.value.shouldAutoRefresh,
  ]);
  const selected = useMemo(
    () =>
      data.snapshot?.environments.filter(
        (value) =>
          data.selected === null || data.selected.has(value.environmentId),
      ) ?? [],
    [data.snapshot, data.selected],
  );
  const merged = useMemo(
    () => mergeAnsweredUsage(metric === "limits" ? [] : selected),
    [selected, metric],
  );
  const usageWindow = data.snapshot?.window;
  const answered = selected.filter((value) => value.summary !== null).length;
  const stillReporting = selected.filter(
    (value) => value.summary === null && !value.error,
  ).length;
  const presentations: LimitPresentations = new Map(
    selected.map((value) => [
      value.environmentId,
      {
        entry: { target: { label: value.label } },
        serverConfig: value.serverConfig,
      },
    ]),
  );
  const sourceMessages = [
    ...new Set(
      selected.flatMap(
        (value) =>
          value.summary?.sources.flatMap((source) =>
            source.message &&
            (source.status === "partial" || source.status === "failed")
              ? [source.message]
              : [],
          ) ?? [],
      ),
    ),
  ];
  const hasNoHistory =
    selected.length > 0 &&
    selected.every(
      (value) => !value.summary || value.summary.sources.length === 0,
    );
  const statusLabel = data.isBusy
    ? answered === 0
      ? t("Scanning…")
      : t("Refreshing…")
    : data.snapshot
      ? selected.length === 0
        ? t("No environments selected")
        : answered === 0 && metric !== "limits"
          ? t("Unavailable")
          : null
      : t("Loading…");
  return (
    <section
      className="usage-page"
      aria-label={view === "limits" ? t("Quota") : t("Usage")}
    >
      <PageHeader
        title={view === "limits" ? t("Quota") : t("Usage")}
        status={statusLabel}
      >
        {view === "statistics" && (
          <ToggleGroup
            aria-label={t("Usage metric")}
            variant="segmented"
            value={[metric]}
            onValueChange={(next) => {
              const value = next[0];
              if (value === "cost" || value === "tokens")
                void data.patchDisplay({ metric: value }).catch(() => {});
            }}
          >
            <Toggle value="cost">{t("Cost")}</Toggle>
            <Toggle value="tokens">{t("Tokens")}</Toggle>
          </ToggleGroup>
        )}
        {view === "statistics" && <PeriodToggle label={t("Usage period")} />}
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label={
            data.isBusy
              ? t("Refreshing…")
              : metric === "limits"
                ? t("Refresh limits")
                : t("Refresh usage")
          }
          title={`${t("Refresh")} (Ctrl+R)`}
          aria-busy={data.isBusy}
          disabled={data.isBusy}
          onClick={() => {
            setLimitsNow(Date.now());
            void data.refresh(true, true);
          }}
        >
          <RefreshCwIcon
            aria-hidden
            className={cn(data.isBusy && "animate-spin")}
          />
        </Button>
      </PageHeader>
      <PageBody>
        {(data.error || selected.some((environment) => environment.error)) && (
          <div className="flex flex-col gap-2">
            {data.error && <Notice tone="error">{data.error}</Notice>}
            {selected
              .filter((environment) => environment.error)
              .map((environment) => (
                <Notice tone="error" key={environment.environmentId}>
                  {environment.label}: {environment.error}
                </Notice>
              ))}
          </div>
        )}
        {metric === "limits" ? (
          <Suspense fallback={null}>
            <UsageLimitsPooled
              presentations={presentations}
              now={limitsNow}
              cycleValues={indexCycleValues(selected)}
            />
          </Suspense>
        ) : (
          usageWindow && (
            <>
              {((answered > 0 && stillReporting > 0) ||
                sourceMessages.length > 0 ||
                hasNoHistory) && (
                <div className="flex flex-col gap-2">
                  {answered > 0 && stillReporting > 0 && (
                    <Notice>
                      {t(
                        "Some environments are still reporting. Totals include {count} available environments.",
                        { count: answered },
                      )}
                    </Notice>
                  )}
                  {sourceMessages.map((message) => (
                    <Notice key={message}>{message}</Notice>
                  ))}
                  {hasNoHistory && (
                    <Notice
                      action={
                        <Button
                          size="xs"
                          variant="outline"
                          onClick={() => onSettings("sources")}
                        >
                          {t("Review data sources")}
                        </Button>
                      }
                    >
                      {t("No saved history has been scanned yet.")}
                    </Notice>
                  )}
                </div>
              )}
              {answered === 0 ? (
                <EmptyState>
                  {selected.length === 0
                    ? t("Select an environment to view usage.")
                    : stillReporting > 0
                      ? t("Waiting for usage reports…")
                      : t(
                          "No usage report is available. Refresh or review the selected environments.",
                        )}
                </EmptyState>
              ) : (
                <Suspense
                  fallback={
                    <EmptyState>{t("Loading usage statistics…")}</EmptyState>
                  }
                >
                  <UsageStatsPanel
                    merged={merged}
                    environments={selected}
                    metric={metric === "tokens" ? "tokens" : "cost"}
                    hasChatGptSharing={selected.some((value) =>
                      value.serverConfig?.providers.some(usesChatGptSharing),
                    )}
                    onManageChatGpt={() =>
                      void nativeApi.OpenExternal(CHATGPT_USAGE_URL)
                    }
                    chartWindow={{
                      days: enumerateDays(
                        usageWindow.sinceDay,
                        usageWindow.untilDay,
                      ),
                      hours:
                        usageWindow.sinceTimeMs !== undefined &&
                        usageWindow.untilTimeMs !== undefined
                          ? enumerateHourStarts(
                              new Date(usageWindow.sinceTimeMs).toISOString(),
                              new Date(usageWindow.untilTimeMs).toISOString(),
                            )
                          : [],
                      resolution: usageWindow.resolution,
                      timeZone: usageWindow.timeZone,
                      referenceTime:
                        usageWindow.untilTimeMs !== undefined
                          ? new Date(usageWindow.untilTimeMs).toISOString()
                          : undefined,
                    }}
                    onSetPrice={(model) => onSettings("pricing", model)}
                  />
                </Suspense>
              )}
            </>
          )
        )}
      </PageBody>
    </section>
  );
}
