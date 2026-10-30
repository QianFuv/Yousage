/** Phones keep every desktop report figure and explanation visible or tappable, from the same data and permissions. */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactElement, ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import type { LimitPresentations } from "@t3tools/shared/usageLimits";
import {
  LossReport,
  SpeedReport,
} from "../../bindings/yousage/internal/service/models";
import { setLanguage } from "../i18n";
import { ReadOnlyContext } from "../web/readOnly";
import { LossContent } from "../loss/LossPage";
import { SpeedContent } from "../speed/SpeedPage";
import { UsageShareBar } from "../../vendor/UsageShareBar";
import { PaceIcon } from "../../vendor/UsageLimits";
import { UsageLimitsPooled } from "../../vendor/UsageLimitsPooled";
import { UsageStatsPanel } from "./UsageStatsPanel";
import { cycleValueKey } from "./CycleValue";
import { useResetCredit } from "./host";
import { mergeAnsweredUsage, type CycleValue } from "./data";
import type { EnvironmentUsageStatus } from "./data";
import type { ServerProvider, UsageBucket } from "./contracts";

const harness = vi.hoisted(() => ({
  /** Initial values handed to the next `useState(null)` calls, oldest first. */
  forcedNullStates: [] as unknown[],
  /** Setters handed out with forced states, in the same order. */
  forcedSetters: [] as ((value: unknown) => void)[],
  /** Whether popovers render their popup inline instead of through a portal. */
  isPopoverInline: false,
  /** Props of every model dialog rendered. */
  dialogs: [] as { model: { model: string } }[],
}));

vi.mock("react", async (importOriginal) => {
  const actual = await importOriginal<typeof import("react")>();
  return {
    ...actual,
    useState: (initial: unknown) => {
      if (initial === null && harness.forcedNullStates.length > 0) {
        const setter = vi.fn();
        harness.forcedSetters.push(setter);
        return [harness.forcedNullStates.shift(), setter];
      }
      return actual.useState(initial);
    },
  };
});
vi.mock("./controller", () => ({
  useData: () => ({
    display: { value: { breakdown: "model", timestampFormat: "24-hour" } },
    patchDisplay: async () => {},
    isBusy: false,
    refreshState: { isRefreshing: false, lastCompletedAt: "", lastError: "" },
    refresh: async () => {},
  }),
}));
vi.mock("./host", async (importOriginal) => ({
  ...(await importOriginal<typeof import("./host")>()),
  useResetCredit: vi.fn(() => ({
    confirming: false,
    setConfirming: () => {},
    busy: false,
    redeem: async () => {},
  })),
}));
vi.mock("../receipt/ReceiptButton", () => ({ ReceiptButton: () => null }));
vi.mock("../../vendor/UsageProviderChart", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../vendor/UsageProviderChart")>()),
  UsageProviderChart: () => <div data-chart />,
}));
vi.mock("../../vendor/UsageModelDialog", () => ({
  SpeedPremium: () => null,
  UsageModelDialog: (props: { model: { model: string } }) => {
    harness.dialogs.push(props);
    return <div data-dialog={props.model.model} />;
  },
}));

/*
 * Base UI resolves its own React copy, so its primitives are replaced by plain elements that keep
 * the trigger element, its hover option and the popup content, the parts these pages decide.
 */
