/** A shown snapshot is acknowledged only by a commit of its answered data, never by time alone. */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  ACKNOWLEDGE_TIMEOUT_MS,
  createRenderAcknowledger,
} from "./renderAcknowledgment";

beforeEach(() => {
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
});

/**
 * A report read the test answers or fails when it chooses.
 *
 * @returns The read and the ways to settle it.
 */
function deferredRead() {
  let answer: (value: string) => void = () => {};
  let fail: (cause: Error) => void = () => {};
  const read = new Promise<string>((resolve, reject) => {
    answer = resolve;
    fail = reject;
  });
  return { read, answer, fail };
}

/**
 * An acknowledger whose confirmations are recorded, with the outcome of one wait.
 *
 * @returns The acknowledger, the confirmations rendered and a way to wait for a generation.
 */
function harness() {
  const confirmations: number[] = [];
  const acknowledger = createRenderAcknowledger(() =>
    confirmations.push(acknowledger.token()),
  );
  /**
   * Wait for a generation and record how the wait ends.
   *
   * @param generation - The shown snapshot.
   * @returns The recorded outcome, updated when the wait ends.
   */
  const wait = (generation: number) => {
    const outcome = { state: "waiting" as "waiting" | "shown" | "failed" };
    acknowledger.wait(generation).then(
      () => (outcome.state = "shown"),
      () => (outcome.state = "failed"),
    );
    return outcome;
  };
  return { acknowledger, confirmations, wait };
}

describe("createRenderAcknowledger", () => {
  it("does not confirm while a report read is unanswered, however long it takes", async () => {
    const { acknowledger, confirmations, wait } = harness();
    const late = deferredRead();
    const tracked = acknowledger.track(1, late.read);
    const outcome = wait(1);
    await vi.advanceTimersByTimeAsync(ACKNOWLEDGE_TIMEOUT_MS / 2);
    acknowledger.committed(acknowledger.token());
    await vi.advanceTimersByTimeAsync(0);
    expect(confirmations).toEqual([]);
    expect(outcome.state).toBe("waiting");
    late.answer("report");
    expect(await tracked).toBe("report");
    await vi.advanceTimersByTimeAsync(0);
    expect(confirmations).toEqual([1]);
    acknowledger.committed(1);
    await vi.advanceTimersByTimeAsync(0);
    expect(outcome.state).toBe("shown");
  });

  it("ignores commits before the confirmation, such as a loading fallback's", async () => {
    const { acknowledger, confirmations, wait } = harness();
    const outcome = wait(1);
    acknowledger.committed(0);
    await vi.advanceTimersByTimeAsync(0);
    expect(outcome.state).toBe("waiting");
    expect(confirmations).toEqual([1]);
    acknowledger.committed(1);
    await vi.advanceTimersByTimeAsync(0);
    expect(outcome.state).toBe("shown");
  });

  it("accepts the confirmation's commit when lazy content commits it late", async () => {
    const { acknowledger, wait } = harness();
    const outcome = wait(1);
    await vi.advanceTimersByTimeAsync(ACKNOWLEDGE_TIMEOUT_MS - 1);
    expect(outcome.state).toBe("waiting");
    acknowledger.committed(1);
    await vi.advanceTimersByTimeAsync(0);
    expect(outcome.state).toBe("shown");
  });

  it("needs a new confirmation when a read starts after the last one", async () => {
    const { acknowledger, confirmations, wait } = harness();
    const outcome = wait(1);
    await vi.advanceTimersByTimeAsync(0);
    const late = deferredRead();
    void acknowledger.track(1, late.read);
    acknowledger.committed(1);
    late.answer("report");
    await vi.advanceTimersByTimeAsync(0);
    expect(outcome.state).toBe("waiting");
    expect(confirmations).toEqual([1, 2]);
    acknowledger.committed(1);
    await vi.advanceTimersByTimeAsync(0);
    expect(outcome.state).toBe("waiting");
    acknowledger.committed(2);
    await vi.advanceTimersByTimeAsync(0);
    expect(outcome.state).toBe("shown");
  });

  it("fails at once when the waiting snapshot's pages fail, even after its data rendered late", async () => {
    const { acknowledger, wait } = harness();
    const late = deferredRead();
    void acknowledger.track(1, late.read);
    const outcome = wait(1);
    late.answer("report");
    await vi.advanceTimersByTimeAsync(0);
    acknowledger.failed(1);
    acknowledger.committed(1);
    await vi.advanceTimersByTimeAsync(0);
    expect(outcome.state).toBe("failed");
  });

  it("fails when a report read for the waiting snapshot fails", async () => {
    const { acknowledger, wait } = harness();
    const late = deferredRead();
    const tracked = acknowledger.track(1, late.read).catch(() => "failed");
    const outcome = wait(1);
    late.fail(new Error("unreadable"));
    expect(await tracked).toBe("failed");
    await vi.advanceTimersByTimeAsync(0);
    expect(outcome.state).toBe("failed");
  });

  it("keeps waiting when a failure belongs to another snapshot", async () => {
    const { acknowledger, wait } = harness();
    const outcome = wait(2);
    acknowledger.failed(1);
    await vi.advanceTimersByTimeAsync(0);
    acknowledger.committed(1);
    await vi.advanceTimersByTimeAsync(0);
    expect(outcome.state).toBe("shown");
  });

  it("remembers a failure reported before the wait starts", async () => {
    const { acknowledger, wait } = harness();
    acknowledger.failed(0);
    const outcome = wait(0);
    await vi.advanceTimersByTimeAsync(0);
    acknowledger.committed(1);
    await vi.advanceTimersByTimeAsync(0);
    expect(outcome.state).toBe("failed");
  });

  it("gives up after the timeout and on cancellation, leaving no timers", async () => {
    const { acknowledger, wait } = harness();
    const timedOut = wait(1);
    await vi.advanceTimersByTimeAsync(ACKNOWLEDGE_TIMEOUT_MS);
    expect(timedOut.state).toBe("failed");
    const cancelled = wait(2);
    await vi.advanceTimersByTimeAsync(0);
    acknowledger.cancel();
    acknowledger.committed(acknowledger.token());
    await vi.advanceTimersByTimeAsync(0);
    expect(cancelled.state).toBe("failed");
    expect(vi.getTimerCount()).toBe(0);
  });
});
