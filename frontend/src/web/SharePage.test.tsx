/** Share data maps onto the chart and receipt without inventing hidden amounts. */
import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { SharePage, shareChart, shareReceipt } from "./SharePage";
import { isSafePaletteEntry } from "./palette";
import type { SharePayload } from "./payloads";

const HOUR = 3_600_000;
const START = Date.UTC(2026, 9, 15, 0);

/**
 * Build a two-provider share payload.
 *
 * @param overrides - Fields to replace.
 * @returns The payload.
 */
function payloadFixture(overrides: Partial<SharePayload> = {}): SharePayload {
  return {
    formatVersion: 1,
    createdAt: "",
    expiresAt: "",
    days: 1,
    timeZone: "Asia/Shanghai",
    window: {
      sinceDay: "2026-10-15",
      untilDay: "2026-10-15",
      resolution: "hour",
      startsAtMs: START,
      endsAtMs: START + 2 * HOUR,
    },
    hideAmounts: false,
    receipt: {
      number: "N1",
      totalUsd: 3,
      subscriptionUsd: 3,
      paidUsd: 0,
      cacheSavingsUsd: 1,
      tokens: 600,
      sessions: 2,
      activeDays: 1,
      unpricedRecords: 0,
      items: [
        {
          provider: "codex",
          model: "m1",
          tokens: 400,
          costUsd: 2,
          isSubscription: true,
        },
      ],
    },
    series: [
      {
        startMs: START,
        values: [
          { provider: "claude", tokens: 100, costUsd: 0.5 },
          { provider: "codex", tokens: 300, costUsd: 1.5 },
        ],
      },
      { startMs: START + HOUR, values: [{ provider: "codex", tokens: 200 }] },
    ],
    models: [],
    palette: { appearance: "dark", colors: {} },
    ...overrides,
  };
}

describe("share chart", () => {
  it("draws one line per provider in presentation order, filling gaps with zero", () => {
    const chart = shareChart(payloadFixture());
    expect(chart.series.map((line) => line.id)).toEqual(["codex", "claude"]);
    expect(chart.series[0].values).toEqual([1.5, 0]);
    expect(chart.series[1].values).toEqual([0.5, 0]);
    expect(chart.periods).toEqual([
      new Date(START).toISOString(),
      new Date(START + HOUR).toISOString(),
    ]);
  });

  it("charts tokens when amounts are hidden", () => {
    const chart = shareChart(payloadFixture({ hideAmounts: true }));
    expect(chart.series[0].values).toEqual([300, 200]);
  });

  it("labels daily buckets by the sharer's calendar day", () => {
    const chart = shareChart(
      payloadFixture({
        days: 7,
        window: {
          sinceDay: "2026-10-09",
          untilDay: "2026-10-15",
          resolution: "day",
          startsAtMs: Date.UTC(2026, 9, 14, 16),
          endsAtMs: Date.UTC(2026, 9, 15, 16),
        },
        series: [{ startMs: Date.UTC(2026, 9, 14, 16), values: [] }],
      }),
    );
    expect(chart.periods).toEqual(["2026-10-15"]);
  });
});

describe("share receipt", () => {
  it("reads missing amounts as zero and carries no summary code", () => {
    const base = payloadFixture().receipt;
    const receipt = shareReceipt(
      payloadFixture({
        hideAmounts: true,
        receipt: {
          ...base,
          totalUsd: undefined,
          paidUsd: undefined,
          subscriptionUsd: undefined,
          cacheSavingsUsd: undefined,
          items: [{ ...base.items[0], costUsd: undefined }],
        },
      }),
    );
    expect(receipt.totalUsd).toBe(0);
    expect(receipt.items[0].costUsd).toBe(0);
    expect(receipt.qrModules).toEqual([]);
    expect(receipt.tokens).toBe(600);
  });

  it("reads shares from older desktops as having no unknown payments", () => {
    const receipt = shareReceipt(payloadFixture());
    expect(receipt.unknownPaymentUsd).toBe(0);
    expect(receipt.items[0].isPaymentUnknown).toBe(false);
  });

  it("keeps Pi usage of unknown payment apart from the amount paid", () => {
    const base = payloadFixture().receipt;
    const receipt = shareReceipt(
      payloadFixture({
        receipt: {
          ...base,
          totalUsd: 3.5,
          paidUsd: 0,
          unknownPaymentUsd: 0.5,
          items: [
            ...base.items,
            {
              provider: "pi",
              model: "backend/pi-model",
              tokens: 200,
              costUsd: 0.5,
              isSubscription: false,
              isPaymentUnknown: true,
            },
          ],
        },
      }),
    );
    expect(receipt.unknownPaymentUsd).toBe(0.5);
    expect(receipt.paidUsd).toBe(0);
    expect(receipt.items.map((item) => item.isPaymentUnknown)).toEqual([
      false,
      true,
    ]);
  });
});