vi.mock(
  "../../../tools/parity/upstream/apps/web/src/components/ui/button",
  () => ({
    Button: ({
      children,
      className,
      disabled,
    }: {
      readonly children?: ReactNode;
      readonly className?: string;
      readonly disabled?: boolean;
    }) => (
      <button type="button" className={className} disabled={disabled}>
        {children}
      </button>
    ),
    InlineButton: ({
      tone,
      ...props
    }: { readonly tone?: string } & Record<string, unknown>) => (
      <button
        type="button"
        data-slot="inline-button"
        data-tone={tone}
        {...props}
      />
    ),
  }),
);
vi.mock(
  "../../../tools/parity/upstream/apps/web/src/components/ui/popover",
  async () => {
    const { cloneElement } = await import("react");
    /** A shared popover handle: the first trigger rendered becomes its active payload. */
    type Handle = { payload: unknown; close: () => void };
    return {
      PopoverCreateHandle: (): Handle => ({
        payload: undefined,
        close: () => {},
      }),
      Popover: ({
        children,
        handle,
      }: {
        readonly children?:
          | ReactNode
          | ((state: { readonly payload: unknown }) => ReactNode);
        readonly handle?: Handle;
      }) => (
        <>
          {typeof children === "function"
            ? children({ payload: handle?.payload })
            : children}
        </>
      ),
      PopoverTrigger: ({
        render,
        openOnHover,
        handle,
        payload,
        children,
        ...props
      }: {
        readonly render?: ReactElement;
        readonly openOnHover?: boolean;
        readonly handle?: Handle;
        readonly payload?: unknown;
        readonly children?: ReactNode;
      } & Record<string, unknown>) => {
        if (handle && handle.payload === undefined) handle.payload = payload;
        return cloneElement(
          render ?? <button type="button" />,
          { ...props, "data-open-on-hover": openOnHover ? "" : undefined },
          children,
        );
      },
      PopoverPopup: ({ children }: { readonly children?: ReactNode }) => (
        <div data-popup hidden={!harness.isPopoverInline}>
          {children}
        </div>
      ),
    };
  },
);
vi.mock(
  "../../../tools/parity/upstream/apps/web/src/components/ui/tooltip",
  async () => {
    const { cloneElement } = await import("react");
    return {
      Tooltip: ({ children }: { readonly children?: ReactNode }) => (
        <>{children}</>
      ),
      TooltipTrigger: ({
        render,
        children,
      }: {
        readonly render: ReactElement;
        readonly children?: ReactNode;
      }) => cloneElement(render, {}, children),
      TooltipPopup: ({ children }: { readonly children?: ReactNode }) => (
        <div data-tooltip hidden>
          {children}
        </div>
      ),
    };
  },
);
vi.mock(
  "../../../tools/parity/upstream/apps/web/src/components/ui/toggle-group",
  () => ({
    ToggleGroup: ({ children }: { readonly children?: ReactNode }) => (
      <div>{children}</div>
    ),
    Toggle: ({ children }: { readonly children?: ReactNode }) => (
      <button type="button">{children}</button>
    ),
  }),
);
vi.mock("../../vendor/ui/alert-dialog", () => {
  const nothing = () => null;
  return {
    AlertDialog: nothing,
    AlertDialogClose: nothing,
    AlertDialogDescription: nothing,
    AlertDialogFooter: nothing,
    AlertDialogHeader: nothing,
    AlertDialogPopup: nothing,
    AlertDialogTitle: nothing,
  };
});

beforeEach(() => {
  harness.forcedNullStates = [];
  harness.forcedSetters = [];
  harness.isPopoverInline = false;
  harness.dialogs = [];
  vi.mocked(useResetCredit).mockClear();
});
afterEach(() => setLanguage("en"));

/** Elements without a closing tag in serialized HTML. */
const VOID_ELEMENTS = new Set(["br", "hr", "img", "input", "meta", "wbr"]);

/**
 * Whether an element's classes hide it at a viewport width.
 *
 * Only display utilities used by these pages are understood: an unprefixed `hidden`
 * applies on phones, `md:hidden` from 768 px, and `md:table-cell` reveals from 768 px.
 *
 * @param classes - The element's class attribute.
 * @param viewport - Phone below 640 px or desktop at 1024 px and wider.
 * @returns Whether the element is not displayed.
 */
function isHiddenAt(classes: string, viewport: "phone" | "desktop"): boolean {
  const tokens = classes.split(/\s+/);
  if (tokens.includes("sr-only")) return true;
  if (viewport === "phone")
    return tokens.some((token) =>
      ["hidden", "max-sm:hidden", "max-md:hidden"].includes(token),
    );
  if (tokens.some((token) => /^(sm|md|lg):hidden$/.test(token))) return true;
  return (
    tokens.includes("hidden") &&
    !tokens.some((token) => /^(sm|md|lg):(?!hidden)/.test(token))
  );
}

/**
 * Text a sighted reader sees at a viewport width, ignoring attributes such as `title`.
 *
 * @param markup - Static markup.
 * @param viewport - Phone or desktop width.
 * @returns Visible text with collapsed whitespace.
 */
