/** Stable aggregate helpers and UI wire envelopes independent of connected app stores. */
import { mergeUsage, type MergedUsage } from "@t3tools/shared/usageMerge";
import type {
  UsageBucket,
  UsageSummary,
  ServerProvider,
  UsageLimitSourceSnapshots,
  UsagePriceOverrides,
  UsageModelAliases,
} from "./contracts";

export type EnvironmentUsageStatus = {
  environmentId: string;
  label: string;
  local: boolean;
  connected: boolean;
  canOperate: boolean;
  serverConfig: {
    providers: ServerProvider[];
    usageLimitSources: UsageLimitSourceSnapshots;
    settings: {
      usagePriceOverrides: UsagePriceOverrides;
      usageModelAliases: UsageModelAliases;
    };
    environment: {
      capabilities: Record<string, boolean>;
      orchestrationProtocolVersion?: number;
    };
  } | null;
  summary: UsageSummary | null;
  error?: string;
  refreshing: boolean;
  cycleValues?: CycleValue[];
};
/** One quota observation with the API-equivalent spend at that moment and the value range it implies. */
export type CycleValueSample = {
  at: string;
  usedPercent: number;
  spentUsd: number;
  impliedLowUsd?: number;
  impliedHighUsd?: number;
  usable: boolean;
};
/** API-equivalent value of 100% of a subscription window in its current cycle, as an interval. */
export type CycleValue = {
  instanceId: string;
  driver: string;
  windowId: string;
  status:
    | "estimated"
    | "inconsistent"
    | "insufficientData"
    | "noLocalUsage"
    | "noCycle";
  cycleStartedAt?: string;
  resetsAt?: string;
  resetDetected: boolean;
  usedPercent: number;
  usedLowPercent: number;
  usedHighPercent: number;
  spentUsd: number;
  records: number;
  unpricedRecords: number;
  valueUsd?: number;
  valueLowUsd?: number;
  valueHighUsd?: number;
  remainingLowUsd?: number;
  remainingHighUsd?: number;
  constraintCount: number;
  samples: CycleValueSample[];
  observationSource: "logs" | "probes" | "both" | "none";
  priorUsed: boolean;
  priorCycleCount: number;
  adjustmentSuspected: boolean;
  history: CycleValueHistory[];
};
/** A completed earlier cycle of the same window, as used for the prior. */
export type CycleValueHistory = {
  cycleStartedAt: string;
  resetsAt: string;
  status: CycleValue["status"];
  valueUsd?: number;
  valueLowUsd?: number;
  valueHighUsd?: number;
  planType?: string;
  usedForPrior: boolean;
};
export type UsageSnapshot = {
  window: {
    timeZone: string;
    sinceDay: string;
    untilDay: string;
    resolution: "day" | "hour";
    sinceTimeMs?: number;
    untilTimeMs?: number;
  };
  environments: EnvironmentUsageStatus[];
};

/** Retain source ownership while optionally narrowing facts for a model dialog. */
export function mergeAnsweredUsage(
  environments: readonly EnvironmentUsageStatus[],
  filter?: (bucket: UsageBucket) => boolean,
): MergedUsage {
  return mergeUsage(
    environments.flatMap((environment) =>
      environment.summary
        ? [
            {
              environmentId: environment.environmentId,
              label: environment.label,
              summary: filter
                ? {
                    ...environment.summary,
                    buckets: environment.summary.buckets.filter(filter),
                  }
                : environment.summary,
            },
          ]
        : [],
    ),
    6,
  );
}
