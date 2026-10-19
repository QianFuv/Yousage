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
 * One provider in a summary column: color dot, icon, name and count, its value, and a detail line.
 *
 * @param props - Provider, formatted count, value and detail.
 * @returns The provider row.
 */
export function ProviderSummaryRow({
  provider,
  count,
  value,
  detail,
}: {
  provider: UsageProviderKind;
  count: string;
  value: string;
  detail: string;
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
          <span className="flex min-w-0 flex-wrap items-baseline gap-1.5">
            <span>{presentation.label}</span>
            <span className="whitespace-nowrap text-2xs text-muted-foreground tabular-nums">
              {count}
            </span>
          </span>
        </span>
        <span className="shrink-0 text-sm font-medium tabular-nums">
          {value}
        </span>
      </div>
      <span className="text-xs text-muted-foreground">{detail}</span>
    </div>
  );
}