function visibleText(markup: string, viewport: "phone" | "desktop"): string {
  const hidden: boolean[] = [false];
  let text = "";
  for (const match of markup.matchAll(
    /<(\/?)([a-zA-Z][\w-]*)([^>]*?)(\/?)>|([^<]+)/g,
  )) {
    const [, closing, tag, attributes, selfClosing, content] = match;
    const isParentHidden = hidden[hidden.length - 1];
    if (content !== undefined) {
      if (!isParentHidden) text += content;
    } else if (closing) hidden.pop();
    else if (!VOID_ELEMENTS.has(tag.toLowerCase()) && !selfClosing) {
      const classes = /\bclass="([^"]*)"/.exec(attributes)?.[1] ?? "";
      hidden.push(
        isParentHidden ||
          / hidden(=|\s|$)/.test(attributes) ||
          isHiddenAt(classes, viewport),
      );
      text += " ";
    }
  }
  return text
    .replace(/&amp;/g, "&")
    .replace(/&lt;/g, "<")
    .replace(/&gt;/g, ">")
    .replace(/&quot;/g, '"')
    .replace(/&#x27;/g, "'")
    .replace(/\s+/g, " ");
}

/**
 * Render an element and read it at both widths.
 *
 * @param element - Element to render.
 * @param isReadOnly - Whether it renders inside the relay viewer.
 * @returns The markup and its phone and desktop text.
 */
function render(element: ReactElement, isReadOnly = true) {
  const markup = renderToStaticMarkup(
    <ReadOnlyContext.Provider value={isReadOnly}>
      {element}
    </ReadOnlyContext.Provider>,
  );
  return {
    markup,
    phone: visibleText(markup, "phone"),
    desktop: visibleText(markup, "desktop"),
  };
}

/**
 * Count occurrences of a fragment.
 *
 * @param text - Text to search.
 * @param fragment - Fragment to count.
 * @returns How often it occurs.
 */
function occurrences(text: string, fragment: string): number {
  return text.split(fragment).length - 1;
}

const LONG_MODEL =
  "fixture-backend/an-extremely-long-model-identifier-without-natural-breaks-2026-10-20";

/**
 * One answered environment with a single day of usage.
 *
 * @param models - Model name, cost and token scale of each bucket.
 * @returns The environment.
 */
function usageEnvironment(
  models: readonly (readonly [string, number, number])[],
): EnvironmentUsageStatus {
  const buckets: UsageBucket[] = models.map(([model, costUsd, tokens]) => ({
    day: "2026-10-20",
    provider: "codex",
    model,
    totals: {
      uncachedInputTokens: tokens / 2,
      cachedInputTokens: tokens / 4,
      cacheCreationTokens: 0,
      outputTokens: tokens / 4,
      reasoningTokens: 0,
    },
    costUsd,
    cacheSavingsUsd: 0,
    costSource: "modelPriced",
    records: 1,
    unpricedRecords: 0,
    sessions: 1,
  }));
  return {
    environmentId: "local",
    label: "Desktop",
    local: true,
    connected: true,
    canOperate: true,
    refreshing: false,
    serverConfig: null,
    summary: {
      contractVersion: 6,
      readAt: "2026-10-20T12:00:00Z",
      timeZone: "UTC",
      sinceDay: "2026-10-20",
      untilDay: "2026-10-20",
      pricing: {
        status: "cached",
        source: "fixture",
        fetchedAt: null,
        knownModels: 2,
      },
      scanDurationMs: 1,
      sources: [
        {
          fingerprint: {
            hostId: "local",
            provider: "codex",
            resolvedHomePath: "/local/codex",
            volumeId: "v",
          },
          status: "ok",
          scannedFiles: 1,
          skippedFiles: 0,
          malformedRecords: 0,
          distinctSessions: 2,
          message: null,
        },
      ],
      buckets,
    },
  };
}

/**
 * Render the usage statistics of one environment.
 *
 * @param environment - The answered environment.
 * @param metric - Cost or tokens.
 * @returns The rendered page.
 */
function renderUsage(
  environment: EnvironmentUsageStatus,
  metric: "cost" | "tokens",
) {
  return render(
    <UsageStatsPanel
      merged={mergeAnsweredUsage([environment])}
      environments={[environment]}
      chartWindow={{
        days: ["2026-10-20"],
        hours: [],
        resolution: "day",
        timeZone: "UTC",
        referenceTime: undefined,
      }}
      metric={metric}
      hasChatGptSharing={false}
      onManageChatGpt={() => {}}
      onSetPrice={() => {}}
    />,
  );
}

