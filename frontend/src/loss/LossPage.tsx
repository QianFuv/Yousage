/** Extra spend caused by expired prompt caches. */
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Events } from "@wailsio/runtime";
import { RefreshCwIcon } from "lucide-react";
import {
  formatCount,
  formatPercent,
  formatTokens,
  formatUsd,
} from "@t3tools/shared/usageFormat";
import { cn } from "../../../tools/parity/src/bridges/utils";
import { Button } from "../../../tools/parity/upstream/apps/web/src/components/ui/button";
import { providerPresentation } from "../../vendor/usageProviders";
import { ProviderInstanceIcon } from "../../vendor/ProviderInstanceIcon";
import { EmptyState, Notice, PageBody, PageHeader } from "../ui";
import { formatDateTimeShort, formatDuration, msg, t } from "../i18n";
import { notifyError } from "../notify";
import { useData } from "../usage/controller";
import { PeriodToggle, usePeriodDays } from "../usage/PeriodToggle";
import { bucketLabels } from "../usage/chartAxis";
import { SeriesChart } from "../usage/SeriesChart";
import {
  orderProviders,
  ProviderSummaryRow,
} from "../usage/ProviderSummaryRow";
import type { UsageProviderKind } from "../usage/contracts";
import * as nativeApi from "../../bindings/yousage/internal/desktop/app";
import type {
  LossDay,
  LossEvent,
  LossProvider,
  LossReport,
} from "../../bindings/yousage/internal/service/models";
import type { Window } from "../../bindings/yousage/internal/accounting/models";

/**
 * Loss analysis page: cache-expiry rewrites over a trailing window.
 *
 * @param props - Whether the page is the visible route.
 * @returns The loss page.
 */
export function LossPage({ isActive }: { isActive: boolean }) {
  const data = useData();
  const zone = Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  const days = usePeriodDays();
  const [report, setReport] = useState<LossReport | null>(null);
  const [error, setError] = useState("");
  const [isLoading, setIsLoading] = useState(false);
  const request = useRef(0);
  const hasReport = useRef(false);

  /** Read the selected window, ignoring answers to superseded selections. */
  const load = useCallback(async () => {
    const identity = ++request.current;
    setIsLoading(true);
    try {
      const value = await nativeApi.GetLossReport(days, zone);
      if (identity !== request.current) return;
      hasReport.current = true;
      setReport(value);
      setError("");
    } catch (cause) {
      if (identity !== request.current) return;
      if (hasReport.current) notifyError(cause);
      else setError(String(cause));
    } finally {
      if (identity === request.current) setIsLoading(false);
    }
  }, [days, zone]);

  useEffect(() => {
    if (!isActive) return;
    void load();
    return Events.On("usage-invalidated", () => void load());
  }, [isActive, load]);

  const isBusy = data.isBusy || isLoading;
  return (
    <section className="loss-page" aria-label={t("Loss")}>
      <PageHeader
        title={t("Loss")}
        status={data.isBusy ? t("Refreshing…") : !report ? t("Loading…") : null}
        actions={
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label={isBusy ? t("Refreshing…") : t("Refresh usage")}
            title={`${t("Refresh")} (Ctrl+R)`}
            aria-busy={isBusy}
            disabled={isBusy}
            onClick={() => void data.refresh(true, true)}
          >
            <RefreshCwIcon
              aria-hidden
              className={cn(isBusy && "animate-spin")}
            />
          </Button>
        }
      >
        <PeriodToggle label={t("Loss period")} />
      </PageHeader>
      <PageBody>
        {error && <Notice tone="error">{error}</Notice>}
        {report ? (
          <LossContent report={report} />
        ) : (
          !error && <EmptyState>{t("Loading loss analysis…")}</EmptyState>
        )}
      </PageBody>
    </section>
  );
}

/**
 * Cache-expiry totals per provider, their daily chart and the event list of one report.
 *
 * @param props - The loss report.
 * @returns The page content.
 */
