/** Differential import/color tests keep the immutable upstream implementation as the oracle. */
import { expect, it, vi } from "vitest";
import { readFileSync, writeFileSync, mkdirSync } from "node:fs";
import { BUILT_IN_THEMES } from "../../../tools/parity/upstream/packages/shared/src/themePalettes";
import { toCanonicalThemeColor as originalCanonical } from "../../../tools/parity/upstream/apps/web/src/themePalette";
import {
  themeColorToHex,
  toCanonicalThemeColor as desktopCanonical,
} from "../../vendor/themePalette";
import { parseVsCodeThemeFile } from "../../vendor/vscodeThemeImport";
vi.mock("@wailsio/runtime", async () => ({
  ...(await vi.importActual<typeof import("@wailsio/runtime")>(
    "@wailsio/runtime",
  )),
  Events: { On: () => () => {} },
}));
vi.mock("../../bindings/yousage/internal/desktop/app", () => ({}));
import {
  collectionOwners,
  survivingCollectionVariant,
  mergedImport,
  parseThemeBatch,
} from "./ThemePanel";
import { fontFamilyList, frontendTheme, nativeLibrary } from "./appearance";
import {
  LibraryValue,
  Definition,
} from "../../bindings/yousage/internal/theme/models";

const CSS_INPUTS = [
  "rebeccapurple",
  "gold",
  "transparent",
  "#abcd",
  "rgb(12 34 56 / 40%)",
  "rgba(20,40,60,.8)",
  "hsl(230 50% 40% / .5)",
  "hwb(200 10% 20%)",
  "lab(60% 30 -20 / .7)",
  "lch(50% 40 270)",
  "oklab(0.5 0.1 -0.1)",
  "oklch(none none none / none)",
  "rgb(none 20 none / none)",
  "color(srgb .1 .2 .3 / .4)",
  "color(display-p3 1 .2 .3 / .5)",
  "color(rec2020 .1 .5 .8)",
  "oklch(6e-1 2e-1 -40)",
  "not-a-color",
  "currentColor",
  "var(--accent)",
  "color-mix(in srgb,red,blue)",
];

it("keeps surviving collection variants and preserves unrelated half ownership", () => {
  const light = {
    ...BUILT_IN_THEMES[0],
    id: "collection-light",
    appearance: "light" as const,
    variants: undefined,
  };
  const dark = {
    ...BUILT_IN_THEMES[0],
    id: "collection-dark",
    appearance: "dark" as const,
    variants: undefined,
  };
  expect(survivingCollectionVariant([light], dark.id)).toBe(light);
  expect(collectionOwners([light, dark])).toEqual({
    lightOwner: light.id,
    darkOwner: dark.id,
  });
  expect(collectionOwners([dark])).toEqual({ darkOwner: dark.id });
  expect(collectionOwners([BUILT_IN_THEMES[0]])).toBeNull();
  const current = {
    mode: "system",
    baseThemeId: "grove",
    lightOwner: "unresolved-published",
    darkOwner: "old",
  };
  expect({ ...current, ...collectionOwners([dark]) }).toEqual({
    ...current,
    darkOwner: dark.id,
  });
});

it("accepts the actual Wails nullable optional collection shape after a save", () => {
  const original = BUILT_IN_THEMES[0];
  const native = {
    id: "fixture-native",
    label: "Native fixture",
    appearance: original.appearance,
    colors: original.colors,
    variants: original.variants,
    collection: null,
    managed: false,
    sidebarArtwork: false,
  };
  const decoded = frontendTheme(native);
  expect(decoded.id).toBe(native.id);
  expect(decoded.collection).toBeUndefined();
  expect(decoded.colors.canvas).toBe(originalCanonical(original.colors.canvas));
});

it("does not let generated constructors mutate cached frontend definitions", () => {
  const theme = {
    ...BUILT_IN_THEMES[0],
    id: "fixture-wire",
    collection: undefined,
  };
  const encoded = nativeLibrary({
    ...new LibraryValue(),
    customThemes: [theme],
  });
  expect(theme.collection).toBeUndefined();
  expect(encoded.customThemes[0].collection).toBeUndefined();
  expect(
    frontendTheme(
      Definition.createFrom(JSON.stringify(encoded.customThemes[0])),
    ).id,
  ).toBe(theme.id);
});