/** $62.50 of 6,000 tokens and $37.50 of 2,000 tokens: 62.5%/37.5% of cost, 75.0%/25.0% of tokens. */
const usage = () =>
  usageEnvironment([
    [LONG_MODEL, 62.5, 6000],
    ["fixture-small", 37.5, 2000],
  ]);

describe("usage model breakdown", () => {
  it("shows each model's cost share on phones, under the model, as the desktop column does", () => {
    const page = renderUsage(usage(), "cost");
    expect(page.phone).toContain(`${LONG_MODEL} Share 62.5%`);
    expect(page.phone).toContain("fixture-small Share 37.5%");
    expect(occurrences(page.desktop, "62.5%")).toBe(1);
    expect(page.desktop).toMatch(/Cost Share Tokens/);
    expect(page.desktop).not.toContain("Share 62.5%");
  });

  it("shows each model's token share on phones in token mode", () => {
    const page = renderUsage(usage(), "tokens");
    expect(page.phone).toContain(`${LONG_MODEL} Share 75.0%`);
    expect(page.phone).toContain("fixture-small Share 25.0%");
  });

  it("forgets a selected model that left the data, so it cannot reopen when it returns", () => {
    harness.forcedNullStates = [`codex:${LONG_MODEL}`];
    renderUsage(usageEnvironment([["fixture-small", 1, 100]]), "cost");
    expect(harness.forcedSetters[0]).toHaveBeenCalledWith(null);
    expect(harness.dialogs).toEqual([]);
  });

  it("keeps a selected model that is still present", () => {
    harness.forcedNullStates = [`codex:${LONG_MODEL}`];
    renderUsage(usage(), "cost");
    expect(harness.forcedSetters[0]).not.toHaveBeenCalled();
    expect(harness.dialogs.map((dialog) => dialog.model.model)).toEqual([
      LONG_MODEL,
    ]);
  });
});

describe("share bars", () => {
  const segments = [
    { label: "Input", value: 30, color: "red" },
    { label: "Output", value: 10, color: "blue" },
  ];

  it("shows every segment's percentage in the legend at every width", () => {
    const page = render(
      <UsageShareBar
        label="Cost by type"
        segments={segments}
        format={String}
      />,
    );
    for (const text of [page.phone, page.desktop]) {
      expect(text).toContain("Input 30 75.0%");
      expect(text).toContain("Output 10 25.0%");
    }
    expect(page.markup).toContain(
      'aria-label="Cost by type: Input 30 75.0%, Output 10 25.0%"',
    );
  });
});

/** Loss report with one estimated, one exact and one unknown-gap event. */
function lossReport(events: Record<string, unknown>[]): LossReport {
  return LossReport.createFrom({
    window: {
      sinceDay: "2026-10-14",
      untilDay: "2026-10-20",
      resolution: "day",
      timeZone: "UTC",
    },
    cacheExpiry: { events: 7, tokens: 999_000, costUsd: 4.56 },
    providers: [
      { provider: "codex", events: 7, tokens: 999_000, costUsd: 4.56 },
    ],
    days: [
      {
        day: "2026-10-20",
        providers: [{ provider: "codex", costUsd: 4.56, count: 7 }],
      },
    ],
    events,
  });
}

/**
 * One cache-expiry event.
 *
 * @param fields - Fields replacing the defaults.
 * @returns The event.
 */
function lossEvent(fields: Record<string, unknown> = {}) {
  return {
    kind: "cacheExpiry",
    provider: "codex",
    model: LONG_MODEL,
    sessionId: "s",
    atMs: Date.UTC(2026, 9, 20, 9, 30),
    idleMs: 754_000,
    tokens: 123_456,
    costUsd: 1.23,
    precision: "exact",
    ...fields,
  };
}

