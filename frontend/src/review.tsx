/** Development-only browser transport fixture; production index never imports this module. */
import { objectNames, setTransport } from "@wailsio/runtime";
import {
  enumerateDays,
  enumerateHourStarts,
} from "@t3tools/shared/usageFormat";
import * as nativeApi from "../bindings/yousage/internal/desktop/app";
import type {
  LibrarySnapshot,
  LibraryValue,
} from "../bindings/yousage/internal/theme/models";

if (!import.meta.env.DEV)
  throw new Error("Browser review fixtures are development-only.");

const HOUR_MS = 3_600_000;
const DAY_MS = 24 * HOUR_MS;
const REFERENCE_MS = Date.parse("2026-10-06T12:00:00Z");
const UNTIL_DAY = "2026-10-06";
let display = {
  revision: 0,
  value: {
    zoom: 1,
    shouldRestoreWindow: true,
    shouldCloseToTray: true,
    shouldStartMinimized: false,
    timestampFormat: "locale",
    breakdown: "model",
    metric: "cost",
    windowDays: 30,
    shouldAutoRefresh: false,
    language: "system",
  },
};
let isAutostart = false;
let sourceSettings = {
  revision: 1,
  value: [
    {
      id: "codex-auto",
      provider: "codex",
      root: "",
      isEnabled: true,
      isAutomatic: true,
    },
    {
      id: "claude-auto",
      provider: "claude",
      root: "",
      isEnabled: true,
      isAutomatic: true,
    },
    {
      id: "pi-auto",
      provider: "pi",
      root: "",
      isEnabled: true,
      isAutomatic: true,
    },
    {
      id: "pi-custom-fixture",
      provider: "pi",
      root: "D:/Fixture/pi-sessions",
      isEnabled: true,
      isAutomatic: false,
    },
  ],
};
let configuration = {
  revision: 1,
  value: {
    connections: [],
    providers: [
      {
        id: "codex-default",
        driver: "codex",
        label: "",
        executable: "",
        home: "",
        enabled: true,
      },
      {
        id: "claudeAgent-default",
        driver: "claudeAgent",
        label: "",
        executable: "",
        home: "",
        enabled: false,
      },
    ],
    hubs: [],
    primaryEnvironmentId: "local-fixture",
  },
};
let syncState = {
  isEnabled: true,
  relayUrl: "https://relay.example.invalid",
  hasToken: true,
  pairingUrl: "",
  pairingQrModules: [] as string[],
  status: { lastPushAt: "", lastError: "", isPushing: false },
  shares: [] as Record<string, unknown>[],
};

/** One synthetic history stream: a provider model read from one source folder. */
type FixtureStream = {
  provider: "codex" | "claude" | "pi";
  model: string;
  sourcePath: string;
  costSource: "providerReported" | "modelPriced" | "unpriced";
};

/** Environments in the fixture; the remote one also reports Pi to exercise merging. */
const ENVIRONMENTS: {
  id: string;
  label: string;
  streams: FixtureStream[];
}[] = [
  {
    id: "local-fixture",
    label: "This device",
    streams: [
      {
        provider: "codex",
        model: "fixture-model",
        sourcePath: "/fixture/codex",
        costSource: "modelPriced",
      },
      {
        provider: "claude",
        model: "claude-fixture-model",
        sourcePath: "/fixture/claude",
        costSource: "modelPriced",
      },
      {
        provider: "pi",
        model: "fixture-backend/pi-fixture-model",
        sourcePath: "/fixture/pi/agent/sessions",
        costSource: "providerReported",
      },
      {
        provider: "pi",
        model: "fixture-backend/pi-unpriced-model",
        sourcePath: "D:/Fixture/pi-sessions",
        costSource: "unpriced",
      },
    ],
  },
  {
    id: "remote-fixture",
    label: "Remote fixture",
    streams: [
      {
        provider: "codex",
        model: "fixture-model",
        sourcePath: "/remote/codex",
        costSource: "modelPriced",
      },
      {
        provider: "pi",
        model: "fixture-backend/pi-fixture-model",
        sourcePath: "/remote/pi/agent/sessions",
        costSource: "providerReported",
      },
    ],
  },
];
const SETTINGS = Object.fromEntries(
  ENVIRONMENTS.map(({ id }) => [
    id,
    {
      usagePriceOverrides: {
        "fixture-model": {
          inputCostPerMillionTokens: id === "local-fixture" ? 1 : 3,
          outputCostPerMillionTokens: 2,
        },
      } as Record<string, unknown>,
      usageModelAliases: {} as Record<string, string>,
    },
  ]),
);