describe("share chart with Pi", () => {
  it("draws Pi in its own color after the original providers", () => {
    const chart = shareChart(
      payloadFixture({
        series: [
          {
            startMs: START,
            values: [
              { provider: "pi", tokens: 50, costUsd: 0.25 },
              { provider: "codex", tokens: 300, costUsd: 1.5 },
            ],
          },
        ],
      }),
    );
    expect(chart.series.map((line) => [line.id, line.color])).toEqual([
      ["codex", "var(--contrast-foreground)"],
      ["pi", "#4d9f8a"],
    ]);
    expect(chart.series[1].values).toEqual([0.25]);
  });
});

/**
 * Build a share where a known free model sits beside an unknown Pi model with no price.
 *
 * @param hideAmounts - Whether amounts are withheld, as the desktop then omits every amount.
 * @param hasUnpricedCounts - Whether the share carries per-line unpriced counts; older
 * desktops omit them.
 * @returns The payload.
 */
function unpricedPiPayload(
  hideAmounts = false,
  hasUnpricedCounts = true,
): SharePayload {
  const amount = (value: number) => (hideAmounts ? undefined : value);
  const unpriced = hasUnpricedCounts ? 3 : undefined;
  return payloadFixture({
    hideAmounts,
    series: [],
    receipt: {
      number: "N2",
      totalUsd: amount(2),
      subscriptionUsd: amount(0),
      paidUsd: amount(2),
      unknownPaymentUsd: amount(0),
      cacheSavingsUsd: amount(0),
      tokens: 900,
      sessions: 2,
      activeDays: 1,
      unpricedRecords: 3,
      items: [
        {
          provider: "claude",
          model: "claude-model",
          tokens: 300,
          costUsd: amount(2),
          isSubscription: false,
        },
        {
          provider: "codex",
          model: "free-model",
          tokens: 200,
          costUsd: amount(0),
          isSubscription: false,
        },
        {
          provider: "pi",
          model: "backend/pi-unpriced-model",
          tokens: 400,
          costUsd: amount(0),
          unpricedRecords: unpriced,
          isSubscription: false,
          isPaymentUnknown: true,
        },
      ],
    },
    models: [
      {
        provider: "claude",
        model: "claude-model",
        tokens: 300,
        costUsd: amount(2),
        share: hideAmounts ? 1 / 3 : 1,
      },
      {
        provider: "codex",
        model: "free-model",
        tokens: 200,
        costUsd: amount(0),
        share: hideAmounts ? 2 / 9 : 0,
      },
      {
        provider: "pi",
        model: "backend/pi-unpriced-model",
        tokens: 400,
        costUsd: amount(0),
        unpricedRecords: unpriced,
        share: hideAmounts ? 4 / 9 : 0,
      },
    ],
  });
}

/**
 * Visible text of a rendered share page.
 *
 * @param payload - Share payload to render.
 * @returns Text content without markup.
 */
function sharePageText(payload: SharePayload): string {
  return renderToStaticMarkup(<SharePage payload={payload} />)
    .replace(/<[^>]+>/g, " ")
    .replace(/\s+/g, " ");
}

describe("share page with unpriced Pi usage", () => {
  it("shows an unknown Pi model as unpriced while a known free model stays at zero", () => {
    const text = sharePageText(unpricedPiPayload());
    expect(text).toContain("backend/pi-unpriced-model Unpriced");
    expect(text).toContain("Unpriced · 400 tokens");
    expect(text).toContain("free-model $0.00");
    expect(text).toContain("$0.00 · 200 tokens");
    expect(text).toContain("$2.00 · 300 tokens");
    expect(text).not.toContain("backend/pi-unpriced-model $0.00");
    expect(shareReceipt(unpricedPiPayload()).items[2].unpricedRecords).toBe(3);
  });

  it("reads shares without per-line unpriced counts as priced", () => {
    const payload = unpricedPiPayload(false, false);
    const text = sharePageText(payload);
    expect(text).not.toContain("Unpriced ·");
    expect(text).toContain("backend/pi-unpriced-model $0.00");
    expect(shareReceipt(payload).items[2].unpricedRecords).toBe(0);
  });

  it("shows only tokens when amounts are hidden", () => {
    const text = sharePageText(unpricedPiPayload(true));
    expect(text).toContain("400 tokens");
    expect(text).not.toContain("Unpriced");
    expect(text).not.toContain("$");
    expect(text).not.toMatch(/payment|paid/i);
  });
});

describe("share palette", () => {
  it("accepts plain custom properties and rejects resource or declaration escapes", () => {
    expect(isSafePaletteEntry("--background", "#1e1e2e")).toBe(true);
    expect(isSafePaletteEntry("--card", "oklch(0.2 0.01 270 / 80%)")).toBe(
      true,
    );
    expect(isSafePaletteEntry("color", "red")).toBe(false);
    expect(isSafePaletteEntry("--x", "url(https://evil.test/a.png)")).toBe(
      false,
    );
    expect(isSafePaletteEntry("--x", "red; background: blue")).toBe(false);
    expect(isSafePaletteEntry("--x", "</style>")).toBe(false);
    expect(isSafePaletteEntry("--x", "a".repeat(129))).toBe(false);
  });
});
