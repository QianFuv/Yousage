/** Extra spend caused by expired prompt caches and by errors followed by a full resend. */
import { useCallback, useEffect, useRef, useState } from "react";
import { Events } from "@wailsio/runtime";
import { RefreshCwIcon } from "lucide-react";
import {
  formatCount,
  formatTokens,
  formatUsd,
} from "@t3tools/shared/usageFormat";
import { cn } from "../../../tools/parity/src/bridges/utils";
import { Button } from "../../../tools/parity/upstream/apps/web/src/components/ui/button";
import {
  Toggle,
  ToggleGroup,
} from "../../../tools/parity/upstream/apps/web/src/components/ui/toggle-group";
import {
  Tooltip,
  TooltipPopup,
  TooltipTrigger,
} from "../../../tools/parity/upstream/apps/web/src/components/ui/tooltip";
import { UsageShareBar } from "../../vendor/UsageShareBar";
import { PROVIDER_PRESENTATION } from "../../vendor/usageProviders";
import { ProviderInstanceIcon } from "../../vendor/ProviderInstanceIcon";
import { Notice, PageBody, PageHeader } from "../ui";
import {
  formatDateTimeShort,
  formatDayShort,
  formatDuration,
  msg,
  t,
} from "../i18n";
import { useData } from "../usage/controller";
import type { UsageProviderKind } from "../usage/contracts";
import * as nativeApi from "../../bindings/yousage/app";
import type {
  LossDay,
  LossEvent,
  LossReport,
} from "../../bindings/yousage/internal/service/models";

/** A neutral step between background and foreground, matching the usage mixes. */
const ink = (percent: number) =>
  `color-mix(in oklab, var(--foreground) ${percent}%, var(--background))`;

/** Series colors shared by the totals, the daily chart and the event table. */
const LOSS_COLORS = { cacheExpiry: ink(100), error: ink(42) } as const;

/** Period choices; the loss report has no rolling 24-hour view. */
const PERIOD_OPTIONS = [
  { days: 7, label: msg("7d") },
  { days: 30, label: msg("30d") },
  { days: 90, label: msg("90d") },
] as const;

/** Labels of the error kinds reported by the history collectors. */
const ERROR_KIND_LABELS: Record<string, string> = {
  rateLimit: msg("Rate limited"),
  overloaded: msg("Overloaded"),
  server: msg("Server error"),
  streamDisconnected: msg("Stream disconnected"),
  interrupted: msg("Interrupted"),
  aborted: msg("Aborted"),
  auth: msg("Authentication failed"),
  other: msg("Other error"),
};

/** Error kind shades, darkest first, cycling for kinds beyond the palette. */
const KIND_SHADES = [100, 72, 52, 36, 24] as const;

/**
 * Localized label of an error kind.
 *
 * @param kind - Collector error kind.
 * @returns The label, or the raw kind when it is unknown.
 */
function errorKindLabel(kind: string | undefined): string {
  return kind && ERROR_KIND_LABELS[kind]
    ? t(ERROR_KIND_LABELS[kind])
    : (kind ?? t("Other error"));
}

/**
 * Loss analysis page: cache-expiry rewrites and error resends over a trailing window.
 *
 * @param props - Whether the page is the visible route.
 * @returns The loss page.
 */
export function LossPage({ isActive }: { isActive: boolean }) {
  const data = useData();
  const zone = Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  const [days, setDays] = useState(30);
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
        <ToggleGroup
          aria-label={t("Loss period")}
          variant="segmented"
          value={[String(days)]}
          onValueChange={(next) => {
            const value = Number(next[0]);
            if (value) setDays(value);
          }}
        >
          {PERIOD_OPTIONS.map((option) => (
            <Toggle key={option.days} value={String(option.days)}>
              {t(option.label)}
            </Toggle>
          ))}
        </ToggleGroup>
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
        {report && <LossContent report={report} />}
      </PageBody>
    </section>
  );
}

/**
 * Totals, daily chart, error kinds and the event list of one report.
 *
 * @param props - The loss report.
 * @returns The page content.
 */