/**
 * The synthetic report window for a trailing period.
 *
 * @param days - Period length in days.
 * @param zone - IANA time zone of the request.
 * @returns The window and its bucket labels.
 */
function fixtureWindow(days: number, zone: string) {
  const isHourly = days === 1;
  const sinceDay = new Date(
    Date.parse(`${UNTIL_DAY}T00:00:00Z`) - (days - 1) * DAY_MS,
  )
    .toISOString()
    .slice(0, 10);
  const window = {
    timeZone: zone,
    sinceDay: isHourly ? "2026-10-05" : sinceDay,
    untilDay: UNTIL_DAY,
    resolution: isHourly ? "hour" : "day",
    ...(isHourly
      ? { sinceTimeMs: REFERENCE_MS - DAY_MS, untilTimeMs: REFERENCE_MS }
      : {}),
  };
  const labels = isHourly
    ? enumerateHourStarts(
        new Date(REFERENCE_MS - DAY_MS).toISOString(),
        new Date(REFERENCE_MS).toISOString(),
      )
    : enumerateDays(window.sinceDay, window.untilDay);
  return { window, labels: [...labels], isHourly };
}

/**
 * Deterministic usage buckets of one environment across the window.
 *
 * @param streams - The environment's history streams.
 * @param labels - Bucket labels of the window.
 * @param isHourly - Whether labels are hour starts.
 * @returns Plain bucket objects.
 */
function fixtureBuckets(
  streams: readonly FixtureStream[],
  labels: readonly string[],
  isHourly: boolean,
) {
  return labels.flatMap((label, labelIndex) =>
    streams.flatMap((stream, streamIndex) => {
      if ((labelIndex + streamIndex) % 3 === 1) return [];
      const factor = 1 + ((labelIndex * 7 + streamIndex * 3) % 5);
      const isUnpriced = stream.costSource === "unpriced";
      const costUsd = isUnpriced ? 0 : 0.04 * factor * (streamIndex + 1);
      return [
        {
          day: label.slice(0, 10),
          ...(isHourly ? { hourStart: label } : {}),
          provider: stream.provider,
          model: stream.model,
          sourcePath: stream.sourcePath,
          totals: {
            uncachedInputTokens: 4000 * factor,
            cachedInputTokens: 9000 * factor,
            cacheCreationTokens: stream.provider === "codex" ? 0 : 600 * factor,
            outputTokens: 2500 * factor,
            reasoningTokens: 500 * factor,
          },
          costUsd,
          cacheSavingsUsd: isUnpriced ? 0 : 0.01 * factor,
          categoryCostUsd: {
            input: costUsd * 0.3,
            cacheRead: costUsd * 0.1,
            cacheWrite: costUsd * 0.05,
            output: costUsd * 0.55,
          },
          fastCostUsd: 0,
          ultrafastCostUsd: 0,
          speedPremiumUsd: 0,
          costSource: stream.costSource,
          records: factor,
          unpricedRecords: isUnpriced ? factor : 0,
          sessions: 1,
        },
      ];
    }),
  );
}

