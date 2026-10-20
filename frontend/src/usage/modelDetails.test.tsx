/** Model details keep their figures exact in the original grid and table, inside an inset dialog. */
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { setLanguage } from "../i18n";
import { ZH_CN } from "../locales/zh-CN";
import { mergeAnsweredUsage, type EnvironmentUsageStatus } from "./data";
import { environmentComparisonRows, modelDetailStats } from "./modelDetails";
import type { UsageBucket } from "./contracts";
import { ReadOnlyContext } from "../web/readOnly";
import { UsageModelDialog } from "../../vendor/UsageModelDialog";

vi.mock(
  "../../../tools/parity/upstream/apps/web/src/components/ui/button",
  () => ({
    Button: ({ children }: { readonly children?: ReactNode }) => (
      <button type="button">{children}</button>
    ),
  }),
);
vi.mock("../../vendor/UsageShareBar", () => ({
  UsageShareBar: ({ label }: { readonly label: string }) => (
    <div data-share-bar={label} />
  ),
}));
vi.mock("../../vendor/UsageProviderChart", () => ({
  UsageProviderChart: ({ metric }: { readonly metric: string }) => (
    <div data-chart={metric} />
  ),
}));
vi.mock(
  "../../../tools/parity/upstream/apps/web/src/components/ui/dialog",
  () => {
    /**
     * A plain element in place of a dialog part, showing its options as attributes.
     *
     * @param slot - The dialog part.
     * @returns The stand-in component.
     */
    const part =
      (slot: string) =>
      ({
        children,
        className,
        bottomStickOnMobile,
      }: {
        readonly children?: ReactNode;
        readonly className?: string;
        readonly bottomStickOnMobile?: boolean;
      }) => (
        <div
          data-slot={slot}
          data-bottom-stick={
            bottomStickOnMobile === undefined
              ? undefined
              : String(bottomStickOnMobile)
          }
          className={className}
        >
          {children}
        </div>
      );
    return {
      Dialog: ({ children }: { readonly children?: ReactNode }) => (
        <>{children}</>
      ),
      DialogPopup: part("dialog-popup"),
      DialogHeader: part("dialog-header"),
      DialogTitle: part("dialog-title"),
      DialogDescription: part("dialog-description"),
      DialogPanel: part("dialog-panel"),
      DialogFooter: part("dialog-footer"),
    };
  },
);

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

/**
 * Render the model dialog of the long-named model.
 *
 * @param environments - Environments of the current window.
 * @param isReadOnly - Whether the dialog shows a read-only snapshot.
 * @returns Static markup of the dialog.
 */
function renderDialog(
  environments: readonly EnvironmentUsageStatus[],
  isReadOnly: boolean,
): string {
  return renderToStaticMarkup(
    <ReadOnlyContext value={isReadOnly}>
      <UsageModelDialog
        model={modelTotals(environments)}
        environments={environments}
        metric="cost"
        chartWindow={{
          days: ["2026-10-20"],
          hours: [],
          resolution: "day",
          timeZone: "UTC",
          referenceTime: undefined,
        }}
        onSetPrice={() => {}}
        onClose={() => {}}
      />
    </ReadOnlyContext>,
  );
}

/**
 * Class names of the element with the given data slot.
 *
 * @param markup - Static markup to search.
 * @param slot - The data slot.
 * @returns The class names, or none when absent.
 */
function slotClasses(markup: string, slot: string): string[] {
  return (
    new RegExp(`data-slot="${slot}"[^>]*class="([^"]*)"`)
      .exec(markup)?.[1]
      .split(" ") ?? []
  );
}

/**
 * Inline style of each cell in a table row, in column order.
 *
 * @param row - Inner markup of the row.
 * @returns Each cell's declarations by property, empty when it has no inline style.
 */
function cellStyles(row: string): Record<string, string>[] {
  return [...row.matchAll(/<t[hd]([^>]*)>/g)].map(([, attributes]) =>
    Object.fromEntries(
      (/ style="([^"]*)"/.exec(attributes)?.[1] ?? "")
        .split(";")
        .filter(Boolean)
        .map((declaration) => declaration.split(":")),
    ),
  );
}

/**
 * Declarations of one rule in the app stylesheet.
 *
 * @param selector - The rule's exact selector text.
 * @returns Its declarations by property, empty when the rule is absent.
 */
function stylesheetRule(selector: string): Record<string, string> {
  const css = readFileSync(join(__dirname, "..", "style.css"), "utf8").replace(
    /\r\n/g,
    "\n",
  );
  const start = css.indexOf(`${selector} {`);
  if (start === -1) return {};
  return Object.fromEntries(
    css
      .slice(start + selector.length + 2, css.indexOf("}", start))
      .split(";")
      .map((declaration) => declaration.split(":").map((part) => part.trim()))
      .filter(([property]) => property),
  );
}

/**
 * A rem length as a number.
 *
 * @param length - A CSS length such as `12rem`.
 * @returns The number of rems, or NaN for any other unit.
 */
function rems(length: string | undefined): number {
  const match = /^(\d+(?:\.\d+)?)rem$/.exec(length ?? "");
  return match ? Number(match[1]) : Number.NaN;
}

