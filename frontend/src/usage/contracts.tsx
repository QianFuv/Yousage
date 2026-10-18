/** Wire types for the pinned usage contract; validation and credentials belong to the Go service. */
export type EnvironmentId = string;
export type ProviderInstanceId = string;
export type ProviderDriverKind = string;
export type UsageProviderKind = "codex" | "claude" | "pi";
export type UsageDay = string;
export type UsageResolution = "day" | "hour";
export type UsageSummaryInput = {
  sinceDay: string;
  untilDay: string;
  timeZone: string;
  resolution?: UsageResolution;
  sinceTime?: string;
  untilTime?: string;
};
export const USAGE_CONTRACT_VERSION = 6;
export const USAGE_MERGE_COMPATIBLE_SINCE = 4;
export const UsageDay = {
  /** Preserve the already validated wire day. */ make: (value: string) =>
    value,
};
export const ProviderDriverKind = {
  /** Preserve the verified provider driver identity. */ make: (
    value: string,
  ) => value,
};
/** Match omitted availability behavior in the pinned server contract. */
export const isProviderAvailable = (provider: { availability?: string }) =>
  provider.availability !== "unavailable";
export type UsageTokenTotals = {
  uncachedInputTokens: number;
  cachedInputTokens: number;
  cacheCreationTokens: number;
  outputTokens: number;
  reasoningTokens: number;
};
export type UsageSourceFingerprint = {
  hostId: string;
  provider: UsageProviderKind;
  resolvedHomePath: string;
  volumeId: string;
};
export type UsageSource = {
  fingerprint: UsageSourceFingerprint;
  status: "ok" | "missing" | "partial" | "failed";
  scannedFiles: number;
  skippedFiles: number;
  malformedRecords: number;
  distinctSessions: number;
  message: string | null;
};
export type UsageBucket = {
  day: string;
  hourStart?: string;
  provider: UsageProviderKind;
  model: string;
  sourcePath?: string;
  totals: UsageTokenTotals;
  costUsd: number;
  cacheSavingsUsd: number;
  categoryCostUsd?: {
    input: number;
    cacheRead: number;
    cacheWrite: number;
    output: number;
  };
  fastCostUsd?: number;
  ultrafastCostUsd?: number;
  speedPremiumUsd?: number;
  costSource: "providerReported" | "modelPriced" | "unpriced";
  records: number;
  unpricedRecords: number;
  sessions: number;
};
export type UsageSummary = {
  contractVersion: number;
  readAt: string;
  timeZone: string;
  sinceDay: string;
  untilDay: string;
  buckets: UsageBucket[];
  sources: UsageSource[];
  pricing: {
    status: "fresh" | "cached" | "unavailable";
    source: string;
    fetchedAt: string | null;
    knownModels: number;
  };
  scanDurationMs: number;
};
export type ServerProviderUsageWindow = {
  id: string;
  kind: "session" | "weekly" | "monthly" | "other";
  label: string;
  usedPercent: number;
  resetsAt?: string;
  windowDurationMins?: number;
};
export type ServerProviderResetCredits = {
  availableCount: number;
  nextExpiresAt?: string;
  nextCreditId?: string;
};
export type ServerProviderUsageLimits = {
  checkedAt: string;
  windows: ServerProviderUsageWindow[];
  credentialFingerprint?: string;
  resetCredits?: ServerProviderResetCredits;
  externalUsage?: { label: string; url: string };
  unavailable?: { reason: "unsupported" | "probeFailed"; message?: string };
};
export type ServerProvider = {
  instanceId: string;
  driver: string;
  displayName?: string;
  accentColor?: string;
  enabled: boolean;
  installed: boolean;
  availability?: string;
  status: string;
  auth: {
    status: string;
    email?: string;
    label?: string;
    subscriptionSharing?: boolean;
    workspaceId?: string;
  };
  usageLimits?: ServerProviderUsageLimits;
  slashCommands: ServerProviderSlashCommand[];
  workspaceSnapshots?: { slashCommands: ServerProviderSlashCommand[] }[];
};
export type ServerSettingsPatch = {
  usagePriceOverrides?: Record<string, UsageModelPriceOverride | null>;
  usageModelAliases?: Record<string, string | null>;
};
export type UsageLimitSourceSnapshots = {
  id: string;
  kind: "cliproxy";
  label: string;
  checkedAt: string;
  accounts: {
    id: string;
    driver: string;
    email?: string;
    plan?: string;
    workspaceId?: string;
    usageLimits: ServerProviderUsageLimits;
  }[];
  error?: string;
}[];
export type ProviderConsumeResetCreditInput =
  | { instanceId: string }
  | { sourceId: string; accountId: string; creditId: string };
export type ProviderConsumeResetCreditOutcome =
  | "reset"
  | "nothingToReset"
  | "noCredit"
  | "alreadyRedeemed";
export type ServerProviderSlashCommand = {
  name: string;
  description: string;
  argumentHint?: string;
};
export type UsageLimitsReport = {
  createdAt: string;
  accounts: {
    id: string;
    driver: string;
    label: string;
    plan?: string;
    instanceId?: string;
    resetCreditInput?: ProviderConsumeResetCreditInput;
    displayName?: string;
    accentColor?: string;
    email?: string;
    sourceLabel?: string;
    limits: ServerProviderUsageLimits;
  }[];
  notices: string[];
};
export type UsageModelPriceOverride = {
  inputCostPerMillionTokens: number;
  outputCostPerMillionTokens: number;
  cacheReadCostPerMillionTokens?: number;
  cacheWriteCostPerMillionTokens?: number;
};
export type UsagePriceOverrides = Record<string, UsageModelPriceOverride>;
export type UsageModelAliases = Record<string, string>;