/** Return plain JSON so generated constructors cannot mutate shared fixture state. */
function usageFixture(days: number, zone: string) {
  const { window, labels, isHourly } = fixtureWindow(days, zone);
  return {
    window,
    environments: ENVIRONMENTS.map((environment, environmentIndex) => ({
      environmentId: environment.id,
      label: environment.label,
      local: environmentIndex === 0,
      connected: true,
      canOperate: true,
      refreshing: false,
      serverConfig: {
        environment: {
          environmentId: environment.id,
          orchestrationProtocolVersion: 2,
          capabilities: { usagePriceOverrides: true, usageModelAliases: true },
        },
        settings: SETTINGS[environment.id],
        providers: [
          {
            instanceId: "codex-account",
            driver: "codex",
            displayName: "Codex",
            enabled: true,
            installed: true,
            status: "ready",
            auth: {
              status: "authenticated",
              email: "fixture@example.invalid",
              subscriptionSharing: true,
            },
            slashCommands: [],
            usageLimits: {
              checkedAt: "2026-10-06T12:00:00Z",
              windows: [
                {
                  id: "session",
                  kind: "session",
                  label: "Session",
                  usedPercent: 37,
                  resetsAt: "2026-10-06T15:00:00Z",
                  windowDurationMins: 300,
                },
                {
                  id: "weekly",
                  kind: "weekly",
                  label: "Weekly",
                  usedPercent: 23,
                  resetsAt: "2026-10-10T00:00:00Z",
                  windowDurationMins: 10080,
                },
              ],
            },
          },
        ],
        usageLimitSources: [],
      },
      summary: {
        contractVersion: 6,
        readAt: "2026-10-06T12:00:00Z",
        timeZone: zone,
        sinceDay: window.sinceDay,
        untilDay: window.untilDay,
        scanDurationMs: 10,
        pricing: {
          status: "cached",
          source: "Fixture",
          fetchedAt: "2026-10-05T12:00:00Z",
          knownModels: 5,
        },
        sources: environment.streams.map((stream) => ({
          fingerprint: {
            hostId: environment.id,
            provider: stream.provider,
            resolvedHomePath: stream.sourcePath,
            volumeId: "fixture",
          },
          status: "ok",
          scannedFiles: 2,
          skippedFiles: 0,
          malformedRecords: 0,
          distinctSessions: 2,
          message: null,
        })),
        buckets: fixtureBuckets(environment.streams, labels, isHourly),
      },
    })),
  };
}

/**
 * Speed statistics with every unmeasured field at zero.
 *
 * @param values - Measured fields to set.
 * @returns A complete statistics object.
 */
function speedStats(values: Record<string, number>) {
  return {
    requests: 0,
    unmeasuredRequests: 0,
    totalTokens: 0,
    outputTokens: 0,
    generationMs: 0,
    tokensPerSecond: 0,
    medianTokensPerSecond: 0,
    p10TokensPerSecond: 0,
    p90TokensPerSecond: 0,
    ttftRequests: 0,
    medianTtftMs: 0,
    p90TtftMs: 0,
    decodeTokensPerSecond: 0,
    medianLatencyMs: 0,
    p90LatencyMs: 0,
    inputTokens: 0,
    cachedInputTokens: 0,
    cacheHitRate: 0,
    contextTokensPerRequest: 0,
    outputTokensPerRequest: 0,
    ...values,
  };
}

/** Measured Codex and Claude timings; Pi history records no request timing. */
const MEASURED = {
  codex: {
    requests: 40,
    unmeasuredRequests: 2,
    totalTokens: 640000,
    outputTokens: 100000,
    generationMs: 1600000,
    tokensPerSecond: 62.5,
    medianTokensPerSecond: 60.1,
    p10TokensPerSecond: 41.2,
    p90TokensPerSecond: 80.4,
    ttftRequests: 38,
    medianTtftMs: 850,
    p90TtftMs: 2100,
    decodeTokensPerSecond: 71.3,
    medianLatencyMs: 14000,
    p90LatencyMs: 32000,
    inputTokens: 540000,
    cachedInputTokens: 380000,
    cacheHitRate: 0.7,
    contextTokensPerRequest: 13500,
    outputTokensPerRequest: 2500,
  },
  claude: {
    requests: 25,
    totalTokens: 300000,
    outputTokens: 50000,
    generationMs: 1040000,
    tokensPerSecond: 48.1,
    medianTokensPerSecond: 47,
    p10TokensPerSecond: 30.5,
    p90TokensPerSecond: 66,
    ttftRequests: 25,
    medianTtftMs: 1200,
    p90TtftMs: 3100,
    decodeTokensPerSecond: 55.8,
    medianLatencyMs: 21000,
    p90LatencyMs: 45000,
    inputTokens: 250000,
    cachedInputTokens: 150000,
    cacheHitRate: 0.6,
    contextTokensPerRequest: 10000,
    outputTokensPerRequest: 2000,
  },
  pi: {
    unmeasuredRequests: 18,
    totalTokens: 270000,
    outputTokens: 45000,
    inputTokens: 225000,
    cachedInputTokens: 162000,
    cacheHitRate: 0.72,
    contextTokensPerRequest: 12500,
    outputTokensPerRequest: 2500,
  },
};

