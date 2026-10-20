/** Localize composed quota failure notices without rewriting the labels they name. */
import type { LimitPresentations } from "@t3tools/shared/usageLimits";
import { msg, t } from "../i18n";
import type { EnvironmentUsageStatus } from "./data";

/** Fixed quota failure sentences that the backend reports and the catalog translates. */
const QUOTA_FAILURES: readonly string[] = [
  msg("Claude Code stored credentials are malformed."),
  msg(
    "Claude Code stored access token has expired; usage limits could not be read.",
  ),
  msg("Claude usage API rejected the stored credential (HTTP 401)."),
  msg("Claude usage API denied access to usage limits (HTTP 403)."),
  msg("Claude usage API rate limit reached (HTTP 429). Try again later."),
  msg(
    "Claude usage API is temporarily unavailable (HTTP 5xx). Try again later.",
  ),
  msg("Claude usage API returned an unexpected HTTP status."),
  msg("Claude usage API request timed out."),
  msg("Claude usage API request was cancelled."),
  msg("Claude usage API returned invalid JSON."),
  msg(
    "Claude usage API could not be reached or read because of a network error.",
  ),
  msg("Claude Code rejected authentication for the usage probe."),
  msg("Claude Code usage probe timed out."),
  msg("Claude Code usage probe was cancelled."),
  msg("Claude Code returned an invalid usage probe response."),
  msg("Claude Code usage probe could not communicate with the CLI."),
  msg("Claude Code login could not be read."),
  msg("Claude returned invalid usage limits."),
  msg("Claude Code login has expired. Open Claude Code to renew it."),
  msg("Claude could not read usage limits."),
  msg("Claude could not initialize its usage probe."),
  msg("Codex could not be started to read usage."),
  msg("Codex returned invalid usage limits."),
];

/** Notes the backend appends to a failed quota read to say whether earlier limits remain shown. */
const RETENTION_NOTES: readonly string[] = [
  msg("Last successful limits are retained."),
  msg("No previous successful limits are available."),
];

/** One parsed `<label>: <body>` notice and the index just past it. */
type NoticeSegment = { readonly text: string; readonly end: number };

/**
 * Check that a segment ends at the end of the text or before the space that separates notices.
 *
 * @param text - The composed notice text.
 * @param index - Index just past the candidate segment.
 * @returns Whether the candidate segment is complete.
 */
function isSegmentEnd(text: string, index: number): boolean {
  return index === text.length || text[index] === " ";
}

/**
 * Read a notice body: a fixed failure, optionally followed by a retention note, or an
 * unrecognized failure that is followed by a retention note.
 *
 * @param text - The composed notice text.
 * @param start - Index where the body begins.
 * @returns The localized body and the index just past it, or null when it is not recognized.
 */
function readBody(text: string, start: number): NoticeSegment | null {
  let best: NoticeSegment | null = null;
  for (const failure of QUOTA_FAILURES) {
    if (!text.startsWith(failure, start)) continue;
    const afterFailure = start + failure.length;
    for (const note of RETENTION_NOTES) {
      const end = afterFailure + 1 + note.length;
      if (
        text.startsWith(` ${note}`, afterFailure) &&
        isSegmentEnd(text, end) &&
        (!best || end > best.end)
      )
        best = { text: `${t(failure)} ${t(note)}`, end };
    }
    if (isSegmentEnd(text, afterFailure) && (!best || afterFailure > best.end))
      best = { text: t(failure), end: afterFailure };
  }
  if (best) return best;
  let unknown: NoticeSegment | null = null;
  for (const note of RETENTION_NOTES) {
    const index = text.indexOf(` ${note}`, start);
    const end = index + 1 + note.length;
    if (
      index > start &&
      isSegmentEnd(text, end) &&
      (!unknown || end < unknown.end)
    )
      unknown = { text: `${text.slice(start, index)} ${t(note)}`, end };
  }
  return unknown;
}

/**
 * Localize the fixed failure sentences and retention notes in composed quota notices.
 *
 * Each notice reads `<label>: <failure>[ <note>]`, and several notices are joined by
 * single spaces. Only labels supplied by the caller are accepted as prefixes, so a
 * label that contains `: ` or a known sentence is kept exactly. Unrecognized text,
 * and everything after it, is returned unchanged; English output equals the input.
 *
 * @param text - Composed notice text.
 * @param labels - Labels the caller knows can prefix a notice.
 * @returns The text with recognized sentences translated into the active language.
 */
export function localizeQuotaNotice(
  text: string,
  labels: readonly string[],
): string {
  const prefixes = [...new Set(labels)]
    .filter((label) => label.length > 0)
    .sort((first, second) => second.length - first.length);
  let output = "";
  let position = 0;
  for (;;) {
    let segment: NoticeSegment | null = null;
    for (const label of prefixes) {
      const head = `${label}: `;
      if (!text.startsWith(head, position)) continue;
      const body = readBody(text, position + head.length);
      if (body) {
        segment = { text: head + body.text, end: body.end };
        break;
      }
    }
    if (!segment) return output + text.slice(position);
    output += segment.text;
    position = segment.end;
    if (position === text.length) return output;
    output += " ";
    position += 1;
  }
}

/**
 * Localize an environment error composed from per-provider quota notices.
 *
 * @param environment - The environment whose error is shown.
 * @returns The localized error, prefixed by provider labels exactly as reported.
 */
export function localizeEnvironmentError(
  environment: Pick<EnvironmentUsageStatus, "error" | "serverConfig">,
): string {
  return localizeQuotaNotice(
    environment.error ?? "",
    environment.serverConfig?.providers.map(
      (provider) => provider.displayName ?? "",
    ) ?? [],
  );
}

/**
 * Provider prefixes that `collectLimitNotices` places before limit notices.
 *
 * @param presentations - The environments whose limits are shown.
 * @returns Every provider prefix, naming the environment when more than one is shown.
 */
export function limitNoticeLabels(
  presentations: LimitPresentations,
): readonly string[] {
  return [...presentations.values()].flatMap((presentation) =>
    (presentation.serverConfig?.providers ?? []).map((provider) => {
      const name = provider.displayName?.trim() || String(provider.driver);
      return presentations.size > 1
        ? `${presentation.entry.target.label} · ${name}`
        : name;
    }),
  );
}
