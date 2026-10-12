/** Extra spend caused by expired prompt caches. */
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Events } from "@wailsio/runtime";
import { RefreshCwIcon } from "lucide-react";
import {
  formatCount,
  formatTokens,
  formatUsd,
} from "@t3tools/shared/usageFormat";
import { cn } from "../../../tools/parity/src/bridges/utils";
import { Button } from "../../../tools/parity/upstream/apps/web/src/components/ui/button";
import { PROVIDER_PRESENTATION } from "../../vendor/usageProviders";
import { ProviderInstanceIcon } from "../../vendor/ProviderInstanceIcon";
import { EmptyState, Notice, PageBody, PageHeader } from "../ui";
import { formatDateTimeShort, formatDuration, msg, t } from "../i18n";
import { useData } from "../usage/controller";
import { PeriodToggle, usePeriodDays } from "../usage/PeriodToggle";
import { bucketLabels } from "../usage/chartAxis";
import { SeriesChart } from "../usage/SeriesChart";
import type { UsageProviderKind } from "../usage/contracts";
import * as nativeApi from "../../bindings/yousage/app";
import type {
  LossDay,
  LossEvent,
  LossReport,
} from "../../bindings/yousage/internal/service/models";
import type { Window } from "../../bindings/yousage/internal/accounting/models";

/** A neutral step between background and foreground, matching the usage mixes. */
const ink = (percent: number) =>
  `color-mix(in oklab, var(--foreground) ${percent}%, var(--background))`;

/** Cache-expiry color shared by the totals, the daily chart and the event table. */
const CACHE_EXPIRY_COLOR = ink(100);

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

  /** Read the selected window, ignoring answers to superseded selections. */
  const load = useCallback(async () => {
    const identity = ++request.current;
    setIsLoading(true);
    try {
      const value = await nativeApi.GetLossReport(days, zone);
      if (identity !== request.current) return;
      setReport(value);
      setError("");
    } catch (cause) {
      if (identity === request.current) setError(String(cause));
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
      >
        <PeriodToggle label={t("Loss period")} />
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label={isBusy ? t("Refreshing…") : t("Refresh usage")}
          title={`${t("Refresh")} (Ctrl+R)`}
          aria-busy={isBusy}
          disabled={isBusy}
          onClick={() => void data.refresh(true, true)}
        >
          <RefreshCwIcon aria-hidden className={cn(isBusy && "animate-spin")} />
        </Button>
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
 * Cache-expiry totals, daily chart and the event list of one report.
 *
 * @param props - The loss report.
 * @returns The page content.
 */
function LossContent({ report }: { report: LossReport }) {
  const totalUsd = report.cacheExpiry.costUsd;
  const totalEvents = report.cacheExpiry.events;
  return (
    <div className="flex flex-col gap-10">
      <section className="grid gap-x-12 gap-y-8 lg:grid-cols-[18rem_minmax(0,1fr)]">
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
          <LossTotal
            color={CACHE_EXPIRY_COLOR}
            label={t("Cache expiry")}
            costUsd={report.cacheExpiry.costUsd}
            detail={t("{count} rewrites · {tokens} tokens", {
              count: formatCount(report.cacheExpiry.events),
              tokens: formatTokens(report.cacheExpiry.tokens),
            })}
          />
        </div>
        <div className="flex min-w-0 flex-col gap-3">
          <h2 className="text-sm font-medium">
            {report.window.resolution === "hour"
              ? t("Hourly extra spend")
              : t("Daily extra spend")}
          </h2>
          {totalEvents > 0 ? (
            <LossChart days={report.days} reportWindow={report.window} />
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
 * One loss category in the summary column.
 *
 * @param props - Series color, label, cost and detail line.
 * @returns The total row.
 */
function LossTotal({
  color,
  label,
  costUsd,
  detail,
}: {
  color: string;
  label: string;
  costUsd: number;
  detail: string;
}) {
  return (
    <div className="flex flex-col gap-1">
      <div className="flex items-baseline justify-between gap-4">
        <span className="flex min-w-0 items-center gap-2 text-sm text-foreground">
          <span
            aria-hidden
            className="size-2 shrink-0 rounded-full"
            style={{ backgroundColor: color }}
          />
          {label}
        </span>
        <span className="shrink-0 text-sm font-medium tabular-nums">
          {formatUsd(costUsd)}
        </span>
      </div>
      <span className="text-xs text-muted-foreground">{detail}</span>
    </div>
  );
}

/**
 * Cache-expiry spend per day, or per hour in a 24-hour window.
 *
 * @param props - Every bucket of the window, ascending, and the report window.
 * @returns The chart element.
 */
function LossChart({
  days,
  reportWindow,
}: {
  days: readonly LossDay[];
  reportWindow: Window;
}) {
  const labels = bucketLabels(reportWindow);
  const series = useMemo(
    () => [
      {
        id: "cacheExpiry",
        color: CACHE_EXPIRY_COLOR,
        values: days.map((day) => day.cacheExpiryUsd),
      },
    ],
    [days],
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
      renderTooltip={(index) => {
        const day = days[index];
        return (
          <TooltipRow
            color={CACHE_EXPIRY_COLOR}
            label={`${t("Cache expiry")} · ${formatCount(day.cacheExpiryCount)}`}
            value={formatUsd(day.cacheExpiryUsd)}
          />
        );
      }}
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
              <th className="hidden py-2 pl-6 font-normal sm:table-cell">
                {t("Gap")}
              </th>
              <th className="py-2 pl-6 font-normal">{t("Tokens")}</th>
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
                const provider = event.provider as UsageProviderKind;
                const presentation = PROVIDER_PRESENTATION[provider];
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
                        {presentation && (
                          <ProviderInstanceIcon
                            driverKind={presentation.driverKind}
                            displayName={presentation.label}
                            iconClassName="size-3.5"
                          />
                        )}
                        {event.model || t("Unknown model")}
                      </span>
                    </td>
                    <td className="hidden py-2.5 pl-6 sm:table-cell">
                      {gap > 0 ? formatDuration(gap) : "—"}
                    </td>
                    <td className="py-2.5 pl-6">
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