/** Speed report: measured Codex/Claude rates beside Pi requests without timing. */
function speedFixture(days: number, zone: string) {
  const { window, labels } = fixtureWindow(days, zone);
  return {
    window,
    overall: speedStats({
      requests: 65,
      unmeasuredRequests: 20,
      totalTokens: 1210000,
      outputTokens: 195000,
      generationMs: 2640000,
      tokensPerSecond: 56.8,
      medianTokensPerSecond: 55.2,
      p10TokensPerSecond: 33.4,
      p90TokensPerSecond: 77.9,
      ttftRequests: 63,
      medianTtftMs: 960,
      p90TtftMs: 2600,
      decodeTokensPerSecond: 64.1,
      medianLatencyMs: 16500,
      p90LatencyMs: 39000,
      inputTokens: 1015000,
      cachedInputTokens: 692000,
      cacheHitRate: 0.68,
      contextTokensPerRequest: 11900,
      outputTokensPerRequest: 2290,
    }),
    providers: (["codex", "claude", "pi"] as const).map((provider) => ({
      provider,
      ...speedStats(MEASURED[provider]),
    })),
    models: [
      {
        provider: "codex",
        model: "fixture-model",
        speed: "standard",
        ...speedStats(MEASURED.codex),
      },
      {
        provider: "claude",
        model: "claude-fixture-model",
        speed: "standard",
        ...speedStats(MEASURED.claude),
      },
      {
        provider: "pi",
        model: "fixture-backend/pi-fixture-model",
        speed: "standard",
        ...speedStats({ ...MEASURED.pi, unmeasuredRequests: 15 }),
      },
      {
        provider: "pi",
        model: "fixture-backend/pi-unpriced-model",
        speed: "standard",
        ...speedStats({
          unmeasuredRequests: 3,
          totalTokens: 40000,
          outputTokens: 7000,
          inputTokens: 33000,
        }),
      },
    ],
    buckets: labels.map((label, index) => {
      const measured = (["codex", "claude"] as const).flatMap(
        (provider, position) =>
          (index + position) % 3 === 1
            ? []
            : [
                {
                  provider,
                  model:
                    provider === "codex"
                      ? "fixture-model"
                      : "claude-fixture-model",
                  speed: "standard",
                  requests: 2,
                  outputTokens: 5000,
                  generationMs: 90000,
                  tokensPerSecond:
                    MEASURED[provider].tokensPerSecond + ((index * 5) % 11) - 5,
                },
              ],
      );
      return {
        label,
        models: measured,
        providers: measured.map((entry) => ({
          provider: entry.provider,
          requests: entry.requests,
          outputTokens: entry.outputTokens,
          generationMs: entry.generationMs,
          tokensPerSecond: entry.tokensPerSecond,
        })),
      };
    }),
    measuredRequests: 65,
    unmeasuredRequests: 20,
  };
}

