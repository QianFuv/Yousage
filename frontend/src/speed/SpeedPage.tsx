/** Output speed per model, in tokens per second, measured from local history timings. */
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Events } from "@wailsio/runtime";
import { RefreshCwIcon } from "lucide-react";
import {
  formatCount,
  formatPercent,
  formatTokens,
} from "@t3tools/shared/usageFormat";
import { cn } from "../../../tools/parity/src/bridges/utils";
import { Button } from "../../../tools/parity/upstream/apps/web/src/components/ui/button";
import { PROVIDER_PRESENTATION } from "../../vendor/usageProviders";
import { ProviderInstanceIcon } from "../../vendor/ProviderInstanceIcon";
import { EmptyState, Notice, PageBody, PageHeader } from "../ui";
import { msg, t } from "../i18n";
import { useData } from "../usage/controller";
import type { UsageProviderKind } from "../usage/contracts";
import { PeriodToggle, usePeriodDays } from "../usage/PeriodToggle";
import { bucketLabels } from "../usage/chartAxis";
import { SeriesChart } from "../usage/SeriesChart";
import { Metric } from "../usage/UsageStatsPanel";
import {
  orderProviders,
  ProviderSummaryRow,
} from "../usage/ProviderSummaryRow";
import * as nativeApi from "../../bindings/yousage/internal/desktop/app";
import type {
  SpeedBucket,
  SpeedModel,
  SpeedProvider,
  SpeedReport,
  SpeedStats,
} from "../../bindings/yousage/internal/service/models";
import type { Window } from "../../bindings/yousage/internal/accounting/models";

/** Color of models without a provider color in the speed bars. */
const LINE_COLOR = "var(--foreground)";

/** Labels of the faster billing tiers; standard rows carry no badge. */
const TIER_LABELS: Record<string, string> = {
  fast: msg("Fast"),
  ultrafast: msg("Ultrafast"),
};

/**
 * Identity of one provider, model and billing tier row.
 *
 * @param row - A model row or bucket entry.
 * @returns A stable key.
 */
function rowKey(row: {
  provider: string;
  model: string;
  speed: string;
}): string {
  return `${row.provider}\u0000${row.model}\u0000${row.speed}`;
}

/**
 * Format a rate with one decimal below 100 and whole numbers above.
 *
 * @param tokensPerSecond - Output tokens per second.
 * @returns The rate without a unit.
 */
function formatRate(tokensPerSecond: number): string {
  return tokensPerSecond >= 100
    ? formatCount(Math.round(tokensPerSecond))
    : tokensPerSecond.toFixed(1);
}

/**
 * Format a rate with its unit.
 *
 * @param tokensPerSecond - Output tokens per second.
 * @returns `42.5 tok/s`, or a dash without a measurement.
 */
function formatSpeed(tokensPerSecond: number): string {
  return tokensPerSecond > 0
    ? t("{rate} tok/s", { rate: formatRate(tokensPerSecond) })
    : "—";
}

/**
 * Format a latency with milliseconds below a second and one decimal of seconds above.
 *
 * @param milliseconds - Elapsed milliseconds.
 * @returns `850 ms` or `1.2 s`, or a dash without a measurement.
 */
function formatLatency(milliseconds: number): string {
  if (milliseconds <= 0) return "—";
  return milliseconds < 1000
    ? t("{value} ms", { value: formatCount(Math.round(milliseconds)) })
    : t("{value} s", { value: (milliseconds / 1000).toFixed(1) });
}

/**
 * Speed page: per-model output rates over the shared period, with a per-bucket trend.
 *
 * @param props - Whether the page is the visible route.
 * @returns The speed page.
 */