describe("loss events", () => {
  it("shows each event's gap and tokens on phones, labelled, beside the full model name", () => {
    const page = render(
      <LossContent
        report={lossReport([
          lossEvent(),
          lossEvent({ idleMs: 0, tokens: 0, atMs: 1 }),
        ])}
      />,
    );
    expect(page.phone).toContain(`${LONG_MODEL} Gap 12m · Tokens 123K`);
    expect(page.phone).toContain("Gap — · Tokens —");
    expect(page.desktop).toContain("12m 123K");
    expect(page.desktop).not.toContain("Gap 12m");
    for (const text of [page.phone, page.desktop]) {
      expect(text).toContain("$4.56");
      expect(text).toContain("Extra spend · 7 events");
    }
  });

  it("explains the estimate mark in visible text only when a shown event is estimated", () => {
    const estimated = render(
      <LossContent
        report={lossReport([lossEvent({ precision: "estimated" })])}
      />,
    );
    for (const text of [estimated.phone, estimated.desktop])
      expect(text).toContain("≈ Estimated from cache hit rates");

    const exact = render(<LossContent report={lossReport([lossEvent()])} />);
    expect(exact.phone).not.toContain("Estimated from cache hit rates");

    const hiddenEstimate = render(
      <LossContent
        report={lossReport([
          ...Array.from({ length: 20 }, (_, index) =>
            lossEvent({ atMs: index }),
          ),
          lossEvent({ precision: "estimated", atMs: 99 }),
        ])}
      />,
    );
    expect(hiddenEstimate.phone).not.toContain(
      "Estimated from cache hit rates",
    );
    expect(hiddenEstimate.phone).toContain("Show all 21 events");
  });
});

/** Speed report with a measured row partly without timing and a Pi row without any timing. */
const SPEED = SpeedReport.createFrom({
  window: {
    sinceDay: "2026-10-14",
    untilDay: "2026-10-20",
    resolution: "day",
    timeZone: "UTC",
  },
  overall: { requests: 12, unmeasuredRequests: 21, tokensPerSecond: 48.2 },
  models: [
    {
      provider: "codex",
      model: LONG_MODEL,
      speed: "fast",
      requests: 12,
      unmeasuredRequests: 3,
      totalTokens: 456_000,
      tokensPerSecond: 48.2,
      medianTokensPerSecond: 45.1,
      p10TokensPerSecond: 20.4,
      p90TokensPerSecond: 88.8,
      medianTtftMs: 850,
      decodeTokensPerSecond: 71.3,
      medianLatencyMs: 2400,
      inputTokens: 1000,
      cacheHitRate: 0.625,
    },
    {
      provider: "pi",
      model: "deepseek/deepseek-flash",
      speed: "standard",
      requests: 0,
      unmeasuredRequests: 18,
      totalTokens: 7_000,
      inputTokens: 5000,
      cacheHitRate: 0.4,
    },
  ],
  providers: [
    { provider: "codex", requests: 12, unmeasuredRequests: 3 },
    { provider: "pi", requests: 0, unmeasuredRequests: 18 },
  ],
  buckets: [],
  measuredRequests: 12,
  unmeasuredRequests: 21,
});

