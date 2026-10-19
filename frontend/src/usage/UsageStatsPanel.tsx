/** Complete controlled statistics content using the pinned chart, mixes and model-detail algorithms. */
import { useData } from "./controller";
import { formatDayShort, formatHourShort, msg, t } from "../i18n";
import { useState } from "react";
import { InfoIcon, ChevronRightIcon } from "lucide-react";
import { Button } from "../../../tools/parity/upstream/apps/web/src/components/ui/button";
import {
  formatCount,
  formatPercent,
  formatTokens,
  formatUsd,
} from "@t3tools/shared/usageFormat";
import {
  isModelCostUnknown,
  type MergedUsage,
  type DailyTotals,
  type HourlyTotals,
} from "@t3tools/shared/usageMerge";
import { UsageProviderChart } from "../../vendor/UsageProviderChart";
import { UsageShareBar } from "../../vendor/UsageShareBar";
import {
  SpeedPremium,
  UsageModelDialog,
  type UsageChartWindow,
} from "../../vendor/UsageModelDialog";
import {
  costTypeSegments,
  modelShare,
  sortModelsByTokens,
  speedCostSegments,
  tokenTypeSegments,
} from "../../vendor/usageBreakdown";
import {
  providerPresentation,
  providersWithUsage,
} from "../../vendor/usageProviders";
import { ProviderInstanceIcon } from "../../vendor/ProviderInstanceIcon";
import {
  Toggle,
  ToggleGroup,
} from "../../../tools/parity/upstream/apps/web/src/components/ui/toggle-group";
import {
  Popover,
  PopoverPopup,
  PopoverTrigger,
} from "../../../tools/parity/upstream/apps/web/src/components/ui/popover";
import { InlineButton } from "../../../tools/parity/upstream/apps/web/src/components/ui/button";
import type { EnvironmentUsageStatus } from "./data";
import type { UsageProviderKind } from "./contracts";
import { ReceiptButton } from "../receipt/ReceiptButton";

/** Reuse the harness mark without treating model identity as a globally unique key. */
function ProviderMark({
  provider,
  className,
}: {
  provider: UsageProviderKind;
  className: string;
}) {
  const value = providerPresentation(provider);
  return (
    <ProviderInstanceIcon
      driverKind={value.driverKind}
      displayName={value.label}
      iconClassName={className}
    />
  );
}
/** Present an original totals label/value pair. */
export function Metric({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex min-w-0 flex-col gap-0.5">
      <span className="text-xs text-muted-foreground">{label}</span>
      <span className="text-base font-medium text-foreground tabular-nums">
        {value}
      </span>
    </div>
  );
}

