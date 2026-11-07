/** Usage navigation presents observations; source and display configuration live in Settings. */
import { useEffect, useState, useMemo, lazy, Suspense } from "react";
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
import { EmptyState, Notice, PageBody, PageHeader } from "../ui";
import { Button } from "../../../tools/parity/upstream/apps/web/src/components/ui/button";
import {
  Toggle,
  ToggleGroup,
} from "../../../tools/parity/upstream/apps/web/src/components/ui/toggle-group";
import { mergeAnsweredUsage } from "./data";
import { hour12ForTimestampFormat } from "./host";
import { indexCycleValues } from "./CycleValue";
import { t } from "../i18n";
import { useData } from "./controller";
import { RefreshButton, useRefreshStatus } from "./refreshStatus";
import { localizeEnvironmentError } from "./notices";
import { PeriodMenu, PeriodToggle } from "./PeriodToggle";
import { ChoiceMenu } from "./ChoiceMenu";
import { useIsReadOnly } from "../web/readOnly";
import { ViewerCommitMarker } from "../web/renderAcknowledgment";
import * as nativeApi from "../../bindings/yousage/internal/desktop/app";

/**
 * Usage statistics or quota of this device, read from the latest snapshot.
 *
 * @param props - Whether the page is visible, which view to show and how to open data source settings.
 * @returns The Usage or Quota page.
 */
export function UsagePage({
  isActive,
  view,
  onSettings,
}: {
  isActive: boolean;
  view: "limits" | "statistics";
  onSettings: (section: "sources") => void;
}) {
  const data = useData();
  const isReadOnly = useIsReadOnly();
  /** Tells a relay viewer that this page rendered its loaded data, in place of loading fallbacks. */
  const commitMarker = isActive && data.snapshot && <ViewerCommitMarker />;
  const metric =
    view === "limits"
      ? "limits"
      : data.display.value.metric === "tokens"
        ? "tokens"
        : "cost";
  const [limitsNow, setLimitsNow] = useState(Date.now);
  useEffect(() => {
    if (isActive) setLimitsNow(Date.now());
  }, [isActive, data.snapshot]);
  const environments = useMemo(
    () => data.snapshot?.environments ?? [],
    [data.snapshot],
  );
  const merged = useMemo(
    () => mergeAnsweredUsage(metric === "limits" ? [] : environments),
    [environments, metric],
  );
  const usageWindow = data.snapshot?.window;
  const answered = environments.filter(
    (value) => value.summary !== null,
  ).length;
  const stillReporting = environments.filter(
    (value) => value.summary === null && !value.error,
  ).length;
  const presentations: LimitPresentations = new Map(
    environments.map((value) => [
      value.environmentId,
      {
        entry: { target: { label: value.label } },
        serverConfig: value.serverConfig,
      },
    ]),
  );
  const sourceMessages = [
    ...new Set(
      environments.flatMap(
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
    environments.length > 0 &&
    environments.every(
      (value) => !value.summary || value.summary.sources.length === 0,
    );
  const refreshStatus = useRefreshStatus();
  const statusLabel =
    data.isBusy && answered === 0 && metric !== "limits"
      ? t("Scanning…")
      : data.snapshot
        ? answered === 0 && metric !== "limits" && !data.isBusy
          ? t("Unavailable")
          : refreshStatus
        : t("Loading…");
  return (
    <section
      className="usage-page"
      aria-label={view === "limits" ? t("Quota") : t("Usage")}
    >
      <PageHeader
        title={view === "limits" ? t("Quota") : t("Usage")}
        status={statusLabel}
        actions={<RefreshButton />}
        phoneControls={
          view === "statistics" && (
            <>
              <ChoiceMenu
                label={t("Usage metric")}
                value={metric === "tokens" ? "tokens" : "cost"}
                choices={[
                  { value: "cost", label: t("Cost") },
                  { value: "tokens", label: t("Tokens") },
                ]}
                onChange={(value) =>
                  void data.patchDisplay({ metric: value }).catch(() => {})
                }
              />
              <PeriodMenu label={t("Usage period")} />
            </>
          )
        }
      >
        {view === "statistics" && (
          <>
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
            <PeriodToggle label={t("Usage period")} />
          </>
        )}
      </PageHeader>
      <PageBody>
        {(data.error ||
          environments.some((environment) => environment.error)) && (
          <div className="flex flex-col gap-2">
            {data.error && <Notice tone="error">{data.error}</Notice>}
            {environments
              .filter((environment) => environment.error)
              .map((environment) => (
                <Notice tone="error" key={environment.environmentId}>
                  {localizeEnvironmentError(environment)}
                </Notice>
              ))}
          </div>
        )}
        {metric === "limits" ? (
          <Suspense fallback={null}>
            <UsageLimitsPooled
              presentations={presentations}
              now={limitsNow}
              cycleValues={indexCycleValues(environments)}
            />
            {commitMarker}
          </Suspense>
        ) : (
          usageWindow && (
            <>
              {(sourceMessages.length > 0 || hasNoHistory) && (
                <div className="flex flex-col gap-2">
                  {sourceMessages.map((message) => (
                    <Notice key={message}>{message}</Notice>
                  ))}
                  {hasNoHistory && (
                    <Notice
                      action={
                        !isReadOnly && (
                          <Button
                            size="xs"
                            variant="outline"
                            onClick={() => onSettings("sources")}
                          >
                            {t("Review data sources")}
                          </Button>
                        )
                      }
                    >
                      {t("No saved history has been scanned yet.")}
                    </Notice>
                  )}
                </div>
              )}
              {answered === 0 ? (
                <>
                  <EmptyState>
                    {stillReporting > 0
                      ? t("Waiting for usage reports…")
                      : t("No usage report is available.")}
                  </EmptyState>
                  {commitMarker}
                </>
              ) : (
                <Suspense
                  fallback={
                    <EmptyState>{t("Loading usage statistics…")}</EmptyState>
                  }
                >
                  <UsageStatsPanel
                    merged={merged}
                    environments={environments}
                    metric={metric === "tokens" ? "tokens" : "cost"}
                    hasChatGptSharing={environments.some((value) =>
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
                      hour12: hour12ForTimestampFormat(
                        data.display.value.timestampFormat,
                      ),
                    }}
                  />
                  {commitMarker}
                </Suspense>
              )}
            </>
          )
        )}
      </PageBody>
    </section>
  );
}