export function SpeedPage({ isActive }: { isActive: boolean }) {
  const data = useData();
  const days = usePeriodDays();
  const zone = Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  const [report, setReport] = useState<SpeedReport | null>(null);
  const [error, setError] = useState("");
  const [isLoading, setIsLoading] = useState(false);
  const request = useRef(0);

  /** Read the selected window, ignoring answers to superseded selections. */
  const load = useCallback(async () => {
    const identity = ++request.current;
    setIsLoading(true);
    try {
      const value = await nativeApi.GetSpeedReport(days, zone);
      if (identity !== request.current) return;
      setReport(value);
      setError("");
    } catch (cause) {
      if (identity === request.current) setError(String(cause));
    } finally {
      if (identity === request.current) setIsLoading(false);
    }
  }, [days, zone]);

  useEffect(() => {
    if (!isActive) return;
    void load();
    return Events.On("usage-invalidated", () => void load());
  }, [isActive, load]);

  const isBusy = data.isBusy || isLoading;
  return (
    <section className="speed-page" aria-label={t("Speed")}>
      <PageHeader
        title={t("Speed")}
        status={data.isBusy ? t("Refreshing…") : !report ? t("Loading…") : null}
        actions={
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label={isBusy ? t("Refreshing…") : t("Refresh usage")}
            title={`${t("Refresh")} (Ctrl+R)`}
            aria-busy={isBusy}
            disabled={isBusy}
            onClick={() => void data.refresh(true, true)}
          >
            <RefreshCwIcon
              aria-hidden
              className={cn(isBusy && "animate-spin")}
            />
          </Button>
        }
      >
        <PeriodToggle label={t("Speed period")} />
      </PageHeader>
      <PageBody>
        {error && <Notice tone="error">{error}</Notice>}
        {report ? (
          <SpeedContent report={report} />
        ) : (
          !error && <EmptyState>{t("Loading speed statistics…")}</EmptyState>
        )}
      </PageBody>
    </section>
  );
}

/**
 * Summary per provider, their trend chart, latency and context metrics and the per-model table.
 *
 * @param props - The speed report.
 * @returns The page content.
 */
function SpeedContent({ report }: { report: SpeedReport }) {
  const labels = bucketLabels(report.window);
  const providers = orderProviders(
    report.providers,
    (provider) => provider.requests > 0,
  );
  return (
    <div className="flex flex-col gap-10">
      <section className="grid gap-x-12 gap-y-8 md:grid-cols-[18rem_minmax(0,1fr)]">
        <div className="flex min-w-0 flex-col gap-4">
          <div className="flex flex-col gap-1">
            <span className="text-4xl font-semibold tracking-tight text-foreground tabular-nums">
              {formatSpeed(report.overall.tokensPerSecond)}
            </span>
            <span className="text-xs text-muted-foreground">
              {t("Average output speed · {count} measured requests", {
                count: formatCount(report.measuredRequests),
              })}
            </span>
          </div>
          {providers.map((provider) => (
            <ProviderSummaryRow
              key={provider.provider}
              provider={provider.provider}
              count={t(
                provider.requests === 1
                  ? msg("{count} request")
                  : msg("{count} requests"),
                { count: formatCount(provider.requests) },
              )}
              value={formatSpeed(provider.tokensPerSecond)}
              detail={t("TTFT {ttft} · generation {rate}", {
                ttft: formatLatency(provider.medianTtftMs),
                rate: formatSpeed(provider.decodeTokensPerSecond),
              })}
            />
          ))}
        </div>
        <div className="flex min-w-0 flex-col gap-3">
          <h2 className="text-sm font-medium">
            {labels.isHourly ? t("Hourly speed") : t("Daily speed")}
          </h2>
          {providers.length > 0 ? (
            <SpeedChart
              buckets={report.buckets}
              providers={providers}
              reportWindow={report.window}
            />
          ) : (
            <EmptyState>{t("No measured requests in this window.")}</EmptyState>
          )}
        </div>
      </section>
      <LatencyMetrics stats={report.overall} />
      <section className="flex min-w-0 flex-col gap-3">
        <h2 className="text-sm font-medium">{t("Models")}</h2>
        <SpeedTable models={report.models} />
      </section>
    </div>
  );
}

/**
 * Window-wide time to first token, generation speed, latency and context, like the Usage totals.
 *
 * @param props - Statistics of every request in the window.
 * @returns The metrics section.
 */
