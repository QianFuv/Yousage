/** Only preferences the desktop actually changed reach the viewer. */
import { describe, expect, it } from "vitest";
import { displayChanges } from "./displaySync";

describe("displayChanges", () => {
  it("reports changed preferences and leaves the rest to the viewer", () => {
    expect(
      displayChanges(
        { language: "system", timestampFormat: "locale", windowDays: 30 },
        { language: "zh-CN", timestampFormat: "24h", windowDays: 30 },
      ),
    ).toEqual({ language: "zh-CN", timestampFormat: "24h" });
  });

  it("reports nothing when only the snapshot changed", () => {
    expect(
      displayChanges(
        { language: "en", windowDays: 7 },
        { language: "en", windowDays: 7 },
      ),
    ).toEqual({});
  });

  it("includes preferences added by a newer desktop", () => {
    expect(
      displayChanges(
        { language: "en" },
        { language: "en", timestampFormat: "24h" },
      ),
    ).toEqual({ timestampFormat: "24h" });
  });
});
