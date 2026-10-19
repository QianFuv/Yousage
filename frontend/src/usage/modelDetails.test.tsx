/** Model details keep their figures exact while phones get stacked, wrapping cards instead of a sideways table. */
import { afterEach, describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { setLanguage } from "../i18n";
import { ZH_CN } from "../locales/zh-CN";
import { mergeAnsweredUsage, type EnvironmentUsageStatus } from "./data";
import {
  environmentComparisonRows,
  ModelDetailStats,
  ModelEnvironmentComparison,
  modelDetailStats,
} from "./modelDetails";
import type { UsageBucket } from "./contracts";

const LONG_MODEL =
  "fixture-backend/an-extremely-long-model-identifier-without-natural-breaks-2026-10-20";
const LONG_ENVIRONMENT =
  "Remote workstation with a deliberately long environment label for narrow phones";

/**
 * Build one day bucket of the long-named model.
 *
 * @param costUsd - Cost of the bucket.
 * @param costSource - How the cost was determined.
 * @param scale - Multiplier applied to every token count.
 * @returns The bucket.
 */
function bucket(
  costUsd: number,
  costSource: UsageBucket["costSource"],
  scale = 1,
): UsageBucket {
  return {
    day: "2026-10-20",
    provider: "codex",
    model: LONG_MODEL,
    totals: {
      uncachedInputTokens: 300 * scale,
      cachedInputTokens: 600 * scale,
      cacheCreationTokens: 100 * scale,
      outputTokens: 200 * scale,
      reasoningTokens: 0,
    },
    costUsd,
    cacheSavingsUsd: 0,
    costSource,
    records: 2,
    unpricedRecords: costSource === "unpriced" ? 2 : 0,
    sessions: 1,
  };
}

/**
 * Build one environment status.
 *
 * @param environmentId - Stable environment identity.
 * @param label - Display label.
 * @param buckets - Usage buckets, or `null` when the environment has not answered.
 * @returns The environment status.
 */
function environment(
  environmentId: string,
  label: string,
  buckets: UsageBucket[] | null,
): EnvironmentUsageStatus {
  return {
    environmentId,
    label,
    local: environmentId === "local",
    connected: buckets !== null,
    canOperate: true,
    refreshing: false,
    serverConfig: null,
    summary:
      buckets === null
        ? null
        : {
            contractVersion: 6,
            readAt: "2026-10-20T12:00:00Z",
            timeZone: "UTC",
            sinceDay: "2026-10-20",
            untilDay: "2026-10-20",
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
                  hostId: environmentId,
                  provider: "codex",
                  resolvedHomePath: `/${environmentId}/codex`,
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
            buckets,
          },
  };
}

/** Priced huge usage, unpriced usage, no usage of the model, and an environment that has not answered. */
const ENVIRONMENTS = [
  environment("local", LONG_ENVIRONMENT, [
    bucket(12_345_678.9, "modelPriced", 1_000_000_000),
  ]),
  environment("unpriced", "Unpriced host", [bucket(0, "unpriced")]),
  environment("idle", "Idle host", []),
  environment("offline", "Offline host", null),
];

/**
 * Inner markup of every element with the given tag, in document order.
 *
 * @param markup - Static markup to search.
 * @param tag - Element tag name without attributes.
 * @returns Each element's inner markup.
 */
function inner(markup: string, tag: string): string[] {
  return [
    ...markup.matchAll(new RegExp(`<${tag}(?: [^>]*)?>(.*?)</${tag}>`, "gs")),
  ].map((match) => match[1]);
}

/**
 * Class names of the first element with the given tag.
 *
 * @param markup - Static markup to search.
 * @param tag - Element tag name.
 * @returns The class names, or none when absent.
 */
function classesOf(markup: string, tag: string): string[] {
  return (
    new RegExp(`<${tag} [^>]*class="([^"]*)"`).exec(markup)?.[1].split(" ") ??
    []
  );
}

/**
 * The merged totals of the long-named model across the given environments.
 *
 * @param environments - Environments to merge.
 * @returns The model's totals.
 */
function modelTotals(environments: readonly EnvironmentUsageStatus[]) {
  const model = mergeAnsweredUsage(environments).models.find(
    (entry) => entry.model === LONG_MODEL,
  );
  if (model === undefined) throw new Error("fixture model missing");
  return model;
}

describe("model detail figures", () => {
  it("shows exact huge priced figures with per-million cost and cache hit", () => {
    const stats = modelDetailStats(modelTotals(ENVIRONMENTS.slice(0, 1)));
    expect(stats).toEqual([
      { label: "Cost", value: "$12,345,678.90" },
      { label: "Tokens", value: "1.20T" },
      { label: "Per 1M tokens", value: "$10.29" },
      { label: "Cache hit", value: "60.0%" },
    ]);
  });

  it("reports unknown cost as unpriced rather than zero and omits its rate", () => {
    const stats = modelDetailStats(modelTotals(ENVIRONMENTS.slice(1, 2)));
    expect(stats.map((stat) => stat.value)).toEqual([
      "Unpriced",
      "1.20K",
      "60.0%",
    ]);
  });

  it("keeps unanswered, unpriced and idle environments distinct", () => {
    const rows = environmentComparisonRows(ENVIRONMENTS, "codex", LONG_MODEL);
    expect(rows).toEqual([
      {
        id: "local",
        label: LONG_ENVIRONMENT,
        cost: "$12,345,678.90",
        tokens: "1.20T",
        cachedInput: "600B",
      },
      {
        id: "unpriced",
        label: "Unpriced host",
        cost: "Unpriced",
        tokens: "1.20K",
        cachedInput: "600",
      },
      {
        id: "idle",
        label: "Idle host",
        cost: "$0.00",
        tokens: "0",
        cachedInput: "0",
      },
      {
        id: "offline",
        label: "Offline host",
        cost: "Unavailable",
        tokens: "—",
        cachedInput: "—",
      },
    ]);
  });
});

describe("model detail localization", () => {
  afterEach(() => setLanguage("en"));

  it("localizes comparison states in the active language so the dialog must recompute them", () => {
    setLanguage("zh-CN");
    const rows = environmentComparisonRows(ENVIRONMENTS, "codex", LONG_MODEL);
    expect(rows.map((row) => row.cost)).toEqual([
      "$12,345,678.90",
      ZH_CN["Unpriced"],
      "$0.00",
      ZH_CN["Unavailable"],
    ]);
    setLanguage("en");
    expect(
      environmentComparisonRows(ENVIRONMENTS, "codex", LONG_MODEL).map(
        (row) => row.cost,
      ),
    ).toEqual(["$12,345,678.90", "Unpriced", "$0.00", "Unavailable"]);
  });
});

describe("model detail phone layout", () => {
  it("stacks every environment with all figures on phones and keeps the wide table", () => {
    const rows = environmentComparisonRows(ENVIRONMENTS, "codex", LONG_MODEL);
    const markup = renderToStaticMarkup(
      <ModelEnvironmentComparison rows={rows} />,
    );
    const [phoneList] = inner(markup, "ul");
    const cards = inner(phoneList, "li");
    const tableRows = inner(inner(markup, "tbody")[0], "tr");

    expect(classesOf(markup, "ul")).toContain("sm:hidden");
    expect(classesOf(markup.slice(markup.indexOf("</ul>")), "div")).toEqual(
      expect.arrayContaining(["usage-table-scroll", "max-sm:hidden"]),
    );
    expect(cards).toHaveLength(ENVIRONMENTS.length);
    expect(
      cards.map((card) => [...inner(card, "p"), ...inner(card, "dd")]),
    ).toEqual(tableRows.map((row) => inner(row, "td")));
    expect(inner(cards[0], "dt")).toEqual(["Cost", "Tokens", "Cached input"]);
    expect(classesOf(cards[0], "p")).toContain("wrap-anywhere");
    expect(markup).toContain(
      "Environment values can overlap when they share history.",
    );
  });

  it("omits the overlap note for a single environment", () => {
    const markup = renderToStaticMarkup(
      <ModelEnvironmentComparison
        rows={environmentComparisonRows(
          ENVIRONMENTS.slice(3),
          "codex",
          LONG_MODEL,
        )}
      />,
    );
    expect(markup).not.toContain("overlap");
    expect(markup).toContain("Unavailable");
  });

  it("lays headline figures out two by two on phones with wrapping values", () => {
    const markup = renderToStaticMarkup(
      <ModelDetailStats
        stats={modelDetailStats(modelTotals(ENVIRONMENTS.slice(0, 1)))}
      />,
    );
    expect(classesOf(markup, "div")).toEqual(
      expect.arrayContaining(["grid-cols-2", "sm:grid-cols-4"]),
    );
    expect(
      [
        ...markup.matchAll(
          /<span class="[^"]*">([^<]*)<\/span><span class="([^"]*)">([^<]*)<\/span>/g,
        ),
      ].map(([, label, classes, value]) => [
        label,
        value,
        classes.split(" ").includes("wrap-anywhere"),
      ]),
    ).toEqual([
      ["Cost", "$12,345,678.90", true],
      ["Tokens", "1.20T", true],
      ["Per 1M tokens", "$10.29", true],
      ["Cache hit", "60.0%", true],
    ]);
  });
});
