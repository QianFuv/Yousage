/** Development-only browser transport fixture; production index never imports this module. */
import { setTransport } from "@wailsio/runtime";
import type {
  LibrarySnapshot,
  LibraryValue,
} from "../bindings/yousage/internal/theme/models";

if (!import.meta.env.DEV)
  throw new Error("Browser review fixtures are development-only.");
let display = {
  revision: 0,
  value: {
    density: "comfortable",
    zoom: 1,
    shouldRestoreWindow: true,
    shouldCloseToTray: true,
    timestampFormat: "locale",
    breakdown: "model",
    metric: "limits",
    windowDays: 30,
    shouldAutoRefresh: false,
  },
};
let isAutostart = false;
let sourceSettings = {
  revision: 1,
  value: [
    {
      id: "fixture-history",
      provider: "codex",
      root: "C:/Fixture/history",
      isEnabled: true,
      isAutomatic: false,
    },
  ],
};
let configuration = {
  revision: 1,
  value: {
    connections: [],
    providers: [],
    hubs: [],
    primaryEnvironmentId: "local-fixture",
  },
};
const ENVIRONMENTS = ["local-fixture", "remote-fixture"];
const PROVIDERS = ["codex", "claude"];
const SETTINGS = Object.fromEntries(
  ENVIRONMENTS.map((identity) => [
    identity,
    {
      usagePriceOverrides: {
        "fixture-model": {
          inputCostPerMillionTokens: identity === "local-fixture" ? 1 : 3,
          outputCostPerMillionTokens: 2,
        },
      },
      usageModelAliases: {},
    },
  ]),
);
/** Return plain JSON so generated constructors cannot mutate shared fixture state. */
function usageFixture(days: number, zone: string) {
  const reference = Date.parse("2026-10-06T12:00:00Z");
  return {
    window: {
      timeZone: zone,
      sinceDay:
        days === 1 ? "2026-10-05" : days === 7 ? "2026-09-30" : "2026-09-07",
      untilDay: "2026-10-06",
      resolution: days === 1 ? "hour" : "day",
      ...(days === 1
        ? { sinceTimeMs: reference - 86400000, untilTimeMs: reference }
        : {}),
    },
    environments: ENVIRONMENTS.map((identity, environmentIndex) => ({
      environmentId: identity,
      label: environmentIndex === 0 ? "This device" : "Remote fixture",
      local: environmentIndex === 0,
      connected: true,
      canOperate: true,
      refreshing: false,
      serverConfig: {
        environment: {
          environmentId: identity,
          orchestrationProtocolVersion: 2,
          capabilities: { usagePriceOverrides: true, usageModelAliases: true },
        },
        settings: SETTINGS[identity],
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
        sinceDay: "2026-09-07",
        untilDay: "2026-10-06",
        scanDurationMs: 10,
        pricing: {
          status: "cached",
          source: "Fixture",
          fetchedAt: "2026-10-05T12:00:00Z",
          knownModels: 5,
        },
        sources: PROVIDERS.map((provider) => ({
          fingerprint: {
            hostId: identity,
            provider,
            resolvedHomePath: `/fixture/${provider}`,
            volumeId: "fixture",
          },
          status: "ok",
          scannedFiles: 2,
          skippedFiles: 0,
          malformedRecords: 0,
          distinctSessions: 2,
          message: null,
        })),
        buckets: PROVIDERS.map((provider, index) => ({
          day: "2026-10-05",
          ...(days === 1 ? { hourStart: "2026-10-05T18:00:00.000Z" } : {}),
          provider,
          model: index === 0 ? "fixture-model" : `${provider}-model`,
          sourcePath: `/fixture/${provider}`,
          totals: {
            uncachedInputTokens: 10000 * (index + 1),
            cachedInputTokens: 15000,
            cacheCreationTokens: 1000,
            outputTokens: 5000,
            reasoningTokens: 1000,
          },
          costUsd: index === 5 ? 0 : (index + 1) * 0.3,
          cacheSavingsUsd: 0.05,
          categoryCostUsd: {
            input: 0.1,
            cacheRead: 0.03,
            cacheWrite: 0.01,
            output: 0.16,
          },
          fastCostUsd: 0.1,
          ultrafastCostUsd: 0,
          speedPremiumUsd: 0.03,
          costSource: index === 5 ? "unpriced" : "modelPriced",
          records: 5,
          unpricedRecords: index === 5 ? 5 : 0,
          sessions: 2,
        })),
      },
    })),
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
setTransport({
  /** Keep fixture mutations in memory while exercising the actual generated call/model conversions. */
  async call(object, ...parameters) {
    const args = parameters[2];
    if (object !== 0) return null;
    const request = args as { methodID: number; args: unknown[] };
    switch (request.methodID) {
      case 2564077708:
        return PROVIDERS.map((provider) => ({
          provider,
          driver: provider === "claude" ? "claudeAgent" : provider,
          isInstalled: true,
          hasHistory: true,
          message: "Detected automatically.",
        }));
      case 2299238358:
        return "local-fixture";
      case 3791071355:
        return structuredClone(sourceSettings);
      case 23962130:
        sourceSettings = JSON.parse(JSON.stringify(request.args[0]));
        sourceSettings.revision++;
        return structuredClone(sourceSettings);
      case 1089004527:
        return structuredClone(configuration);
      case 2212158658:
        configuration = JSON.parse(JSON.stringify(request.args[0]));
        configuration.revision++;
        return structuredClone(configuration);
      case 2811458213:
        return JSON.stringify(
          {
            application: "Yousage",
            wails: "3.0.0-beta.30",
            platform: "browser-fixture",
            historySources: sourceSettings.value.length,
          },
          null,
          2,
        );
      case 4266831633:
        return false;
      case 584700173:
        return "hub-fixture-" + crypto.randomUUID();
      case 1066918238:
        return {
          connection: {
            url: request.args[0],
            environmentId: "remote-fixture",
            credentialId: "environment-fixture-" + crypto.randomUUID(),
          },
          label: request.args[1] || "Remote fixture",
          enabled: true,
        };
      case 909584628:
      case 4181451325:
      case 2798394763:
      case 3259341121:
      case 3076200806:
      case 2665324948:
      case 31117496:
        return null;

      case 3280491753:
        return isAutostart;
      case 97457885:
        isAutostart = request.args[0] as boolean;
        return isAutostart;
      case 2885542999:
        return JSON.parse(JSON.stringify(display));
      case 920552350:
        display = JSON.parse(JSON.stringify(request.args[0])) as typeof display;
        display.revision++;
        return JSON.parse(JSON.stringify(display));
      case 2234764926:
        return JSON.parse(
          JSON.stringify(
            usageFixture(request.args[0] as number, request.args[1] as string),
          ),
        );
      case 59860548:
      case 2450955827:
        await new Promise((resolve) => setTimeout(resolve, 100));
        return null;
      case 3520645103: {
        const [ids, patch] = request.args as [
          string[],
          {
            overrides: Record<string, unknown>;
            aliases: Record<string, string>;
          },
        ];
        for (const identity of ids) {
          Object.assign(
            SETTINGS[identity]!.usagePriceOverrides,
            patch.overrides,
          );
          Object.assign(SETTINGS[identity]!.usageModelAliases, patch.aliases);
        }
        return ids.map((id) => ({ id, saved: true, outcomeUnknown: false }));
      }

      case 2483192963:
        return {
          isSystemDark: false,
          systemLocale: "en-US",
        };
      case 3469612837:
        return JSON.parse(JSON.stringify(snapshot));
      case 2592400194: {
        if (shouldRejectAppearance) {
          shouldRejectAppearance = false;
          throw new Error("Controlled appearance save failure");
        }
        const [revision, value] = request.args;
        if (revision !== snapshot.revision)
          throw new Error(
            "Appearance changed. Reload before saving this draft.",
          );
        await new Promise((resolve) => setTimeout(resolve, 100));
        snapshot = {
          ...snapshot,
          revision: snapshot.revision + 1,
          value: JSON.parse(JSON.stringify(value)) as LibraryValue,
        };
        return JSON.parse(JSON.stringify(snapshot));
      }
      case 1388571440:
        return [
          {
            name: "Fixture.json",
            text: '{"version":1,"id":"browser-fixture","name":"Browser fixture","appearance":"dark","colors":{"canvas":"rebeccapurple","accent":"gold"}}',
            size: 160,
            error: "",
          },
        ];
      case 3294236452:
        return false;
      case 645226692:
      case 757831160:
      case 3754150517:
        return null;
      default:
        throw new Error(
          `Unsupported browser fixture method ${request.methodID}`,
        );
    }
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