function LossContent({ report }: { report: LossReport }) {
  const totalUsd = report.cacheExpiry.costUsd;
  const totalEvents = report.cacheExpiry.events;
  const providers = orderProviders(
    report.providers,
    (provider) => provider.events > 0,
  );
  return (
    <div className="flex flex-col gap-10">
      <section className="grid gap-x-12 gap-y-8 md:grid-cols-[18rem_minmax(0,1fr)]">
        <div className="flex min-w-0 flex-col gap-4">
          <div className="flex flex-col gap-1">
            <span className="text-4xl font-semibold tracking-tight text-foreground tabular-nums">
              {formatUsd(totalUsd)}
            </span>
            <span className="text-xs text-muted-foreground">
              {t(
                totalEvents === 1
                  ? msg("Extra spend · {count} event")
                  : msg("Extra spend · {count} events"),
                { count: formatCount(totalEvents) },
              )}
            </span>
          </div>
          {providers.map((provider) => (
            <ProviderSummaryRow
              key={provider.provider}
              provider={provider.provider}
              count={rewriteCount(provider.events)}
              value={formatUsd(provider.costUsd)}
              detail={t("{share} of extra spend · {tokens} tokens", {
                share: formatPercent(
                  totalUsd > 0 ? provider.costUsd / totalUsd : 0,
                ),
                tokens: formatTokens(provider.tokens),
              })}
            />
          ))}
        </div>
        <div className="flex min-w-0 flex-col gap-3">
          <h2 className="text-sm font-medium">
            {report.window.resolution === "hour"
              ? t("Hourly extra spend")
              : t("Daily extra spend")}
          </h2>
          {totalEvents > 0 ? (
            <LossChart
              days={report.days}
              providers={providers}
              reportWindow={report.window}
            />
          ) : (
            <EmptyState>{t("No loss in this window.")}</EmptyState>
          )}
        </div>
      </section>
      <section className="flex min-w-0 flex-col gap-3">
        <h2 className="text-sm font-medium">{t("Events")}</h2>
        <LossEvents events={report.events} timeZone={report.window.timeZone} />
      </section>
    </div>
  );
}

/**
 * Localized count of cache rewrites.
 *
 * @param count - Number of rewrites.
 * @returns `1 rewrite` or `{count} rewrites`.
 */
function rewriteCount(count: number): string {
  return t(count === 1 ? msg("{count} rewrite") : msg("{count} rewrites"), {
    count: formatCount(count),
  });
}

/**
 * One provider's share of a bucket.
 *
 * @param day - The bucket.
 * @param provider - Provider kind.
 * @returns Its cost and rewrite count, zero when it had no loss.
 */
function providerDay(day: LossDay, provider: string) {
  return (
    day.providers.find((entry) => entry.provider === provider) ?? {
      costUsd: 0,
      count: 0,
    }
  );
}

/**
 * Cache-expiry spend per provider and day, or per hour in a 24-hour window, as layered lines.
 *
 * @param props - Every bucket of the window ascending, the providers with loss and the report window.
 * @returns The chart element.
 */
function LossChart({
  days,
  providers,
  reportWindow,
}: {
  days: readonly LossDay[];
  providers: readonly (LossProvider & { provider: UsageProviderKind })[];
  reportWindow: Window;
}) {
  const labels = bucketLabels(reportWindow);
  const series = useMemo(
    () =>
      providers.map(({ provider }) => ({
        id: provider,
        color: providerPresentation(provider).color,
        values: days.map((day) => providerDay(day, provider).costUsd),
      })),
    [days, providers],
  );
  return (
    <SeriesChart
      periods={days.map((day) => day.day)}
      series={series}
      format={formatUsd}
      ariaLabel={
        labels.isHourly ? t("Hourly extra spend") : t("Daily extra spend")
      }
      formatPeriod={labels.axis}
      formatTooltipPeriod={labels.full}
      renderTooltip={(index) =>
        providers.map(({ provider }) => {
          const entry = providerDay(days[index], provider);
          return (
            <TooltipRow
              key={provider}
              color={providerPresentation(provider).color}
              label={`${providerPresentation(provider).label} · ${rewriteCount(entry.count)}`}
              value={formatUsd(entry.costUsd)}
            />
          );
        })
      }
    />
  );
}

/**
 * One series in the chart readout: a color dot, its label and value.
 *
 * @param props - Series color, label and formatted value.
 * @returns The readout row.
 */
