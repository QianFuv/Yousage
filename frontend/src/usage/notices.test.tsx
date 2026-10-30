/** Composed quota failure notices read in the active language while labels and unknown text stay exact. */
import { afterEach, describe, expect, it, vi } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import type { LimitPresentations } from "@t3tools/shared/usageLimits";
import { setLanguage, t } from "../i18n";
import { UsageLimitsPooled } from "../../vendor/UsageLimitsPooled";
import type { ServerProvider } from "./contracts";
import type { EnvironmentUsageStatus } from "./data";
import {
  limitNoticeLabels,
  localizeEnvironmentError,
  localizeQuotaNotice,
} from "./notices";
import { UsagePage } from "./UsagePage";

const page = vi.hoisted(() => ({ environments: [] as unknown[] }));

vi.mock("./controller", () => ({
  useData: () => ({
    snapshot: {
      environments: page.environments,
      window: undefined,
    },
    selected: null,
    error: "",
    isBusy: false,
    display: {
      value: {
        metric: "cost",
        windowDays: 7,
        shouldAutoRefresh: false,
        timestampFormat: "",
      },
    },
    refreshState: { isRefreshing: false, lastCompletedAt: "", lastError: "" },
    refresh: async () => {},
    patchDisplay: async () => {},
  }),
}));
vi.mock("../../bindings/yousage/internal/desktop/app", () => ({
  OpenExternal: vi.fn(),
}));

/** Every fixed failure sentence the backend reports for a quota read. */
const FAILURES = [
  "Claude Code stored credentials are malformed.",
  "Claude Code stored access token has expired; usage limits could not be read.",
  "Claude usage API rejected the stored credential (HTTP 401).",
  "Claude usage API denied access to usage limits (HTTP 403).",
  "Claude usage API rate limit reached (HTTP 429). Try again later.",
  "Claude usage API is temporarily unavailable (HTTP 5xx). Try again later.",
  "Claude usage API returned an unexpected HTTP status.",
  "Claude usage API request timed out.",
  "Claude usage API request was cancelled.",
  "Claude usage API returned invalid JSON.",
  "Claude usage API could not be reached or read because of a network error.",
  "Claude Code rejected authentication for the usage probe.",
  "Claude Code usage probe timed out.",
  "Claude Code usage probe was cancelled.",
  "Claude Code returned an invalid usage probe response.",
  "Claude Code usage probe could not communicate with the CLI.",
  "Claude Code login could not be read.",
  "Claude returned invalid usage limits.",
  "Claude Code login has expired. Open Claude Code to renew it.",
  "Claude could not read usage limits.",
  "Claude could not initialize its usage probe.",
  "Codex could not be started to read usage.",
  "Codex returned invalid usage limits.",
];
const RETAINED = "Last successful limits are retained.";
const NO_PREVIOUS = "No previous successful limits are available.";
const HISTORY_NOTE =
  "Local history could not be refreshed. Cached history is retained.";

/** Match a Han character, which every Chinese translation contains. */
const HAN = /[一-鿿]/;

/**
 * Build a provider that reports a failed quota read and no earlier limits.
 *
 * @param displayName - The provider label.
 * @param message - The failure message.
 * @returns The provider.
 */
function failedProvider(displayName: string, message: string): ServerProvider {
  return {
    instanceId: displayName,
    driver: "claudeAgent",
    displayName,
    enabled: true,
    installed: true,
    status: "ready",
    auth: { status: "unknown" },
    usageLimits: {
      checkedAt: "2026-10-20T08:00:00.000Z",
      windows: [],
      unavailable: { reason: "probeFailed", message },
    },
    slashCommands: [],
  };
}

/**
 * Build an environment as the snapshot reports it.
 *
 * @param providers - The providers it serves.
 * @param error - The composed environment error.
 * @returns The environment.
 */
function environment(
  providers: ServerProvider[],
  error: string,
): EnvironmentUsageStatus {
  return {
    environmentId: "local",
    label: "This device",
    local: true,
    connected: true,
    canOperate: true,
    serverConfig: {
      providers,
      usageLimitSources: [],
      settings: { usagePriceOverrides: {}, usageModelAliases: {} },
      environment: { capabilities: {} },
    },
    summary: null,
    error,
    refreshing: false,
  };
}

