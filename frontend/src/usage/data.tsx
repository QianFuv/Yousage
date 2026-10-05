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
    environment: { capabilities: Record<string, boolean> };
  } | null;
  summary: UsageSummary | null;
  error?: string;
  refreshing: boolean;
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
