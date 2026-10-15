import {
  formatPercent,
  formatTokens,
  formatUsd,
} from "@t3tools/shared/usageFormat";
import {
  isModelCostUnknown,
  type ModelTotals,
} from "@t3tools/shared/usageMerge";
import { useMemo } from "react";
import { t } from "../src/i18n";
import { useIsReadOnly } from "../src/web/readOnly";

import {
  mergeAnsweredUsage,
  type EnvironmentUsageStatus,
} from "../src/usage/data";
import { ProviderInstanceIcon } from "./ProviderInstanceIcon";
import { Button } from "../../tools/parity/upstream/apps/web/src/components/ui/button";
import {
  Dialog,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogPanel,
  DialogPopup,
  DialogTitle,
} from "../../tools/parity/upstream/apps/web/src/components/ui/dialog";
import {
  UsageProviderChart,
  type UsageChartMetric,
} from "./UsageProviderChart";
import { UsageShareBar } from "./UsageShareBar";
import {
  cacheHitRate,
  costPerMillionTokens,
  costTypeSegments,
  speedCostSegments,
  tokenTypeSegments,
} from "./usageBreakdown";
import { PROVIDER_PRESENTATION } from "./usageProviders";

export interface UsageChartWindow {
  readonly days: readonly string[];
  readonly hours: readonly string[];
  readonly resolution: "day" | "hour";
  readonly timeZone: string;
  readonly referenceTime: string | undefined;
}

/**
 * One model's usage in the current window, opened from the Breakdown list.
 * The trend and mixes come from the same merge as the page, narrowed to this
 * model's buckets.
 */
