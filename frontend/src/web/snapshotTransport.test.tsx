/** The viewer answers reads from the snapshot and refuses every desktop-only action. */
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  getTransport,
  setTransport,
  type RuntimeTransport,
} from "@wailsio/runtime";
import * as nativeApi from "../../bindings/yousage/internal/desktop/app";
import {
  captureMethodIds,
  createSnapshotTransport,
  type SnapshotSource,
} from "./snapshotTransport";
import type { PrivateSnapshot } from "./payloads";

/**
 * Build a snapshot whose reports name their own period.
 *
 * @returns The snapshot.
 */
function snapshotFixture(): PrivateSnapshot {
  const periods = Object.fromEntries(
    ["1", "7", "30", "90"].map((days) => [
      days,
      {
        usage: { name: `usage-${days}` },
        loss: { name: `loss-${days}` },
        speed: { name: `speed-${days}` },
        receipt: { name: `receipt-${days}` },
      },
    ]),
  );
  return {
    formatVersion: 1,
    generatedAt: "2026-10-16T00:00:00Z",
    appVersion: "test",
    timeZone: "UTC",
    periods,
    display: { revision: 3, value: { windowDays: 30 } },
    appearance: { revision: 1 },
    bootstrap: { isSystemDark: false, systemLocale: "zh-CN" },
  };
}

/**
 * Install a snapshot transport and return its spies.
 *
 * @returns The refetch and open spies.
 */
async function install() {
  const names = await captureMethodIds(setTransport);
  const source: SnapshotSource = {
    current: snapshotFixture,
    refetch: vi.fn(async () => {}),
    device: { isDark: () => true, locale: "en-US" },
    open: vi.fn(),
  };
  setTransport(createSnapshotTransport(names, source));
  return source;
}

afterEach(() => setTransport(null));

describe("snapshot transport", () => {
  it("answers each report for the requested period", async () => {
    await install();
    expect(await nativeApi.GetUsage(7, "Asia/Tokyo")).toMatchObject({
      name: "usage-7",
    });
    expect(await nativeApi.GetLossReport(90, "UTC")).toMatchObject({
      name: "loss-90",
    });
    expect(await nativeApi.GetSpeedReport(1, "UTC")).toMatchObject({
      name: "speed-1",
    });
    expect(await nativeApi.GetReceipt(30, "UTC")).toMatchObject({
      name: "receipt-30",
    });
  });

  it("hands out copies so model constructors cannot change the snapshot", async () => {
    const snapshot = snapshotFixture();
    const names = await captureMethodIds(setTransport);
    setTransport(
      createSnapshotTransport(names, {
        current: () => snapshot,
        refetch: async () => {},
        device: { isDark: () => false, locale: "en" },
        open: () => {},
      }),
    );
    const usage = {
      environments: [{ environmentId: "local", summary: { buckets: [] } }],
    };
    snapshot.periods["7"].usage = structuredClone(usage);
    await nativeApi.GetUsage(7, "UTC");
    await nativeApi.GetUsage(7, "UTC");
    expect(snapshot.periods["7"].usage).toStrictEqual(usage);
  });

  it("uses the viewing device for color scheme and locale", async () => {
    await install();
    const bootstrap = await nativeApi.GetBootstrap();
    expect(bootstrap.isSystemDark).toBe(true);
    expect(bootstrap.systemLocale).toBe("en-US");
  });

  it("keeps display changes on the device", async () => {
    await install();
    const saved = await nativeApi.SaveDisplay({
      revision: 3,
      value: { windowDays: 7 },
    } as never);
    expect(saved.revision).toBe(4);
    expect((await nativeApi.GetDisplay()).value.windowDays).toBe(7);
  });

  it("turns refreshes into a relay refetch and opens links on the device", async () => {
    const source = await install();
    await nativeApi.RefreshUsage(30, "UTC", [], {} as never);
    await nativeApi.RefreshLimits([], {} as never);
    await nativeApi.OpenExternal("https://example.com/");
    expect(source.refetch).toHaveBeenCalledTimes(2);
    expect(source.open).toHaveBeenCalledWith("https://example.com/");
  });

  it("refuses desktop-only actions", async () => {
    await install();
    await expect(
      nativeApi.ConsumeResetCredit({} as never, "operation", false),
    ).rejects.toThrow();
    await expect(nativeApi.GetConfiguration()).rejects.toThrow();
    await expect(nativeApi.SaveAppearance(1, {} as never)).rejects.toThrow();
  });

  it("restores the host transport after recording method IDs", async () => {
    const host: RuntimeTransport = { call: async () => null };
    setTransport(host);
    await captureMethodIds(setTransport, host);
    expect(getTransport()).toBe(host);
  });

  it("passes other calls to the host transport and keeps reads local", async () => {
    const forwarded = vi.fn(async () => ({}));
    const names = await captureMethodIds(setTransport);
    setTransport(
      createSnapshotTransport(names, {
        current: snapshotFixture,
        refetch: async () => {},
        device: { isDark: () => false, locale: "en" },
        open: () => {},
        fallback: { call: forwarded },
      }),
    );
    expect(await nativeApi.GetUsage(7, "UTC")).toMatchObject({
      name: "usage-7",
    });
    expect(forwarded).not.toHaveBeenCalled();
    await expect(nativeApi.GetConfiguration()).resolves.toBeDefined();
    expect(forwarded).toHaveBeenCalledTimes(1);
  });
});
