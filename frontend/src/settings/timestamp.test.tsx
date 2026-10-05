/** Reset labels preserve the upstream local calendar day and invalid-input rules. */
import { describe, expect, it } from "vitest";
import { formatUpcomingTimestamp } from "../../vendor/timestampFormat";

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