/**
 * Present environments to the limits page.
 *
 * @param environments - Environment labels and their providers.
 * @returns The presentations.
 */
function presentations(
  environments: readonly (readonly [string, ServerProvider[]])[],
): LimitPresentations {
  return new Map(
    environments.map(([label, providers]) => [
      label,
      {
        entry: { target: { label } },
        serverConfig: { providers, usageLimitSources: [] },
      },
    ]),
  );
}

afterEach(() => setLanguage("en"));

describe("localizeQuotaNotice", () => {
  it.each(FAILURES)("translates %s with each retention note", (failure) => {
    for (const note of [RETAINED, NO_PREVIOUS]) {
      const notice = `Claude: ${failure} ${note}`;
      setLanguage("zh-CN");
      expect(localizeQuotaNotice(notice, ["Claude"])).toBe(
        `Claude: ${t(failure)} ${t(note)}`,
      );
      expect(t(failure)).toMatch(HAN);
      setLanguage("en");
      expect(localizeQuotaNotice(notice, ["Claude"])).toBe(notice);
    }
    setLanguage("zh-CN");
    expect(localizeQuotaNotice(`Claude: ${failure}`, ["Claude"])).toBe(
      `Claude: ${t(failure)}`,
    );
  });

  it("keeps every HTTP status code in the translated failure", () => {
    setLanguage("zh-CN");
    for (const status of ["401", "403", "429", "5xx"]) {
      const failure = FAILURES.find((value) =>
        value.includes(`(HTTP ${status})`),
      )!;
      expect(localizeQuotaNotice(`Claude: ${failure}`, ["Claude"])).toContain(
        `HTTP ${status}`,
      );
    }
  });

  it("translates two concatenated warnings and keeps them distinct", () => {
    const error = `Claude: ${FAILURES[2]} ${RETAINED} Claude Work: ${FAILURES[1]} ${NO_PREVIOUS}`;
    setLanguage("zh-CN");
    expect(localizeQuotaNotice(error, ["Claude", "Claude Work"])).toBe(
      "Claude: Claude 用量 API 拒绝了存储的凭据（HTTP 401）。 已保留上次成功读取的额度。 " +
        "Claude Work: Claude Code 存储的访问令牌已过期，无法读取用量额度。 没有上次成功读取的额度可用。",
    );
  });

  it("keeps labels containing a colon or a known sentence exactly", () => {
    const colonLabel = "Team: Claude";
    const sentenceLabel = FAILURES[7];
    const error = `${colonLabel}: ${FAILURES[10]} ${RETAINED} ${sentenceLabel}: ${FAILURES[7]} ${NO_PREVIOUS}`;
    setLanguage("zh-CN");
    expect(
      localizeQuotaNotice(error, ["Team", colonLabel, sentenceLabel]),
    ).toBe(
      `${colonLabel}: ${t(FAILURES[10])} ${t(RETAINED)} ${sentenceLabel}: ${t(FAILURES[7])} ${t(NO_PREVIOUS)}`,
    );
  });

  it("keeps an unrecognized failure but still reads the notices around it", () => {
    const unknown = "Codex did not answer the initialization request.";
    const error = `Codex: ${unknown} ${RETAINED} Claude: ${FAILURES[7]} ${RETAINED}`;
    setLanguage("zh-CN");
    expect(localizeQuotaNotice(error, ["Codex", "Claude"])).toBe(
      `Codex: ${unknown} ${t(RETAINED)} Claude: ${t(FAILURES[7])} ${t(RETAINED)}`,
    );
  });

  it("leaves unknown labels, unknown text and trailing notes unchanged", () => {
    setLanguage("zh-CN");
    const unknownLabel = `Renamed: ${FAILURES[7]} ${RETAINED}`;
    expect(localizeQuotaNotice(unknownLabel, ["Claude"])).toBe(unknownLabel);
    expect(localizeQuotaNotice("environment disconnected", ["Claude"])).toBe(
      "environment disconnected",
    );
    expect(
      localizeQuotaNotice(
        `Claude: ${FAILURES[7]} ${RETAINED} ${HISTORY_NOTE}`,
        ["Claude"],
      ),
    ).toBe(`Claude: ${t(FAILURES[7])} ${t(RETAINED)} ${HISTORY_NOTE}`);
    expect(localizeQuotaNotice(HISTORY_NOTE, ["Claude"])).toBe(HISTORY_NOTE);
    expect(localizeQuotaNotice(`Claude: ${FAILURES[7]}extra`, ["Claude"])).toBe(
      `Claude: ${FAILURES[7]}extra`,
    );
  });

  it("follows live language switches", () => {
    const notice = `Claude: ${FAILURES[7]} ${RETAINED}`;
    setLanguage("zh-CN");
    expect(localizeQuotaNotice(notice, ["Claude"])).toMatch(HAN);
    setLanguage("en");
    expect(localizeQuotaNotice(notice, ["Claude"])).toBe(notice);
    setLanguage("zh-CN");
    expect(localizeQuotaNotice(notice, ["Claude"])).toMatch(HAN);
  });
});