function LossContent({ report }: { report: LossReport }) {
  const totalUsd = report.cacheExpiry.costUsd + report.errors.costUsd;
  const totalEvents = report.cacheExpiry.events + report.errors.events;
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
            color={LOSS_COLORS.cacheExpiry}
            label={t("Cache expiry")}
            costUsd={report.cacheExpiry.costUsd}
            detail={t("{count} rewrites · {tokens} tokens", {
              count: formatCount(report.cacheExpiry.events),
              tokens: formatTokens(report.cacheExpiry.tokens),
            })}
          />
          <LossTotal
            color={LOSS_COLORS.error}
            label={t("Errors")}
            costUsd={report.errors.costUsd}
            detail={t("{count} errors · {tokens} tokens resent", {
              count: formatCount(report.errors.events),
              tokens: formatTokens(report.errors.tokens),
            })}
          />
        </div>
        <div className="flex min-w-0 flex-col gap-3">
          <h2 className="text-sm font-medium">{t("Daily extra spend")}</h2>
          <LossChart days={report.days} />
        </div>
      </section>
      {report.errorKinds.length > 0 && (
        <section className="grid gap-x-12 gap-y-8 lg:grid-cols-2">
          <UsageShareBar
            label={t("Errors by kind")}
            segments={report.errorKinds.map((kind, index) => ({
              label: errorKindLabel(kind.kind),
              value: kind.count,
              color: ink(KIND_SHADES[index % KIND_SHADES.length]),
            }))}
            format={formatCount}
          />
        </section>
      )}
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

/** Number of evenly spaced value gridlines above zero. */
const TICK_COUNT = 4;

/**
 * Round a maximum up to a readable axis ceiling (1, 2, 2.5 or 5 times a power of ten).
 *
 * @param maximum - Largest plotted value.
 * @returns The axis ceiling.
 */
function niceCeiling(maximum: number): number {
  if (maximum <= 0) return 1;
  const magnitude = 10 ** Math.floor(Math.log10(maximum));
  const step = [1, 2, 2.5, 5, 10].find(
    (factor) => factor * magnitude >= maximum,
  )!;
  return step * magnitude;
}

/**
 * Stacked daily columns of cache-expiry and error spend.
 *
 * @param props - Every day of the window, ascending.
 * @returns The chart element.
 */
function LossChart({ days }: { days: readonly LossDay[] }) {
  const ceiling = niceCeiling(
    Math.max(0, ...days.map((day) => day.cacheExpiryUsd + day.errorUsd)),
  );
  const ticks = Array.from(
    { length: TICK_COUNT + 1 },
    (_, index) => (ceiling * (TICK_COUNT - index)) / TICK_COUNT,
  );
  const labelEvery = Math.max(1, Math.ceil(days.length / 7));
  return (
    <div className="flex flex-col gap-1">
      <div className="flex gap-2">
        <div className="relative h-56 w-14 shrink-0">
          {ticks.map((tick) => (
            <span
              key={tick}
              className="absolute right-0 -translate-y-1/2 text-3xs text-muted-foreground tabular-nums"
              style={{ top: `${(1 - tick / ceiling) * 100}%` }}
            >
              {tick === 0 ? "0" : formatUsd(tick)}
            </span>
          ))}
        </div>
        <div
          role="img"
          aria-label={t("Daily extra spend")}
          className="relative h-56 flex-1"
        >
          {ticks.map((tick) => (
            <div
              key={tick}
              aria-hidden
              className="absolute inset-x-0 border-t border-border"
              style={{ top: `${(1 - tick / ceiling) * 100}%` }}
            />
          ))}
          <div className="absolute inset-0 flex items-end gap-px">
            {days.map((day) => {
              const total = day.cacheExpiryUsd + day.errorUsd;
              return (
                <Tooltip key={day.day}>
                  <TooltipTrigger
                    render={
                      <div className="flex h-full min-w-0 flex-1 flex-col justify-end rounded-t-xs hover:bg-muted/50" />
                    }
                  >
                    <div
                      className="w-full rounded-t-xs"
                      style={{
                        height: `${(day.errorUsd / ceiling) * 100}%`,
                        backgroundColor: LOSS_COLORS.error,
                      }}
                    />
                    <div
                      className={cn(
                        "w-full",
                        day.errorUsd === 0 && "rounded-t-xs",
                      )}
                      style={{
                        height: `${(day.cacheExpiryUsd / ceiling) * 100}%`,
                        backgroundColor: LOSS_COLORS.cacheExpiry,
                      }}
                    />
                  </TooltipTrigger>
                  <TooltipPopup>
                    <div className="flex min-w-36 flex-col gap-0.5 text-xs">
                      <span className="text-muted-foreground">
                        {formatDayShort(day.day)}
                      </span>
                      <span className="flex justify-between gap-3">
                        <span>
                          {t("Cache expiry")} ·{" "}
                          {formatCount(day.cacheExpiryCount)}
                        </span>
                        <span className="tabular-nums">
                          {formatUsd(day.cacheExpiryUsd)}
                        </span>
                      </span>
                      <span className="flex justify-between gap-3">
                        <span>
                          {t("Errors")} · {formatCount(day.errorCount)}
                        </span>
                        <span className="tabular-nums">
                          {formatUsd(day.errorUsd)}
                        </span>
                      </span>
                      <span className="mt-0.5 flex justify-between gap-3 border-t border-border pt-0.5">
                        <span>{t("Total")}</span>
                        <span className="tabular-nums">{formatUsd(total)}</span>
                      </span>
                    </div>
                  </TooltipPopup>
                </Tooltip>
              );
            })}
          </div>
        </div>
      </div>
      <div className="flex pl-16 text-3xs text-muted-foreground uppercase">
        {days.map((day, index) => (
          <span
            key={day.day}
            className="min-w-0 flex-1 overflow-visible whitespace-nowrap"
          >
            {index % labelEvery === 0 ? formatDayShort(day.day) : ""}
          </span>
        ))}
      </div>
    </div>
  );
}

/** Events listed before the table is expanded. */
const COLLAPSED_EVENTS = 20;

/**
 * Newest loss events with their cause, model and extra spend.
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
              <th className="py-2 pr-6 text-left font-normal">{t("Cause")}</th>
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
                  colSpan={6}
                  className="py-6 text-center text-muted-foreground"
                >
                  {t("No loss in this window.")}
                </td>
              </tr>
            ) : (
              shown.map((event, index) => {
                const provider = event.provider as UsageProviderKind;
                const presentation = PROVIDER_PRESENTATION[provider];
                const gap =
                  event.kind === "cacheExpiry"
                    ? event.idleMs
                    : event.followUpMs;
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
                    <td className="py-2.5 pr-6 text-left text-foreground">
                      <span className="flex items-center gap-2">
                        <span
                          aria-hidden
                          className="size-2 shrink-0 rounded-full"
                          style={{
                            backgroundColor:
                              event.kind === "cacheExpiry"
                                ? LOSS_COLORS.cacheExpiry
                                : LOSS_COLORS.error,
                          }}
                        />
                        {event.kind === "cacheExpiry"
                          ? t("Cache expiry")
                          : errorKindLabel(event.errorKind)}
                      </span>
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