/** Render all summary, chart, category and breakdown interactions from the same selected aggregate. */
export function UsageStatsPanel({
  merged,
  environments,
  chartWindow,
  metric,
  hasChatGptSharing,
  onManageChatGpt,
  onSetPrice,
}: {
  hasChatGptSharing: boolean;
  onManageChatGpt: () => void;
  merged: MergedUsage;
  environments: readonly EnvironmentUsageStatus[];
  chartWindow: UsageChartWindow;
  metric: "cost" | "tokens";
  onSetPrice: (model?: string) => void;
}) {
  const data = useData();
  const breakdown = data.display.value.breakdown;
  const setBreakdown = (value: "model" | "time") =>
    void data.patchDisplay({ breakdown: value }).catch(() => {});
  const [selectedModelKey, setSelectedModelKey] = useState<string | null>(null);
  const activeProviders = providersWithUsage(merged.providers);
  const isPast24Hours = chartWindow.resolution === "hour";
  const periods: readonly (DailyTotals | HourlyTotals)[] = isPast24Hours
    ? merged.hourly.toReversed()
    : merged.daily.toReversed();
  const models =
    metric === "tokens" ? sortModelsByTokens(merged.models) : merged.models;
  const peak = models.reduce(
    (maximum, model) =>
      Math.max(
        maximum,
        metric === "tokens" ? model.totalTokens : model.costUsd,
      ),
    0,
  );
  const selectedModel = merged.models.find(
    (model) => `${model.provider}:${model.model}` === selectedModelKey,
  );
  return (
    <div className="usage-statistics flex flex-col gap-10">
      <section className="grid gap-x-12 gap-y-8 md:grid-cols-[18rem_minmax(0,1fr)]">
        <div className="flex min-w-0 flex-col gap-4">
          <div className="flex flex-col gap-1">
            <div className="flex items-center gap-2">
              <span className="text-4xl font-semibold tracking-tight text-foreground tabular-nums">
                {metric === "cost"
                  ? formatUsd(merged.costUsd)
                  : formatTokens(merged.totalTokens)}
              </span>
              <ReceiptButton />
            </div>
            <span className="text-xs text-muted-foreground">
              {t(
                merged.sessions === 1
                  ? msg("{count} session")
                  : msg("{count} sessions"),
                {
                  count: formatCount(merged.sessions),
                },
              )}
              {metric === "cost" && (
                <>
                  {` · ${t("API estimate")}`}
                  {merged.costQuality.unpricedShare > 0 && (
                    <Popover>
                      <PopoverTrigger
                        openOnHover
                        render={<InlineButton tone="muted" />}
                        aria-label={t("Unpriced usage details")}
                      >
                        <InfoIcon className="size-3" aria-hidden />
                      </PopoverTrigger>
                      <PopoverPopup side="top" tooltipStyle>
                        {t("API estimate excludes {share} unpriced records.", {
                          share: formatPercent(
                            merged.costQuality.unpricedShare,
                          ),
                        })}
                      </PopoverPopup>
                    </Popover>
                  )}
                </>
              )}
            </span>
          </div>
          {hasChatGptSharing && (
            <div className="flex items-center justify-between gap-2 text-xs text-muted-foreground">
              <span>{t("ChatGPT shared usage")}</span>
              <Button size="xs" variant="outline" onClick={onManageChatGpt}>
                {t("Manage usage")}
              </Button>
            </div>
          )}
          {activeProviders.map((provider) => {
            const totals = merged.providers.find(
              (value) => value.provider === provider,
            )!;
            const share =
              metric === "cost" ? totals.costShare : totals.tokenShare;
            return (
              <div key={provider} className="flex flex-col gap-1">
                <div className="flex items-baseline justify-between gap-4">
                  <span className="flex min-w-0 items-center gap-2 text-sm text-foreground">
                    <span
                      aria-hidden
                      className="size-2 shrink-0 rounded-full"
                      style={{
                        backgroundColor: providerPresentation(provider).color,
                      }}
                    />
                    <ProviderMark provider={provider} className="size-4" />
                    <span className="flex min-w-0 flex-wrap items-baseline gap-1.5">
                      <span>{providerPresentation(provider).label}</span>
                      <span className="whitespace-nowrap text-2xs text-muted-foreground tabular-nums">
                        {t(
                          totals.sessions === 1
                            ? msg("{count} session")
                            : msg("{count} sessions"),
                          { count: formatCount(totals.sessions) },
                        )}
                      </span>
                    </span>
                  </span>
                  <span className="shrink-0 text-sm font-medium tabular-nums">
                    {metric === "cost"
                      ? formatUsd(totals.costUsd)
                      : formatTokens(totals.totalTokens)}
                  </span>
                </div>
                <span className="text-xs text-muted-foreground">
                  {metric === "cost"
                    ? t("{share} of cost · {tokens} tokens", {
                        share: formatPercent(share),
                        tokens: formatTokens(totals.totalTokens),
                      })
                    : t("{share} of tokens · {cost}", {
                        share: formatPercent(share),
                        cost: formatUsd(totals.costUsd),
                      })}
                </span>
              </div>
            );
          })}
        </div>
        <div className="flex min-w-0 flex-col gap-3">
          <h2 className="text-sm font-medium">
            {isPast24Hours
              ? metric === "tokens"
                ? t("Hourly processed tokens")
                : t("Hourly cost")
              : metric === "tokens"
                ? t("Daily processed tokens")
                : t("Daily cost")}
          </h2>
          <UsageProviderChart
            providers={activeProviders}
            days={chartWindow.days}
            daily={merged.daily}
            hours={chartWindow.hours}
            hourly={merged.hourly}
            metric={metric}
            referenceTime={chartWindow.referenceTime}
            resolution={chartWindow.resolution}
            timeZone={chartWindow.timeZone}
          />
        </div>
      </section>
      <section className="flex flex-col gap-2">
        <h2 className="text-sm font-medium">{t("Totals")}</h2>
        <div className="grid grid-cols-2 gap-x-6 gap-y-4 py-1 md:grid-cols-5">
          <Metric
            label={t("Processed tokens")}
            value={formatTokens(merged.totalTokens)}
          />
          <Metric
            label={t("Cached input")}
            value={formatTokens(merged.cachedInputTokens)}
          />
          <Metric
            label={t("Uncached input")}
            value={formatTokens(merged.uncachedInputTokens)}
          />
          <Metric
            label={t("Output")}
            value={formatTokens(merged.outputTokens)}
          />
          <Metric
            label={t("Cache savings")}
            value={formatUsd(merged.costQuality.cacheSavingsUsd)}
          />
        </div>
      </section>
      {merged.totalTokens > 0 && (
        <section className="grid gap-x-12 gap-y-8 md:grid-cols-2">
          {metric === "tokens" ? (
            <UsageShareBar
              label={t("Tokens by type")}
              segments={tokenTypeSegments(merged)}
              format={formatTokens}
            />
          ) : (
            <>
              <UsageShareBar
                label={t("Cost by type")}
                segments={costTypeSegments(merged.categoryCost)}
                format={formatUsd}
              />
              {merged.speedCost.fast + merged.speedCost.ultrafast > 0 && (
                <UsageShareBar
                  label={t("Cost by speed")}
                  segments={speedCostSegments(merged.speedCost)}
                  format={formatUsd}
                  aside={<SpeedPremium premiumUsd={merged.speedCost.premium} />}
                />
              )}
            </>
          )}
        </section>
      )}
      <section className="flex min-w-0 flex-col gap-3">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <h2 className="text-sm font-medium">{t("Breakdown")}</h2>
          <ToggleGroup
            aria-label={t("Usage breakdown")}
            variant="segmented"
            value={[breakdown]}
            onValueChange={(values) => {
              const value = values[0];
              if (value === "model" || value === "time") setBreakdown(value);
            }}
          >
            <Toggle value="model">{t("Model")}</Toggle>
            <Toggle value="time">{isPast24Hours ? t("Hour") : t("Day")}</Toggle>
          </ToggleGroup>
        </div>
        <div
          className="usage-table-scroll"
          tabIndex={0}
          aria-label={t("Usage breakdown table")}
        >
          {breakdown === "model" ? (
            <table data-breakdown="model" className="w-full text-sm">
              <thead>
                <tr className="border-b border-border text-right text-xs text-muted-foreground">
                  <th className="py-2 pr-3 text-left font-normal">#</th>
                  <th className="w-full py-2 text-left font-normal">
                    {t("Model")}
                  </th>
                  <th className="py-2 pl-6 font-normal">{t("Cost")}</th>
                  <th className="hidden py-2 pl-6 font-normal md:table-cell">
                    {t("Share")}
                  </th>
                  <th className="py-2 pl-6 font-normal">{t("Tokens")}</th>
                </tr>
              </thead>
              <tbody>
                {models.length === 0 ? (
                  <tr>
                    <td
                      colSpan={5}
                      className="py-6 text-center text-muted-foreground"
                    >
                      {t("No activity in this window.")}
                    </td>
                  </tr>
                ) : (
                  models.map((model, index) => {
                    const value =
                      metric === "tokens" ? model.totalTokens : model.costUsd;
                    const share = modelShare(model, metric);
                    return (
                      <tr
                        key={`${model.provider}:${model.model}`}
                        className="group relative border-b border-border/50 text-right whitespace-nowrap text-muted-foreground tabular-nums transition-colors hover:bg-muted/50 has-focus-visible:bg-muted/50"
                      >
                        <td className="py-2.5 pr-3 text-left text-xs">
                          {index + 1}
                        </td>
                        <td className="py-2.5 text-left whitespace-normal">
                          <button
                            type="button"
                            onClick={() =>
                              setSelectedModelKey(
                                `${model.provider}:${model.model}`,
                              )
                            }
                            className="flex items-center gap-2 text-left text-foreground outline-none after:absolute after:inset-0"
                          >
                            <ProviderMark
                              provider={model.provider}
                              className="size-3.5"
                            />
                            {model.model}
                            <ChevronRightIcon
                              aria-hidden
                              className="size-3.5 text-muted-foreground opacity-0 transition-opacity group-hover:opacity-100"
                            />
                          </button>
                          <div aria-hidden className="mt-1.5 h-0.5 max-w-48">
                            <div
                              className="h-full rounded-full"
                              style={{
                                width:
                                  value > 0 && peak > 0
                                    ? `max(0.5rem, ${(value / peak) * 100}%)`
                                    : 0,
                                backgroundColor: providerPresentation(
                                  model.provider,
                                ).color,
                              }}
                            />
                          </div>
                        </td>
                        <td className="py-2.5 pl-6 text-foreground">
                          {isModelCostUnknown(model) ? (
                            <span className="text-muted-foreground">
                              {t("Unpriced")}
                            </span>
                          ) : (
                            formatUsd(model.costUsd)
                          )}
                        </td>
                        <td className="hidden py-2.5 pl-6 md:table-cell">
                          {share === null ? "—" : formatPercent(share)}
                        </td>
                        <td className="py-2.5 pl-6">
                          {formatTokens(model.totalTokens)}
                        </td>
                      </tr>
                    );
                  })
                )}
              </tbody>
            </table>
          ) : (
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-border text-left text-xs text-muted-foreground">
                  <th className="py-2 font-normal">
                    {isPast24Hours ? t("Hour") : t("Day")}
                  </th>
                  {activeProviders.map((provider) => (
                    <th
                      key={provider}
                      className="py-2 pl-6 text-right font-normal"
                    >
                      {providerPresentation(provider).label}
                    </th>
                  ))}
                  <th className="py-2 pl-6 text-right font-normal">
                    {t("Total")}
                  </th>
                  <th className="py-2 pl-6 text-right font-normal">
                    {t("Tokens")}
                  </th>
                </tr>
              </thead>
              <tbody>
                {periods.length === 0 ? (
                  <tr>
                    <td
                      colSpan={activeProviders.length + 3}
                      className="py-6 text-center text-muted-foreground"
                    >
                      {t("No activity in this window.")}
                    </td>
                  </tr>
                ) : (
                  periods.map((period) => (
                    <tr
                      key={
                        "hourStart" in period ? period.hourStart : period.day
                      }
                      className="border-b border-border/50 transition-colors hover:bg-muted/50"
                    >
                      <td className="py-2 text-foreground">
                        {"hourStart" in period
                          ? formatHourShort(
                              period.hourStart,
                              chartWindow.timeZone,
                            )
                          : formatDayShort(period.day)}
                      </td>
                      {activeProviders.map((provider) => (
                        <td
                          key={provider}
                          className="py-2 pl-6 text-right text-muted-foreground tabular-nums"
                        >
                          {formatUsd(
                            period.byProvider.get(provider)?.costUsd ?? 0,
                          )}
                        </td>
                      ))}
                      <td className="py-2 pl-6 text-right text-foreground tabular-nums">
                        {formatUsd(period.costUsd)}
                      </td>
                      <td className="py-2 pl-6 text-right text-muted-foreground tabular-nums">
                        {formatTokens(period.totalTokens)}
                      </td>
                    </tr>
                  ))
                )}
              </tbody>
            </table>
          )}
        </div>
      </section>
      {selectedModel && (
        <UsageModelDialog
          model={selectedModel}
          environments={environments}
          metric={metric}
          chartWindow={chartWindow}
          onSetPrice={() => {
            setSelectedModelKey(null);
            onSetPrice(selectedModel.model);
          }}
          onClose={() => setSelectedModelKey(null)}
        />
      )}
    </div>
  );
}
