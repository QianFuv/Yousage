/** Verify reset confirmation identities across completed operations and ambiguous retries. */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useResetCredit } from "./host";
import * as nativeApi from "../../bindings/yousage/internal/desktop/app";

vi.mock("react", () => ({
  useRef: (value: unknown) => ({ current: value }),
  useState: (value: unknown) => [value, vi.fn()],
}));
vi.mock("./controller", () => ({ useData: vi.fn() }));
vi.mock("../i18n", () => ({
  msg: (value: string) => value,
  t: (value: string) => value,
}));
vi.mock("../notify", () => ({ notify: vi.fn(), notifyError: vi.fn() }));
vi.mock("../../bindings/yousage/internal/desktop/app", () => ({
  ConsumeResetCredit: vi.fn(),
  RefreshLimits: vi.fn(),
}));

beforeEach(() => {
  vi.clearAllMocks();
  vi.stubGlobal("document", { visibilityState: "visible" });
  vi.mocked(nativeApi.RefreshLimits).mockResolvedValue(undefined);
});
afterEach(() => vi.unstubAllGlobals());

describe("reset operation lifecycle", () => {
  it.each(["reset", "nothingToReset", "noCredit", "alreadyRedeemed"])(
    "starts a fresh operation after %s",
    async (outcome) => {
      vi.mocked(nativeApi.ConsumeResetCredit).mockResolvedValue({ outcome });
      const action = useResetCredit("local", { instanceId: outcome });
      await action.redeem();
      await action.redeem();
      const calls = vi.mocked(nativeApi.ConsumeResetCredit).mock.calls;
      expect(calls).toHaveLength(2);
      expect(calls[0][1]).not.toBe(calls[1][1]);
      expect(calls.map((call) => call[2])).toEqual([false, false]);
    },
  );

  it.each(["unknown", "failed", "rejected"])(
    "retains the operation for an explicit %s retry after remount",
    async (outcome) => {
      if (outcome === "rejected") {
        vi.mocked(nativeApi.ConsumeResetCredit).mockRejectedValueOnce(
          new Error("transport failed"),
        );
      } else {
        vi.mocked(nativeApi.ConsumeResetCredit).mockResolvedValueOnce({
          outcome,
        });
      }
      vi.mocked(nativeApi.ConsumeResetCredit).mockResolvedValue({
        outcome: "reset",
      });
      await useResetCredit("local", { instanceId: outcome }).redeem();
      await useResetCredit("local", { instanceId: outcome }).redeem();
      const calls = vi.mocked(nativeApi.ConsumeResetCredit).mock.calls;
      expect(calls[0][1]).toBe(calls[1][1]);
      expect(calls.map((call) => call[2])).toEqual([false, true]);
    },
  );
});
