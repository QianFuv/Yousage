/** API-equivalent value of a subscription cycle: what 100% of the quota is worth at API prices, as an interval. */
import { formatUsd } from "@t3tools/shared/usageFormat";
import { cn } from "../../../tools/parity/src/bridges/utils";
import { msg, t } from "../i18n";
import type {
  CycleValue,
  CycleValueHistory,
  EnvironmentUsageStatus,
} from "./data";

/** Cycle values keyed by pooled account key and window id. */
export type CycleValueIndex = ReadonlyMap<string, CycleValue>;

/** How far a prediction can be trusted, from the width of its interval and its consistency. */
export type PredictionConfidence = "high" | "medium" | "low" | "conflict";

const HIGH_CONFIDENCE_SPREAD = 10;
const MEDIUM_CONFIDENCE_SPREAD = 25;

/** Theme status colours keep the dot legible in every light, dark and custom theme. */
const CONFIDENCE: Record<
  PredictionConfidence,
  { readonly label: string; readonly className: string }
> = {
  high: { label: msg("High confidence"), className: "bg-success" },
  medium: { label: msg("Medium confidence"), className: "bg-warning" },
  low: { label: msg("Low confidence"), className: "bg-muted-foreground" },
  conflict: {
    label: msg("Conflicting observations"),
    className: "bg-destructive",
  },
};

/**
 * Key a cycle value the same way pooled native accounts are keyed.
 *
 * @param accountKey - Pooled account key, `environmentId:instanceId` for native accounts.
 * @param windowId - Limit window id.
 * @returns The lookup key.
 */
export function cycleValueKey(accountKey: string, windowId: string): string {
  return `${accountKey}\u0000${windowId}`;
}

/**
 * Index every environment's cycle values for lookup by pooled account and window.
 *
 * @param environments - Selected environments.
 * @returns Cycle values keyed by {@link cycleValueKey}.
 */
export function indexCycleValues(
  environments: readonly EnvironmentUsageStatus[],
): CycleValueIndex {
  const index = new Map<string, CycleValue>();
  for (const environment of environments) {
    for (const value of environment.cycleValues ?? []) {
      index.set(
        cycleValueKey(
          `${environment.environmentId}:${value.instanceId}`,
          value.windowId,
        ),
        value,
      );
    }
  }
  return index;
}

/**
 * A USD interval as `$L – $H`, `≥ $L` or `≤ $H`.
 *
 * @param low - Lower bound, if known.
 * @param high - Upper bound, if known.
 * @returns The formatted interval, or null without bounds.
 */
export function rangeText(
  low: number | undefined,
  high: number | undefined,
): string | null {
  if (low !== undefined && high !== undefined)
    return `${formatUsd(low)} – ${formatUsd(high)}`;
  if (low !== undefined) return `≥ ${formatUsd(low)}`;
  if (high !== undefined) return `≤ ${formatUsd(high)}`;
  return null;
}

/**
 * Half the interval width relative to its midpoint.
 *
 * @param value - Cycle value.
 * @returns The relative uncertainty as a percent, or null without a closed interval.
 */
export function relativeSpread(value: CycleValue): number | null {
  const { valueLowUsd: low, valueHighUsd: high } = value;
  if (low === undefined || high === undefined) return null;
  const middle = (low + high) / 2;
  return middle > 0 ? (Math.abs(high - low) / 2 / middle) * 100 : null;
}

/**
 * The amount shown on the quota bar: the point estimate, or the lower bound while one is all
 * the observations support.
 *
 * @param value - Cycle value.
 * @returns The formatted prediction, or null when nothing can be predicted.
 */
export function predictionText(value: CycleValue): string | null {
  if (value.status === "estimated" || value.status === "inconsistent")
    return value.valueUsd === undefined
      ? null
      : `≈ ${formatUsd(value.valueUsd)}`;
  if (value.status === "insufficientData" && value.valueLowUsd !== undefined)
    return `≥ ${formatUsd(value.valueLowUsd)}`;
  return null;
}

/**
 * Grade a prediction: narrow consistent intervals are trustworthy, open or wide ones are not.
 *
 * @param value - Cycle value.
 * @returns The confidence, or null when there is no prediction to grade.
 */
export function predictionConfidence(
  value: CycleValue,
): PredictionConfidence | null {
  if (predictionText(value) === null) return null;
  if (value.status === "inconsistent") return "conflict";
  const spread = relativeSpread(value);
  if (value.status !== "estimated" || spread === null) return "low";
  if (spread <= HIGH_CONFIDENCE_SPREAD)
    return value.priorUsed ? "medium" : "high";
  if (spread <= MEDIUM_CONFIDENCE_SPREAD) return "medium";
  return "low";
}

/**
 * Whether an active cycle is still waiting for its first usable observation.
 *
 * @param value - Cycle value.
 * @returns True while the estimate is being gathered.
 */
export function isPredictionPending(value: CycleValue): boolean {
  return value.status === "insufficientData" && predictionText(value) === null;
}

