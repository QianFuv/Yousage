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
import { useDialogHistoryEntry } from "../src/usage/dialogHistory";
import { modelDetailStats } from "../src/usage/modelDetails";
import { ProviderInstanceIcon } from "./ProviderInstanceIcon";
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
  readonly hour12?: boolean | undefined;
}

/**
 * One model's usage in the current window, opened from the Breakdown list.
 * The trend and mixes come from the same merge as the page, narrowed to this
 * model's buckets. In the read-only viewer, Back closes it.
 */
export function UsageModelDialog({
  model,
  environments,
  metric,
  chartWindow,
  onClose,
}: {
  readonly model: ModelTotals;
  readonly environments: readonly EnvironmentUsageStatus[];
  readonly metric: UsageChartMetric;
  readonly chartWindow: UsageChartWindow;
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
  const providers = useMemo(() => [model.provider], [model.provider]);
  const presentation = providerPresentation(model.provider);
  const costUnknown = isModelCostUnknown(model);
  const stats = modelDetailStats(model);
  useDialogHistoryEntry(isReadOnly, onClose);

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <DialogPopup
        bottomStickOnMobile={false}
        className="usage-model-dialog max-w-3xl"
      >
        <DialogHeader className="max-sm:px-4 max-sm:pt-5">
          <div className="flex items-center gap-2 pe-8">
            <ProviderInstanceIcon
              driverKind={presentation.driverKind}
              displayName={presentation.label}
              iconClassName="size-5"
            />
            <DialogTitle className="min-w-0">{model.model}</DialogTitle>
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
            <div className="grid grid-cols-2 gap-x-6 gap-y-4 sm:grid-cols-4">
              {stats.map((stat) => (
                <div key={stat.label} className="flex min-w-0 flex-col gap-0.5">
                  <span className="text-xs text-muted-foreground">
                    {stat.label}
                  </span>
                  <span className="text-xl font-semibold wrap-anywhere text-foreground sm:text-2xl">
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
              hour12={chartWindow.hour12}
            />

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
              {costUnknown ? null : (
                <UsageShareBar
                  label={t("Cost by speed")}
                  segments={speedCostSegments(usage.speedCost)}
                  format={formatUsd}
                  aside={<SpeedPremium premiumUsd={usage.speedCost.premium} />}
                />
              )}
            </div>
          </div>
        </DialogPanel>
        {model.unpricedTokens > 0 ? (
          <DialogFooter
            variant="bare"
            className="items-center sm:justify-between"
          >
            <span className="text-xs wrap-anywhere text-muted-foreground">
              {t("{tokens} tokens have no known price", {
                tokens: formatTokens(model.unpricedTokens),
              })}
            </span>
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