function LatencyMetrics({ stats }: { stats: SpeedStats }) {
  const requests = stats.requests + stats.unmeasuredRequests;
  return (
    <section className="flex flex-col gap-2">
      <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
        <h2 className="text-sm font-medium">{t("Latency and context")}</h2>
        <span className="text-xs text-muted-foreground">
          {t("First-token timing from {count} of {total} requests", {
            count: formatCount(stats.ttftRequests),
            total: formatCount(requests),
          })}
        </span>
      </div>
      <div className="grid grid-cols-2 gap-x-6 gap-y-4 py-1 md:grid-cols-4">
        <Metric
          label={t("Median TTFT")}
          value={formatLatency(stats.medianTtftMs)}
        />
        <Metric label={t("P90 TTFT")} value={formatLatency(stats.p90TtftMs)} />
        <Metric
          label={t("Generation speed")}
          value={formatSpeed(stats.decodeTokensPerSecond)}
        />
        <Metric
          label={t("Cache hit rate")}
          value={requests > 0 ? formatPercent(stats.cacheHitRate) : "—"}
        />
        <Metric
          label={t("Median latency")}
          value={formatLatency(stats.medianLatencyMs)}
        />
        <Metric
          label={t("P90 latency")}
          value={formatLatency(stats.p90LatencyMs)}
        />
        <Metric
          label={t("Context per request")}
          value={
            requests > 0
              ? formatTokens(Math.round(stats.contextTokensPerRequest))
              : "—"
          }
        />
        <Metric
          label={t("Output per request")}
          value={
            requests > 0
              ? formatTokens(Math.round(stats.outputTokensPerRequest))
              : "—"
          }
        />
      </div>
    </section>
  );
}

/**
 * Provider icon, model name and a badge for faster billing tiers.
 *
 * @param props - The model row.
 * @returns The inline name elements.
 */
function ModelName({ model }: { model: SpeedModel }) {
  const presentation =
    PROVIDER_PRESENTATION[model.provider as UsageProviderKind];
  const tier = TIER_LABELS[model.speed];
  return (
    <>
      {presentation && (
        <ProviderInstanceIcon
          driverKind={presentation.driverKind}
          displayName={presentation.label}
          iconClassName="size-3.5"
        />
      )}
      <span className="truncate">{model.model || t("Unknown model")}</span>
      {tier && (
        <span className="shrink-0 rounded-sm border border-border px-1 text-3xs text-muted-foreground uppercase">
          {t(tier)}
        </span>
      )}
    </>
  );
}

/**
 * Output rate of every measured provider in every bucket of the window, each in its provider color.
 *
 * Buckets without a measurement are drawn at zero, like idle periods on the Usage chart.
 *
 * @param props - Every bucket ascending, the measured providers and the report window.
 * @returns The chart element.
 */
function SpeedChart({
  buckets,
  providers,
  reportWindow,
}: {
  buckets: readonly SpeedBucket[];
  providers: readonly (SpeedProvider & { provider: UsageProviderKind })[];
  reportWindow: Window;
}) {
  const labels = bucketLabels(reportWindow);
  const entries = useMemo(
    () =>
      buckets.map((bucket) =>
        providers.map(({ provider }) =>
          bucket.providers.find((entry) => entry.provider === provider),
        ),
      ),
    [buckets, providers],
  );
  const series = useMemo(
    () =>
      providers.map(({ provider }, index) => ({
        id: provider,
        color: PROVIDER_PRESENTATION[provider].color,
        values: entries.map((row) => row[index]?.tokensPerSecond ?? 0),
      })),
    [entries, providers],
  );
  return (
    <SeriesChart
      periods={buckets.map((bucket) => bucket.label)}
      series={series}
      format={formatRate}
      ariaLabel={labels.isHourly ? t("Hourly speed") : t("Daily speed")}
      formatPeriod={labels.axis}
      formatTooltipPeriod={labels.full}
      renderTooltip={(index) => {
        const row = entries[index];
        if (row.every((entry) => !entry))
          return (
            <div className="text-muted-foreground">
              {t("No measured requests")}
            </div>
          );
        return providers.map(({ provider }, position) => (
          <TooltipRow
            key={provider}
            color={PROVIDER_PRESENTATION[provider].color}
            label={PROVIDER_PRESENTATION[provider].label}
            value={formatSpeed(row[position]?.tokensPerSecond ?? 0)}
          />
        ));
      }}
    />
  );
}

/**
 * One provider in the chart readout: its line color, name and rate.
 *
 * @param props - Line color, provider name and formatted rate.
 * @returns The readout row.
 */
function TooltipRow({
  color,
  label,
  value,
}: {
  color: string;
  label: string;
  value: string;
}) {
  return (
    <div className="flex items-center justify-between gap-3">
      <span className="flex min-w-0 items-center gap-1.5 text-muted-foreground">
        <span
          aria-hidden
          className="size-2 shrink-0 rounded-full"
          style={{ backgroundColor: color }}
        />
        {label}
      </span>
      <span className="shrink-0 text-foreground tabular-nums">{value}</span>
    </div>
  );
}