describe("speed models", () => {
  it("shows every figure of a model on phones, labelled, under its name", () => {
    const page = render(<SpeedContent report={SPEED} />);
    expect(page.phone).toContain(
      `${LONG_MODEL} Fast Output speed 48.2 tok/s TTFT 850 ms Generation speed 71.3 tok/s Latency 2.4 s Cache hit 62.5% Requests 12 +3 Tokens 456K`,
    );
    expect(page.phone).toContain(
      "deepseek/deepseek-flash Output speed — TTFT — Generation speed — Latency — Cache hit 40.0% Requests 0 +18 Tokens 7K",
    );
    expect(page.desktop).toContain(
      "48.2 tok/s 71.3 tok/s 850 ms 2.4 s 62.5% 12 +3 456K",
    );
    expect(page.desktop).not.toContain("Output speed 48.2 tok/s");
  });

  it("gives the model name the full phone row, keeping each label beside its value", () => {
    const { markup } = render(<SpeedContent report={SPEED} />);
    const phoneCells = [...markup.matchAll(/<td class="([^"]*)"/g)].filter(
      ([, classes]) => !isHiddenAt(classes, "phone"),
    );
    expect(phoneCells).toHaveLength(2 * SPEED.models.length);
    expect(markup).toContain(
      '<span>Output speed <span class="text-foreground">48.2 tok/s</span></span>',
    );
    expect(markup).toContain("<span>Requests <span>12 +3</span></span>");
    expect(markup).toMatch(/class="[^"]*flex-wrap[^"]*md:hidden"/);
  });

  it("reserves room for the model name once every column shows, scrolling the table instead", () => {
    const { markup } = render(<SpeedContent report={SPEED} />);
    expect(
      markup.match(/class="flex min-w-0 items-center gap-2 md:min-w-48"/g),
    ).toHaveLength(SPEED.models.length);
    expect(markup).toMatch(
      /<div class="usage-table-scroll" tabindex="0" aria-label="Model speed table">/,
    );
  });

  it("keeps the empty state readable on phones", () => {
    const page = render(
      <SpeedContent
        report={SpeedReport.createFrom({
          ...SPEED,
          models: [],
          providers: [],
          overall: { requests: 0, unmeasuredRequests: 0 },
          measuredRequests: 0,
          unmeasuredRequests: 0,
        })}
      />,
    );
    expect(page.phone).toContain("No measured requests in this window.");
  });

  it("shows the rate spread and untimed requests as visible text at every width", () => {
    const page = render(<SpeedContent report={SPEED} />);
    for (const text of [page.phone, page.desktop]) {
      expect(text).toContain(
        "Median 45.1 tok/s · P10–P90 20.4–88.8 · 3 without timing",
      );
      expect(text).toContain("deepseek/deepseek-flash");
      expect(text).toContain("18 without timing");
    }
    expect(page.markup).not.toMatch(/title="[^"]*(Median|without timing)/);
  });

  it("never invents timing for requests without it", () => {
    const page = render(<SpeedContent report={SPEED} />);
    const piRow = page.phone.slice(
      page.phone.indexOf("deepseek/deepseek-flash"),
    );
    expect(piRow).not.toMatch(/Median|P10–P90/);
    expect(piRow).not.toMatch(/\d tok\/s|\d ms\b/);
  });

  it("lets long model names wrap instead of truncating them", () => {
    const { markup } = render(<SpeedContent report={SPEED} />);
    expect(markup).toMatch(
      new RegExp(
        `<span class="[^"]*whitespace-normal[^"]*">${LONG_MODEL}</span>`,
      ),
    );
    expect(markup).not.toContain('class="truncate"');
  });
});

describe("pace", () => {
  it("is a button that opens its explanation on hover, tap or keyboard", () => {
    const { markup } = render(<PaceIcon pace="ahead" />);
    expect(markup).toMatch(
      /^<button[^>]*aria-label="Ahead of pace: spending faster than the window elapses"/,
    );
    expect(markup).toContain('data-slot="inline-button"');
    expect(markup).toContain('data-open-on-hover=""');
    expect(markup).toMatch(
      /<div data-popup="true" hidden="">Ahead of pace: spending faster than the window elapses<\/div>/,
    );
    expect(markup).not.toContain('role="img"');
  });
});

const NOW = Date.parse("2026-10-20T12:00:00Z");

/**
 * One Codex account with a primary window, two banked reset credits and its email.
 *
 * @returns The limits page presentations.
 */
function limitPresentations(): LimitPresentations {
  const provider: ServerProvider = {
    instanceId: "codex-work",
    driver: "codex",
    enabled: true,
    installed: true,
    status: "ready",
    auth: { status: "authenticated", email: "someone@example.com" },
    usageLimits: {
      checkedAt: "2026-10-20T11:00:00Z",
      windows: [
        {
          id: "primary",
          kind: "session",
          label: "5h",
          usedPercent: 40,
          resetsAt: "2026-10-20T14:00:00Z",
          windowDurationMins: 300,
        } as never,
      ],
      resetCredits: {
        availableCount: 2,
        nextExpiresAt: new Date(NOW + 27 * 86_400_000).toISOString(),
        nextCreditId: "credit-1",
      },
    },
    slashCommands: [],
  };
  return new Map([
    [
      "local",
      {
        entry: { target: { label: "Desktop" } },
        serverConfig: { providers: [provider], usageLimitSources: [] },
      },
    ],
  ]) as LimitPresentations;
}

