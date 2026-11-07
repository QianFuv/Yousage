/** Formatted headline statistics of one model. */
import {
  formatPercent,
  formatTokens,
  formatUsd,
} from "@t3tools/shared/usageFormat";
import {
  isModelCostUnknown,
  type ModelTotals,
} from "@t3tools/shared/usageMerge";
import { t } from "../i18n";
import {
  cacheHitRate,
  costPerMillionTokens,
} from "../../vendor/usageBreakdown";

/** One labelled headline figure of a model. */
export interface ModelDetailStat {
  readonly label: string;
  readonly value: string;
}

/**
 * Headline figures of a model, leaving out rates that have no basis.
 *
 * @param model - The model's totals in the current window.
 * @returns Cost and tokens, then per-million cost and cache hit when known.
 */
export function modelDetailStats(
  model: ModelTotals,
): readonly ModelDetailStat[] {
  const hitRate = cacheHitRate(model);
  const perMillion = costPerMillionTokens(model);
  return [
    {
      label: t("Cost"),
      value: isModelCostUnknown(model)
        ? t("Unpriced")
        : formatUsd(model.costUsd),
    },
    { label: t("Tokens"), value: formatTokens(model.totalTokens) },
    perMillion === null
      ? null
      : { label: t("Per 1M tokens"), value: formatUsd(perMillion) },
    hitRate === null
      ? null
      : { label: t("Cache hit"), value: formatPercent(hitRate) },
  ].filter((stat) => stat !== null);
}
