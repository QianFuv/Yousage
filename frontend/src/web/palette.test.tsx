/** A shared palette takes over the page's colors, as a desktop theme does. */
import { afterEach, describe, expect, it, vi } from "vitest";
import { applySharePalette, SHARED_THEME_ID } from "./palette";

/**
 * Install a minimal document root that records what the palette changes.
 *
 * @returns The fake root.
 */
function fakeRoot() {
  const properties = new Map<string, string>();
  const classes = new Set<string>();
  const root = {
    dataset: {} as Record<string, string>,
    classList: {
      toggle: (name: string, isOn: boolean) =>
        isOn ? classes.add(name) : classes.delete(name),
    },
    style: {
      colorScheme: "",
      setProperty: (name: string, value: string) => properties.set(name, value),
    },
    properties,
    classes,
  };
  vi.stubGlobal("document", { documentElement: root });
  return root;
}

afterEach(() => vi.unstubAllGlobals());

describe("share palette application", () => {
  it("marks the root as themed so role variables drive the interface colors", () => {
    const root = fakeRoot();
    applySharePalette({
      appearance: "light",
      colors: { "--app-theme-accent": "#cba6f7", "--x": "url(evil)" },
    });
    expect(root.dataset.themeId).toBe(SHARED_THEME_ID);
    expect(root.properties.get("--app-theme-accent")).toBe("#cba6f7");
    expect(root.properties.has("--x")).toBe(false);
    expect(root.classes.has("dark")).toBe(false);
    expect(root.style.colorScheme).toBe("light");
  });

  it("keeps the default theme when no safe color arrives", () => {
    const root = fakeRoot();
    applySharePalette({ appearance: "dark", colors: { "--x": "url(evil)" } });
    expect(root.dataset.themeId).toBeUndefined();
    expect(root.classes.has("dark")).toBe(true);
  });
});
