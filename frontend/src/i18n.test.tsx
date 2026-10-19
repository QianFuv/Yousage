/** Every source message has a matching Chinese translation and interpolation stays intact. */
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import {
  formatDayShort,
  formatResetDuration,
  formatResetsIn,
  resolveLanguage,
  setLanguage,
  t,
} from "./i18n";
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

describe("Claude limit failure messages", () => {
  const CLAUDE_FAILURES = [
    "Claude Code stored credentials are malformed.",
    "Claude Code stored access token has expired; usage limits could not be read.",
    "Claude usage API rejected the stored credential (HTTP 401).",
    "Claude usage API denied access to usage limits (HTTP 403).",
    "Claude usage API rate limit reached (HTTP 429). Try again later.",
    "Claude usage API is temporarily unavailable (HTTP 5xx). Try again later.",
    "Claude usage API returned an unexpected HTTP status.",
    "Claude usage API request timed out.",
    "Claude usage API request was cancelled.",
    "Claude usage API returned invalid JSON.",
    "Claude usage API could not be reached or read because of a network error.",
    "Claude Code rejected authentication for the usage probe.",
    "Claude Code usage probe timed out.",
    "Claude Code usage probe was cancelled.",
    "Claude Code returned an invalid usage probe response.",
    "Claude Code usage probe could not communicate with the CLI.",
    "Claude Code login could not be read.",
    "Claude returned invalid usage limits.",
  ];
  const RETENTION_NOTES = [
    "Last successful limits are retained.",
    "No previous successful limits are available.",
  ];
  const LEGACY_MESSAGES = [
    "Claude Code login has expired. Open Claude Code to renew it.",
    "Claude could not read usage limits.",
    "Claude could not initialize its usage probe.",
  ];

  /** Translate every message into Chinese. */
  function translateAll(messages: readonly string[]): string[] {
    setLanguage("zh-CN");
    return messages.map((message) => t(message));
  }

  it("translates every backend failure, retention note and legacy message", () => {
    const messages = [
      ...CLAUDE_FAILURES,
      ...RETENTION_NOTES,
      ...LEGACY_MESSAGES,
    ];
    const translations = translateAll(messages);
    const untranslated = messages.filter(
      (_message, index) => !/[一-鿿]/.test(translations[index]),
    );
    expect(untranslated).toEqual([]);
  });

  it("keeps each failure cause distinguishable in Chinese", () => {
    const messages = [...CLAUDE_FAILURES, ...LEGACY_MESSAGES];
    expect(new Set(translateAll(messages)).size).toBe(messages.length);
    const [retained, missing] = translateAll(RETENTION_NOTES);
    expect(retained).not.toBe(missing);
  });

  it("keeps HTTP status codes in usage API translations", () => {
    const withStatus = CLAUDE_FAILURES.filter((message) =>
      /HTTP \w+\)/.test(message),
    );
    const translations = translateAll(withStatus);
    expect(withStatus).toHaveLength(4);
    withStatus.forEach((message, index) =>
      expect(translations[index]).toContain(/HTTP \w+/.exec(message)?.[0]),
    );
  });

  it("does not advise renewing the login for non-login failures", () => {
    const legacyAdvice = translateAll(LEGACY_MESSAGES)[0];
    expect(legacyAdvice).toContain("续期");
    const advising = translateAll(CLAUDE_FAILURES).filter((translation) =>
      /续期|重新登录|打开 Claude Code/.test(translation),
    );
    expect(advising).toEqual([]);
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

  it("localizes a reset in its final minute", () => {
    const now = Date.parse("2026-10-06T12:00:00Z");
    const window = {
      id: "primary",
      kind: "session",
      label: "Session",
      usedPercent: 50,
      resetsAt: "2026-10-06T12:00:30Z",
    } as Parameters<typeof formatResetsIn>[0];
    expect(formatResetsIn(window, now)).toBe("resets momentarily");
    expect(formatResetDuration(window, now)).toBe("<1m");
    setLanguage("zh-CN");
    expect(formatResetsIn(window, now)).toBe("1 分钟内重置");
  });
});
