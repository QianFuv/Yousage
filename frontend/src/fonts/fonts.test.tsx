/** The bundled font subset covers every character the interface ships, and stays small. */
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative, sep } from "node:path";
import { describe, expect, it } from "vitest";
import { FONT_STACK } from "../theme/appearance";

const FRONTEND = join(__dirname, "..", "..");
const FONTS = __dirname;
const REGENERATE =
  "uv run --no-project --with fonttools --with brotli python build/subset-font.py";

/**
 * Whether a file holds shipped interface text, matching build/subset-font.py.
 *
 * @param path - Absolute file path under the frontend.
 * @returns True for shipped sources, styles and pages.
 */
function isInterfaceSource(path: string): boolean {
  const name = path.split(sep).at(-1)!;
  return (
    /\.(tsx|ts|css|html)$/.test(name) &&
    !name.includes(".test.") &&
    name !== "review.tsx" &&
    !relative(FRONTEND, path).split(sep).slice(0, -1).includes("fonts")
  );
}

/**
 * List files below a directory.
 *
 * @param directory - Directory to walk.
 * @returns Absolute file paths.
 */
function files(directory: string): string[] {
  return readdirSync(directory).flatMap((name) => {
    const path = join(directory, name);
    return statSync(path).isDirectory() ? files(path) : [path];
  });
}

describe("bundled font subset", () => {
  it("covers or knowingly falls back for every non-ASCII interface character", () => {
    const charset = new Set([
      ...readFileSync(join(FONTS, "charset.txt"), "utf8"),
      ...readFileSync(join(FONTS, "fallback.txt"), "utf8"),
    ]);
    const sources = [
      ...readdirSync(FRONTEND)
        .filter((name) => name.endsWith(".html"))
        .map((name) => join(FRONTEND, name)),
      ...files(join(FRONTEND, "src")),
      ...files(join(FRONTEND, "vendor")),
    ].filter(isInterfaceSource);
    const missing = new Set<string>();
    for (const path of sources)
      for (const character of readFileSync(path, "utf8"))
        if (
          character.codePointAt(0)! > 0x7e &&
          !/\s/u.test(character) &&
          !charset.has(character)
        )
          missing.add(character);
    expect([...missing].join(""), `Regenerate the subset: ${REGENERATE}`).toBe(
      "",
    );
  });

  it("ships small subsets rather than the full font", () => {
    for (const weight of ["Regular", "Bold"]) {
      const size = statSync(
        join(FONTS, `JetBrainsLxgwNerdMono-${weight}.woff2`),
      ).size;
      expect(size).toBeGreaterThan(10_000);
      expect(size).toBeLessThan(1_000_000);
    }
  });

  it("uses the bundled family first for interface and monospace text", () => {
    const css = readFileSync(join(FONTS, "fonts.css"), "utf8");
    expect(css).toContain("JetBrainsLxgwNerdMono-Regular.woff2");
    expect(css).toContain("JetBrainsLxgwNerdMono-Bold.woff2");
    expect(FONT_STACK.startsWith('"JetBrainsLxgwNerdMono"')).toBe(true);
  });
});
