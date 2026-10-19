/** Model detail statistics and environment comparison, laid out as compact cards on phones. */
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

/**
 * Headline figures as a four-column row, or as a two-by-two grid of cards on
 * phones where long values wrap inside their card.
 */
export function ModelDetailStats({
  stats,
}: {
  readonly stats: readonly ModelDetailStat[];
}) {
  return (
    <div className="grid grid-cols-2 gap-2 sm:grid-cols-4 sm:gap-x-6 sm:gap-y-4">
      {stats.map((stat) => (
        <div
          key={stat.label}
          className="flex min-w-0 flex-col gap-0.5 max-sm:rounded-lg max-sm:border max-sm:border-border/70 max-sm:px-3 max-sm:py-2"
        >
          <span className="text-xs text-muted-foreground">{stat.label}</span>
          <span className="text-xl font-semibold wrap-anywhere text-foreground max-sm:text-lg max-sm:leading-tight sm:text-2xl">
            {stat.value}
          </span>
        </div>
      ))}
    </div>
  );
}

/**
 * Per-environment usage: a table on wider screens and one stacked card per
 * environment on phones, so no column scrolls out of view.
 */
export function ModelEnvironmentComparison({
  rows,
}: {
  readonly rows: readonly EnvironmentComparisonRow[];
}) {
  return (
    <section className="model-environment-comparison">
      <h3 className="text-sm font-medium">{t("Environment comparison")}</h3>
      <ul className="flex flex-col gap-2 sm:hidden">
        {rows.map((row) => (
          <li
            key={row.id}
            className="rounded-lg border border-border/70 px-3 py-2.5"
          >
            <p className="text-sm font-medium wrap-anywhere text-foreground">
              {row.label}
            </p>
            <dl className="mt-1.5 grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1 text-xs">
              {[
                [t("Cost"), row.cost],
                [t("Tokens"), row.tokens],
                [t("Cached input"), row.cachedInput],
              ].map(([label, value]) => (
                <div key={label} className="contents">
                  <dt className="text-muted-foreground">{label}</dt>
                  <dd className="text-right wrap-anywhere text-foreground tabular-nums">
                    {value}
                  </dd>
                </div>
              ))}
            </dl>
          </li>
        ))}
      </ul>
      <div className="usage-table-scroll max-sm:hidden">
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
            {rows.map((row) => (
              <tr key={row.id}>
                <td>{row.label}</td>
                <td>{row.cost}</td>
                <td>{row.tokens}</td>
                <td>{row.cachedInput}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {rows.length > 1 && (
        <p className="text-xs text-muted-foreground">
          {t("Environment values can overlap when they share history.")}
        </p>
      )}
    </section>
  );
}