/** Loss report with cache-expiry rewrites for every provider, including an estimated Pi rewrite. */
function lossFixture(days: number, zone: string) {
  const { window, labels } = fixtureWindow(days, zone);
  const events = [
    {
      kind: "cacheExpiry",
      provider: "codex",
      model: "fixture-model",
      sessionId: "fixture-codex-session",
      atMs: REFERENCE_MS - 5 * HOUR_MS,
      idleMs: 40 * 60_000,
      tokens: 42000,
      costUsd: 0.21,
      precision: "exact",
    },
    {
      kind: "cacheExpiry",
      provider: "claude",
      model: "claude-fixture-model",
      sessionId: "fixture-claude-session",
      atMs: REFERENCE_MS - 9 * HOUR_MS,
      idleMs: 12 * 60_000,
      tokens: 30000,
      costUsd: 0.11,
      precision: "exact",
    },
    {
      kind: "cacheExpiry",
      provider: "pi",
      model: "fixture-backend/pi-fixture-model",
      sessionId: "fixture-pi-session",
      atMs: REFERENCE_MS - 3 * HOUR_MS,
      idleMs: 25 * 60_000,
      tokens: 26000,
      costUsd: 0.08,
      precision: "estimated",
    },
  ];
  const providers = ["codex", "claude", "pi"].map((provider) => {
    const owned = events.filter((event) => event.provider === provider);
    return {
      provider,
      events: owned.length,
      tokens: owned.reduce((sum, event) => sum + event.tokens, 0),
      costUsd: owned.reduce((sum, event) => sum + event.costUsd, 0),
    };
  });
  const last = labels.length - 1;
  return {
    window,
    cacheExpiry: {
      events: events.length,
      tokens: events.reduce((sum, event) => sum + event.tokens, 0),
      costUsd: events.reduce((sum, event) => sum + event.costUsd, 0),
    },
    providers,
    days: labels.map((label, index) => {
      const owned = index === last ? providers : [];
      return {
        day: label,
        cacheExpiryUsd: owned.reduce((sum, entry) => sum + entry.costUsd, 0),
        cacheExpiryCount: owned.reduce((sum, entry) => sum + entry.events, 0),
        providers: owned.map((entry) => ({
          provider: entry.provider,
          costUsd: entry.costUsd,
          count: entry.events,
        })),
      };
    }),
    events,
    cacheWriteTokens: 120000,
  };
}

/** Local receipt: Codex by subscription, Claude paid, Pi with an unknown payment method. */
function receiptFixture(days: number, zone: string) {
  const { window, labels, isHourly } = fixtureWindow(days, zone);
  const buckets = fixtureBuckets(ENVIRONMENTS[0].streams, labels, isHourly);
  const items = new Map<
    string,
    {
      provider: string;
      model: string;
      tokens: number;
      costUsd: number;
      unpricedRecords: number;
      isSubscription: boolean;
      isPaymentUnknown: boolean;
    }
  >();
  let subscriptionUsd = 0;
  let unknownPaymentUsd = 0;
  for (const bucket of buckets) {
    const key = `${bucket.provider}\u0000${bucket.model}`;
    const item = items.get(key) ?? {
      provider: bucket.provider,
      model: bucket.model,
      tokens: 0,
      costUsd: 0,
      unpricedRecords: 0,
      isSubscription: bucket.provider === "codex",
      isPaymentUnknown: bucket.provider === "pi",
    };
    const totals = bucket.totals;
    item.tokens +=
      totals.uncachedInputTokens +
      totals.cachedInputTokens +
      totals.cacheCreationTokens +
      totals.outputTokens;
    item.costUsd += bucket.costUsd;
    item.unpricedRecords += bucket.unpricedRecords;
    items.set(key, item);
    if (item.isPaymentUnknown) unknownPaymentUsd += bucket.costUsd;
    else if (item.isSubscription) subscriptionUsd += bucket.costUsd;
  }
  const list = [...items.values()].sort(
    (first, second) => second.costUsd - first.costUsd,
  );
  const totalUsd = list.reduce((sum, item) => sum + item.costUsd, 0);
  return {
    days,
    window,
    startsAtMs: isHourly
      ? REFERENCE_MS - DAY_MS
      : Date.parse(`${window.sinceDay}T00:00:00Z`),
    endsAtMs: REFERENCE_MS,
    number: `FX-${days}`,
    timeZone: zone,
    sinceDay: window.sinceDay,
    untilDay: window.untilDay,
    items: list,
    totalUsd,
    subscriptionUsd,
    paidUsd: Math.max(0, totalUsd - subscriptionUsd - unknownPaymentUsd),
    unknownPaymentUsd,
    tokens: list.reduce((sum, item) => sum + item.tokens, 0),
    sessions: buckets.length,
    cacheSavingsUsd: buckets.reduce(
      (sum, bucket) => sum + bucket.cacheSavingsUsd,
      0,
    ),
    unpricedRecords: list.reduce((sum, item) => sum + item.unpricedRecords, 0),
    activeDays: new Set(buckets.map((bucket) => bucket.day)).size,
    firstAtMs: null,
    lastAtMs: null,
    qrText: "",
    qrModules: [],
  };
}