export function UsageModelDialog({
  model,
  environments,
  metric,
  chartWindow,
  onSetPrice,
  onClose,
}: {
  readonly model: ModelTotals;
  readonly environments: readonly EnvironmentUsageStatus[];
  readonly metric: UsageChartMetric;
  readonly chartWindow: UsageChartWindow;
  readonly onSetPrice: () => void;
  readonly onClose: () => void;
}) {
  const isReadOnly = useIsReadOnly();
  const usage = useMemo(
    () =>
      mergeAnsweredUsage(
        environments,
        (bucket) =>
          bucket.provider === model.provider && bucket.model === model.model,
      ),
    [environments, model.provider, model.model],
  );
  const comparisons = useMemo(
    () =>
      environments.map((environment) => ({
        id: environment.environmentId,
        label: environment.label,
        hasSummary: environment.summary !== null,
        usage: mergeAnsweredUsage(
          [environment],
          (bucket) =>
            bucket.provider === model.provider && bucket.model === model.model,
        ),
      })),
    [environments, model.provider, model.model],
  );
  const providers = useMemo(() => [model.provider], [model.provider]);
  const presentation = PROVIDER_PRESENTATION[model.provider];
  const costUnknown = isModelCostUnknown(model);
  const hitRate = cacheHitRate(model);
  const perMillion = costPerMillionTokens(model);
  const stats = [
    {
      label: t("Cost"),
      value: costUnknown ? t("Unpriced") : formatUsd(model.costUsd),
    },
    { label: t("Tokens"), value: formatTokens(model.totalTokens) },
    perMillion === null
      ? null
      : { label: t("Per 1M tokens"), value: formatUsd(perMillion) },
    hitRate === null
      ? null
      : { label: t("Cache hit"), value: formatPercent(hitRate) },
  ].filter((stat) => stat !== null);

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <DialogPopup className="max-w-3xl">
        <DialogHeader>
          <div className="flex items-center gap-2">
            <ProviderInstanceIcon
              driverKind={presentation.driverKind}
              displayName={presentation.label}
              iconClassName="size-5"
            />
            <DialogTitle>{model.model}</DialogTitle>
          </div>
          <DialogDescription>
            {presentation.label}
            {costUnknown
              ? ""
              : ` · ${t("{share} of cost", { share: formatPercent(model.costShare) })}`}
          </DialogDescription>
        </DialogHeader>
        <DialogPanel>
          <div className="flex flex-col gap-8">
            <div className="grid grid-cols-2 gap-x-6 gap-y-4 sm:grid-cols-4">
              {stats.map((stat) => (
                <div key={stat.label} className="flex min-w-0 flex-col gap-0.5">
                  <span className="text-xs text-muted-foreground">
                    {stat.label}
                  </span>
                  <span className="text-2xl font-semibold text-foreground">
                    {stat.value}
                  </span>
                </div>
              ))}
            </div>

            {/* Unpriced cost is unknown, not zero, so its trend shows tokens. */}
            <UsageProviderChart
              providers={providers}
              days={chartWindow.days}
              daily={usage.daily}
              hours={chartWindow.hours}
              hourly={usage.hourly}
              metric={costUnknown ? "tokens" : metric}
              referenceTime={chartWindow.referenceTime}
              resolution={chartWindow.resolution}
              timeZone={chartWindow.timeZone}
            />

            <section className="model-environment-comparison">
              <h3 className="text-sm font-medium">
                {t("Environment comparison")}
              </h3>
              <div className="usage-table-scroll">
                <table>
                  <thead>
                    <tr>
                      <th>{t("Environment")}</th>
                      <th>{t("Cost")}</th>
                      <th>{t("Tokens")}</th>
                      <th>{t("Cached input")}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {comparisons.map((row) => (
                      <tr key={row.id}>
                        <td>{t(row.label)}</td>
                        <td>
                          {!row.hasSummary
                            ? t("Unavailable")
                            : row.usage.costQuality.unpricedShare > 0 &&
                                row.usage.costUsd === 0
                              ? t("Unpriced")
                              : formatUsd(row.usage.costUsd)}
                        </td>
                        <td>
                          {row.hasSummary
                            ? formatTokens(row.usage.totalTokens)
                            : "—"}
                        </td>
                        <td>
                          {row.hasSummary
                            ? formatTokens(row.usage.cachedInputTokens)
                            : "—"}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
              {comparisons.length > 1 && (
                <p className="text-xs text-muted-foreground">
                  {t("Environment values can overlap when they share history.")}
                </p>
              )}
            </section>
            <div className="grid gap-x-10 gap-y-8 sm:grid-cols-2">
              {costUnknown ? null : (
                <UsageShareBar
                  label={t("Cost by type")}
                  segments={costTypeSegments(usage.categoryCost)}
                  format={formatUsd}
                />
              )}
              <UsageShareBar
                label={t("Tokens by type")}
                segments={tokenTypeSegments(model.tokens)}
                format={formatTokens}
              />
              {usage.speedCost.fast + usage.speedCost.ultrafast > 0 ? (
                <UsageShareBar
                  label={t("Cost by speed")}
                  segments={speedCostSegments(usage.speedCost)}
                  format={formatUsd}
                  aside={<SpeedPremium premiumUsd={usage.speedCost.premium} />}
                />
              ) : null}
            </div>
          </div>
        </DialogPanel>
        {model.unpricedTokens > 0 ? (
          <DialogFooter
            variant="bare"
            className="items-center sm:justify-between"
          >
            <span className="text-xs text-muted-foreground">
              {t("{tokens} tokens have no known price", {
                tokens: formatTokens(model.unpricedTokens),
              })}
            </span>
            {!isReadOnly && (
              <Button onClick={onSetPrice}>{t("Set price")}</Button>
            )}
          </DialogFooter>
        ) : null}
      </DialogPopup>
    </Dialog>
  );
}

/** What the faster speeds cost above standard rates, beside the speed bar. */
export function SpeedPremium({ premiumUsd }: { readonly premiumUsd: number }) {
  return (
    <span className="text-xs text-muted-foreground">
      {t("Premium")}{" "}
      <span className="text-foreground tabular-nums">
        {formatUsd(premiumUsd)}
      </span>
    </span>
  );
}
