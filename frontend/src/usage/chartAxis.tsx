/** Bucket label formatting shared by the Loss and Speed charts. */
import type { Window } from "../../bindings/yousage/internal/accounting/models";
import {
  formatDayShort,
  formatHourShort,
  formatRelativeHourShort,
} from "../i18n";

/**
 * Label formatters for the buckets of a report window: days, or hours in a 24-hour window.
 *
 * @param reportWindow - Window whose bucket labels are formatted.
 * @returns Whether buckets are hourly, a short axis label and a fuller tooltip label.
 */
export function bucketLabels(reportWindow: Window) {
  const isHourly = reportWindow.resolution === "hour";
  const windowEnd = reportWindow.untilTimeMs
    ? new Date(reportWindow.untilTimeMs).toISOString()
    : undefined;
  /** Axis label of one bucket. */
  const axis = (label: string) =>
    isHourly
      ? formatHourShort(label, reportWindow.timeZone)
      : formatDayShort(label);
  /** Tooltip label of one bucket. */
  const full = (label: string) =>
    isHourly && windowEnd
      ? formatRelativeHourShort(label, windowEnd, reportWindow.timeZone)
      : axis(label);
  return { isHourly, axis, full };
}