/** Public share preview of the local receipt, without amounts when they are hidden. */
function sharePreview(request: {
  days: number;
  ttlDays: number;
  hideAmounts: boolean;
  timeZone: string;
  palette: unknown;
}) {
  const receipt = receiptFixture(request.days, request.timeZone);
  const { labels, isHourly } = fixtureWindow(request.days, request.timeZone);
  const buckets = fixtureBuckets(ENVIRONMENTS[0].streams, labels, isHourly);
  const amount = (value: number) => (request.hideAmounts ? undefined : value);
  const measure = (item: { tokens: number; costUsd: number }) =>
    request.hideAmounts ? item.tokens : item.costUsd;
  const total = receipt.items.reduce((sum, item) => sum + measure(item), 0);
  return {
    formatVersion: 1,
    createdAt: new Date(REFERENCE_MS).toISOString(),
    expiresAt: new Date(REFERENCE_MS + request.ttlDays * DAY_MS).toISOString(),
    days: request.days,
    timeZone: request.timeZone,
    window: {
      sinceDay: receipt.sinceDay,
      untilDay: receipt.untilDay,
      resolution: isHourly ? "hour" : "day",
      startsAtMs: receipt.startsAtMs,
      endsAtMs: receipt.endsAtMs,
    },
    hideAmounts: request.hideAmounts,
    receipt: {
      number: receipt.number,
      totalUsd: amount(receipt.totalUsd),
      subscriptionUsd: amount(receipt.subscriptionUsd),
      paidUsd: amount(receipt.paidUsd),
      unknownPaymentUsd:
        receipt.unknownPaymentUsd > 0
          ? amount(receipt.unknownPaymentUsd)
          : undefined,
      cacheSavingsUsd: amount(receipt.cacheSavingsUsd),
      tokens: receipt.tokens,
      sessions: receipt.sessions,
      activeDays: receipt.activeDays,
      unpricedRecords: receipt.unpricedRecords,
      items: receipt.items.map((item) => ({
        provider: item.provider,
        model: item.model,
        tokens: item.tokens,
        costUsd: amount(item.costUsd),
        unpricedRecords: item.unpricedRecords || undefined,
        isSubscription: item.isSubscription,
        isPaymentUnknown: item.isPaymentUnknown || undefined,
      })),
    },
    series: labels.map((label) => ({
      startMs: isHourly ? Date.parse(label) : Date.parse(`${label}T12:00:00Z`),
      values: ["codex", "claude", "pi"].flatMap((provider) => {
        const owned = buckets.filter(
          (bucket) =>
            bucket.provider === provider && bucket.day === label.slice(0, 10),
        );
        if (owned.length === 0) return [];
        const tokens = owned.reduce(
          (sum, bucket) =>
            sum +
            bucket.totals.uncachedInputTokens +
            bucket.totals.outputTokens,
          0,
        );
        return [
          {
            provider,
            tokens,
            costUsd: amount(
              owned.reduce((sum, bucket) => sum + bucket.costUsd, 0),
            ),
          },
        ];
      }),
    })),
    models: receipt.items.map((item) => ({
      provider: item.provider,
      model: item.model,
      tokens: item.tokens,
      costUsd: amount(item.costUsd),
      unpricedRecords: item.unpricedRecords || undefined,
      share: total > 0 ? measure(item) / total : 0,
    })),
    palette: request.palette,
  };
}

let shouldRejectAppearance = false;
let isFixtureSystemDark = false;
let snapshot: LibrarySnapshot = {
  revision: 0,
  value: {
    preferences: {
      mode: "system",
      baseThemeId: "system",
      lightOwner: "",
      darkOwner: "",
      typography: {
        sans: "",
        code: "",
        interfaceSize: 16,
        codeSize: 14,
        smoothing: false,
      },
      contrast: 100,
      glassOpacity: 80,
      motionMs: 0,
      appliedGenerations: {},
    },
    customThemes: [],
    attributions: [],
  },
  published: { themes: [] },
};

