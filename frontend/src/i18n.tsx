/** Application localization: language selection, message lookup and locale-aware formatting. */
import { useSyncExternalStore } from "react";
import {
  formatDateTimeShort as formatDateTimeShortEnglish,
  formatDayShort as formatDayShortEnglish,
  formatHourShort as formatHourShortEnglish,
  formatRelativeHourShort as formatRelativeHourShortEnglish,
} from "@t3tools/shared/usageFormat";
import {
  formatDuration,
  formatResetsIn as formatResetsInEnglish,
} from "@t3tools/shared/usageLimits";
import { ZH_CN } from "./locales/zh-CN";

/** A concrete interface language. */
export type Language = "zh-CN" | "en";

/** The saved choice; `system` follows the operating system locale. */
export type LanguagePreference = "system" | Language;

let currentLanguage: Language = "en";
const listeners = new Set<() => void>();

/**
 * Resolve a saved preference to a concrete language.
 *
 * @param preference - Saved preference, possibly empty for older settings.
 * @param systemLocale - Operating system locale reported by the native host.
 * @returns The language to render.
 */
export function resolveLanguage(
  preference: string,
  systemLocale: string,
): Language {
  if (preference === "zh-CN" || preference === "en") return preference;
  return systemLocale.toLowerCase().startsWith("zh") ? "zh-CN" : "en";
}

/**
 * Switch the interface language and notify subscribed components.
 *
 * @param language - The language to render.
 */
export function setLanguage(language: Language) {
  if (typeof document !== "undefined") document.documentElement.lang = language;
  if (language === currentLanguage) return;
  currentLanguage = language;
  for (const listener of listeners) listener();
}

/**
 * Read the active interface language.
 *
 * @returns The active language.
 */
export function getLanguage(): Language {
  return currentLanguage;
}

/**
 * BCP 47 locale used for dates and numbers in the active language.
 *
 * @returns The formatting locale.
 */
export function formattingLocale(): string {
  return currentLanguage === "zh-CN" ? "zh-CN" : "en-US";
}

/**
 * Translate an English source message, substituting `{name}` placeholders.
 *
 * @param message - English source text, which is also the dictionary key.
 * @param values - Placeholder values.
 * @returns The localized text, or the English source when no translation exists.
 */
export function t(
  message: string,
  values?: Readonly<Record<string, string | number>>,
): string {
  const template =
    currentLanguage === "zh-CN" ? (ZH_CN[message] ?? message) : message;
  if (!values) return template;
  return template.replace(/\{(\w+)\}/g, (match: string, name: string) =>
    name in values ? String(values[name]) : match,
  );
}

/**
 * Mark an English source message that is translated later with {@link t}.
 *
 * @param message - English source text.
 * @returns The same text, so constant tables stay readable.
 */
export function msg(message: string): string {
  return message;
}

/** Subscribe a listener to language changes. */
function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

/**
 * Re-render the calling component when the language changes.
 *
 * @returns The active language and the translation function.
 */
export function useI18n() {
  const language = useSyncExternalStore(subscribe, getLanguage, getLanguage);
  return { language, t };
}

const formatters = new Map<string, Intl.DateTimeFormat>();

/** Reuse one formatter per locale and option set. */
function dateFormatter(options: Intl.DateTimeFormatOptions) {
  const key = formattingLocale() + JSON.stringify(options);
  let formatter = formatters.get(key);
  if (!formatter) {
    formatter = new Intl.DateTimeFormat(formattingLocale(), options);
    formatters.set(key, formatter);
  }
  return formatter;
}

/**
 * `2026-08-07` as `Aug 7` or `8月7日`.
 *
 * @param day - Calendar day in `YYYY-MM-DD` form.
 * @returns The short localized day.
 */
export function formatDayShort(day: string): string {
  if (currentLanguage === "en") return formatDayShortEnglish(day);
  const [year, month, dayOfMonth] = day.split("-").map(Number);
  if (!year || !month || !dayOfMonth) return day;
  return `${month}月${dayOfMonth}日`;
}

/**
 * An hour start as `2 PM` or `14时`.
 *
 * @param hourStart - ISO instant at the start of the hour.
 * @param timeZone - Optional IANA zone.
 * @returns The short localized hour.
 */
export function formatHourShort(hourStart: string, timeZone?: string): string {
  if (currentLanguage === "en")
    return formatHourShortEnglish(hourStart, timeZone);
  const instant = new Date(hourStart);
  if (Number.isNaN(instant.getTime())) return hourStart;
  return dateFormatter({ timeZone, hour: "numeric", hourCycle: "h23" }).format(
    instant,
  );
}

/**
 * An instant as `Aug 11, 2 PM` or `8月11日 14时`.
 *
 * @param instant - ISO instant.
 * @param timeZone - Optional IANA zone.
 * @returns The short localized date and hour.
 */
export function formatDateTimeShort(
  instant: string,
  timeZone?: string,
): string {
  if (currentLanguage === "en")
    return formatDateTimeShortEnglish(instant, timeZone);
  const date = new Date(instant);
  if (Number.isNaN(date.getTime())) return instant;
  return dateFormatter({
    timeZone,
    month: "numeric",
    day: "numeric",
    hour: "numeric",
    hourCycle: "h23",
  }).format(date);
}

/**
 * An hourly label relative to the window end: `2 PM today` or `今天 14时`.
 *
 * @param hourStart - ISO instant at the start of the hour.
 * @param relativeTo - ISO instant at the end of the window.
 * @param timeZone - Optional IANA zone.
 * @returns The relative localized label.
 */
export function formatRelativeHourShort(
  hourStart: string,
  relativeTo: string,
  timeZone?: string,
): string {
  const english = formatRelativeHourShortEnglish(
    hourStart,
    relativeTo,
    timeZone,
  );
  if (currentLanguage === "en") return english;
  const hour = formatHourShort(hourStart, timeZone);
  if (english.endsWith(" today")) return t("{hour} today", { hour });
  if (english.endsWith(" yesterday")) return t("{hour} yesterday", { hour });
  return formatDateTimeShort(hourStart, timeZone);
}

/**
 * Time until a limit window resets, without the `resets in` prefix.
 *
 * @param window - Usage window with a reset instant.
 * @param now - Current time in milliseconds.
 * @returns `2h 13m`, the localized word for now, or null without a reset.
 */
export function formatResetDuration(
  window: Parameters<typeof formatResetsInEnglish>[0],
  now: number,
): string | null {
  const english = formatResetsInEnglish(window, now);
  if (english === null) return null;
  return english === "resets now"
    ? t("now")
    : english.replace(/^resets in /, "");
}

/**
 * `resets in 2h 13m` in the active language, or null without a reset.
 *
 * @param window - Usage window with a reset instant.
 * @param now - Current time in milliseconds.
 * @returns The localized reset phrase.
 */
export function formatResetsIn(
  window: Parameters<typeof formatResetsInEnglish>[0],
  now: number,
): string | null {
  const english = formatResetsInEnglish(window, now);
  if (english === null) return null;
  return english === "resets now"
    ? t("resets now")
    : t("resets in {duration}", {
        duration: english.replace(/^resets in /, ""),
      });
}

export { formatDuration };
