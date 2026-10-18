/** Draft rules for history sources and provider capabilities shown in Sources & connections. */
import { msg } from "../i18n";

/** One saved history source row as the settings draft edits it. */
export type SourceRow = {
  id: string;
  provider: string;
  root: string;
  isEnabled: boolean;
  isAutomatic: boolean;
};

/** Detection facts that decide which settings a provider offers. */
export type DetectionFacts = {
  driver: string;
  provider: string;
  hasHistory: boolean;
  supportsHistory?: boolean;
  supportsQuota?: boolean;
};

/** Display names keyed by history provider and by native quota driver. */
const PROVIDER_LABELS: Readonly<Record<string, string>> = {
  codex: "Codex",
  claude: "Claude Code",
  claudeAgent: "Claude Code",
  pi: "Pi",
};

/** Providers whose history may live in several user-chosen session folders. */
const MULTI_FOLDER_PROVIDERS: ReadonlySet<string> = new Set(["pi"]);

/**
 * Display name of a history provider or quota driver.
 *
 * @param kind - Provider or driver identity.
 * @returns The display name, or the identity itself when unknown.
 */
export function providerLabel(kind: string): string {
  return PROVIDER_LABELS[kind] ?? kind;
}

/**
 * Whether a provider accepts additional custom session folders.
 *
 * @param provider - History provider identity.
 * @returns Whether rows can be added and removed for it.
 */
export function canAddSessionFolder(provider: string): boolean {
  return MULTI_FOLDER_PROVIDERS.has(provider);
}

/**
 * Providers that currently offer an "add session folder" action.
 *
 * Detection facts keep a provider addable after its last custom row is removed,
 * including configurations that never had an automatic row for it.
 *
 * @param sources - Current draft rows.
 * @param detections - Latest provider detections.
 * @returns Distinct addable providers, draft rows first, then detected providers.
 */
export function sessionFolderProviders<Row extends SourceRow>(
  sources: readonly Row[],
  detections: readonly DetectionFacts[],
): Row["provider"][] {
  return [
    ...new Set([
      ...sources.map((source) => source.provider),
      ...detections
        .filter((detection) => detection.supportsHistory !== false)
        .map((detection) => detection.provider as Row["provider"]),
    ]),
  ].filter((provider) => canAddSessionFolder(provider));
}

/**
 * Whether a row is a removable custom session folder.
 *
 * @param source - History source row.
 * @returns Whether the row can be removed from the draft.
 */
export function isRemovableSessionFolder(source: SourceRow): boolean {
  return !source.isAutomatic && canAddSessionFolder(source.provider);
}

/**
 * Append an enabled, empty custom session folder after the provider's existing rows.
 *
 * @param sources - Current draft rows.
 * @param provider - Provider receiving the folder.
 * @param makeId - Identity generator; must not collide with existing rows.
 * @returns The new rows and the added row's identity.
 */
export function addSessionFolder<Row extends SourceRow>(
  sources: readonly Row[],
  provider: Row["provider"],
  makeId: () => string = () => `${provider}-custom-${crypto.randomUUID()}`,
): { sources: Row[]; id: string } {
  let id = makeId();
  while (sources.some((source) => source.id === id)) id = makeId();
  const added = {
    id,
    provider,
    root: "",
    isEnabled: true,
    isAutomatic: false,
  } as Row;
  const last = sources.findLastIndex((source) => source.provider === provider);
  const position = last === -1 ? sources.length : last + 1;
  return {
    sources: [...sources.slice(0, position), added, ...sources.slice(position)],
    id,
  };
}

/**
 * Remove one custom session folder; automatic and single-folder rows are kept.
 *
 * @param sources - Current draft rows.
 * @param id - Identity of the row to remove.
 * @returns The remaining rows.
 */
export function removeSessionFolder<Row extends SourceRow>(
  sources: readonly Row[],
  id: string,
): Row[] {
  return sources.filter(
    (source) => source.id !== id || !isRemovableSessionFolder(source),
  );
}

/**
 * Position of a custom session folder among its provider's custom folders, counted from 1.
 *
 * @param sources - Current draft rows.
 * @param index - Index of the custom folder row.
 * @returns Its stable display number within the draft.
 */
export function sessionFolderNumber(
  sources: readonly SourceRow[],
  index: number,
): number {
  const provider = sources[index]?.provider;
  return sources
    .slice(0, index + 1)
    .filter(
      (source) =>
        source.provider === provider && isRemovableSessionFolder(source),
    ).length;
}

/**
 * Whether a custom row still needs a folder before the draft can be applied.
 *
 * @param source - History source row.
 * @returns Whether its folder path is blank.
 */
export function isMissingFolder(source: SourceRow): boolean {
  return !source.isAutomatic && source.root.trim() === "";
}

/**
 * Status line for a history source row.
 *
 * @param source - History source row.
 * @param detections - Latest provider detections.
 * @returns The status message, translated later with `t`.
 */
export function sourceStatus(
  source: SourceRow,
  detections: readonly DetectionFacts[],
): string {
  if (!source.isAutomatic)
    return canAddSessionFolder(source.provider)
      ? msg("Custom session folder")
      : msg("Custom history location");
  return detections.find((item) => item.provider === source.provider)
    ?.hasHistory
    ? msg("History location detected")
    : msg("No history location detected yet");
}

/**
 * Detected providers that keep history but offer no subscription quota to check.
 *
 * Older hosts omit the capability fields; they only report quota-capable providers.
 *
 * @param detections - Latest provider detections.
 * @returns The providers without native quota support.
 */
export function providersWithoutQuota(
  detections: readonly DetectionFacts[],
): DetectionFacts[] {
  return detections.filter(
    (detection) =>
      detection.supportsQuota === false && detection.supportsHistory !== false,
  );
}
