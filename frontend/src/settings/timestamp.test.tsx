/** Regional time choices must use the native locale and replace cached formatters. */
import { afterEach, describe, expect, it } from "vitest";
import {
  configureTimestampLocale,
  formatDayAwareTimestamp,
  formatShortTimestamp,
  formatUpcomingTimestamp,
  weekStartsOn,
} from "../../vendor/timestampFormat";

afterEach(() => configureTimestampLocale(undefined));

describe("reset timestamp presentation", () => {
  it("uses tomorrow's calendar date rather than a rolling duration", () => {
    const now = new Date(2026, 9, 6, 23, 55);
    const reset = new Date(2026, 9, 7, 0, 5);
    expect(
      formatUpcomingTimestamp(reset.toISOString(), "24-hour", now.getTime()),
    ).toMatch(/^tomorrow at /);
  });
  it("keeps yesterday distinct from today's time", () => {
    const now = new Date(2026, 9, 6, 0, 5);
    const reset = new Date(2026, 9, 5, 23, 55);
    expect(
      formatUpcomingTimestamp(reset.toISOString(), "12-hour", now.getTime()),
    ).toMatch(/^yesterday at /);
  });
  it("does not throw or invent a reset time for an invalid instant", () => {
    expect(formatUpcomingTimestamp("unavailable", "locale", Date.now())).toBe(
      "",
    );
  });
});

it("changes locale time, date order and calendar week without stale caches", () => {
  const instant = new Date(2026, 9, 6, 13, 4).toISOString();
  configureTimestampLocale("en-US");
  expect(formatShortTimestamp(instant, "locale")).toBe(
    new Intl.DateTimeFormat("en-US", {
      hour: "numeric",
      minute: "2-digit",
    }).format(new Date(instant)),
  );
  configureTimestampLocale("de-DE");
  expect(formatShortTimestamp(instant, "locale")).toBe(
    new Intl.DateTimeFormat("de-DE", {
      hour: "numeric",
      minute: "2-digit",
    }).format(new Date(instant)),
  );
  expect(
    formatDayAwareTimestamp(instant, "24-hour", new Date(2026, 9, 9).getTime()),
  ).toBe("6.10. 13:04");
  expect(weekStartsOn).toBe(1);
  configureTimestampLocale("not a locale");
  expect(() => formatShortTimestamp(instant, "locale")).not.toThrow();
});
