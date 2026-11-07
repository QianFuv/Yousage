/** Model details keep their figures exact in the original grid, inside an inset dialog. */
import { describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { setLanguage } from "../i18n";
import { ZH_CN } from "../locales/zh-CN";
import { mergeAnsweredUsage, type EnvironmentUsageStatus } from "./data";
import { modelDetailStats } from "./modelDetails";
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

  it("shows no environment comparison, since only this device reports usage", () => {
    const markup = renderDialog(ENVIRONMENTS, true);
    expect(markup).not.toContain("<table");
    expect(markup).not.toContain("usage-table-scroll");
    expect(markup).not.toContain("Environment");
    expect(markup).not.toContain(LONG_ENVIRONMENT);
  });

  it("explains unpriced tokens without offering to edit prices", () => {
    const readOnly = renderDialog(ENVIRONMENTS.slice(1, 2), true);
    const desktop = renderDialog(ENVIRONMENTS.slice(1, 2), false);
    for (const markup of [readOnly, desktop]) {
      expect(markup).toContain("1.20K tokens have no known price");
    }
    expect(readOnly).not.toContain("Set price");
    expect(desktop).not.toContain("Set price");
  });

  it("renders its figures and states in the active language", () => {
    setLanguage("zh-CN");
    try {
      const markup = renderDialog(ENVIRONMENTS, true);
      expect(markup).toContain(ZH_CN["Per 1M tokens"]);
      expect(markup).toContain("$12,345,678.90");
    } finally {
      setLanguage("en");
    }
  });
});
