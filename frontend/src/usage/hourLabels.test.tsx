/** Hourly labels follow the 12/24-hour preference in both interface languages. */
import { afterEach, describe, expect, it } from "vitest";
import {
  formatDateTimeShort,
  formatHourShort,
  formatRelativeHourShort,
  setLanguage,
} from "../i18n";
import { hour12ForTimestampFormat } from "./host";

const AFTERNOON = "2026-10-01T14:00:00Z";
const WINDOW_END = "2026-10-01T20:00:00Z";

afterEach(() => setLanguage("en"));

describe("hour clock preference", () => {
  it("maps the stored preference and leaves locale to the language default", () => {
    expect(hour12ForTimestampFormat("12h")).toBe(true);
    expect(hour12ForTimestampFormat("24h")).toBe(false);
    expect(hour12ForTimestampFormat("locale")).toBeUndefined();
  });
});

describe("English hourly labels", () => {
  it("keep the 12-hour clock by default and switch to 24-hour on request", () => {
    expect(formatHourShort(AFTERNOON, "UTC")).toBe("2 PM");
    expect(formatHourShort(AFTERNOON, "UTC", false)).toBe("14:00");
    expect(formatRelativeHourShort(AFTERNOON, WINDOW_END, "UTC", false)).toBe(
      "14:00 today",
    );
    expect(formatDateTimeShort(AFTERNOON, "UTC", false)).toContain("14:00");
  });
});

describe("Chinese hourly labels", () => {
  it("keep the 24-hour clock by default and switch to 12-hour on request", () => {
    setLanguage("zh-CN");
    expect(formatHourShort(AFTERNOON, "UTC")).toBe("14时");
    expect(formatHourShort(AFTERNOON, "UTC", true)).toContain("下午");
    expect(
      formatRelativeHourShort(AFTERNOON, WINDOW_END, "UTC", true),
    ).toContain("下午");
    expect(formatDateTimeShort(AFTERNOON, "UTC", true)).toContain("下午");
  });
});