/**
 * Per-model rates, first-token time, latency and cache use ranked like the Usage breakdown,
 * with a provider-colored bar of relative speed.
 *
 * @param props - Models by token usage.
 * @returns The model table.
 */
function SpeedTable({ models }: { models: readonly SpeedModel[] }) {
  const fastest = Math.max(0, ...models.map((model) => model.tokensPerSecond));
  return (
    <div
      className="usage-table-scroll"
      tabIndex={0}
      aria-label={t("Model speed table")}
    >
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b border-border text-right text-xs text-muted-foreground">
            <th className="py-2 pr-3 text-left font-normal">#</th>
            <th className="w-full py-2 text-left font-normal">{t("Model")}</th>
            <th className="py-2 pl-4 font-normal">{t("Output speed")}</th>
            <th className="hidden py-2 pl-4 font-normal md:table-cell">
              {t("Generation speed")}
            </th>
            <th className="py-2 pl-4 font-normal">TTFT</th>
            <th className="hidden py-2 pl-4 font-normal md:table-cell">
              {t("Latency")}
            </th>
            <th className="hidden py-2 pl-4 font-normal md:table-cell">
              {t("Cache hit")}
            </th>
            <th className="py-2 pl-4 font-normal">{t("Requests")}</th>
            <th className="py-2 pl-4 font-normal">{t("Tokens")}</th>
          </tr>
        </thead>
        <tbody>
          {models.length === 0 ? (
            <tr>
              <td
                colSpan={9}
                className="py-6 text-center text-muted-foreground"
              >
                {t("No measured requests in this window.")}
              </td>
            </tr>
          ) : (
            models.map((model, index) => {
              const isMeasured = model.requests > 0;
              return (
                <tr
                  key={rowKey(model)}
                  className="border-b border-border/50 text-right whitespace-nowrap text-muted-foreground tabular-nums transition-colors hover:bg-muted/50"
                >
                  <td className="py-2.5 pr-3 text-left text-xs">{index + 1}</td>
                  <td className="py-2.5 text-left text-foreground">
                    <span className="flex min-w-0 items-center gap-2">
                      <ModelName model={model} />
                    </span>
                    <div aria-hidden className="mt-1.5 h-0.5 max-w-48">
                      <div
                        className="h-full rounded-full"
                        style={{
                          width:
                            isMeasured && fastest > 0
                              ? `max(0.5rem, ${(model.tokensPerSecond / fastest) * 100}%)`
                              : 0,
                          backgroundColor:
                            PROVIDER_PRESENTATION[
                              model.provider as UsageProviderKind
                            ]?.color ?? LINE_COLOR,
                        }}
                      />
                    </div>
                  </td>
                  <td
                    className="py-2.5 pl-4 text-foreground"
                    title={
                      isMeasured
                        ? t("Median {median} · P10–P90 {low}–{high}", {
                            median: formatSpeed(model.medianTokensPerSecond),
                            low: formatRate(model.p10TokensPerSecond),
                            high: formatRate(model.p90TokensPerSecond),
                          })
                        : undefined
                    }
                  >
                    {formatSpeed(model.tokensPerSecond)}
                  </td>
                  <td className="hidden py-2.5 pl-4 md:table-cell">
                    {formatSpeed(model.decodeTokensPerSecond)}
                  </td>
                  <td className="py-2.5 pl-4">
                    {formatLatency(model.medianTtftMs)}
                  </td>
                  <td className="hidden py-2.5 pl-4 md:table-cell">
                    {formatLatency(model.medianLatencyMs)}
                  </td>
                  <td className="hidden py-2.5 pl-4 md:table-cell">
                    {model.inputTokens > 0
                      ? formatPercent(model.cacheHitRate)
                      : "—"}
                  </td>
                  <td className="py-2.5 pl-4">
                    {formatCount(model.requests)}
                    {model.unmeasuredRequests > 0 && (
                      <span
                        className="ms-1 text-muted-foreground/70"
                        title={t("{count} without timing", {
                          count: formatCount(model.unmeasuredRequests),
                        })}
                      >
                        +{formatCount(model.unmeasuredRequests)}
                      </span>
                    )}
                  </td>
                  <td className="py-2.5 pl-4">
                    {formatTokens(model.totalTokens)}
                  </td>
                </tr>
              );
            })
          )}
        </tbody>
      </table>
    </div>
  );
}
