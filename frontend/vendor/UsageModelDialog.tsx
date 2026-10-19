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
import { t, useI18n } from "../src/i18n";
import { useIsReadOnly } from "../src/web/readOnly";

import {
  mergeAnsweredUsage,
  type EnvironmentUsageStatus,
} from "../src/usage/data";
import {
  environmentComparisonRows,
  ModelDetailStats,
  ModelEnvironmentComparison,
  modelDetailStats,
} from "../src/usage/modelDetails";
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
  costTypeSegments,
  speedCostSegments,
  tokenTypeSegments,
} from "./usageBreakdown";
import { providerPresentation } from "./usageProviders";

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
  const { language } = useI18n();
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
    () => environmentComparisonRows(environments, model.provider, model.model),
    [environments, model.provider, model.model, language],
  );
  const providers = useMemo(() => [model.provider], [model.provider]);
  const presentation = providerPresentation(model.provider);
  const costUnknown = isModelCostUnknown(model);

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <DialogPopup className="max-w-3xl max-sm:rounded-t-2xl">
        <DialogHeader className="max-sm:px-4 max-sm:pt-5">
          <div className="flex items-start gap-2 pe-8">
            <ProviderInstanceIcon
              driverKind={presentation.driverKind}
              displayName={presentation.label}
              className="mt-0.5"
              iconClassName="size-5"
            />
            <DialogTitle className="min-w-0 leading-tight">
              {model.model}
            </DialogTitle>
          </div>
          <DialogDescription className="wrap-anywhere">
            {presentation.label}
            {costUnknown
              ? ""
              : ` · ${t("{share} of cost", { share: formatPercent(model.costShare) })}`}
          </DialogDescription>
        </DialogHeader>
        <DialogPanel className="max-sm:px-4">
          <div className="flex flex-col gap-6 sm:gap-8">
            <ModelDetailStats stats={modelDetailStats(model)} />

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

            <ModelEnvironmentComparison rows={comparisons} />
            <div className="grid gap-x-10 gap-y-6 sm:grid-cols-2 sm:gap-y-8">
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
            className="items-center sm:justify-between max-sm:flex-col max-sm:items-stretch max-sm:px-4 max-sm:pb-[calc(1rem+env(safe-area-inset-bottom))]"
          >
            <span className="text-xs wrap-anywhere text-muted-foreground">
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
