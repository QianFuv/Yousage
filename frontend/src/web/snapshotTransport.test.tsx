/** The viewer answers reads from the snapshot and refuses every desktop-only action. */
import { readFileSync } from "node:fs";
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
import { SnapshotResult } from "../../bindings/yousage/internal/mobile/models";
import {
  LossReport,
  Receipt,
  ReceiptItem,
  Snapshot,
  SpeedReport,
} from "../../bindings/yousage/internal/service/models";
import { interpretSnapshot } from "../android/snapshot";
import { mergeAnsweredUsage, type UsageSnapshot } from "../usage/data";
import { modelShare } from "../../vendor/usageBreakdown";

/**
 * Expand the Go-owned wire fixture, keeping one source for cross-language business fields.
 *
 * @returns All four periods with distinct values and complete shared settings.
 */
function snapshotFixture(): PrivateSnapshot {
  const source = readFileSync(
    new URL("../../../internal/sync/snapshot_test.go", import.meta.url),
    "utf8",
  );
  const fixture = /const reportParityJSON = `([\s\S]*?)`/.exec(source)?.[1];
  if (!fixture) throw new Error("Shared report parity fixture is missing");
  const snapshots = [1, 7, 30, 90].map((days) => {
    const start = Date.UTC(2026, 9, 20 - Math.max(1, days - 1));
    const since = new Date(start).toISOString().slice(0, 10);
    const window = {
      timeZone: "UTC",
      sinceDay: since,
      untilDay: "2026-10-20",
      resolution: days === 1 ? "hour" : "day",
      ...(days === 1
        ? { sinceTimeMs: start, untilTimeMs: Date.UTC(2026, 9, 20) }
        : {}),
    };
    return JSON.parse(
      fixture
        .replaceAll("__DAYS__", String(days))
        .replaceAll("__SINCE__", since)
        .replaceAll("__WINDOW__", JSON.stringify(window))
        .replaceAll("__START_MS__", String(start)),
    ) as PrivateSnapshot;
  });
  return {
    ...snapshots[0],
    periods: Object.assign(
      {},
      ...snapshots.map((snapshot) => snapshot.periods),
    ),
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
  it.each([1, 7, 30, 90])(
    "preserves every supplied business field through interpretation and generated models for %i days",
    async (days) => {
      const expected = snapshotFixture();
      const text = JSON.stringify(expected);
      const read = interpretSnapshot(
        new SnapshotResult({
          status: "ok",
          payload: text,
          candidateId: "synthetic-candidate",
          etag: "synthetic-etag",
        }),
      );
      if (read.status !== "ok")
        throw new Error("Business fixture was rejected");
      const names = await captureMethodIds(setTransport);
      setTransport(
        createSnapshotTransport(names, {
          current: () => read.payload,
          refetch: async () => {},
          device: { isDark: () => true, locale: "en-US" },
          open: () => {},
        }),
      );
      const usage = await nativeApi.GetUsage(days, "UTC");
      const loss = await nativeApi.GetLossReport(days, "UTC");
      const speed = await nativeApi.GetSpeedReport(days, "UTC");
      const receipt = await nativeApi.GetReceipt(days, "UTC");
      expect(usage).toBeInstanceOf(Snapshot);
      expect(loss).toBeInstanceOf(LossReport);
      expect(speed).toBeInstanceOf(SpeedReport);
      expect(receipt).toBeInstanceOf(Receipt);
      expect(receipt.items[0]).toBeInstanceOf(ReceiptItem);
      expect(
        JSON.parse(JSON.stringify({ usage, loss, speed, receipt })),
      ).toMatchObject(expected.periods[String(days)]);
      const merged = mergeAnsweredUsage(
        (usage as unknown as UsageSnapshot).environments,
      );
      const model = merged.models.find((value) => value.provider === "codex")!;
      expect(model.model).toBe(
        `synthetic/vendor/complete-model-identifier-${days}`,
      );
      expect(model.costUsd).toBe(days + 0.25);
      expect(model.totalTokens).toBe(days * 400 + 10);
      expect(modelShare(model, "cost")).toBeCloseTo((days + 0.25) / (days + 3));
      expect(modelShare(model, "tokens")).toBeCloseTo(
        (days * 400 + 10) / (days * 400 + 220),
      );
      expect(speed.models[1]).toMatchObject({
        provider: "pi",
        requests: 0,
        unmeasuredRequests: days,
        totalTokens: days * 1000 + 210,
        tokensPerSecond: 0,
        decodeTokensPerSecond: 0,
        ttftRequests: 0,
        medianLatencyMs: 0,
      });
      expect(await nativeApi.GetDisplay()).toMatchObject(expected.display);
      expect(await nativeApi.GetAppearance()).toMatchObject(
        expected.appearance as Record<string, unknown>,
      );
      expect(await nativeApi.GetBootstrap()).toEqual({
        isSystemDark: true,
        systemLocale: "en-US",
      });
      expect(read.text).toBe(text);
      expect(read.payload).toEqual(expected);
      usage.environments[0].summary!.buckets[0].totals.outputTokens = -1;
      receipt.items[0].model = "mutated consumer copy";
      expect(
        JSON.parse(JSON.stringify(await nativeApi.GetUsage(days, "UTC"))),
      ).toMatchObject(
        expected.periods[String(days)].usage as Record<string, unknown>,
      );
      expect(
        JSON.parse(JSON.stringify(await nativeApi.GetReceipt(days, "UTC"))),
      ).toMatchObject(
        expected.periods[String(days)].receipt as Record<string, unknown>,
      );
      expect(read.payload).toEqual(expected);
    },
  );

  it("preserves the desktop Loss event cap without replacing complete-period totals", async () => {
    const snapshot = snapshotFixture();
    const loss = LossReport.createFrom(
      structuredClone(snapshot.periods["90"].loss),
    );
    loss.events = Array.from({ length: 200 }, (_, index) => ({
      ...loss.events[0],
      atMs: loss.events[0].atMs - index * 1000,
    }));
    loss.cacheExpiry.events = 290;
    snapshot.periods["90"].loss = loss;
    const names = await captureMethodIds(setTransport);
    setTransport(
      createSnapshotTransport(names, {
        current: () => snapshot,
        refetch: async () => {},
        device: { isDark: () => false, locale: "en" },
        open: () => {},
      }),
    );
    const actual = await nativeApi.GetLossReport(90, "UTC");
    expect(actual.events).toEqual(loss.events);
    expect(actual.events).toHaveLength(200);
    expect(actual.cacheExpiry.events).toBe(290);
  });

  it("answers each report for the requested period", async () => {
    await install();
    expect(await nativeApi.GetUsage(7, "Asia/Tokyo")).toMatchObject({
      environments: [{ environmentId: "desktop-7" }],
    });
    expect(await nativeApi.GetLossReport(90, "UTC")).toMatchObject({
      cacheExpiry: { tokens: 90500 },
    });
    expect(await nativeApi.GetSpeedReport(1, "UTC")).toMatchObject({
      overall: { decodeTokensPerSecond: 1.8 },
    });
    expect(await nativeApi.GetReceipt(30, "UTC")).toMatchObject({
      days: 30,
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

  it("reads receipts from older snapshots as having no unknown payments", async () => {
    const snapshot = snapshotFixture();
    snapshot.periods["7"].receipt = {
      totalUsd: 3,
      subscriptionUsd: 1,
      paidUsd: 2,
      items: [{ provider: "codex", model: "m", costUsd: 3 }],
    };
    snapshot.periods["30"].receipt = {
      totalUsd: 4,
      subscriptionUsd: 1,
      paidUsd: 2,
      unknownPaymentUsd: 1,
      items: [
        { provider: "pi", model: "b/m", costUsd: 1, isPaymentUnknown: true },
      ],
    };
    const names = await captureMethodIds(setTransport);
    setTransport(
      createSnapshotTransport(names, {
        current: () => snapshot,
        refetch: async () => {},
        device: { isDark: () => false, locale: "en" },
        open: () => {},
      }),
    );
    const older = await nativeApi.GetReceipt(7, "UTC");
    expect(older.unknownPaymentUsd).toBe(0);
    expect(older.items[0].isPaymentUnknown).toBe(false);
    const current = await nativeApi.GetReceipt(30, "UTC");
    expect(current.unknownPaymentUsd).toBe(1);
    expect(current.items[0].isPaymentUnknown).toBe(true);
  });

  it("uses the viewing device for color scheme and locale", async () => {
    await install();
    const bootstrap = await nativeApi.GetBootstrap();
    expect(bootstrap.isSystemDark).toBe(true);
    expect(bootstrap.systemLocale).toBe("en-US");
  });

  it("keeps display changes on the device", async () => {
    await install();
    expect((await nativeApi.GetDisplay()).value.windowDays).toBe(7);
    const saved = await nativeApi.SaveDisplay({
      revision: 3,
      value: { windowDays: 30 },
    } as never);
    expect(saved.revision).toBe(4);
    expect((await nativeApi.GetDisplay()).value.windowDays).toBe(30);
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
    await expect(nativeApi.SaveConfiguration({} as never)).rejects.toThrow();
    await expect(nativeApi.SaveSources({} as never)).rejects.toThrow();
    await expect(nativeApi.GetSources()).rejects.toThrow();
    await expect(nativeApi.GetSync()).rejects.toThrow();
    await expect(nativeApi.SaveSync({} as never)).rejects.toThrow();
    await expect(nativeApi.RotatePairing()).rejects.toThrow();
    await expect(nativeApi.CreateShare({} as never)).rejects.toThrow();
    await expect(nativeApi.ApplyPricing([], {} as never)).rejects.toThrow();
    await expect(nativeApi.SetAutostart(true)).rejects.toThrow();
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
      environments: [{ environmentId: "desktop-7" }],
    });
    expect(forwarded).not.toHaveBeenCalled();
    await expect(nativeApi.GetConfiguration()).resolves.toBeDefined();
    expect(forwarded).toHaveBeenCalledTimes(1);
  });
});
