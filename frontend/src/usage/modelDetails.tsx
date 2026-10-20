/** Formatted model detail statistics and per-environment comparison rows. */
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
import { mergeAnsweredUsage, type EnvironmentUsageStatus } from "./data";

/** One labelled headline figure of a model. */
export interface ModelDetailStat {
  readonly label: string;
  readonly value: string;
}

/** One environment's formatted share of a model's usage. */
export interface EnvironmentComparisonRow {
  readonly id: string;
  readonly label: string;
  readonly cost: string;
  readonly tokens: string;
  readonly cachedInput: string;
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

/**
 * Each environment's usage of one model, keeping unanswered environments
 * distinct from unpriced and zero usage.
 *
 * @param environments - Every environment of the current window.
 * @param provider - The model's provider.
 * @param model - The model identity.
 * @returns One row per environment, in environment order.
 */
export function environmentComparisonRows(
  environments: readonly EnvironmentUsageStatus[],
  provider: ModelTotals["provider"],
  model: string,
): readonly EnvironmentComparisonRow[] {
  return environments.map((environment) => {
    const hasSummary = environment.summary !== null;
    const usage = mergeAnsweredUsage(
      [environment],
      (bucket) => bucket.provider === provider && bucket.model === model,
    );
    return {
      id: environment.environmentId,
      label: t(environment.label),
      cost: !hasSummary
        ? t("Unavailable")
        : usage.costQuality.unpricedShare > 0 && usage.costUsd === 0
          ? t("Unpriced")
          : formatUsd(usage.costUsd),
      tokens: hasSummary ? formatTokens(usage.totalTokens) : "—",
      cachedInput: hasSummary ? formatTokens(usage.cachedInputTokens) : "—",
    };
  });
}
