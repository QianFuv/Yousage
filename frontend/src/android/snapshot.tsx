/** Interpret snapshot reads made by the Android app's Go side. */
import type { SnapshotResult } from "../../bindings/yousage/internal/mobile/models";
import { checkPayload, PROBLEMS, type LoadResult } from "../web/load";
import { isPrivateSnapshot, type PrivateSnapshot } from "../web/payloads";

/**
 * A snapshot read on the device, or the marker that no link or no saved snapshot exists yet,
 * or that a newer read replaced this one.
 *
 * An opened snapshot carries its JSON text, which identifies its content, and the ID that
 * saves it on the device; saved snapshots have no ID.
 */
export type DeviceRead =
  | Exclude<LoadResult<PrivateSnapshot>, { status: "ok" }>
  | {
      status: "ok";
      payload: PrivateSnapshot;
      etag: string;
      text: string;
      candidateId: string;
    }
  | { status: "unpaired" }
  | { status: "empty" }
  | { status: "stale" };

/**
 * Turn the Go side's snapshot result into a validated payload or a problem.
 *
 * @param result - What `LoadSnapshot` or `CachedSnapshot` returned.
 * @returns The snapshot, an unchanged, unpaired, empty or stale marker, or a problem key.
 */
export function interpretSnapshot(result: SnapshotResult): DeviceRead {
  switch (result.status) {
    case "unpaired":
      return { status: "unpaired" };
    case "empty":
      return { status: "empty" };
    case "unchanged":
      return { status: "unchanged" };
    case "ok": {
      let value: unknown;
      try {
        value = JSON.parse(result.payload);
      } catch {
        return { status: "problem", problem: "data" };
      }
      const read = checkPayload(value, isPrivateSnapshot, result.etag);
      return read.status === "ok"
        ? { ...read, text: result.payload, candidateId: result.candidateId }
        : read;
    }
    default:
      if (result.problem === "stale") return { status: "stale" };
      return {
        status: "problem",
        problem: Object.hasOwn(PROBLEMS, result.problem)
          ? (result.problem as keyof typeof PROBLEMS)
          : "network",
      };
  }
}
