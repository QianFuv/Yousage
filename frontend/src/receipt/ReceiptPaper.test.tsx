/** Receipts never present usage with an unknown payment method as money actually paid. */
import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { Receipt } from "../../bindings/yousage/internal/service/models";
import { ReceiptPaper } from "./ReceiptPaper";

/**
 * Build a seven-day receipt with Codex by subscription, Claude paid and Pi of unknown payment.
 *
 * @param overrides - Fields to replace; `undefined` omits a field, as older desktops do.
 * @returns The receipt read through the generated model.
 */
function piReceipt(overrides: Record<string, unknown> = {}): Receipt {
  const source: Record<string, unknown> = {
    days: 7,
    startsAtMs: Date.UTC(2026, 9, 1),
    endsAtMs: Date.UTC(2026, 9, 8),
    number: "R1",
    timeZone: "UTC",
    sinceDay: "2026-10-01",
    untilDay: "2026-10-07",
    items: [
      {
        provider: "codex",
        model: "codex-model",
        tokens: 1000,
        costUsd: 3,
        isSubscription: true,
      },
      { provider: "claude", model: "claude-model", tokens: 500, costUsd: 2 },
      {
        provider: "pi",
        model: "backend/pi-model",
        tokens: 700,
        costUsd: 1.25,
        isPaymentUnknown: true,
      },
    ],
    totalUsd: 6.25,
    subscriptionUsd: 3,
    paidUsd: 2,
    unknownPaymentUsd: 1.25,
    tokens: 2200,
    sessions: 3,
    activeDays: 2,
    ...overrides,
  };
  for (const [key, value] of Object.entries(source))
    if (value === undefined) delete source[key];
  return Receipt.createFrom(JSON.stringify(source));
}

/**
 * Visible text of a rendered receipt.
 *
 * @param receipt - Receipt to render.
 * @param hideAmounts - Whether amounts are withheld.
 * @returns Text content without markup.
 */
function receiptText(receipt: Receipt, hideAmounts = false): string {
  return renderToStaticMarkup(
    <ReceiptPaper receipt={receipt} hideAmounts={hideAmounts} />,
  )
    .replace(/<[^>]+>/g, " ")
    .replace(/\s+/g, " ");
}

describe("receipt payments", () => {
  it("separates unknown Pi payments from the amount actually paid", () => {
    const text = receiptText(piReceipt());
    expect(text).toContain("Payment method unknown −$1.25");
    expect(text).toContain("Known amount paid $2.00");
    expect(text).toContain("Paid by subscription −$3.00");
    expect(text).toContain("payment unknown");
    expect(text).not.toMatch(/(?<!Known )Amount paid/);
  });

  it("keeps the original payment lines for receipts without unknown payments", () => {
    const text = receiptText(
      piReceipt({
        items: [
          {
            provider: "codex",
            model: "codex-model",
            tokens: 1000,
            costUsd: 3,
            isSubscription: true,
          },
        ],
        totalUsd: 3,
        unknownPaymentUsd: undefined,
        paidUsd: 0,
      }),
    );
    expect(text).toContain("Amount paid $0.00");
    expect(text).not.toContain("unknown");
    expect(text).not.toContain("Known amount paid");
  });

  it("reveals neither amounts nor payment methods when amounts are hidden", () => {
    const text = receiptText(piReceipt(), true);
    expect(text).not.toContain("$");
    expect(text).not.toContain("unknown");
  });
});
