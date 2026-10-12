/** Output speed per model, in tokens per second, measured from local history timings. */
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Events } from "@wailsio/runtime";
import { RefreshCwIcon } from "lucide-react";
import { formatCount, formatTokens } from "@t3tools/shared/usageFormat";
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
import * as nativeApi from "../../bindings/yousage/app";
import type {
  SpeedBucket,
  SpeedModel,
  SpeedReport,
} from "../../bindings/yousage/internal/service/models";
import type { Window } from "../../bindings/yousage/internal/accounting/models";

/** Color of models without a provider color, in the chart and the speed bars. */
const LINE_COLOR = "var(--foreground)";

/** Provider color share by rank within one provider; their count caps the chart. */
const CHART_SHADES = [100, 62, 34] as const;

/**
 * Line color of a charted model: its provider color, lighter for each more used model of the same provider.
 *
 * @param models - The charted models, by token usage.
 * @param index - Position of the model to color.
 * @returns A CSS color.
 */
function chartColor(models: readonly SpeedModel[], index: number): string {
  const model = models[index];
  const rank = models
    .slice(0, index)
    .filter((other) => other.provider === model.provider).length;
  const base =
    PROVIDER_PRESENTATION[model.provider as UsageProviderKind]?.color ??
    LINE_COLOR;
  return `color-mix(in oklab, ${base} ${CHART_SHADES[rank]}%, var(--background))`;
}

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
      >
        <PeriodToggle label={t("Speed period")} />
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label={isBusy ? t("Refreshing…") : t("Refresh usage")}
          title={`${t("Refresh")} (Ctrl+R)`}
          aria-busy={isBusy}
          disabled={isBusy}
          onClick={() => void data.refresh(true, true)}
        >
          <RefreshCwIcon aria-hidden className={cn(isBusy && "animate-spin")} />
        </Button>
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
 * Summary with the charted models as legend, their trend chart and the per-model table.
 *
 * @param props - The speed report.
 * @returns The page content.
 */
function SpeedContent({ report }: { report: SpeedReport }) {
  const outputTokens = report.models.reduce(
    (sum, model) => sum + model.outputTokens,
    0,
  );
  const generationMs = report.models.reduce(
    (sum, model) => sum + model.generationMs,
    0,
  );
  const overall = generationMs > 0 ? (outputTokens * 1000) / generationMs : 0;
  const labels = bucketLabels(report.window);
  const charted = report.models
    .filter((model) => model.requests > 0)
    .slice(0, CHART_SHADES.length);
  return (
    <div className="flex flex-col gap-10">
      <section className="grid gap-x-12 gap-y-8 md:grid-cols-[18rem_minmax(0,1fr)]">
        <div className="flex min-w-0 flex-col gap-4">
          <div className="flex flex-col gap-1">
            <span className="text-4xl font-semibold tracking-tight text-foreground tabular-nums">
              {formatSpeed(overall)}
            </span>
            <span className="text-xs text-muted-foreground">
              {t("Average output speed · {count} measured requests", {
                count: formatCount(report.measuredRequests),
              })}
            </span>
          </div>
          {charted.map((model, index) => (
            <SpeedTotal
              key={rowKey(model)}
              color={chartColor(charted, index)}
              model={model}
            />
          ))}
          {report.unmeasuredRequests > 0 && (
            <span className="text-xs text-muted-foreground">
              {t("{count} requests have no usable timing and are excluded.", {
                count: formatCount(report.unmeasuredRequests),
              })}
            </span>
          )}
        </div>
        <div className="flex min-w-0 flex-col gap-3">
          <h2 className="text-sm font-medium">
            {labels.isHourly ? t("Hourly speed") : t("Daily speed")}
          </h2>
          {charted.length > 0 ? (
            <SpeedChart
              buckets={report.buckets}
              models={charted}
              reportWindow={report.window}
            />
          ) : (
            <EmptyState>{t("No measured requests in this window.")}</EmptyState>
          )}
        </div>
      </section>
      <section className="flex min-w-0 flex-col gap-3">
        <h2 className="text-sm font-medium">{t("Models")}</h2>
        <SpeedTable models={report.models} />
      </section>
    </div>
  );
}

/**
 * One charted model in the summary column, doubling as the chart legend.
 *
 * @param props - Line color and model row.
 * @returns The total row.
 */
