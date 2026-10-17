/** Interpret snapshot reads made by the Android app's Go side. */
import type { SnapshotResult } from "../../bindings/yousage/internal/mobile/models";
import { checkPayload, PROBLEMS, type LoadResult } from "../web/load";
import { isPrivateSnapshot, type PrivateSnapshot } from "../web/payloads";

/** A snapshot read on the device, or the marker that no link or no saved snapshot exists yet. */
export type DeviceRead =
  | LoadResult<PrivateSnapshot>
  | { status: "unpaired" }
  | { status: "empty" };

/**
 * Turn the Go side's snapshot result into a validated payload or a problem.
 *
 * @param result - What `LoadSnapshot` or `CachedSnapshot` returned.
 * @returns The snapshot, an unchanged, unpaired or empty marker, or a problem key.
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
        return { status: "problem", problem: "decrypt" };
      }
      return checkPayload(value, isPrivateSnapshot, result.etag);
    }
    default:
      return {
        status: "problem",
        problem:
          result.problem in PROBLEMS
            ? (result.problem as keyof typeof PROBLEMS)
            : "network",
      };
  }
}
