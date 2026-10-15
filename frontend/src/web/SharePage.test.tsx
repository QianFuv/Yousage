/** Share data maps onto the chart and receipt without inventing hidden amounts. */
import { describe, expect, it } from "vitest";
import { shareChart, shareReceipt } from "./SharePage";
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
