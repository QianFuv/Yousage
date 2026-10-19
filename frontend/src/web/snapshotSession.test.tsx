/** A newer snapshot replaces the kept one only after it shows and saves; failures keep the last good one. */
import { describe, expect, it, vi } from "vitest";
import {
  createSnapshotSession,
  SnapshotRejectedError,
  type SnapshotCandidate,
} from "./snapshotSession";

/** What the fake viewer is showing, with each call the session made. */
type Harness = ReturnType<typeof harness>;

/**
 * Build a candidate whose identity is its payload.
 *
 * @param payload - Snapshot content.
 * @param etag - Entity tag served with it.
 * @param commit - Saving it on the device.
 * @returns The candidate.
 */
function candidate(
  payload: string,
  etag: string,
  commit?: () => Promise<void>,
): SnapshotCandidate<string> {
  return { payload, etag, identity: payload, commit };
}

/**
 * Drive a session with a relay queue and a viewer that fails to show listed payloads.
 *
 * @param accepted - Payload kept before the session starts, or `null`.
 * @returns The session, the served queue, and records of downloads, shows and restores.
 */
function harness(accepted: string | null = "A") {
  const served: (SnapshotCandidate<string> | null)[] = [];
  const unshowable = new Set<string>();
  const downloads: string[] = [];
  const shown: string[] = [];
  const state = { onScreen: accepted };
  const session = createSnapshotSession<string>({
    accepted: accepted
      ? { payload: accepted, etag: `e-${accepted}`, identity: accepted }
      : null,
    async download(etag) {
      downloads.push(etag);
      return served.shift() ?? null;
    },
    async show(payload) {
      shown.push(payload);
      state.onScreen = payload;
      if (unshowable.has(payload)) throw new Error("render failed");
    },
    restore(payload) {
      state.onScreen = payload;
    },
    describe: (reason) => `rejected:${reason}`,
  });
  /**
   * Run one refresh: download, then apply what arrived.
   *
   * @param isRetry - The person asked to try again.
   */
  const refresh = async (isRetry = false) => {
    const next = await session.fetch(isRetry);
    if (next) await session.accept(next);
  };
  return { session, served, unshowable, downloads, shown, state, refresh };
}

/**
 * Expect a refresh to reject with a shown rejection.
 *
 * @param test - The harness.
 * @param reason - The expected rejection reason.
 * @param isRetry - Whether the person asked to try again.
 */
async function expectRejected(test: Harness, reason: string, isRetry = false) {
  const failure = await test.refresh(isRetry).catch((cause: unknown) => cause);
  expect(failure).toBeInstanceOf(SnapshotRejectedError);
  expect((failure as Error).message).toBe(`rejected:${reason}`);
}

describe("snapshot session", () => {
  it("keeps A and its entity tag when B cannot be shown, then accepts C", async () => {
    const test = harness();
    const commitB = vi.fn(async () => {});
    test.unshowable.add("B");
    test.served.push(candidate("B", "e-B", commitB));
    await expectRejected(test, "render");
    expect(test.state.onScreen).toBe("A");
    expect(test.session.accepted()?.etag).toBe("e-A");
    expect(commitB).not.toHaveBeenCalled();

    test.served.push(candidate("B", "e-B", commitB));
    await expectRejected(test, "render");
    expect(test.shown).toEqual(["B"]);

    const commitC = vi.fn(async () => {});
    test.served.push(candidate("C", "e-C", commitC));
    await test.refresh();
    expect(test.state.onScreen).toBe("C");
    expect(commitC).toHaveBeenCalledOnce();
    expect(test.session.accepted()).toEqual({
      payload: "C",
      etag: "e-C",
      identity: "C",
    });
    await test.refresh();
    expect(test.downloads).toEqual(["e-A", "e-A", "e-A", "e-C"]);
  });

  it("shows a rejected snapshot again only when the person asks to try again", async () => {
    const test = harness();
    test.unshowable.add("B");
    test.served.push(candidate("B", "e-B"));
    await expectRejected(test, "render");
    test.unshowable.delete("B");
    test.served.push(candidate("B", "e-B"));
    await expectRejected(test, "render");
    expect(test.shown).toEqual(["B"]);
    test.served.push(candidate("B", "e-B"));
    await test.refresh(true);
    expect(test.shown).toEqual(["B", "B"]);
    expect(test.session.accepted()?.etag).toBe("e-B");
  });

  it("restores A without advancing when the device cannot save B", async () => {
    const test = harness();
    const commitB = vi.fn(async () => {
      throw new Error("stale candidate");
    });
    test.served.push(candidate("B", "e-B", commitB));
    await expectRejected(test, "save");
    expect(test.state.onScreen).toBe("A");
    expect(test.session.accepted()?.payload).toBe("A");

    test.served.push(candidate("B", "e-B", commitB));
    await expectRejected(test, "save");
    expect(commitB).toHaveBeenCalledOnce();

    test.served.push(candidate("C", "e-C", async () => {}));
    await test.refresh();
    expect(test.session.accepted()?.etag).toBe("e-C");
  });

  it("recognizes corrected content even when the relay reuses or omits the entity tag", async () => {
    const test = harness();
    test.unshowable.add("B");
    test.served.push(candidate("B", "e-reused"));
    await expectRejected(test, "render");
    test.served.push(candidate("C", "e-reused"));
    await test.refresh();
    expect(test.state.onScreen).toBe("C");

    test.unshowable.add("D");
    test.served.push(candidate("D", ""));
    await expectRejected(test, "render");
    test.served.push(candidate("E", ""));
    await test.refresh();
    expect(test.session.accepted()).toMatchObject({ payload: "E", etag: "" });
  });

  it("saves content that is already shown without showing it again", async () => {
    const test = harness();
    const commit = vi.fn(async () => {});
    test.served.push(candidate("A", "e-A2", commit));
    await test.refresh();
    expect(test.shown).toEqual([]);
    expect(commit).toHaveBeenCalledOnce();
    expect(test.session.accepted()?.etag).toBe("e-A2");
  });

  it("downloads from scratch until the first snapshot is kept, and keeps a shown first snapshot that failed to save", async () => {
    const test = harness(null);
    let isSaveFailing = true;
    const commit = vi.fn(async () => {
      if (isSaveFailing) throw new Error("disk full");
    });
    await test.refresh();
    expect(test.downloads).toEqual([""]);
    test.unshowable.add("B");
    await test.session.accept(candidate("B", "e-B")).catch(() => {});
    expect(test.state.onScreen).toBe("B");
    expect(test.session.accepted()).toBeNull();

    const first = candidate("C", "e-C", commit);
    await expect(test.session.accept(first)).rejects.toThrow("rejected:save");
    expect(test.state.onScreen).toBe("C");
    expect(test.session.accepted()).toBeNull();

    test.served.push(candidate("C", "e-C", commit));
    await expectRejected(test, "save");
    isSaveFailing = false;
    test.served.push(candidate("C", "e-C", commit));
    await test.refresh(true);
    expect(test.session.accepted()?.etag).toBe("e-C");
    expect(test.downloads.filter((etag) => etag === "")).toHaveLength(3);
  });
});
