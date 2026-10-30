/** Every page reads one global refresh state and offers no per-page refresh button. */
import { afterEach, describe, expect, it, vi } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { setLanguage } from "../i18n";
import type { RefreshState } from "./controller";
import { describeRefresh, REFRESH_FAILURES } from "./refreshStatus";
import { UsagePage } from "./UsagePage";
import { LossPage } from "../loss/LossPage";
import { SpeedPage } from "../speed/SpeedPage";

const page = vi.hoisted(() => ({
  refreshState: {
    isRefreshing: false,
    lastCompletedAt: "",
    lastError: "",
  } as RefreshState,
}));

vi.mock("./controller", () => ({
  useData: () => ({
    snapshot: {
      environments: [
        {
          environmentId: "local",
          label: "This device",
          connected: true,
          summary: null,
          error: "",
        },
      ],
      window: undefined,
    },
    selected: null,
    error: "",
    isBusy: page.refreshState.isRefreshing,
    refreshState: page.refreshState,
    display: {
      value: {
        metric: "cost",
        windowDays: 7,
        shouldAutoRefresh: true,
        timestampFormat: "24h",
      },
    },
    refresh: async () => {},
    patchDisplay: async () => {},
  }),
}));
vi.mock("./PeriodToggle", () => ({
  PeriodToggle: () => null,
  usePeriodDays: () => 7,
}));
vi.mock("../../bindings/yousage/internal/desktop/app", () => ({
  OpenExternal: vi.fn(),
  GetLossReport: vi.fn(),
  GetSpeedReport: vi.fn(),
}));

afterEach(() => {
  setLanguage("en");
  page.refreshState = {
    isRefreshing: false,
    lastCompletedAt: "",
    lastError: "",
  };
});

/** Render each routed page as the desktop shows it; Usage shares the quota page header. */
function renderPages(): Record<string, string> {
  return {
    quota: renderToStaticMarkup(
      <UsagePage isActive view="limits" onSettings={() => {}} />,
    ),
    loss: renderToStaticMarkup(<LossPage isActive />),
    speed: renderToStaticMarkup(<SpeedPage isActive />),
  };
}

describe("global refresh pages", () => {
  it("offer no manual refresh button on any page", () => {
    for (const [name, html] of Object.entries(renderPages())) {
      expect(html, name).not.toMatch(/aria-label="Refresh (usage|limits)"/);
      expect(html, name).not.toContain("Ctrl+R");
      expect(html, name).not.toContain("animate-spin");
    }
  });

  it("show the scheduler progress in the page header", () => {
    page.refreshState = { ...page.refreshState, isRefreshing: true };
    expect(renderPages().quota).toContain("Refreshing…");
  });

  it("keep a failed refresh visible with its cause instead of hiding it", () => {
    page.refreshState = {
      isRefreshing: false,
      lastCompletedAt: "2026-10-30T19:27:49Z",
      lastError: REFRESH_FAILURES[0],
    };
    const quota = renderPages().quota;
    expect(quota).toContain("Refresh failed");
    expect(quota).toContain(`title="${REFRESH_FAILURES[0]}"`);
  });
});

describe("describeRefresh", () => {
  const idle: RefreshState = {
    isRefreshing: false,
    lastCompletedAt: "",
    lastError: "",
  };

  it("reports nothing before the first cycle", () => {
    expect(describeRefresh(idle, "24h")).toBeNull();
  });

  it("reports the last completion time in the chosen clock", () => {
    const completed = "2026-10-30T19:42:49Z";
    const expected = new Date(completed).toLocaleTimeString(undefined, {
      hour: "2-digit",
      minute: "2-digit",
      hour12: false,
    });
    expect(
      describeRefresh({ ...idle, lastCompletedAt: completed }, "24h"),
    ).toBe(`Updated ${expected}`);
  });

  it("prefers progress over an earlier failure", () => {
    expect(
      describeRefresh(
        { isRefreshing: true, lastCompletedAt: "", lastError: "offline" },
        "locale",
      ),
    ).toBe("Refreshing…");
  });

  it("translates the status", () => {
    setLanguage("zh-CN");
    expect(describeRefresh({ ...idle, isRefreshing: true }, "locale")).toBe(
      "正在刷新…",
    );
  });
});