/** Fixture answers keyed by binding name; arguments arrive exactly as the bindings send them. */
const HANDLERS: Record<string, (...args: never[]) => unknown> = {
  DetectProviders: () =>
    [
      ["codex", "codex"],
      ["claude", "claudeAgent"],
      ["pi", "pi"],
    ].map(([provider, driver]) => ({
      provider,
      driver,
      isInstalled: true,
      hasHistory: true,
      supportsHistory: true,
      supportsQuota: provider !== "pi",
      message: "Detected automatically.",
    })),
  GetLocalEnvironmentId: () => "local-fixture",
  GetSources: () => structuredClone(sourceSettings),
  SaveSources: (saved: typeof sourceSettings) => {
    sourceSettings = JSON.parse(JSON.stringify(saved));
    sourceSettings.revision++;
    return structuredClone(sourceSettings);
  },
  GetConfiguration: () => structuredClone(configuration),
  SaveConfiguration: (saved: typeof configuration) => {
    configuration = JSON.parse(JSON.stringify(saved));
    configuration.revision++;
    return structuredClone(configuration);
  },
  GetDiagnostics: () =>
    JSON.stringify(
      {
        application: "Yousage",
        wails: "3.0.0-beta.30",
        platform: "browser-fixture",
        historySources: sourceSettings.value.length,
      },
      null,
      2,
    ),
  ExportDiagnostics: () => false,
  GetAutostart: () => isAutostart,
  SetAutostart: (isEnabled: boolean) => {
    isAutostart = isEnabled;
    return isAutostart;
  },
  GetDisplay: () => JSON.parse(JSON.stringify(display)),
  SaveDisplay: (saved: typeof display) => {
    display = JSON.parse(JSON.stringify(saved)) as typeof display;
    display.revision++;
    return JSON.parse(JSON.stringify(display));
  },
  GetUsage: (days: number, zone: string) => usageFixture(days, zone),
  GetSpeedReport: (days: number, zone: string) => speedFixture(days, zone),
  GetLossReport: (days: number, zone: string) => lossFixture(days, zone),
  GetReceipt: (days: number, zone: string) => receiptFixture(days, zone),
  RefreshUsage: async () => {
    await new Promise((resolve) => setTimeout(resolve, 100));
    return null;
  },
  RefreshLimits: async () => {
    await new Promise((resolve) => setTimeout(resolve, 100));
    return null;
  },
  ApplyPricing: (
    ids: string[],
    patch: {
      overrides: Record<string, unknown>;
      aliases: Record<string, string>;
    },
  ) => {
    for (const identity of ids) {
      Object.assign(SETTINGS[identity]!.usagePriceOverrides, patch.overrides);
      Object.assign(SETTINGS[identity]!.usageModelAliases, patch.aliases);
    }
    return ids.map((id) => ({ id, saved: true, outcomeUnknown: false }));
  },
  GetSync: () => structuredClone(syncState),
  SaveSync: (input: {
    isEnabled: boolean;
    relayUrl: string;
    token: string;
  }) => {
    syncState = {
      ...syncState,
      isEnabled: input.isEnabled,
      relayUrl: input.relayUrl,
      hasToken: syncState.hasToken || input.token !== "",
    };
    return structuredClone(syncState);
  },
  SyncNow: () => structuredClone(syncState),
  RotatePairing: () => structuredClone(syncState),
  TestRelay: () => null,
  PreviewShare: sharePreview,
  CreateShare: (request: Parameters<typeof sharePreview>[0]) => {
    const preview = sharePreview(request);
    const share = {
      id: `share-fixture-${crypto.randomUUID()}`,
      url: "https://relay.example.invalid/s/fixture#synthetic",
      days: request.days,
      ttlDays: request.ttlDays,
      hideAmounts: request.hideAmounts,
      createdAt: preview.createdAt,
      expiresAt: preview.expiresAt,
      qrModules: [],
    };
    syncState = { ...syncState, shares: [...syncState.shares, share] };
    return structuredClone(share);
  },
  RevokeShare: (id: string) => {
    syncState = {
      ...syncState,
      shares: syncState.shares.filter((share) => share.id !== id),
    };
    return structuredClone(syncState);
  },
  GetBootstrap: () => ({ isSystemDark: false, systemLocale: "en-US" }),
  GetAppearance: () => JSON.parse(JSON.stringify(snapshot)),
  SaveAppearance: async (revision: number, value: LibraryValue) => {
    if (shouldRejectAppearance) {
      shouldRejectAppearance = false;
      throw new Error("Controlled appearance save failure");
    }
    if (revision !== snapshot.revision)
      throw new Error("Appearance changed. Reload before saving this draft.");
    await new Promise((resolve) => setTimeout(resolve, 100));
    snapshot = {
      ...snapshot,
      revision: snapshot.revision + 1,
      value: JSON.parse(JSON.stringify(value)) as LibraryValue,
    };
    return JSON.parse(JSON.stringify(snapshot));
  },
  PickThemeFiles: () => [
    {
      name: "Fixture.json",
      text: '{"version":1,"id":"browser-fixture","name":"Browser fixture","appearance":"dark","colors":{"canvas":"rebeccapurple","accent":"gold"}}',
      size: 160,
      error: "",
    },
  ],
  ExportTheme: () => false,
};
/** Native window and shell actions that the browser fixture accepts without effect. */
const NO_EFFECT = new Set([
  "ConfirmClose",
  "CopyText",
  "DiscardDraftCredentials",
  "FrontendReady",
  "OpenDocumentation",
  "OpenExternal",
  "RecenterWindow",
  "SetCloseBridge",
  "SetTrayLabels",
  "ShowWindow",
  "SyncAppearance",
]);

