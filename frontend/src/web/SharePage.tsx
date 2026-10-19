/** Public receipt share page: headline totals, trend, model share and the receipt itself. */
import { useMemo } from "react";
import {
  formatCount,
  formatPercent,
  formatTokens,
  formatUsd,
} from "@t3tools/shared/usageFormat";
import { t } from "../i18n";
import { ReceiptPaper } from "../receipt/ReceiptPaper";
import { SeriesChart } from "../usage/SeriesChart";
import { bucketLabels } from "../usage/chartAxis";
import {
  orderProviderKinds,
  providerPresentation,
} from "../../vendor/usageProviders";
import { ProviderInstanceIcon } from "../../vendor/ProviderInstanceIcon";
import type { Receipt } from "../../bindings/yousage/internal/service/models";
import type { Window } from "../../bindings/yousage/internal/accounting/models";
import type { ShareModel, SharePayload } from "./payloads";
import "./web.css";

/**
 * The calendar day of an instant in a time zone, as `YYYY-MM-DD`.
 *
 * @param instantMs - Unix time in milliseconds.
 * @param timeZone - IANA time zone.
 * @returns The local calendar day.
 */
function localDay(instantMs: number, timeZone: string): string {
  return new Intl.DateTimeFormat("en-CA", {
    timeZone,
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
  }).format(new Date(instantMs));
}

/**
 * Chart buckets and per-provider lines for the share window.
 *
 * @param payload - Share payload.
 * @returns Bucket labels, the report window for axis formatting, and series.
 */
export function shareChart(payload: SharePayload) {
  const isHourly = payload.window.resolution === "hour";
  const periods = payload.series.map((bucket) =>
    isHourly
      ? new Date(bucket.startMs).toISOString()
      : localDay(bucket.startMs, payload.timeZone),
  );
  const providers = orderProviderKinds(
    payload.series.flatMap((bucket) =>
      bucket.values.map((value) => value.provider),
    ),
  );
  const series = providers.map((provider) => ({
    id: provider,
    color: providerPresentation(provider).color,
    values: payload.series.map((bucket) => {
      const value = bucket.values.find((entry) => entry.provider === provider);
      if (!value) return 0;
      return payload.hideAmounts ? value.tokens : (value.costUsd ?? 0);
    }),
  }));
  const reportWindow = {
    resolution: payload.window.resolution,
    timeZone: payload.timeZone,
    sinceDay: payload.window.sinceDay,
    untilDay: payload.window.untilDay,
    ...(isHourly
      ? {
          sinceTimeMs: payload.window.startsAtMs,
          untilTimeMs: payload.window.endsAtMs,
        }
      : {}),
  } as Window;
  return { periods, series, reportWindow };
}

/**
 * Adapt the payload receipt to the receipt paper, which reads the desktop receipt shape.
 *
 * @param payload - Share payload.
 * @returns A receipt without a summary code; hidden amounts and fields absent from older
 * shares read as zero or false.
 */
export function shareReceipt(payload: SharePayload): Receipt {
  const receipt = payload.receipt;
  return {
    days: payload.days,
    startsAtMs: payload.window.startsAtMs,
    endsAtMs: payload.window.endsAtMs,
    number: receipt.number,
    timeZone: payload.timeZone,
    sinceDay: payload.window.sinceDay,
    untilDay: payload.window.untilDay,
    items: receipt.items.map((item) => ({
      provider: item.provider,
      model: item.model,
      tokens: item.tokens,
      costUsd: item.costUsd ?? 0,
      unpricedRecords: item.unpricedRecords ?? 0,
      isSubscription: item.isSubscription,
      isPaymentUnknown: item.isPaymentUnknown ?? false,
    })),
    totalUsd: receipt.totalUsd ?? 0,
    subscriptionUsd: receipt.subscriptionUsd ?? 0,
    paidUsd: receipt.paidUsd ?? 0,
    unknownPaymentUsd: receipt.unknownPaymentUsd ?? 0,
    tokens: receipt.tokens,
    sessions: receipt.sessions,
    cacheSavingsUsd: receipt.cacheSavingsUsd ?? 0,
    unpricedRecords: receipt.unpricedRecords,
    activeDays: receipt.activeDays,
    firstAtMs: null,
    lastAtMs: null,
    qrText: "",
    qrModules: [],
  } as unknown as Receipt;
}

/**
 * Cost and token detail of a model slice.
 *
 * @param model - Model slice from the payload.
 * @param isHidden - Whether amounts are withheld.
 * @returns Tokens alone when amounts are hidden or absent, "Unpriced" in place of a zero
 * cost that only reflects unpriced records, and the formatted cost otherwise.
 */
function modelDetail(model: ShareModel, isHidden: boolean): string {
  const tokens = `${formatTokens(model.tokens)} tokens`;
  if (isHidden || model.costUsd === undefined) return tokens;
  const cost =
    model.costUsd === 0 && (model.unpricedRecords ?? 0) > 0
      ? t("Unpriced")
      : formatUsd(model.costUsd);
  return `${cost} · ${tokens}`;
}

