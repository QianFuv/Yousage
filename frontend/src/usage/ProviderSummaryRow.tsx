/** Provider rows of a summary column, matching the provider rows of the Usage statistics. */
import {
  orderProviderKinds,
  providerPresentation,
} from "../../vendor/usageProviders";
import { ProviderInstanceIcon } from "../../vendor/ProviderInstanceIcon";
import type { UsageProviderKind } from "./contracts";

/**
 * Keep the active rows in the Usage page's provider order, with providers this build
 * does not know after the known ones.
 *
 * @param rows - Report rows, one per provider.
 * @param isActive - Whether a row has anything to show.
 * @returns The active rows in provider order.
 */
export function orderProviders<Row extends { provider: string }>(
  rows: readonly Row[],
  isActive: (row: Row) => boolean,
): (Row & { provider: UsageProviderKind })[] {
  const active = rows.filter(isActive) as (Row & {
    provider: UsageProviderKind;
  })[];
  return orderProviderKinds(active.map((row) => row.provider)).flatMap(
    (provider) => active.filter((row) => row.provider === provider),
  );
}

/**
 * One provider in a summary column: its identity and value, then its facts.
 *
 * The name is never truncated and the value never wraps. Facts such as the request
 * count and shares stay whole and wrap onto further lines as a unit, so longer numbers,
 * wider fonts, larger interface sizes and narrow phone screens only add lines. Each fact
 * leads with its separator inside a clipped gutter, so a separator shows only between
 * two facts on the same line, never at the end or start of one.
 *
 * @param props - Provider, its headline value and its facts, the count first.
 * @returns The provider row.
 */
export function ProviderSummaryRow({
  provider,
  value,
  facts,
}: {
  provider: UsageProviderKind;
  value: string;
  facts: readonly string[];
}) {
  const presentation = providerPresentation(provider);
  return (
    <div className="flex flex-col gap-1">
      <div className="flex items-baseline justify-between gap-4">
        <span className="flex min-w-0 items-center gap-2 text-sm text-foreground">
          <span
            aria-hidden
            className="size-2 shrink-0 rounded-full"
            style={{ backgroundColor: presentation.color }}
          />
          <ProviderInstanceIcon
            driverKind={presentation.driverKind}
            displayName={presentation.label}
            iconClassName="size-4"
          />
          <span className="min-w-0 wrap-anywhere">{presentation.label}</span>
        </span>
        <span className="shrink-0 text-sm font-medium whitespace-nowrap tabular-nums">
          {value}
        </span>
      </div>
      <div className="[clip-path:inset(0_-100vw_0_0)]">
        <ul className="-ms-4 flex flex-wrap gap-y-0.5 text-xs text-muted-foreground tabular-nums">
          {facts.map((fact, index) => (
            <li
              key={index}
              className="whitespace-nowrap before:inline-block before:w-4 before:text-center before:content-['·'_/_'']"
            >
              {fact}
            </li>
          ))}
        </ul>
      </div>
    </div>
  );
}
