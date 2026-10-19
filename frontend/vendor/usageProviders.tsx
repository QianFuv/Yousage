import type { UsageProviderKind } from "@t3tools/contracts";

import { ProviderDriverKind } from "@t3tools/contracts";

type UsageProviderPresentation = {
  readonly label: string;
  readonly color: string;
  readonly driverKind: ProviderDriverKind;
};

/**
 * Exhaustive presentation for providers supported by the usage contract.
 * Declaration order is reused by every chart and table, so adding a provider
 * only requires its contract support and one entry here.
 */
export const PROVIDER_PRESENTATION = {
  codex: {
    label: "Codex",
    color: "var(--contrast-foreground)",
    driverKind: ProviderDriverKind.make("codex"),
  },
  claude: {
    label: "Claude Code",
    color: "#d97757",
    driverKind: ProviderDriverKind.make("claudeAgent"),
  },
  pi: {
    label: "Pi",
    color: "#4d9f8a",
    driverKind: ProviderDriverKind.make("pi"),
  },
} satisfies Record<UsageProviderKind, UsageProviderPresentation>;

/** Stable provider reading order across charts, summaries, tables, and hover rows. */
export const PROVIDER_ORDER = Object.keys(PROVIDER_PRESENTATION) as UsageProviderKind[];

/** Color for a provider kind this build does not present, such as one from a newer desktop. */
const OTHER_PROVIDER_COLOR = "var(--muted-foreground)";

/**
 * Whether this build presents the provider kind.
 *
 * @param provider - Provider kind from a report.
 * @returns Whether it has its own entry in {@link PROVIDER_PRESENTATION}.
 */
function isPresentedProvider(provider: string): provider is UsageProviderKind {
  return Object.hasOwn(PROVIDER_PRESENTATION, provider);
}

/**
 * Presentation of a provider kind, tolerating kinds from newer desktops.
 *
 * @param provider - Provider kind from a report.
 * @returns Its label, color and icon driver; unknown kinds show their own name in a neutral color.
 */
export function providerPresentation(provider: string): UsageProviderPresentation {
  return isPresentedProvider(provider)
    ? PROVIDER_PRESENTATION[provider]
    : {
        label: provider,
        color: OTHER_PROVIDER_COLOR,
        driverKind: ProviderDriverKind.make(provider),
      };
}

/**
 * Put provider kinds in the stable reading order, keeping kinds this build does not know.
 *
 * @param providers - Provider kinds, possibly repeated.
 * @returns Known kinds in {@link PROVIDER_ORDER}, then unknown kinds in first-seen order.
 */
export function orderProviderKinds<Kind extends string>(providers: Iterable<Kind>): Kind[] {
  const present = new Set(providers);
  return [
    ...PROVIDER_ORDER.filter((provider) => present.has(provider as Kind)),
    ...[...present].filter((provider) => !isPresentedProvider(provider)),
  ] as Kind[];
}

/** Providers with real activity, independent of the metric currently displayed. */
export function providersWithUsage(
  totals: readonly {
    readonly provider: UsageProviderKind;
    readonly costUsd: number;
    readonly totalTokens: number;
  }[],
): readonly UsageProviderKind[] {
  return orderProviderKinds(
    totals
      .filter((entry) => entry.totalTokens > 0 || entry.costUsd > 0)
      .map((entry) => entry.provider),
  );
}