describe("caller context", () => {
  it("uses provider labels exactly as the backend composes them", () => {
    const value = environment(
      [failedProvider("Claude", FAILURES[0])],
      `Claude: ${FAILURES[0]} ${NO_PREVIOUS}`,
    );
    setLanguage("zh-CN");
    expect(localizeEnvironmentError(value)).toBe(
      `Claude: ${t(FAILURES[0])} ${t(NO_PREVIOUS)}`,
    );
    expect(localizeEnvironmentError({ ...value, serverConfig: null })).toBe(
      value.error,
    );
  });

  it("names the environment only when more than one is shown", () => {
    const providers = [failedProvider(" Claude ", FAILURES[0])];
    expect(limitNoticeLabels(presentations([["Laptop", providers]]))).toEqual([
      "Claude",
    ]);
    expect(
      limitNoticeLabels(
        presentations([
          ["Laptop", providers],
          ["Server", providers],
        ]),
      ),
    ).toEqual(["Laptop · Claude", "Server · Claude"]);
  });
});

describe("rendered notices", () => {
  it("localizes limit notices on the limits page", () => {
    const sentenceLabel = FAILURES[3];
    const view = presentations([
      ["Laptop", [failedProvider("Claude", FAILURES[2])]],
      ["Server", [failedProvider(sentenceLabel, FAILURES[12])]],
    ]);
    setLanguage("zh-CN");
    const chinese = renderToStaticMarkup(
      <UsageLimitsPooled presentations={view} now={0} />,
    );
    expect(chinese).toContain(`Laptop · Claude: ${t(FAILURES[2])}`);
    expect(chinese).toContain(`Server · ${sentenceLabel}: ${t(FAILURES[12])}`);
    setLanguage("en");
    const english = renderToStaticMarkup(
      <UsageLimitsPooled presentations={view} now={0} />,
    );
    expect(english).toContain(`Laptop · Claude: ${FAILURES[2]}`);
    expect(english).toContain(`Server · ${sentenceLabel}: ${FAILURES[12]}`);
  });

  it("localizes the composed environment error on the usage page", () => {
    const error = `Claude: ${FAILURES[2]} ${RETAINED} Claude Work: ${FAILURES[10]} ${NO_PREVIOUS}`;
    page.environments = [
      environment(
        [
          failedProvider("Claude", FAILURES[2]),
          failedProvider("Claude Work", FAILURES[10]),
        ],
        error,
      ),
    ];
    const render = () =>
      renderToStaticMarkup(
        <UsagePage isActive={false} view="limits" onSettings={() => {}} />,
      );
    setLanguage("zh-CN");
    expect(render()).toContain(
      `This device: Claude: ${t(FAILURES[2])} ${t(RETAINED)} Claude Work: ${t(FAILURES[10])} ${t(NO_PREVIOUS)}`,
    );
    setLanguage("en");
    expect(render()).toContain(`This device: ${error}`);
  });
});
