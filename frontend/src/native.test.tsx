/** Test native contract decisions without falsely substituting browser results for native interaction. */
import { expect, it } from "vitest";
import { droppedNames, effectiveAppearance } from "./native";

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
