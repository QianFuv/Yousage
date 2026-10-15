/** Decrypted relay payloads: the private dashboard snapshot and a public receipt share. */

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

/** A receipt line, without its cost when amounts are hidden. */
export type ShareReceiptItem = {
  provider: string;
  model: string;
  tokens: number;
  costUsd?: number;
  isSubscription: boolean;
};

/** A model's slice of the period, measured by cost or by tokens when amounts are hidden. */
export type ShareModel = {
  provider: string;
  model: string;
  tokens: number;
  costUsd?: number;
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
 * Check the minimal shape of a decrypted private snapshot.
 *
 * @param value - Parsed JSON.
 * @returns Whether every period and setting the viewer reads is present.
 */
export function isPrivateSnapshot(value: unknown): value is PrivateSnapshot {
  const snapshot = value as PrivateSnapshot | null;
  return (
    typeof snapshot === "object" &&
    snapshot !== null &&
    snapshot.formatVersion === PAYLOAD_FORMAT_VERSION &&
    typeof snapshot.periods === "object" &&
    snapshot.periods !== null &&
    SNAPSHOT_PERIODS.every(
      (period) => snapshot.periods[period] !== undefined,
    ) &&
    typeof snapshot.display === "object" &&
    snapshot.display !== null &&
    typeof snapshot.bootstrap === "object" &&
    snapshot.bootstrap !== null
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