/** A cycle value estimated from local logs for the account's primary window. */
const CYCLE_VALUES = new Map([
  [
    cycleValueKey("local:codex-work", "primary"),
    {
      instanceId: "codex-work",
      driver: "codex",
      windowId: "primary",
      status: "estimated",
      resetDetected: false,
      usedPercent: 40,
      usedLowPercent: 39,
      usedHighPercent: 41,
      spentUsd: 12,
      records: 10,
      unpricedRecords: 0,
      valueUsd: 30,
      constraintCount: 1,
      samples: [],
      observationSource: "logs",
      priorUsed: false,
      priorCycleCount: 0,
      adjustmentSuspected: false,
      history: [],
    } as unknown as CycleValue,
  ],
]);

describe("quota account popover", () => {
  it("shows banked reset credits read-only, without the redeem action or its hook", () => {
    harness.isPopoverInline = true;
    const page = render(
      <UsageLimitsPooled
        presentations={limitPresentations()}
        now={NOW}
        cycleValues={CYCLE_VALUES}
      />,
    );
    expect(page.phone).toContain("2 banked · expires in 27d");
    expect(page.phone).not.toContain("Use reset");
    expect(useResetCredit).not.toHaveBeenCalled();
    expect(page.markup).toMatch(
      /<span class="blur-sm select-none">someone@example.com<\/span>/,
    );
  });

  it("keeps the reset action on the desktop that can redeem", () => {
    harness.isPopoverInline = true;
    const page = render(
      <UsageLimitsPooled
        presentations={limitPresentations()}
        now={NOW}
        cycleValues={CYCLE_VALUES}
      />,
      false,
    );
    expect(page.desktop).toContain("2 banked · expires in 27d");
    expect(page.desktop).toContain("Use reset");
    expect(useResetCredit).toHaveBeenCalled();
  });

  it("names the source desktop as where counted usage was recorded in the viewer", () => {
    harness.isPopoverInline = true;
    const viewer = render(
      <UsageLimitsPooled
        presentations={limitPresentations()}
        now={NOW}
        cycleValues={CYCLE_VALUES}
      />,
    );
    expect(viewer.phone).toContain(
      "Only usage recorded on the source desktop is counted.",
    );
    expect(viewer.phone).not.toContain("on this device");
    const desktop = render(
      <UsageLimitsPooled
        presentations={limitPresentations()}
        now={NOW}
        cycleValues={CYCLE_VALUES}
      />,
      false,
    );
    expect(desktop.desktop).toContain(
      "Only usage recorded on this device is counted.",
    );
    setLanguage("zh-CN");
    expect(
      render(
        <UsageLimitsPooled
          presentations={limitPresentations()}
          now={NOW}
          cycleValues={CYCLE_VALUES}
        />,
      ).phone,
    ).toContain("仅统计来源桌面端记录的用量。");
  });

  it("says the source desktop recorded no usage this cycle in the viewer", () => {
    harness.isPopoverInline = true;
    const unrecorded = withCycleStatus("noLocalUsage");
    const viewer = render(
      <UsageLimitsPooled
        presentations={limitPresentations()}
        now={NOW}
        cycleValues={unrecorded}
      />,
    );
    expect(viewer.phone).toContain(
      "Quota was used, but no usage in this cycle was recorded on the source desktop.",
    );
    expect(viewer.phone).not.toContain("on this device");
    const desktop = render(
      <UsageLimitsPooled
        presentations={limitPresentations()}
        now={NOW}
        cycleValues={unrecorded}
      />,
      false,
    );
    expect(desktop.desktop).toContain(
      "Quota was used, but no usage in this cycle was recorded on this device.",
    );
    expect(
      render(
        <UsageLimitsPooled
          presentations={limitPresentations()}
          now={NOW}
          cycleValues={withCycleStatus("inconsistent")}
        />,
      ).phone,
    ).toContain(
      "Observations disagree: quota use was not proportional to API prices, or some usage happened elsewhere.",
    );
    setLanguage("zh-CN");
    expect(
      render(
        <UsageLimitsPooled
          presentations={limitPresentations()}
          now={NOW}
          cycleValues={unrecorded}
        />,
      ).phone,
    ).toContain("额度已被使用，但来源桌面端没有记录本周期的用量。");
  });
});

/**
 * The account's cycle values with a different estimate status.
 *
 * @param status - Status of the primary window's cycle value.
 * @returns Cycle values keyed like the limits page expects.
 */
function withCycleStatus(status: CycleValue["status"]) {
  return new Map(
    [...CYCLE_VALUES].map(([key, value]) => [key, { ...value, status }]),
  );
}
