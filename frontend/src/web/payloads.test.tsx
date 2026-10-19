/** The viewer opens only snapshots whose read parts are present, while older and empty snapshots stay readable. */
import { describe, expect, it } from "vitest";
import { checkPayload } from "./load";
import { isPrivateSnapshot } from "./payloads";

/**
 * Build a snapshot the way a desktop with no recorded usage serializes it, with Go's
 * `null` for empty collections and absent summaries.
 *
 * @returns Parsed snapshot JSON.
 */
function emptySnapshot(): Record<string, unknown> {
  const window = {
    timeZone: "UTC",
    sinceDay: "2026-10-19",
    untilDay: "2026-10-20",
    resolution: "day",
  };
  const period = () => ({
    usage: {
      window,
      environments: [
        {
          environmentId: "local",
          label: "This computer",
          local: true,
          connected: true,
          canOperate: false,
          serverConfig: null,
          summary: null,
          refreshing: false,
        },
      ],
    },
    loss: {
      window,
      cacheExpiry: { costUsd: 0, events: 0 },
      providers: null,
      days: null,
      events: null,
      cacheWriteTokens: 0,
    },
    speed: {
      window,
      overall: {},
      models: null,
      providers: null,
      buckets: null,
      measuredRequests: 0,
      unmeasuredRequests: 0,
    },
    receipt: {
      days: 30,
      window,
      items: null,
      firstAtMs: null,
      lastAtMs: null,
      qrModules: null,
    },
  });
  return {
    formatVersion: 1,
    generatedAt: "2026-10-20T03:00:00Z",
    appVersion: "0.1.5",
    timeZone: "Asia/Singapore",
    periods: { "1": period(), "7": period(), "30": period(), "90": period() },
    display: { revision: 4, value: { windowDays: 30, language: "system" } },
    appearance: { revision: 2, value: { customThemes: null } },
    bootstrap: { isSystemDark: true, systemLocale: "zh-CN" },
  };
}

/** Marks a field to delete instead of replace. */
const REMOVE = Symbol("remove");

/**
 * Change fields of a fresh empty snapshot.
 *
 * @param edits - Field paths with their new values, or {@link REMOVE} to delete them.
 * @returns The changed snapshot.
 */
function changed(...edits: [path: string[], value: unknown][]): unknown {
  const snapshot = emptySnapshot();
  for (const [path, value] of edits) {
    let parent = snapshot;
    for (const key of path.slice(0, -1))
      parent = parent[key] as Record<string, unknown>;
    const field = path.at(-1)!;
    if (value === REMOVE) delete parent[field];
    else parent[field] = value;
  }
  return snapshot;
}

describe("isPrivateSnapshot", () => {
  it("accepts an empty desktop snapshot with null collections", () => {
    expect(isPrivateSnapshot(emptySnapshot())).toBe(true);
  });

  it("accepts reports from older desktops that omit fields", () => {
    const report = { usage: {}, loss: {}, speed: {}, receipt: {} };
    expect(
      isPrivateSnapshot(
        changed(
          ...["1", "7", "30", "90"].map((days): [string[], unknown] => [
            ["periods", days],
            report,
          ]),
          [["appearance"], { revision: 1 }],
          [["display"], { revision: 1, value: {} }],
        ),
      ),
    ).toBe(true);
  });

  it("accepts usage from provider kinds this build does not know", () => {
    expect(
      isPrivateSnapshot(
        changed([
          ["periods", "30", "receipt", "items"],
          [{ provider: "aurora", model: "aurora-large", tokens: 1 }],
        ]),
      ),
    ).toBe(true);
  });

  it.each<[string, string[], unknown]>([
    ["a null period", ["periods", "7"], null],
    ["a missing period", ["periods", "90"], REMOVE],
    ["null periods", ["periods"], null],
    ["a missing report", ["periods", "1", "receipt"], REMOVE],
    ["a null report", ["periods", "30", "usage"], null],
    ["a report that is a list", ["periods", "30", "loss"], []],
    ["a null nested report part", ["periods", "1", "usage", "window"], null],
    ["a null environment", ["periods", "1", "usage", "environments"], [null]],
    ["null display preferences", ["display", "value"], null],
    ["missing display", ["display"], REMOVE],
    ["missing appearance", ["appearance"], REMOVE],
    ["null appearance", ["appearance"], null],
    ["missing bootstrap", ["bootstrap"], REMOVE],
    ["an unreadable snapshot time", ["generatedAt"], "yesterday"],
    ["a missing app version", ["appVersion"], REMOVE],
    ["a blank time zone", ["timeZone"], " "],
  ])("rejects %s", (_, path, value) => {
    expect(isPrivateSnapshot(changed([path, value]))).toBe(false);
  });

  it("leaves the checked snapshot unchanged", () => {
    const snapshot = emptySnapshot();
    const copy = structuredClone(snapshot);
    isPrivateSnapshot(snapshot);
    expect(snapshot).toEqual(copy);
  });
});

describe("checkPayload", () => {
  it("reports a malformed snapshot as a data problem rather than a wrong key", () => {
    expect(
      checkPayload(
        changed([["display", "value"], null]),
        isPrivateSnapshot,
        "e1",
      ),
    ).toEqual({ status: "problem", problem: "data" });
  });

  it("reports another format version as a version problem", () => {
    expect(
      checkPayload(changed([["formatVersion"], 2]), isPrivateSnapshot, "e1"),
    ).toEqual({ status: "problem", problem: "version" });
  });
});