it("matches full literal CSS canonicalization and the independent Go fixture", () => {
  const vectors = CSS_INPUTS.map((input) => ({
    input,
    expected: originalCanonical(input),
  }));
  for (const vector of vectors)
    expect(desktopCanonical(vector.input)).toBe(vector.expected);
  const file = new URL(
    "../../../internal/theme/testdata/css-colors.json",
    import.meta.url,
  );
  if (process.env.UPDATE_GO_CSS === "1") {
    mkdirSync(new URL("../../../internal/theme/testdata/", import.meta.url), {
      recursive: true,
    });
    writeFileSync(file, JSON.stringify(vectors, null, 2) + "\n");
  }
  expect(JSON.parse(readFileSync(file, "utf8"))).toEqual(vectors);
});
it("parses JSONC as a complete bounded batch before mutation", () => {
  const value = {
    version: 1,
    id: "fixture-import",
    name: "Fixture",
    appearance: "light",
    colors: { canvas: "rebeccapurple", accent: "gold" },
  };
  const text = JSON.stringify(value).replace(
    '"version":1,',
    '"version":1, // comment\n',
  );
  const themes = parseThemeBatch([
    { name: "fixture.jsonc", text, size: text.length },
  ]);
  expect(themes).toHaveLength(1);
  expect(themes[0].colors.canvas).toBe(originalCanonical("rebeccapurple"));
  expect(() =>
    parseThemeBatch([
      { name: "first.json", text: JSON.stringify(value), size: 100 },
      { name: "bad.json", text: "{", size: 1 },
    ]),
  ).toThrow();
  expect(() =>
    parseThemeBatch([{ name: "large.json", text: "{}", size: 256 * 1024 + 1 }]),
  ).toThrow("256 KiB");
});
it("preserves update metadata, distinguishes copies and refuses foreign collection conflicts", () => {
  const original = {
    ...BUILT_IN_THEMES[0],
    id: "fixture-import",
    collection: { id: "fixture:collection", label: "Fixture collection" },
  };
  const imported = { ...original, label: "Updated", collection: undefined };
  expect(mergedImport([original], [imported], true)[0].collection).toEqual(
    original.collection,
  );
  const copies = mergedImport([original], [original], false);
  expect(copies[1].id).not.toBe(original.id);
  expect(copies[1].label).not.toBe(original.label);
  expect(copies[0]).toEqual(original);
  const foreign = { ...original, collection: undefined };
  expect(() =>
    mergedImport([foreign], [original], true, original.collection.id),
  ).toThrow("outside");
  expect(foreign.collection).toBeUndefined();
});
it("normalizes comma-separated font fallbacks without losing quoted families", () => {
  expect(fontFamilyList('Segoe UI, Inter, "Noto Sans"')).toBe(
    '"Segoe UI", Inter, "Noto Sans"',
  );
  expect(fontFamilyList(" , ")).toBe("");
});
it("imports current T3 Code theme files that carry search match colors", () => {
  const text = JSON.stringify({
    version: 1,
    id: "current-export",
    name: "Current export",
    appearance: "dark",
    colors: {
      canvas: "#101010",
      accent: "#ff8800",
      searchMatchBackground: "#8c6c1f",
      searchMatchForeground: "#f5f5f5",
      searchMatchActiveBackground: "#eea52b",
      searchMatchActiveForeground: "#21180a",
    },
  });
  const [theme] = parseThemeBatch([
    { name: "current.json", text, size: text.length },
  ]);
  expect(theme.colors.searchMatchActiveBackground).toBe(
    desktopCanonical("#eea52b"),
  );
});
it("maps VS Code find highlights onto the search match roles", () => {
  const text = JSON.stringify({
    name: "Fixture Dark",
    type: "dark",
    colors: {
      "editor.background": "#1e1e1e",
      "editor.foreground": "#d4d4d4",
      "editor.findMatchBackground": "#515c6a",
      "editor.findMatchHighlightBackground": "#3a3d41",
    },
  });
  const [theme] = parseThemeBatch([
    { name: "vscode.json", text, size: text.length },
  ]);
  expect(theme.colors.searchMatchActiveBackground).toBe(
    desktopCanonical("#515c6a"),
  );
  expect(theme.colors.searchMatchBackground).toBe(desktopCanonical("#3a3d41"));
});

/**
 * Measure the WCAG contrast ratio between two CSS colors.
 *
 * @param first - The first CSS color.
 * @param second - The second CSS color.
 * @returns The contrast ratio, from 1 to 21.
 */
function contrastRatio(first: string, second: string): number {
  const luminance = (value: string) => {
    const hex = themeColorToHex(value)!;
    const [red, green, blue] = [1, 3, 5].map((offset) => {
      const channel = Number.parseInt(hex.slice(offset, offset + 2), 16) / 255;
      return channel <= 0.04045
        ? channel / 12.92
        : ((channel + 0.055) / 1.055) ** 2.4;
    });
    return 0.2126 * red + 0.7152 * green + 0.0722 * blue;
  };
  const [darker, lighter] = [luminance(first), luminance(second)].sort(
    (left, right) => left - right,
  );
  return (lighter + 0.05) / (darker + 0.05);
}

/**
 * Read the chroma and hue of a canonical OKLCH color.
 *
 * @param value - A color in the runtime's canonical `oklch(L C h)` form.
 * @returns The chroma and hue components.
 */
function oklchParts(value: string): { chroma: number; hue: number } {
  const [, chroma, hue] = /^oklch\(\S+ (\S+) (\S+)\)$/.exec(
    desktopCanonical(value)!,
  )!;
  return { chroma: Number(chroma), hue: Number(hue) };
}

it.each([
  ["dark", "#282828"],
  ["light", "#ffffff"],
] as const)(
  "keeps a dim VS Code status color's hue on a %s canvas instead of replacing it",
  (type, canvas) => {
    const theme = parseVsCodeThemeFile({
      name: "Gruvbox",
      type,
      colors: {
        "editor.background": canvas,
        "editorError.foreground": "#fb4934",
        "editorWarning.foreground": "#b57614",
      },
    });
    for (const [role, source] of [
      ["error", "#fb4934"],
      ["warning", "#b57614"],
    ] as const) {
      const imported = oklchParts(theme.colors[role]);
      expect(
        contrastRatio(theme.colors[role], theme.colors.canvas),
      ).toBeGreaterThanOrEqual(4.5);
      expect(imported.chroma).toBeGreaterThan(0.1);
      expect(Math.abs(imported.hue - oklchParts(source).hue)).toBeLessThan(2);
    }
  },
);
it("keeps the derived status color when a VS Code theme hides its own", () => {
  const theme = parseVsCodeThemeFile({
    name: "Hidden squiggles",
    type: "dark",
    colors: {
      "editor.background": "#101010",
      "editorError.foreground": "#ff000000",
    },
  });
  expect(oklchParts(theme.colors.error).chroma).toBeGreaterThan(0.1);
});
