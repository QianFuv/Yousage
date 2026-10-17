/** Only preferences the desktop actually changed reach the viewer. */
import { describe, expect, it } from "vitest";
import { displayChanges } from "./displaySync";

describe("displayChanges", () => {
  it("reports changed preferences and leaves the rest to the viewer", () => {
    expect(
      displayChanges(
        { language: "system", density: "comfortable", windowDays: 30 },
        { language: "zh-CN", density: "compact", windowDays: 30 },
      ),
    ).toEqual({ language: "zh-CN", density: "compact" });
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
        { language: "en", density: "compact" },
      ),
    ).toEqual({ density: "compact" });
  });
});