/** Thrown by the capturing transport so probes never reach a backend. */
class ProbeStop extends Error {}

/**
 * Learn each generated binding's method ID by calling it once through a capturing transport.
 *
 * @returns Binding names keyed by method ID.
 */
async function captureBindingNames(): Promise<Map<number, string>> {
  const names = new Map<number, string>();
  let current = "";
  setTransport({
    call(...parameters) {
      const [objectId, , , args] = parameters;
      if (objectId === objectNames.Call)
        names.set((args as { methodID: number }).methodID, current);
      return Promise.reject(new ProbeStop());
    },
  });
  for (const [name, binding] of Object.entries(nativeApi)) {
    if (typeof binding !== "function") continue;
    current = name;
    await (binding as () => Promise<unknown>)().catch(() => {});
  }
  return names;
}

const BINDING_NAMES = await captureBindingNames();
setTransport({
  /** Keep fixture mutations in memory while exercising the actual generated call/model conversions. */
  async call(...parameters) {
    const [objectId, , , args] = parameters;
    if (objectId !== objectNames.Call) return null;
    const request = args as { methodID: number; args: unknown[] };
    const name = BINDING_NAMES.get(request.methodID) ?? "";
    const handler = HANDLERS[name] as
      | ((...values: unknown[]) => unknown)
      | undefined;
    if (handler) return handler(...request.args);
    if (NO_EFFECT.has(name)) return null;
    throw new Error(
      `Unsupported browser fixture method ${name || request.methodID}`,
    );
  },
});
await import("./main");

/** Simulate native close requests only in the development review harness. */
window.addEventListener("keydown", (event) => {
  if (event.key === "F9") {
    event.preventDefault();
    shouldRejectAppearance = true;
  }
  if (event.key === "F7") {
    event.preventDefault();
    isFixtureSystemDark = !isFixtureSystemDark;
    (
      window as unknown as {
        _wails: {
          dispatchWailsEvent: (event: { name: string; data: boolean }) => void;
        };
      }
    )._wails.dispatchWailsEvent({
      name: "system-appearance",
      data: isFixtureSystemDark,
    });
  }
  if (event.key === "F8") {
    event.preventDefault();
    (
      window as unknown as {
        _wails: {
          dispatchWailsEvent: (event: { name: string; data: null }) => void;
        };
      }
    )._wails.dispatchWailsEvent({ name: "close-requested", data: null });
  }
});
