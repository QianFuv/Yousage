/** Output speed per model, in tokens per second, measured from local history timings. */
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Events } from "@wailsio/runtime";
import { RefreshCwIcon } from "lucide-react";
import { formatCount, formatTokens } from "@t3tools/shared/usageFormat";
import { cn } from "../../../tools/parity/src/bridges/utils";
import { Button } from "../../../tools/parity/upstream/apps/web/src/components/ui/button";
import { PROVIDER_PRESENTATION } from "../../vendor/usageProviders";
import { ProviderInstanceIcon } from "../../vendor/ProviderInstanceIcon";
import { Notice, PageBody, PageHeader } from "../ui";
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

/** Ink of the relative speed bars, and of chart lines without a provider color. */
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
        {report && <SpeedContent report={report} />}
      </PageBody>
    </section>
  );
}

/**
 * Summary, trend chart of the selected model and the per-model table.
 *
 * @param props - The speed report.
 * @returns The page content.
 */
function SpeedContent({ report }: { report: SpeedReport }) {
  const [selectedKey, setSelectedKey] = useState<string | null>(null);
  const selected =
    report.models.find((model) => rowKey(model) === selectedKey) ??
    report.models.find((model) => model.requests > 0);
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
  return (
    <div className="flex flex-col gap-10">
      <section className="grid gap-x-12 gap-y-8 lg:grid-cols-[18rem_minmax(0,1fr)]">
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
          {selected && (
            <div className="flex flex-col gap-1 text-sm">
              <span className="flex min-w-0 items-center gap-2 text-foreground">
                <ModelName model={selected} />
              </span>
              <span className="text-xs text-muted-foreground">
                {t("Median {median} · P10–P90 {low}–{high}", {
                  median: formatSpeed(selected.medianTokensPerSecond),
                  low: formatRate(selected.p10TokensPerSecond),
                  high: formatRate(selected.p90TokensPerSecond),
                })}
              </span>
            </div>
          )}
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
          {selected ? (
            <SpeedChart
              buckets={report.buckets}
              model={selected}
              reportWindow={report.window}
            />
          ) : (
            <p className="py-16 text-center text-sm text-muted-foreground">
              {t("No measured requests in this window.")}
            </p>
          )}
        </div>
      </section>
      <section className="flex min-w-0 flex-col gap-3">
        <h2 className="text-sm font-medium">{t("Models")}</h2>
        <SpeedTable
          models={report.models}
          selectedKey={selected ? rowKey(selected) : null}
          onSelect={setSelectedKey}
        />
      </section>
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
 * The selected model's output rate in every bucket of the window, as a line in its provider color.
 *
 * Buckets without a measurement are drawn at zero, like idle periods on the Usage chart.
 *
 * @param props - Every bucket ascending, the selected model and the report window.
 * @returns The chart element.
 */
function SpeedChart({
  buckets,
  model,
  reportWindow,
}: {
  buckets: readonly SpeedBucket[];
  model: SpeedModel;
  reportWindow: Window;
}) {
  const labels = bucketLabels(reportWindow);
  const key = rowKey(model);
  const entries = useMemo(
    () =>
      buckets.map((bucket) =>
        bucket.models.find((entry) => rowKey(entry) === key),
      ),
    [buckets, key],
  );
  const series = useMemo(
    () => [
      {
        id: key,
        color:
          PROVIDER_PRESENTATION[model.provider as UsageProviderKind]?.color ??
          LINE_COLOR,
        values: entries.map((entry) => entry?.tokensPerSecond ?? 0),
      },
    ],
    [entries, key, model.provider],
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
        const entry = entries[index];
        if (!entry)
          return (
            <div className="text-muted-foreground">
              {t("No measured requests")}
            </div>
          );
        return (
          <>
            <TooltipRow
              label={t("Speed")}
              value={formatSpeed(entry.tokensPerSecond)}
            />
            <TooltipRow
              label={t("Requests")}
              value={formatCount(entry.requests)}
            />
            <TooltipRow
              label={t("Output")}
              value={formatTokens(entry.outputTokens)}
            />
          </>
        );
      }}
    />
  );
}

/**
 * One line of the chart readout.
 *
 * @param props - Label and formatted value.
 * @returns The readout row.
 */
function TooltipRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center justify-between gap-3">
      <span className="text-muted-foreground">{label}</span>
      <span className="text-foreground tabular-nums">{value}</span>
    </div>
  );
}

/**
 * Per-model rates with a relative bar; clicking a row charts that model.
 *
 * @param props - Models by output volume, the charted row and a selection handler.
 * @returns The model table.
 */
function SpeedTable({
  models,
  selectedKey,
  onSelect,
}: {
  models: readonly SpeedModel[];
  selectedKey: string | null;
  onSelect: (key: string) => void;
}) {
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
            <th className="py-2 pr-6 text-left font-normal">{t("Model")}</th>
            <th className="w-full py-2 text-left font-normal">{t("Speed")}</th>
            <th className="py-2 pl-6 font-normal">{t("Median")}</th>
            <th className="hidden py-2 pl-6 font-normal md:table-cell">
              P10–P90
            </th>
            <th className="py-2 pl-6 font-normal">{t("Requests")}</th>
            <th className="hidden py-2 pl-6 font-normal sm:table-cell">
              {t("Output")}
            </th>
          </tr>
        </thead>
        <tbody>
          {models.length === 0 ? (
            <tr>
              <td
                colSpan={6}
                className="py-6 text-center text-muted-foreground"
              >
                {t("No measured requests in this window.")}
              </td>
            </tr>
          ) : (
            models.map((model) => {
              const key = rowKey(model);
              const isSelected = key === selectedKey;
              const isMeasured = model.requests > 0;
              return (
                <tr
                  key={key}
                  aria-selected={isSelected}
                  className={cn(
                    "cursor-pointer border-b border-border/50 text-right whitespace-nowrap text-muted-foreground tabular-nums hover:bg-muted/40",
                    isSelected && "bg-muted/60",
                  )}
                  onClick={() => onSelect(key)}
                >
                  <td className="py-2.5 pr-6 text-left text-foreground">
                    <span className="flex max-w-72 min-w-0 items-center gap-2">
                      <ModelName model={model} />
                    </span>
                  </td>
                  <td className="py-2.5 text-left text-foreground">
                    <span className="flex items-center gap-3">
                      <span className="w-20 shrink-0">
                        {formatSpeed(model.tokensPerSecond)}
                      </span>
                      <span className="h-1.5 min-w-16 flex-1 overflow-hidden rounded-full bg-muted">
                        <span
                          className="block h-full rounded-full"
                          style={{
                            width: `${fastest > 0 ? (model.tokensPerSecond / fastest) * 100 : 0}%`,
                            backgroundColor: LINE_COLOR,
                          }}
                        />
                      </span>
                    </span>
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
                  <td className="hidden py-2.5 pl-6 sm:table-cell">
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
