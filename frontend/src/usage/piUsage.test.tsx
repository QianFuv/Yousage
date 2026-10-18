/** Pi usage stays visible through provider ordering and multi-environment merging. */
import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import {
  PROVIDER_ORDER,
  PROVIDER_PRESENTATION,
  providersWithUsage,
} from "../../vendor/usageProviders";
import { mergeAnsweredUsage, type EnvironmentUsageStatus } from "./data";
import { orderProviders, ProviderSummaryRow } from "./ProviderSummaryRow";
import type { UsageBucket, UsageSource, UsageSummary } from "./contracts";

/**
 * Build one history source.
 *
 * @param hostId - Machine that owns the files.
 * @param provider - History provider.
 * @param path - Resolved folder.
 * @returns The source.
 */
function source(
  hostId: string,
  provider: UsageSource["fingerprint"]["provider"],
  path: string,
): UsageSource {
  return {
    fingerprint: { hostId, provider, resolvedHomePath: path, volumeId: "v" },
    status: "ok",
    scannedFiles: 1,
    skippedFiles: 0,
    malformedRecords: 0,
    distinctSessions: 1,
    message: null,
  };
}

/**
 * Build one day bucket.
 *
 * @param provider - History provider.
 * @param model - Model identity.
 * @param path - Owning source folder.
 * @param costUsd - Cost of the bucket.
 * @returns The bucket.
 */
function bucket(
  provider: UsageBucket["provider"],
  model: string,
  path: string,
  costUsd: number,
): UsageBucket {
  return {
    day: "2026-10-06",
    provider,
    model,
    sourcePath: path,
    totals: {
      uncachedInputTokens: 100,
      cachedInputTokens: 50,
      cacheCreationTokens: 0,
      outputTokens: 25,
      reasoningTokens: 5,
    },
    costUsd,
    cacheSavingsUsd: 0,
    costSource: costUsd > 0 ? "providerReported" : "unpriced",
    records: 2,
    unpricedRecords: costUsd > 0 ? 0 : 2,
    sessions: 1,
  };
}

/**
 * Wrap a summary as an answered environment.
 *
 * @param environmentId - Environment identity.
 * @param summary - Its usage summary.
 * @returns The environment status.
 */
function environment(
  environmentId: string,
  summary: Partial<UsageSummary> & Pick<UsageSummary, "buckets" | "sources">,
): EnvironmentUsageStatus {
  return {
    environmentId,
    label: environmentId,
    local: environmentId === "local",
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
      ...summary,
    },
  };
}

const LOCAL = environment("local", {
  sources: [
    source("desk", "codex", "/codex"),
    source("desk", "pi", "/pi/sessions"),
    source("desk", "pi", "D:/custom-sessions"),
  ],
  buckets: [
    bucket("codex", "gpt-fixture", "/codex", 1),
    bucket("pi", "backend/model-a", "/pi/sessions", 0.5),
    bucket("pi", "backend/model-b", "D:/custom-sessions", 0),
  ],
});

describe("Pi provider presentation", () => {
  it("is registered after the original providers with its own color and driver", () => {
    expect(PROVIDER_ORDER).toEqual(["codex", "claude", "pi"]);
    expect(PROVIDER_PRESENTATION.pi).toMatchObject({
      label: "Pi",
      driverKind: "pi",
    });
  });

  it("counts token-only Pi activity even when its cost is unpriced", () => {
    expect(
      providersWithUsage([
        { provider: "pi", costUsd: 0, totalTokens: 10 },
        { provider: "claude", costUsd: 0, totalTokens: 0 },
        { provider: "codex", costUsd: 1, totalTokens: 10 },
      ]),
    ).toEqual(["codex", "pi"]);
  });

  it("keeps Pi report rows in provider order and renders its name", () => {
    const rows = orderProviders(
      [
        { provider: "pi", events: 1 },
        { provider: "unknown", events: 3 },
        { provider: "codex", events: 2 },
      ],
      (row) => row.events > 0,
    );
    expect(rows.map((row) => row.provider)).toEqual(["codex", "pi"]);
    const markup = renderToStaticMarkup(
      <ProviderSummaryRow provider="pi" count="1" value="—" detail="detail" />,
    );
    expect(markup).toContain("Pi");
    expect(markup).toContain("#4d9f8a");
  });
});

describe("merging Pi usage across environments", () => {
  it("keeps every Pi session folder and adds remote Pi usage", () => {
    const remote = environment("remote", {
      sources: [source("server", "pi", "/home/pi/sessions")],
      buckets: [bucket("pi", "backend/model-a", "/home/pi/sessions", 0.25)],
    });
    const merged = mergeAnsweredUsage([LOCAL, remote]);
    const pi = merged.providers.find((entry) => entry.provider === "pi");
    expect(pi).toMatchObject({ costUsd: 0.75, totalTokens: 525, records: 6 });
    expect(
      merged.models
        .filter((model) => model.provider === "pi")
        .map((model) => [model.model, model.records, model.unpricedRecords]),
    ).toEqual(
      expect.arrayContaining([
        ["backend/model-a", 4, 0],
        ["backend/model-b", 2, 2],
      ]),
    );
    expect(merged.daily[0].byProvider.get("pi")).toEqual({
      costUsd: 0.75,
      totalTokens: 525,
    });
  });

  it("counts the same physical Pi folder once when two environments report it", () => {
    const mirror = environment("mirror", {
      sources: [source("desk", "pi", "/pi/sessions")],
      buckets: [bucket("pi", "backend/model-a", "/pi/sessions", 0.5)],
    });
    const merged = mergeAnsweredUsage([LOCAL, mirror]);
    expect(
      merged.providers.find((entry) => entry.provider === "pi")?.costUsd,
    ).toBe(0.5);
  });

  it("still merges older remote summaries that know nothing about Pi", () => {
    const older = environment("older", {
      contractVersion: 4,
      sources: [source("old", "claude", "/claude")],
      buckets: [bucket("claude", "claude-fixture", "/claude", 2)],
    });
    const merged = mergeAnsweredUsage([LOCAL, older]);
    expect(merged.providers.map((entry) => entry.provider).sort()).toEqual([
      "claude",
      "codex",
      "pi",
    ]);
    expect(merged.contributingEnvironments).toEqual(["local", "older"]);
  });

  it("narrows a model dialog to Pi buckets without losing source ownership", () => {
    const merged = mergeAnsweredUsage(
      [LOCAL],
      (entry) => entry.provider === "pi",
    );
    expect(providersWithUsage(merged.providers)).toEqual(["pi"]);
    expect(merged.totalTokens).toBe(350);
  });
});
