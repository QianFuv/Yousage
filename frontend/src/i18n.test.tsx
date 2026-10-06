/** Every source message has a matching Chinese translation and interpolation stays intact. */
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import { formatDayShort, resolveLanguage, setLanguage, t } from "./i18n";
import { ZH_CN } from "./locales/zh-CN";

const FRONTEND = join(__dirname, "..");
const CALL =
  /(?<![\w.$])(?:t|msg)\(\s*(?:"((?:[^"\\]|\\.)*)"|'((?:[^'\\]|\\.)*)')/g;

/** Collect source files that render user-visible text. */
function sourceFiles(directory: string): string[] {
  return readdirSync(directory).flatMap((name) => {
    const path = join(directory, name);
    if (statSync(path).isDirectory())
      return name === "locales" ? [] : sourceFiles(path);
    return /\.tsx?$/.test(name) &&
      !name.includes(".test.") &&
      name !== "review.tsx"
      ? [path]
      : [];
  });
}

/** Extract literal message keys passed to t() or msg(). */
function messageKeys(): Map<string, string> {
  const keys = new Map<string, string>();
  for (const file of [
    ...sourceFiles(join(FRONTEND, "src")),
    ...sourceFiles(join(FRONTEND, "vendor")),
  ]) {
    for (const match of readFileSync(file, "utf8").matchAll(CALL))
      keys.set(
        match[1] === undefined
          ? match[2].replaceAll("\\'", "'")
          : (JSON.parse(`"${match[1]}"`) as string),
        file,
      );
  }
  return keys;
}

/** Placeholder names in a message template, in sorted order. */
function placeholders(message: string): string[] {
  return [...message.matchAll(/\{(\w+)\}/g)].map((match) => match[1]).sort();
}

afterEach(() => setLanguage("en"));

describe("translations", () => {
  const keys = messageKeys();

  it("finds the application messages", () => {
    expect(keys.size).toBeGreaterThan(300);
  });

  it("translates every message key into Chinese", () => {
    const missing = [...keys.keys()].filter((key) => !ZH_CN[key]);
    expect(missing).toEqual([]);
  });

  it("keeps the same placeholders in every translation", () => {
    const mismatched = Object.entries(ZH_CN).filter(
      ([key, value]) => placeholders(key).join() !== placeholders(value).join(),
    );
    expect(mismatched).toEqual([]);
  });
});

describe("language selection", () => {
  it("follows the system locale unless a language is chosen", () => {
    expect(resolveLanguage("system", "zh-CN")).toBe("zh-CN");
    expect(resolveLanguage("system", "zh-Hans-CN")).toBe("zh-CN");
    expect(resolveLanguage("", "en-US")).toBe("en");
    expect(resolveLanguage("en", "zh-CN")).toBe("en");
    expect(resolveLanguage("zh-CN", "en-US")).toBe("zh-CN");
  });

  it("interpolates values and falls back to English source text", () => {
    expect(t("{percent}% left", { percent: 40 })).toBe("40% left");
    setLanguage("zh-CN");
    expect(t("{percent}% left", { percent: 40 })).toBe("剩余 40%");
    expect(t("Untranslated provider message")).toBe(
      "Untranslated provider message",
    );
  });

  it("formats chart days in the active language", () => {
    expect(formatDayShort("2026-10-06")).toBe("Oct 6");
    setLanguage("zh-CN");
    expect(formatDayShort("2026-10-06")).toBe("10月6日");
  });
});
