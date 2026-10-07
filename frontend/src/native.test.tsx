/** Test native contract decisions without falsely substituting browser results for native interaction. */
import { expect, it } from "vitest";
import { droppedNames, effectiveAppearance, opaqueChannels } from "./native";

it("keeps explicit appearance stable and follows System", () => {
  expect(effectiveAppearance("light", true)).toBe("light");
  expect(effectiveAppearance("dark", false)).toBe("dark");
  expect(effectiveAppearance("system", true)).toBe("dark");
  expect(effectiveAppearance("system", false)).toBe("light");
});

it("shows dropped names without exposing absolute home paths", () => {
  expect(
    droppedNames(["C:\\fixture\\theme.json", "/fixture/theme-two.json"]),
  ).toEqual(["theme.json", "theme-two.json"]);
});

it("converts computed colors of any CSS syntax to opaque native channels", () => {
  expect(opaqueChannels("rgb(30, 30, 46)")).toEqual([30, 30, 46]);
  expect(opaqueChannels("rgba(10, 20, 30, 0.5)")).toEqual([10, 20, 30]);
  expect(opaqueChannels("oklch(1 0 0)")).toEqual([255, 255, 255]);
  expect(opaqueChannels("color(srgb 1 0 0)")).toEqual([255, 0, 0]);
  for (const channel of opaqueChannels("oklch(0.7 0.4 30)")) {
    expect(channel).toBeGreaterThanOrEqual(0);
    expect(channel).toBeLessThanOrEqual(255);
  }
});
