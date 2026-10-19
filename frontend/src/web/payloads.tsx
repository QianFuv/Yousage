/** Decrypted relay payloads: the private dashboard snapshot and a public receipt share. */
import { SavedDisplay } from "../../bindings/yousage/internal/preferences/models";
import {
  LossReport,
  Receipt,
  Snapshot,
  SpeedReport,
} from "../../bindings/yousage/internal/service/models";
import { LibrarySnapshot } from "../../bindings/yousage/internal/theme/models";

/** Format version understood by this build for both payload kinds. */
export const PAYLOAD_FORMAT_VERSION = 1;

/** Trailing windows carried by a snapshot, keyed by their day count. */
export const SNAPSHOT_PERIODS = ["1", "7", "30", "90"] as const;

/** Reports for one trailing window, exactly as the desktop bindings return them. */
export type SnapshotPeriod = {
  usage: unknown;
  loss: unknown;
  speed: unknown;
  receipt: unknown;
};

/** The private snapshot pushed by the desktop after each refresh. */
export type PrivateSnapshot = {
  formatVersion: number;
  generatedAt: string;
  appVersion: string;
  timeZone: string;
  periods: Record<string, SnapshotPeriod>;
  display: { revision: number; value: Record<string, unknown> };
  appearance: unknown;
  bootstrap: { isSystemDark: boolean; systemLocale: string };
};

/** One provider's share of one chart bucket. */
export type ShareSeriesValue = {
  provider: string;
  tokens: number;
  costUsd?: number;
};

/**
 * A receipt line, without its cost when amounts are hidden; older shares omit
 * `isPaymentUnknown` and `unpricedRecords`.
 */
export type ShareReceiptItem = {
  provider: string;
  model: string;
  tokens: number;
  costUsd?: number;
  unpricedRecords?: number;
  isSubscription: boolean;
  isPaymentUnknown?: boolean;
};

/**
 * A model's slice of the period, measured by cost or by tokens when amounts are hidden;
 * older shares omit `unpricedRecords`.
 */
export type ShareModel = {
  provider: string;
  model: string;
  tokens: number;
  costUsd?: number;
  unpricedRecords?: number;
  share: number;
};

/** The public share page payload. Every `*Usd` field is absent when amounts are hidden. */
export type SharePayload = {
  formatVersion: number;
  createdAt: string;
  expiresAt: string;
  days: number;
  timeZone: string;
  window: {
    sinceDay: string;
    untilDay: string;
    resolution: "hour" | "day";
    startsAtMs: number;
    endsAtMs: number;
  };
  hideAmounts: boolean;
  receipt: {
    number: string;
    totalUsd?: number;
    subscriptionUsd?: number;
    paidUsd?: number;
    unknownPaymentUsd?: number;
    cacheSavingsUsd?: number;
    tokens: number;
    sessions: number;
    activeDays: number;
    unpricedRecords: number;
    items: ShareReceiptItem[];
  };
  series: { startMs: number; values: ShareSeriesValue[] }[];
  models: ShareModel[];
  palette: { appearance: "dark" | "light"; colors: Record<string, string> };
};

/**
 * Generated binding models the viewer's pages build from each report of a period.
 * They tolerate omitted fields and `null` collections, which older desktops and empty
 * reports produce, but throw where a nested report is `null` or not an object.
 */
const REPORT_MODELS = {
  usage: Snapshot.createFrom,
  loss: LossReport.createFrom,
  speed: SpeedReport.createFrom,
  receipt: Receipt.createFrom,
} satisfies Record<keyof SnapshotPeriod, (source: unknown) => unknown>;

/**
 * Whether a value is a JSON object rather than `null`, an array or a scalar.
 *
 * @param value - Parsed JSON.
 * @returns Whether its fields can be read.
 */
function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

/**
 * Whether a value is non-blank text.
 *
 * @param value - Parsed JSON.
 * @returns Whether it is a string with visible characters.
 */
function isText(value: unknown): value is string {
  return typeof value === "string" && value.trim() !== "";
}

/**
 * Whether a generated binding model can be built from a value, as the pages will build it.
 *
 * @param create - The model's `createFrom`, which rewrites what it receives.
 * @param value - Parsed JSON, left unchanged.
 * @returns Whether building the model succeeded.
 */
function isModelReadable(
  create: (source: unknown) => unknown,
  value: unknown,
): boolean {
  try {
    create(structuredClone(value));
    return true;
  } catch {
    return false;
  }
}

/**
 * Check that a decrypted private snapshot has everything the viewer reads.
 *
 * Fields that older desktops omit and collections that are `null` stay acceptable,
 * as do provider kinds this build does not know.
 *
 * @param value - Parsed JSON.
 * @returns Whether its metadata is valid and every period, report and setting the viewer
 * reads is an object the generated binding models accept.
 */
export function isPrivateSnapshot(value: unknown): value is PrivateSnapshot {
  if (!isObject(value) || value.formatVersion !== PAYLOAD_FORMAT_VERSION)
    return false;
  const { periods, display, appearance, bootstrap } = value;
  return (
    isText(value.generatedAt) &&
    !Number.isNaN(Date.parse(value.generatedAt)) &&
    isText(value.appVersion) &&
    isText(value.timeZone) &&
    isObject(periods) &&
    SNAPSHOT_PERIODS.every((days) => {
      const period = periods[days];
      return (
        isObject(period) &&
        Object.entries(REPORT_MODELS).every(
          ([report, create]) =>
            isObject(period[report]) && isModelReadable(create, period[report]),
        )
      );
    }) &&
    isObject(display) &&
    isObject(display.value) &&
    isModelReadable(SavedDisplay.createFrom, display) &&
    isObject(appearance) &&
    isModelReadable(LibrarySnapshot.createFrom, appearance) &&
    isObject(bootstrap)
  );
}

/**
 * Check the minimal shape of a decrypted share payload.
 *
 * @param value - Parsed JSON.
 * @returns Whether the receipt, series, models and palette are present.
 */
export function isSharePayload(value: unknown): value is SharePayload {
  const payload = value as SharePayload | null;
  return (
    typeof payload === "object" &&
    payload !== null &&
    payload.formatVersion === PAYLOAD_FORMAT_VERSION &&
    typeof payload.receipt === "object" &&
    payload.receipt !== null &&
    Array.isArray(payload.receipt.items) &&
    Array.isArray(payload.series) &&
    Array.isArray(payload.models) &&
    typeof payload.window === "object" &&
    payload.window !== null
  );
}

/**
 * Read the format version of a payload that failed the shape check.
 *
 * @param value - Parsed JSON.
 * @returns Its `formatVersion`, or `null` when absent.
 */
export function payloadVersion(value: unknown): number | null {
  const version = (value as { formatVersion?: unknown } | null)?.formatVersion;
  return typeof version === "number" ? version : null;
}
