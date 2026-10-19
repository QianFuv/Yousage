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
  it("accepts a valid snapshot with its entity tag, text and candidate ID", () => {
    const payload = snapshotJson();
    const read = interpretSnapshot(
      new SnapshotResult({
        status: "ok",
        payload,
        etag: "e1",
        candidateId: "c1",
      }),
    );
    expect(read).toMatchObject({
      status: "ok",
      etag: "e1",
      text: payload,
      candidateId: "c1",
    });
  });

  it("rejects an opened snapshot missing what the viewer reads as a data problem", () => {
    const snapshot = JSON.parse(snapshotJson());
    snapshot.display.value = null;
    const read = interpretSnapshot(
      new SnapshotResult({ status: "ok", payload: JSON.stringify(snapshot) }),
    );
    expect(read).toEqual({ status: "problem", problem: "data" });
  });

  it("passes through data problems and marks reads a newer read replaced", () => {
    expect(
      interpretSnapshot(
        new SnapshotResult({ status: "problem", problem: "data" }),
      ),
    ).toEqual({ status: "problem", problem: "data" });
    expect(
      interpretSnapshot(
        new SnapshotResult({ status: "problem", problem: "stale" }),
      ),
    ).toEqual({ status: "stale" });
  });

  it("reports a newer payload format as a version problem", () => {
    const read = interpretSnapshot(
      new SnapshotResult({ status: "ok", payload: snapshotJson(99) }),
    );
    expect(read).toEqual({ status: "problem", problem: "version" });
  });

  it("treats unreadable JSON as a data problem, not a key mismatch", () => {
    const read = interpretSnapshot(
      new SnapshotResult({ status: "ok", payload: "{" }),
    );
    expect(read).toEqual({ status: "problem", problem: "data" });
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
    for (const problem of ["teapot", "toString", "__proto__"])
      expect(
        interpretSnapshot(new SnapshotResult({ status: "problem", problem })),
      ).toEqual({ status: "problem", problem: "network" });
  });
});
