/** Pooled limit bars show pace, refills and reset plates the way the pinned reference does. */
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ReactElement, ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import type { LimitPresentations } from "@t3tools/shared/usageLimits";
import { setLanguage } from "../i18n";
import { barColor } from "../../vendor/UsageLimits";
import { UsageLimitsPooled } from "../../vendor/UsageLimitsPooled";
import type { ServerProvider, ServerProviderUsageWindow } from "./contracts";

vi.mock("./controller", () => ({
  useData: () => ({ display: { value: { timestampFormat: "24-hour" } } }),
}));
/*
 * Base UI resolves its own React copy, so its primitives are replaced by plain elements. The
 * shared account popover shows the first trigger's payload, as if that segment were hovered.
 */
vi.mock(
  "../../../tools/parity/upstream/apps/web/src/components/ui/button",
  () => ({
    Button: ({ children }: { readonly children?: ReactNode }) => (
      <button type="button">{children}</button>
    ),
    InlineButton: (props: Record<string, unknown>) => (
      <button type="button" {...props} />
    ),
  }),
);
vi.mock(
  "../../../tools/parity/upstream/apps/web/src/components/ui/popover",
  async () => {
    const { cloneElement } = await import("react");
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
        handle,
        payload,
        openOnHover,
        children,
        ...props
      }: {
        readonly render?: ReactElement;
        readonly handle?: Handle;
        readonly payload?: unknown;
        readonly openOnHover?: boolean;
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
        <div data-popup>{children}</div>
      ),
    };
  },
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

afterEach(() => setLanguage("en"));

const NOW = Date.parse("2026-10-20T12:00:00Z");

/** A five-hour session window two hours from its reset, so 40% of it is left. */
const SESSION: ServerProviderUsageWindow = {
  id: "primary",
  kind: "session",
  label: "5h",
  usedPercent: 40,
  resetsAt: "2026-10-20T14:00:00Z",
  windowDurationMins: 300,
} as ServerProviderUsageWindow;

/**
 * Codex accounts on one desktop, each reporting the given windows.
 *
 * @param accounts - Email and windows of each account.
 * @returns The limits page presentations.
 */
function presentations(
  accounts: readonly {
    readonly email: string;
    readonly windows: readonly ServerProviderUsageWindow[];
  }[],
): LimitPresentations {
  const providers: ServerProvider[] = accounts.map(
    ({ email, windows }, index) => ({
      instanceId: `codex-${index}`,
      driver: "codex",
      enabled: true,
      installed: true,
      status: "ready",
      auth: { status: "authenticated", email },
      usageLimits: { checkedAt: "2026-10-20T11:00:00Z", windows: [...windows] },
      slashCommands: [],
    }),
  );
  return new Map([
    [
      "local",
      {
        entry: { target: { label: "Desktop" } },
        serverConfig: { providers, usageLimitSources: [] },
      },
    ],
  ]) as LimitPresentations;
}

/**
 * Static markup of the pooled limits page.
 *
 * @param view - Presentations to render.
 * @returns The markup.
 */
function render(view: LimitPresentations): string {
  return renderToStaticMarkup(
    <UsageLimitsPooled presentations={view} now={NOW} />,
  );
}

/**
 * Widths of the hatched refill layers, in render order.
 *
 * @param markup - Static markup.
 * @returns Each hatch width as written in its style.
 */
function hatchWidths(markup: string): string[] {
  return [
    ...markup.matchAll(/width:([\d.]+%);background-image:repeating/g),
  ].map((match) => match[1]);
}

/**
 * Inner markup of each segment's wide label, the text shown when the bar is roomy.
 *
 * @param markup - Static markup.
 * @returns Each wide label's markup, in bar order.
 */
function wideLabels(markup: string): string[] {
  return [...markup.matchAll(/@2xl\/pool:flex">(.*?)<\/div><\/button>/g)].map(
    (match) => match[1],
  );
}

describe("pooled limit bars", () => {
  it("colors bars with each provider's chart color", () => {
    expect(barColor("pi")).toBe("#4d9f8a");
    expect(barColor("claudeAgent")).toBe("#d97757");
    expect(barColor("unknown-driver")).toBe("var(--foreground)");
  });

  it("names the even-pace share in the segment label and its popover", () => {
    const markup = render(
      presentations([{ email: "someone@example.com", windows: [SESSION] }]),
    );
    expect(markup).toContain(
      "60% left, resets in 2h 0m, 40% of the window left",
    );
    expect(markup).toContain("Even pace");
    expect(markup).toContain("40% left by now");
    setLanguage("zh-CN");
    expect(
      render(
        presentations([{ email: "someone@example.com", windows: [SESSION] }]),
      ),
    ).toContain("此时应剩余 40%");
  });

  it("shows the segment percentage only when the pool has several accounts", () => {
    const single = wideLabels(
      render(
        presentations([{ email: "someone@example.com", windows: [SESSION] }]),
      ),
    );
    expect(single).toHaveLength(1);
    expect(single[0]).not.toContain("60%");
    const pooled = wideLabels(
      render(
        presentations([
          { email: "someone@example.com", windows: [SESSION] },
          { email: "other@example.org", windows: [SESSION] },
        ]),
      ),
    );
    expect(pooled).toHaveLength(2);
    for (const label of pooled) expect(label).toContain(">60%<");
  });

  it("draws no reset plate on a window that has not started", () => {
    const unstarted = {
      ...SESSION,
      usedPercent: 0,
      resetsAt: undefined,
    } as ServerProviderUsageWindow;
    expect(
      render(
        presentations([{ email: "someone@example.com", windows: [unstarted] }]),
      ),
    ).not.toContain("bg-background/85");
    expect(
      render(
        presentations([{ email: "someone@example.com", windows: [SESSION] }]),
      ),
    ).toContain("bg-background/85");
  });

  it("hatches only the session share the next reset can hand back while the week is spent", () => {
    const weekly = (resetsAt: string) =>
      ({
        id: "secondary",
        kind: "weekly",
        label: "Weekly",
        usedPercent: 100,
        resetsAt,
        windowDurationMins: 10_080,
      }) as ServerProviderUsageWindow;
    expect(
      hatchWidths(
        render(
          presentations([
            {
              email: "someone@example.com",
              windows: [SESSION, weekly("2026-10-20T13:00:00Z")],
            },
          ]),
        ),
      ),
    ).toEqual(["60%", "100%"]);
    expect(
      hatchWidths(
        render(
          presentations([
            {
              email: "someone@example.com",
              windows: [SESSION, weekly("2026-10-22T12:00:00Z")],
            },
          ]),
        ),
      ),
    ).toEqual(["100%", "100%"]);
  });
});
