/** The quota-bar prediction follows the backend interval: its amount and confidence never overstate it. */
import { describe, expect, it } from "vitest";
import {
  collectLimitAccounts,
  collectLimitPools,
} from "@t3tools/shared/usageLimits";
import type { CycleValue, EnvironmentUsageStatus } from "./data";
import {
  cycleValueKey,
  indexCycleValues,
  memberCycleValue,
  predictionConfidence,
  predictionPhrase,
  predictionText,
  rangeText,
  relativeSpread,
  usedText,
} from "./CycleValue";

/**
 * Build a cycle value with an estimated weekly interval.
 *
 * @param overrides - Fields that differ from the default estimate.
 * @returns The cycle value.
 */
function cycleValue(overrides: Partial<CycleValue> = {}): CycleValue {
  return {
    instanceId: "codex",
    driver: "codex",
    windowId: "weekly",
    status: "estimated",
    resetDetected: false,
    usedPercent: 23,
    usedLowPercent: 22.5,
    usedHighPercent: 24,
    spentUsd: 41.6,
    records: 10,
    unpricedRecords: 0,
    valueUsd: 180,
    valueLowUsd: 177,
    valueHighUsd: 183,
    constraintCount: 3,
    samples: [],
    observationSource: "logs",
    priorUsed: false,
    priorCycleCount: 0,
    adjustmentSuspected: false,
    history: [],
    ...overrides,
  };
}

describe("rangeText", () => {
  it("shows closed, lower-only and upper-only intervals", () => {
    expect(rangeText(1, 2)).toBe("$1.00 – $2.00");
    expect(rangeText(1, undefined)).toBe("≥ $1.00");
    expect(rangeText(undefined, 2)).toBe("≤ $2.00");
    expect(rangeText(undefined, undefined)).toBeNull();
  });
});

describe("relativeSpread", () => {
  it("is the half-width relative to the midpoint", () => {
    expect(relativeSpread(cycleValue())).toBeCloseTo((3 / 180) * 100);
  });

  it("is unknown without a closed interval", () => {
    expect(relativeSpread(cycleValue({ valueHighUsd: undefined }))).toBeNull();
  });
});

describe("predictionText", () => {
  it("shows the point estimate when the interval is closed", () => {
    expect(predictionText(cycleValue())).toBe("≈ $180.00");
  });

  it("shows only the lower bound while that is all that is known", () => {
    expect(
      predictionText(
        cycleValue({
          status: "insufficientData",
          valueUsd: undefined,
          valueLowUsd: 40,
          valueHighUsd: undefined,
        }),
      ),
    ).toBe("≥ $40.00");
  });

  it("predicts nothing without local usage or a running cycle", () => {
    expect(predictionText(cycleValue({ status: "noLocalUsage" }))).toBeNull();
    expect(predictionText(cycleValue({ status: "noCycle" }))).toBeNull();
  });
});

describe("usedText", () => {
  it("shows the API-equivalent amount already used in the cycle", () => {
    expect(usedText(cycleValue())).toBe("Used $41.60");
  });
});

describe("predictionPhrase", () => {
  it("states the amount used alongside the prediction", () => {
    expect(predictionPhrase(cycleValue())).toBe(
      "$41.60 API-equivalent used of ≈ $180.00 this cycle, High confidence",
    );
  });

  it("states the amount used while the value is still being estimated", () => {
    expect(
      predictionPhrase(
        cycleValue({
          status: "insufficientData",
          valueUsd: undefined,
          valueLowUsd: undefined,
          valueHighUsd: undefined,
        }),
      ),
    ).toBe("$41.60 API-equivalent used this cycle, value being estimated");
  });
});

describe("predictionConfidence", () => {
  it("grades narrow intervals high and wider ones lower", () => {
    expect(predictionConfidence(cycleValue())).toBe("high");
    expect(
      predictionConfidence(cycleValue({ valueLowUsd: 160, valueHighUsd: 200 })),
    ).toBe("medium");
    expect(
      predictionConfidence(cycleValue({ valueLowUsd: 90, valueHighUsd: 270 })),
    ).toBe("low");
  });

  it("is low with only a lower bound and conflicting when observations disagree", () => {
    expect(
      predictionConfidence(
        cycleValue({
          status: "insufficientData",
          valueUsd: undefined,
          valueHighUsd: undefined,
        }),
      ),
    ).toBe("low");
    expect(predictionConfidence(cycleValue({ status: "inconsistent" }))).toBe(
      "conflict",
    );
  });

  it("reaches high confidence at the narrowest band the backend produces", () => {
    expect(
      predictionConfidence(
        cycleValue({
          valueLowUsd: 180 * Math.exp(-0.08),
          valueHighUsd: 180 * Math.exp(0.08),
        }),
      ),
    ).toBe("high");
  });

  it("caps prior-backed estimates at medium confidence", () => {
    expect(predictionConfidence(cycleValue({ priorUsed: true }))).toBe(
      "medium",
    );
  });

  it("has no grade without a prediction", () => {
    expect(predictionConfidence(cycleValue({ status: "noCycle" }))).toBeNull();
  });
});

describe("indexCycleValues", () => {
  it("keys values like pooled native accounts", () => {
    const value = cycleValue();
    const index = indexCycleValues([
      {
        environmentId: "local",
        cycleValues: [value],
      } as EnvironmentUsageStatus,
      { environmentId: "remote" } as EnvironmentUsageStatus,
    ]);
    expect(index.size).toBe(1);
    expect(index.get(cycleValueKey("local:codex", "weekly"))).toBe(value);
  });

  it("finds each pooled Codex member's value by its own window", () => {
    const session = cycleValue({ windowId: "primary" });
    const weekly = cycleValue({ windowId: "secondary" });
    const index = indexCycleValues([
      {
        environmentId: "local",
        cycleValues: [session, weekly],
      } as EnvironmentUsageStatus,
    ]);
    const provider = {
      instanceId: "codex",
      driver: "codex",
      enabled: true,
      installed: true,
      status: "ready",
      auth: { status: "authenticated", email: "user@example.invalid" },
      slashCommands: [],
      usageLimits: {
        checkedAt: "2026-10-06T12:00:00.000Z",
        windows: [
          { id: "primary", kind: "session", label: "5h", usedPercent: 20 },
          { id: "secondary", kind: "weekly", label: "Week", usedPercent: 40 },
        ],
      },
    };
    const [pool] = collectLimitPools(
      collectLimitAccounts(
        new Map([
          [
            "local",
            {
              entry: { target: { label: "Local" } },
              serverConfig: { providers: [provider], usageLimitSources: [] },
            },
          ],
        ]) as unknown as Parameters<typeof collectLimitAccounts>[0],
      ),
      Date.parse("2026-10-06T12:00:00Z"),
    );
    const found = pool.windows.map((window) =>
      window.columns.map((member) =>
        member.window ? memberCycleValue(index, member as never) : undefined,
      ),
    );
    expect(pool.windows.map((window) => window.id)).toEqual([
      "session",
      "weekly",
    ]);
    expect(found).toEqual([[session], [weekly]]);
  });
});