/**
 * Title of the shared period.
 *
 * @param days - Window length in days.
 * @returns “Last 24 hours” or “Last N days”.
 */
function periodTitle(days: number): string {
  return days === 1
    ? t("Last 24 hours")
    : t("Last {days} days", { days: formatCount(days) });
}

/**
 * The share page body, used by the public page and the desktop preview.
 *
 * @param props - Decrypted share payload.
 * @returns The share page.
 */
export function SharePage({ payload }: { payload: SharePayload }) {
  const chart = useMemo(() => shareChart(payload), [payload]);
  const labels = bucketLabels(chart.reportWindow);
  const receipt = payload.receipt;
  const isHidden = payload.hideAmounts;
  const valueFormat = isHidden ? formatTokens : formatUsd;
  const stats = [
    ...(isHidden
      ? []
      : [{ label: t("Tokens"), value: formatTokens(receipt.tokens) }]),
    { label: t("Sessions"), value: formatCount(receipt.sessions) },
    ...(payload.days > 1
      ? [{ label: t("Active days"), value: formatCount(receipt.activeDays) }]
      : []),
    ...(!isHidden && receipt.cacheSavingsUsd !== undefined
      ? [{ label: t("Cache saved"), value: formatUsd(receipt.cacheSavingsUsd) }]
      : []),
  ];
  return (
    <article className="share-page" aria-label={t("Yousage usage share")}>
      <header className="share-hero">
        <strong
          className="sidebar-wordmark share-wordmark"
          aria-label="Yousage"
        >
          <span aria-hidden>Y</span>
          <span className="sidebar-wordmark-tail" aria-hidden>
            ou
          </span>
          <span className="sidebar-wordmark-tail" aria-hidden>
            sage
          </span>
        </strong>
        <p className="share-period">{periodTitle(payload.days)}</p>
        <p className="share-headline share-number">
          {isHidden
            ? formatTokens(receipt.tokens)
            : formatUsd(receipt.totalUsd ?? 0)}
        </p>
        <p className="share-subline">
          {isHidden ? t("Tokens processed") : t("API-equivalent spend")}
        </p>
      </header>
      <section className="share-stats" aria-label={t("Totals")}>
        {stats.map((stat) => (
          <div key={stat.label} className="share-stat">
            <b className="share-number">{stat.value}</b>
            <span>{stat.label}</span>
          </div>
        ))}
      </section>
      {payload.series.length > 0 && chart.series.length > 0 && (
        <section className="share-card">
          <h2>
            {isHidden
              ? labels.isHourly
                ? t("Hourly tokens")
                : t("Daily tokens")
              : labels.isHourly
                ? t("Hourly spend")
                : t("Daily spend")}
          </h2>
          <SeriesChart
            periods={chart.periods}
            series={chart.series}
            format={valueFormat}
            ariaLabel={isHidden ? t("Tokens over time") : t("Spend over time")}
            formatPeriod={labels.axis}
            formatTooltipPeriod={labels.full}
            renderTooltip={(index) =>
              chart.series.map((line) => (
                <div key={line.id} className="share-tooltip-row">
                  <span
                    className="share-dot"
                    style={{ backgroundColor: line.color }}
                  />
                  <span>{providerPresentation(line.id).label}</span>
                  <b className="share-number">
                    {valueFormat(line.values[index] ?? 0)}
                  </b>
                </div>
              ))
            }
          />
        </section>
      )}
      {payload.models.length > 0 && (
        <section className="share-card">
          <h2>{t("Models")}</h2>
          <ul className="share-models">
            {payload.models.map((model) => {
              const presentation = providerPresentation(model.provider);
              return (
                <li key={`${model.provider}:${model.model}`}>
                  <div className="share-model-row">
                    <ProviderInstanceIcon
                      driverKind={presentation.driverKind}
                      displayName={presentation.label}
                      iconClassName="size-4"
                    />
                    <span className="share-model-name">{model.model}</span>
                    <span className="share-number">
                      {formatPercent(model.share)}
                    </span>
                  </div>
                  <div className="share-model-track" aria-hidden>
                    <div
                      style={{
                        width: `${Math.max(model.share * 100, 1)}%`,
                        backgroundColor: presentation.color,
                      }}
                    />
                  </div>
                  <span className="share-model-detail share-number">
                    {modelDetail(model, isHidden)}
                  </span>
                </li>
              );
            })}
          </ul>
        </section>
      )}
      <section className="share-receipt">
        <ReceiptPaper receipt={shareReceipt(payload)} hideAmounts={isHidden} />
      </section>
      <footer className="share-footer">
        {payload.expiresAt
          ? t("Shared from Yousage · available until {date}", {
              date: localDay(Date.parse(payload.expiresAt), payload.timeZone),
            })
          : t("Shared from Yousage")}
      </footer>
    </article>
  );
}