function SpeedTotal({ color, model }: { color: string; model: SpeedModel }) {
  return (
    <div className="flex flex-col gap-1">
      <div className="flex items-baseline justify-between gap-4">
        <span className="flex min-w-0 items-center gap-2 text-sm text-foreground">
          <span
            aria-hidden
            className="size-2 shrink-0 rounded-full"
            style={{ backgroundColor: color }}
          />
          <ModelName model={model} />
        </span>
        <span className="shrink-0 text-sm font-medium tabular-nums">
          {formatSpeed(model.tokensPerSecond)}
        </span>
      </div>
      <span className="text-xs text-muted-foreground">
        {t("Median {median} · P10–P90 {low}–{high}", {
          median: formatSpeed(model.medianTokensPerSecond),
          low: formatRate(model.p10TokensPerSecond),
          high: formatRate(model.p90TokensPerSecond),
        })}
      </span>
    </div>
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
 * Output rate of the most used measured models in every bucket of the window, each in its provider color, lighter for less used models of the same provider.
 *
 * Buckets without a measurement are drawn at zero, like idle periods on the Usage chart.
 *
 * @param props - Every bucket ascending, the charted models by token usage and the report window.
 * @returns The chart element.
 */
function SpeedChart({
  buckets,
  models,
  reportWindow,
}: {
  buckets: readonly SpeedBucket[];
  models: readonly SpeedModel[];
  reportWindow: Window;
}) {
  const labels = bucketLabels(reportWindow);
  const entries = useMemo(
    () =>
      buckets.map((bucket) =>
        models.map((model) =>
          bucket.models.find((entry) => rowKey(entry) === rowKey(model)),
        ),
      ),
    [buckets, models],
  );
  const series = useMemo(
    () =>
      models.map((model, index) => ({
        id: rowKey(model),
        color: chartColor(models, index),
        values: entries.map((row) => row[index]?.tokensPerSecond ?? 0),
      })),
    [entries, models],
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
        return models.map((model, position) => (
          <TooltipRow
            key={rowKey(model)}
            color={chartColor(models, position)}
            model={model}
            value={formatSpeed(row[position]?.tokensPerSecond ?? 0)}
          />
        ));
      }}
    />
  );
}

/**
 * One model in the chart readout: its line color, name and rate.
 *
 * @param props - Line color, model row and formatted rate.
 * @returns The readout row.
 */
function TooltipRow({
  color,
  model,
  value,
}: {
  color: string;
  model: SpeedModel;
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
        <ModelName model={model} />
      </span>
      <span className="shrink-0 text-foreground tabular-nums">{value}</span>
    </div>
  );
}

/**
 * Per-model rates ranked like the Usage breakdown, with a provider-colored bar of relative speed.
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
            <th className="py-2 pl-6 font-normal">{t("Output speed")}</th>
            <th className="py-2 pl-6 font-normal">{t("Median")}</th>
            <th className="hidden py-2 pl-6 font-normal md:table-cell">
              P10–P90
            </th>
            <th className="py-2 pl-6 font-normal">{t("Requests")}</th>
            <th className="py-2 pl-6 font-normal">{t("Tokens")}</th>
            <th className="hidden py-2 pl-6 font-normal md:table-cell">
              {t("Output")}
            </th>
          </tr>
        </thead>
        <tbody>
          {models.length === 0 ? (
            <tr>
              <td
                colSpan={8}
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
                  <td className="py-2.5 pl-6 text-foreground">
                    {formatSpeed(model.tokensPerSecond)}
                  </td>
                  <td className="py-2.5 pl-6">
                    {isMeasured ? formatRate(model.medianTokensPerSecond) : "—"}
                  </td>
                  <td className="hidden py-2.5 pl-6 md:table-cell">
                    {isMeasured
                      ? `${formatRate(model.p10TokensPerSecond)}–${formatRate(model.p90TokensPerSecond)}`
                      : "—"}
                  </td>
                  <td className="py-2.5 pl-6">
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
                  <td className="py-2.5 pl-6">
                    {formatTokens(model.totalTokens)}
                  </td>
                  <td className="hidden py-2.5 pl-6 md:table-cell">
                    {formatTokens(model.outputTokens)}
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