describe("model detail dialog", () => {
  it("is an inset dialog with rounded corners on every side rather than a sheet stuck to the bottom of phones", () => {
    const markup = renderDialog(ENVIRONMENTS, true);
    expect(markup).toContain('data-bottom-stick="false"');
    expect(slotClasses(markup, "dialog-popup")).toContain("usage-model-dialog");
    expect(slotClasses(markup, "dialog-popup")).not.toContain(
      "max-sm:rounded-t-2xl",
    );
    expect(markup).not.toContain("safe-area-inset-bottom");
    expect(markup).toContain(
      `<div data-slot="dialog-title" class="min-w-0">${LONG_MODEL}</div>`,
    );
    expect(markup).toContain('<div class="flex items-center gap-2 pe-8">');
  });

  it("shows every headline figure as plain label and value in a two- and four-column grid, without cards", () => {
    const markup = renderDialog(ENVIRONMENTS.slice(0, 1), true);
    const grid = /<div class="([^"]*grid-cols-2[^"]*)">/.exec(markup);
    expect(grid?.[1].split(" ")).toEqual(
      expect.arrayContaining(["grid", "grid-cols-2", "sm:grid-cols-4"]),
    );
    expect(markup).not.toContain("max-sm:border");
    expect(markup).not.toContain("max-sm:rounded-lg");
    expect(
      [
        ...markup.matchAll(
          /<span class="text-xs text-muted-foreground">([^<]*)<\/span><span class="([^"]*)">([^<]*)<\/span>/g,
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

  it("compares every environment in one table at every width, with no separate phone list", () => {
    const markup = renderDialog(ENVIRONMENTS, true);
    const rows = environmentComparisonRows(ENVIRONMENTS, "codex", LONG_MODEL);
    expect(markup).not.toContain("<ul");
    expect(markup).not.toContain("sm:hidden");
    expect(markup).toMatch(/<div class="usage-table-scroll"[^>]*><table>/);
    expect(inner(inner(markup, "thead")[0], "th")).toEqual([
      "Environment",
      "Cost",
      "Tokens",
      "Cached input",
    ]);
    expect(
      inner(inner(markup, "tbody")[0], "tr").map((row) => inner(row, "td")),
    ).toEqual(
      rows.map((row) => [row.label, row.cost, row.tokens, row.cachedInput]),
    );
    expect(markup).toContain(
      "Environment values can overlap when they share history.",
    );
  });

  it("keeps a long environment name readable several words a line, with every figure of every row complete and unwrapped", () => {
    const markup = renderDialog(ENVIRONMENTS, true);
    const [header] = inner(inner(markup, "thead")[0], "tr");
    const body = inner(inner(markup, "tbody")[0], "tr");
    expect(inner(body[0], "td")).toEqual([
      LONG_ENVIRONMENT,
      "$12,345,678.90",
      "1.20T",
      "600B",
    ]);
    const [headerName, ...headerFigures] = cellStyles(header);
    expect(headerName).toMatchObject({ "white-space": "normal" });
    expect(headerName).not.toHaveProperty("overflow-wrap");
    // At the table's 0.75rem text, 10rem holds about 26 characters, so whole
    // words such as "workstation" and "deliberately" never break mid-word.
    expect(rems(headerName["min-width"])).toBeGreaterThanOrEqual(10);
    expect(headerFigures).toEqual([{}, {}, {}]);
    for (const row of body) {
      const [name, ...figures] = cellStyles(row);
      expect(name).toEqual({
        "white-space": "normal",
        "overflow-wrap": "anywhere",
        "min-width": headerName["min-width"],
      });
      expect(figures).toEqual([{}, {}, {}]);
    }
    expect(
      stylesheetRule(".usage-table-scroll th,\n.usage-table-scroll td"),
    ).toEqual({ "white-space": "nowrap" });
  });

  it("pans a comparison wider than the dialog inside its own labelled region rather than widening the dialog or page", () => {
    const markup = renderDialog(ENVIRONMENTS, true);
    const heading = /<h3 id="([^"]+)"[^>]*>([^<]*)<\/h3>/.exec(markup);
    expect(heading?.[2]).toBe("Environment comparison");
    // Not a tab stop until it is measured overflowing in a browser.
    expect(markup).toContain(
      `<div class="usage-table-scroll" role="region" aria-labelledby="${heading?.[1]}"><table>`,
    );
    expect(stylesheetRule(".usage-table-scroll")).toEqual({
      overflow: "auto",
      "min-width": "0",
    });
    expect(slotClasses(markup, "dialog-popup").join(" ")).not.toMatch(/min-w-/);
  });

  it("omits the overlap note for a single environment", () => {
    const markup = renderDialog(ENVIRONMENTS.slice(1, 2), true);
    expect(markup).not.toContain("overlap");
    expect(markup).toContain("Unpriced");
  });

  it("explains unpriced tokens everywhere but offers Set price only where prices can be edited", () => {
    const readOnly = renderDialog(ENVIRONMENTS.slice(1, 2), true);
    const desktop = renderDialog(ENVIRONMENTS.slice(1, 2), false);
    for (const markup of [readOnly, desktop]) {
      expect(markup).toContain("1.20K tokens have no known price");
    }
    expect(readOnly).not.toContain("Set price");
    expect(desktop).toContain("Set price");
  });

  it("renders its figures and states in the active language", () => {
    setLanguage("zh-CN");
    try {
      const markup = renderDialog(ENVIRONMENTS, true);
      expect(markup).toContain(ZH_CN["Environment comparison"]);
      expect(markup).toContain(ZH_CN["Unavailable"]);
      expect(markup).toContain("$12,345,678.90");
    } finally {
      setLanguage("en");
    }
  });
});