function TooltipRow({
  color,
  label,
  value,
}: {
  color: string;
  label: string;
  value: string;
}) {
  return (
    <div className="flex items-center justify-between gap-3">
      <span className="flex items-center gap-1.5 text-muted-foreground">
        <span
          aria-hidden
          className="size-2 shrink-0 rounded-full"
          style={{ backgroundColor: color }}
        />
        {label}
      </span>
      <span className="text-foreground tabular-nums">{value}</span>
    </div>
  );
}

/** Events listed before the table is expanded. */
const COLLAPSED_EVENTS = 20;

/**
 * Newest cache-expiry events with their model and extra spend.
 *
 * @param props - Events newest first and the report time zone.
 * @returns The event table with an expand toggle.
 */
function LossEvents({
  events,
  timeZone,
}: {
  events: readonly LossEvent[];
  timeZone: string;
}) {
  const [isExpanded, setIsExpanded] = useState(false);
  const shown = isExpanded ? events : events.slice(0, COLLAPSED_EVENTS);
  return (
    <div className="flex flex-col gap-3">
      <div
        className="usage-table-scroll"
        tabIndex={0}
        aria-label={t("Loss events table")}
      >
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b border-border text-right text-xs text-muted-foreground">
              <th className="py-2 pr-6 text-left font-normal">{t("Time")}</th>
              <th className="w-full py-2 text-left font-normal">
                {t("Model")}
              </th>
              <th className="hidden py-2 pl-6 font-normal md:table-cell">
                {t("Gap")}
              </th>
              <th className="hidden py-2 pl-6 font-normal md:table-cell">
                {t("Tokens")}
              </th>
              <th className="py-2 pl-6 font-normal">{t("Extra spend")}</th>
            </tr>
          </thead>
          <tbody>
            {events.length === 0 ? (
              <tr>
                <td
                  colSpan={5}
                  className="py-6 text-center text-muted-foreground"
                >
                  {t("No loss in this window.")}
                </td>
              </tr>
            ) : (
              shown.map((event, index) => {
                const presentation = providerPresentation(event.provider);
                const gap = event.idleMs;
                return (
                  <tr
                    key={`${event.sessionId}:${event.atMs}:${event.kind}:${index}`}
                    className="border-b border-border/50 text-right whitespace-nowrap text-muted-foreground tabular-nums"
                  >
                    <td className="py-2.5 pr-6 text-left">
                      {formatDateTimeShort(
                        new Date(event.atMs).toISOString(),
                        timeZone,
                      )}
                    </td>
                    <td className="py-2.5 text-left text-foreground">
                      <span className="flex items-center gap-2">
                        <ProviderInstanceIcon
                          driverKind={presentation.driverKind}
                          displayName={presentation.label}
                          iconClassName="size-3.5"
                        />
                        {event.model || t("Unknown model")}
                      </span>
                    </td>
                    <td className="hidden py-2.5 pl-6 md:table-cell">
                      {gap > 0 ? formatDuration(gap) : "—"}
                    </td>
                    <td className="hidden py-2.5 pl-6 md:table-cell">
                      {event.tokens > 0 ? formatTokens(event.tokens) : "—"}
                    </td>
                    <td className="py-2.5 pl-6 text-foreground">
                      {event.costUsd > 0 ? (
                        <>
                          {event.precision === "estimated" && (
                            <span
                              className="me-1 text-muted-foreground"
                              title={t("Estimated from cache hit rates")}
                            >
                              ≈
                            </span>
                          )}
                          {formatUsd(event.costUsd)}
                        </>
                      ) : (
                        "—"
                      )}
                    </td>
                  </tr>
                );
              })
            )}
          </tbody>
        </table>
      </div>
      {events.length > COLLAPSED_EVENTS && (
        <div>
          <Button
            size="xs"
            variant="outline"
            onClick={() => setIsExpanded((current) => !current)}
          >
            {isExpanded
              ? t("Show fewer")
              : t("Show all {count} events", {
                  count: formatCount(events.length),
                })}
          </Button>
        </div>
      )}
    </div>
  );
}
