/** The Android app shows only valid snapshots and maps every Go outcome to a problem page. */
import { describe, expect, it } from "vitest";
import { SnapshotResult } from "../../bindings/yousage/internal/mobile/models";
import { interpretSnapshot } from "./snapshot";

/**
 * Build a minimal valid private snapshot.
 *
 * @param formatVersion - Payload format version to claim.
 * @returns The snapshot as JSON text.
 */
function snapshotJson(formatVersion = 1): string {
  const period = { usage: {}, loss: {}, speed: {}, receipt: {} };
  return JSON.stringify({
    formatVersion,
    generatedAt: "2026-10-16T00:00:00Z",
    appVersion: "test",
    timeZone: "UTC",
    periods: { "1": period, "7": period, "30": period, "90": period },
    display: { revision: 1, value: {} },
    appearance: { revision: 1 },
    bootstrap: { isSystemDark: true, systemLocale: "en" },
  });
}

describe("interpretSnapshot", () => {
  it("accepts a valid snapshot with its entity tag", () => {
    const read = interpretSnapshot(
      new SnapshotResult({ status: "ok", payload: snapshotJson(), etag: "e1" }),
    );
    expect(read).toMatchObject({ status: "ok", etag: "e1" });
  });

  it("reports a newer payload format as a version problem", () => {
    const read = interpretSnapshot(
      new SnapshotResult({ status: "ok", payload: snapshotJson(99) }),
    );
    expect(read).toEqual({ status: "problem", problem: "version" });
  });

  it("treats unreadable JSON as a decrypt problem", () => {
    const read = interpretSnapshot(
      new SnapshotResult({ status: "ok", payload: "{" }),
    );
    expect(read).toEqual({ status: "problem", problem: "decrypt" });
  });

  it("passes through unpaired, empty, unchanged and known problems", () => {
    expect(
      interpretSnapshot(new SnapshotResult({ status: "unpaired" })),
    ).toEqual({ status: "unpaired" });
    expect(interpretSnapshot(new SnapshotResult({ status: "empty" }))).toEqual({
      status: "empty",
    });
    expect(
      interpretSnapshot(new SnapshotResult({ status: "unchanged" })),
    ).toEqual({ status: "unchanged" });
    expect(
      interpretSnapshot(
        new SnapshotResult({ status: "problem", problem: "missing" }),
      ),
    ).toEqual({ status: "problem", problem: "missing" });
  });

  it("falls back to a network problem for unknown outcomes", () => {
    expect(
      interpretSnapshot(
        new SnapshotResult({ status: "problem", problem: "teapot" }),
      ),
    ).toEqual({ status: "problem", problem: "network" });
  });
});