/**
 * The localized name of a confidence grade.
 *
 * @param confidence - Confidence grade.
 * @returns The label.
 */
export function confidenceLabel(confidence: PredictionConfidence): string {
  return t(CONFIDENCE[confidence].label);
}

/** A status dot coloured by confidence; the grade is also given as text for assistive technology. */
export function ConfidenceDot({
  confidence,
  className,
}: {
  readonly confidence: PredictionConfidence;
  readonly className?: string;
}) {
  return (
    <span
      role="img"
      aria-label={confidenceLabel(confidence)}
      className={cn(
        "inline-block size-2 shrink-0 rounded-full",
        CONFIDENCE[confidence].className,
        className,
      )}
    />
  );
}

/**
 * The prediction as it sits on a quota bar: confidence dot and amount.
 *
 * @param props - Cycle value to show.
 * @returns The badge, or null when nothing can be predicted.
 */
export function CycleValueBadge({ value }: { readonly value: CycleValue }) {
  const prediction = predictionText(value);
  const confidence = predictionConfidence(value);
  if (isPredictionPending(value))
    return (
      <span className="inline-flex shrink-0 items-center gap-1 text-muted-foreground">
        <PendingDot />
        {t("Estimating…")}
      </span>
    );
  if (prediction === null || confidence === null) return null;
  return (
    <span className="inline-flex shrink-0 items-center gap-1 text-foreground tabular-nums">
      <ConfidenceDot confidence={confidence} />
      {prediction}
    </span>
  );
}

/** A hollow dot for an estimate that is still being gathered. */
export function PendingDot() {
  return (
    <span
      aria-hidden
      className="inline-block size-2 shrink-0 rounded-full border border-muted-foreground"
    />
  );
}

/**
 * Screen-reader phrase for the prediction on a quota bar.
 *
 * @param value - Cycle value.
 * @returns The phrase, or null when nothing can be predicted.
 */
export function predictionPhrase(value: CycleValue): string | null {
  const prediction = predictionText(value);
  const confidence = predictionConfidence(value);
  if (isPredictionPending(value))
    return t("API-equivalent value this cycle is being estimated");
  if (prediction === null || confidence === null) return null;
  return t("{value} API-equivalent value this cycle, {confidence}", {
    value: prediction,
    confidence: confidenceLabel(confidence),
  });
}

/**
 * Why a cycle value has no estimate or deserves caution.
 *
 * @param value - Cycle value.
 * @returns The localized explanation, or null when the estimate needs none.
 */
export function cycleValueNotice(value: CycleValue): string | null {
  switch (value.status) {
    case "estimated":
      return value.adjustmentSuspected
        ? t(
            "This cycle differs from earlier cycles, so the quota may have been adjusted. Earlier cycles are not used.",
          )
        : null;
    case "inconsistent":
      return t(
        "Observations disagree: quota use was not proportional to API prices, or some usage happened elsewhere.",
      );
    case "insufficientData":
      return value.constraintCount === 0
        ? t(
            "Waiting for history to cover the quota observations. The estimate appears after the next refresh.",
          )
        : t(
            "More usage in this cycle is needed before the value can be bounded.",
          );
    case "noLocalUsage":
      return t(
        "Quota was used, but no usage in this cycle was recorded on this device.",
      );
    case "noCycle":
      return t("The window has reset. A new cycle starts with the next use.");
  }
}

/**
 * Where a cycle value comes from: Codex logs, live quota checks, and earlier cycles.
 *
 * @param value - Cycle value.
 * @returns The localized basis, or null without observations.
 */
export function basisText(value: CycleValue): string | null {
  const source =
    value.observationSource === "logs"
      ? t("Codex logs")
      : value.observationSource === "probes"
        ? t("Quota checks")
        : value.observationSource === "both"
          ? t("Codex logs and quota checks")
          : null;
  if (!value.priorUsed || value.priorCycleCount === 0) return source;
  const prior = t("{count} earlier cycles", { count: value.priorCycleCount });
  return source ? `${source} · ${prior}` : prior;
}

/**
 * A completed cycle's value as a short figure.
 *
 * @param entry - Earlier cycle.
 * @returns `≈ $X`, an interval, or a dash when it could not be estimated.
 */
export function historyValueText(entry: CycleValueHistory): string {
  if (entry.valueUsd !== undefined) return `≈ ${formatUsd(entry.valueUsd)}`;
  return rangeText(entry.valueLowUsd, entry.valueHighUsd) ?? "—";
}

/**
 * The precision of a cycle value in words.
 *
 * @param value - Cycle value.
 * @returns The localized precision.
 */
export function precisionText(value: CycleValue): string {
  const spread = relativeSpread(value);
  return spread === null
    ? t("{count} usable observations", { count: value.constraintCount })
    : t("±{spread}% from {count} usable observations", {
        spread: Math.round(spread * 10) / 10,
        count: value.constraintCount,
      });
}
