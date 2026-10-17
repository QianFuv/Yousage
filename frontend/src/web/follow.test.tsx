/** A mounted viewer keeps up with the relay on screen, and stays quiet in the background. */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { FOLLOW_INTERVAL_MS, followSnapshots } from "./follow";

/** A stand-in page whose visibility the test controls. */
class FakeDocument extends EventTarget {
  visibilityState: "visible" | "hidden" = "visible";

  /**
   * Change visibility and announce it like a browser does.
   *
   * @param state - The new visibility.
   */
  show(state: "visible" | "hidden") {
    this.visibilityState = state;
    this.dispatchEvent(new Event("visibilitychange"));
  }
}

let page: FakeDocument;

beforeEach(() => {
  vi.useFakeTimers();
  page = new FakeDocument();
  vi.stubGlobal("document", page);
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe("followSnapshots", () => {
  it("polls while visible and delivers only newer snapshots", async () => {
    const fetchNewer = vi
      .fn<() => Promise<string | null>>()
      .mockResolvedValueOnce(null)
      .mockResolvedValueOnce("second");
    const onNewer = vi.fn();
    const follower = followSnapshots(fetchNewer, onNewer);
    await vi.advanceTimersByTimeAsync(FOLLOW_INTERVAL_MS);
    expect(onNewer).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(FOLLOW_INTERVAL_MS);
    expect(onNewer).toHaveBeenCalledWith("second");
    follower.stop();
  });

  it("skips the background and fetches as soon as the app returns", async () => {
    const fetchNewer = vi.fn(async () => "newer");
    const onNewer = vi.fn();
    const follower = followSnapshots(fetchNewer, onNewer);
    page.show("hidden");
    await vi.advanceTimersByTimeAsync(FOLLOW_INTERVAL_MS * 3);
    expect(fetchNewer).not.toHaveBeenCalled();
    page.show("visible");
    await vi.advanceTimersByTimeAsync(0);
    expect(fetchNewer).toHaveBeenCalledTimes(1);
    expect(onNewer).toHaveBeenCalledWith("newer");
    follower.stop();
  });

  it("shares one request between a manual refresh and a poll", async () => {
    let finish: (value: string) => void = () => {};
    const fetchNewer = vi.fn(
      () => new Promise<string>((resolve) => (finish = resolve)),
    );
    const follower = followSnapshots(fetchNewer, () => {});
    const manual = follower.refetch();
    await vi.advanceTimersByTimeAsync(FOLLOW_INTERVAL_MS);
    expect(fetchNewer).toHaveBeenCalledTimes(1);
    finish("newer");
    await manual;
    follower.stop();
  });

  it("reports manual failures but keeps polling after automatic ones", async () => {
    const fetchNewer = vi
      .fn<() => Promise<string | null>>()
      .mockRejectedValueOnce(new Error("offline"))
      .mockRejectedValueOnce(new Error("offline"))
      .mockResolvedValueOnce("newer");
    const onNewer = vi.fn();
    const follower = followSnapshots(fetchNewer, onNewer);
    await expect(follower.refetch()).rejects.toThrow("offline");
    await vi.advanceTimersByTimeAsync(FOLLOW_INTERVAL_MS * 2);
    expect(onNewer).toHaveBeenCalledWith("newer");
    follower.stop();
  });

  it("shows a refresh on opening, then clears it once the snapshot is current", async () => {
    const statuses: string[] = [];
    const follower = followSnapshots(
      async () => "newer",
      () => {},
      (status) => statuses.push(status.state),
    );
    await follower.refetch(true);
    expect(statuses).toEqual(["refreshing", "current"]);
    follower.stop();
  });

  it("shows a refresh when the app returns, but not for periodic polls", async () => {
    const statuses: string[] = [];
    const follower = followSnapshots(
      async () => null,
      () => {},
      (status) => statuses.push(status.state),
    );
    await vi.advanceTimersByTimeAsync(FOLLOW_INTERVAL_MS * 2);
    expect(statuses).toEqual([]);
    page.show("hidden");
    page.show("visible");
    await vi.advanceTimersByTimeAsync(0);
    expect(statuses).toEqual(["refreshing", "current"]);
    follower.stop();
  });

  it("keeps a shown failure until a later poll succeeds", async () => {
    const statuses: { state: string; problem?: string }[] = [];
    const fetchNewer = vi
      .fn<() => Promise<string | null>>()
      .mockRejectedValueOnce(new Error("The relay cannot be reached"))
      .mockRejectedValueOnce(new Error("offline"))
      .mockResolvedValueOnce(null);
    const follower = followSnapshots(
      fetchNewer,
      () => {},
      (status) => statuses.push(status),
    );
    await expect(follower.refetch(true)).rejects.toThrow();
    expect(statuses.at(-1)).toEqual({
      state: "failed",
      problem: "The relay cannot be reached",
    });
    await vi.advanceTimersByTimeAsync(FOLLOW_INTERVAL_MS);
    expect(statuses.at(-1)?.state).toBe("failed");
    await vi.advanceTimersByTimeAsync(FOLLOW_INTERVAL_MS);
    expect(statuses.at(-1)).toEqual({ state: "current" });
    follower.stop();
  });

  it("ignores everything after it stops", async () => {
    let finish: (value: string) => void = () => {};
    const fetchNewer = vi.fn(
      () => new Promise<string>((resolve) => (finish = resolve)),
    );
    const onNewer = vi.fn();
    const follower = followSnapshots(fetchNewer, onNewer);
    const pending = follower.refetch();
    follower.stop();
    finish("late");
    await pending;
    page.show("visible");
    await vi.advanceTimersByTimeAsync(FOLLOW_INTERVAL_MS * 2);
    expect(onNewer).not.toHaveBeenCalled();
    expect(fetchNewer).toHaveBeenCalledTimes(1);
  });
});
