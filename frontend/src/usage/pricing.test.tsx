/** Verify the desktop writer preserves independent target outcomes and original table semantics. */
import { describe, expect, it, vi } from "vitest";
import {
  writeUsagePrices,
  type UsagePriceTarget,
} from "../../vendor/usagePriceTargets";
import {
  usagePriceTableChanges,
  usagePriceCell,
} from "../../vendor/usagePriceTable";
import { isUsagePriceEdit } from "../../vendor/usagePriceForm";

const FIRST: UsagePriceTarget = {
  environmentId: "first",
  label: "First",
  prices: {
    model: { inputCostPerMillionTokens: 1, outputCostPerMillionTokens: 2 },
  },
  aliases: {},
  unavailable: null,
};
const SECOND: UsagePriceTarget = {
  ...FIRST,
  environmentId: "second",
  prices: {
    model: { inputCostPerMillionTokens: 3, outputCostPerMillionTokens: 4 },
  },
};

it("keeps unedited Mixed prices per target while applying a scientific zero edit", () => {
  expect(
    usagePriceCell([FIRST, SECOND], "model", "inputCostPerMillionTokens")
      .placeholder,
  ).toBe("Mixed");
  const draft = {
    id: "model:model",
    model: "model",
    isNew: false,
    values: { outputCostPerMillionTokens: "0e-8" },
  };
  const first = usagePriceTableChanges(FIRST, [draft]);
  const second = usagePriceTableChanges(SECOND, [draft]);
  expect(first.errors.size).toBe(0);
  expect(second.errors.size).toBe(0);
  expect(first.changes).toEqual([
    {
      model: "model",
      price: { inputCostPerMillionTokens: 1, outputCostPerMillionTokens: 0 },
    },
  ]);
  expect(second.changes).toEqual([
    {
      model: "model",
      price: { inputCostPerMillionTokens: 3, outputCostPerMillionTokens: 0 },
    },
  ]);
});

it("reports ambiguous writes independently and retries only the caller's failed target", async () => {
  const write = vi.fn(async ({ environmentId }: { environmentId: string }) =>
    environmentId === "first"
      ? { _tag: "Success" as const }
      : {
          _tag: "Failure" as const,
          error: "Review saved values first",
          outcomeUnknown: true,
        },
  );
  const results = new Map();
  const changes = new Map([
    ["first", [{ model: "model", alias: "target" }]],
    ["second", [{ model: "model", alias: "target" }]],
  ]);
  await writeUsagePrices({
    targets: [FIRST, SECOND],
    changes,
    write,
    onResult: (identity, result) => results.set(identity, result),
  });
  expect(results.get("first")).toEqual({ status: "saved" });
  expect(results.get("second")).toEqual({
    status: "failed",
    error: "Review saved values first",
    outcomeUnknown: true,
  });
  write.mockClear();
  await writeUsagePrices({
    targets: [SECOND],
    changes,
    write,
    onResult: () => {},
  });
  expect(write).toHaveBeenCalledTimes(1);
  expect(write.mock.calls[0]?.[0].environmentId).toBe("second");
});

describe("price input edits", () => {
  it("accepts numbers in progress and rejects stray text", () => {
    expect(isUsagePriceEdit("1", "1.")).toBe(true);
    expect(isUsagePriceEdit("1.5", "1.5e")).toBe(true);
    expect(isUsagePriceEdit("1.5", "")).toBe(true);
    expect(isUsagePriceEdit("1.5", "1.5a")).toBe(false);
    expect(isUsagePriceEdit("", "abc")).toBe(false);
  });
  it("lets a deletion clean up an already invalid value", () => {
    expect(isUsagePriceEdit("1xy2", "1y2")).toBe(true);
    expect(isUsagePriceEdit("1xy2", "1z2")).toBe(false);
  });
});
