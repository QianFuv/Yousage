/** Compare shared hand-calculated Pi fixtures with the unmodified pinned native reader. */
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { piUsageFormat } from "../upstream/packages/provider-pi/src/server/usage";
import type { UsageRecord } from "../upstream/packages/provider-core/src/server/usage";

const FIXTURE = JSON.parse(readFileSync(new URL("../fixtures/pi-usage.json", import.meta.url), "utf8"));

/** Reduce native records to the shared, independently expected accounting fields. */
function accountingFields(record: UsageRecord) {
  return {
    model: record.model,
    rateModel: record.rateModel ?? "",
    input: record.totals.uncachedInputTokens,
    cacheRead: record.totals.cachedInputTokens,
    cacheWrite: record.totals.cacheCreationTokens,
    output: record.totals.outputTokens,
    reasoning: record.totals.reasoningTokens,
    cost: record.reportedCostUsd,
  };
}

describe("Pi upstream accounting", () => {
  it("counts native, auxiliary and partially completed billed calls without inventing prices", () => {
    const state = piUsageFormat.state!.initial();
    const records = FIXTURE.entries.flatMap((entry: unknown) => piUsageFormat.parseLine(JSON.stringify(entry), state));
    expect(records.map(accountingFields)).toEqual(FIXTURE.expected);
    expect(records.every((record: UsageRecord) => record.provider === "pi" && record.sessionId === "pi-parity" && record.speed === "standard")).toBe(true);
  });

  it("identifies a copied fork entry independently of its new session header", () => {
    const state = piUsageFormat.state!.initial();
    piUsageFormat.parseLine(JSON.stringify(FIXTURE.entries[0]), state);
    const original = piUsageFormat.parseLine(JSON.stringify(FIXTURE.entries[1]), state)[0];
    piUsageFormat.parseLine(JSON.stringify({ type: "session", id: "fork" }), state);
    const copied = piUsageFormat.parseLine(JSON.stringify(FIXTURE.entries[1]), state)[0];
    expect(copied.sessionId).toBe("fork");
    expect(copied.dedupeKey).toBe(original.dedupeKey);
  });
});
