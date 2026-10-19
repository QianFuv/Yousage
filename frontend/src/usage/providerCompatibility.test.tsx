/** Usage from a provider kind this build does not know stays visible and counted instead of breaking the pages. */
import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import {
  orderProviderKinds,
  PROVIDER_PRESENTATION,
  providerPresentation,
  providersWithUsage,
} from "../../vendor/usageProviders";
import {
  buildPeriodColumns,
  UsageProviderChart,
} from "../../vendor/UsageProviderChart";
import { mergeAnsweredUsage, type EnvironmentUsageStatus } from "./data";
import { orderProviders, ProviderSummaryRow } from "./ProviderSummaryRow";
import type { UsageBucket, UsageProviderKind } from "./contracts";

/** A provider kind a newer desktop could report. */
const FUTURE = "aurora" as UsageProviderKind;

/**
 * Build one day bucket.
 *
 * @param provider - History provider.
 * @param model - Model identity.
 * @param costUsd - Cost of the bucket.
 * @returns The bucket.
 */
function bucket(
  provider: UsageProviderKind,
  model: string,
  costUsd: number,
): UsageBucket {
  return {
    day: "2026-10-06",
    provider,
    model,
    totals: {
      uncachedInputTokens: 100,
      cachedInputTokens: 0,
      cacheCreationTokens: 0,
      outputTokens: 50,
      reasoningTokens: 0,
    },
    costUsd,
    cacheSavingsUsd: 0,
    costSource: "modelPriced",
    records: 1,
    unpricedRecords: 0,
    sessions: 1,
  };
}

/** One environment with Codex usage and usage from the future provider. */
const ENVIRONMENTS: EnvironmentUsageStatus[] = [
  {
    environmentId: "local",
    label: "local",
    local: true,
    connected: true,
    canOperate: true,
    refreshing: false,
    serverConfig: null,
    summary: {
      contractVersion: 6,
      readAt: "2026-10-06T12:00:00Z",
      timeZone: "UTC",
      sinceDay: "2026-10-06",
      untilDay: "2026-10-06",
      pricing: {
        status: "cached",
        source: "fixture",
        fetchedAt: null,
        knownModels: 1,
      },
      scanDurationMs: 1,
      sources: [
        {
          fingerprint: {
            hostId: "desk",
            provider: "codex",
            resolvedHomePath: "/codex",
            volumeId: "v",
          },
          status: "ok",
          scannedFiles: 1,
          skippedFiles: 0,
          malformedRecords: 0,
          distinctSessions: 1,
          message: null,
        },
        {
          fingerprint: {
            hostId: "desk",
            provider: FUTURE,
            resolvedHomePath: "/aurora",
            volumeId: "v",
          },
          status: "ok",
          scannedFiles: 1,
          skippedFiles: 0,
          malformedRecords: 0,
          distinctSessions: 1,
          message: null,
        },
      ],
      buckets: [
        bucket("codex", "gpt-fixture", 1),
        bucket(FUTURE, "aurora-large", 2),
      ],
    },
  },
];

const CHART_WINDOW = {
  days: ["2026-10-06"],
  hours: [],
  resolution: "day" as const,
  timeZone: "UTC",
  referenceTime: undefined,
};

describe("provider presentation fallback", () => {
  it("presents unknown kinds by name in a neutral color, never through inherited keys", () => {
    expect(providerPresentation("pi")).toBe(PROVIDER_PRESENTATION.pi);
    expect(providerPresentation(FUTURE)).toEqual({
      label: FUTURE,
      color: "var(--muted-foreground)",
      driverKind: FUTURE,
    });
    expect(providerPresentation("constructor").label).toBe("constructor");
  });

  it("orders known kinds first and keeps unknown kinds once each", () => {
    expect(orderProviderKinds([FUTURE, "pi", "codex", FUTURE, "zeta"])).toEqual(
      ["codex", "pi", FUTURE, "zeta"],
    );
  });

  it("keeps the future provider's activity and totals in the merge", () => {
    const merged = mergeAnsweredUsage(ENVIRONMENTS);
    expect(providersWithUsage(merged.providers)).toEqual(["codex", FUTURE]);
    expect(merged.costUsd).toBe(3);
    expect(merged.models.map((model) => model.provider)).toContain(FUTURE);
  });

  it("includes the future provider in each chart column total", () => {
    const merged = mergeAnsweredUsage(ENVIRONMENTS);
    const [column] = buildPeriodColumns(
      ["2026-10-06"],
      new Map(merged.daily.map((entry) => [entry.day, entry])),
      "cost",
      providersWithUsage(merged.providers),
    );
    expect(column.total).toBe(3);
    expect(column.bands.find((band) => band.provider === FUTURE)?.value).toBe(
      2,
    );
  });

  it("keeps future rows in loss and speed summaries and renders them", () => {
    const rows = orderProviders(
      [
        { provider: FUTURE, events: 1 },
        { provider: "claude", events: 2 },
      ],
      (row) => row.events > 0,
    );
    expect(rows.map((row) => row.provider)).toEqual(["claude", FUTURE]);
    const markup = renderToStaticMarkup(
      <ProviderSummaryRow provider={FUTURE} count="1" value="$2" detail="d" />,
    );
    expect(markup).toContain(FUTURE);
  });

  it("renders the future provider's series in the usage chart", () => {
    const merged = mergeAnsweredUsage(ENVIRONMENTS);
    const chart = renderToStaticMarkup(
      <UsageProviderChart
        providers={providersWithUsage(merged.providers)}
        daily={merged.daily}
        hourly={merged.hourly}
        metric="cost"
        {...CHART_WINDOW}
      />,
    );
    expect(chart).toContain('stroke="var(--muted-foreground)"');
  });
});
