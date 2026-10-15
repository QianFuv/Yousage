/** Receipt paper for the selected trailing period: items, payments, totals and a summary code. */
import type { CSSProperties, RefObject } from "react";
import {
  formatCount,
  formatTokens,
  formatUsd,
} from "@t3tools/shared/usageFormat";
import { t } from "../i18n";
import type { Receipt } from "../../bindings/yousage/internal/service/models";
import "./receipt.css";

/**
 * Format an instant as `MM.DD HH:mm` on a 24-hour clock.
 *
 * @param instantMs - Unix time in milliseconds.
 * @param timeZone - IANA time zone.
 * @returns The wall-clock date and time.
 */
function formatReceiptInstant(instantMs: number, timeZone: string): string {
  const parts = Object.fromEntries(
    new Intl.DateTimeFormat("en-GB", {
      timeZone,
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
      hourCycle: "h23",
    })
      .formatToParts(new Date(instantMs))
      .map((part) => [part.type, part.value]),
  );
  return `${parts.month}.${parts.day} ${parts.hour}:${parts.minute}`;
}

/**
 * The covered range: exact times for 24 hours, calendar dates otherwise.
 *
 * @param receipt - Receipt with its window and bounds.
 * @returns `10.08 14:05 – 10.09 14:05` or `2026.10.03 – 2026.10.09`.
 */
function receiptRange(receipt: Receipt): string {
  if (receipt.days === 1)
    return `${formatReceiptInstant(receipt.startsAtMs, receipt.timeZone)} – ${formatReceiptInstant(receipt.endsAtMs, receipt.timeZone)}`;
  return `${receipt.sinceDay.replaceAll("-", ".")} – ${receipt.untilDay.replaceAll("-", ".")}`;
}

/**
 * The receipt itself: items, payment lines, a few totals and a scannable summary code.
 *
 * @param props - Receipt data, a ref for exports, whether amounts are withheld, and an
 * optional code that replaces the summary code, such as a share link.
 * @returns The receipt paper element.
 */
export function ReceiptPaper({
  receipt,
  paperRef,
  hideAmounts = false,
  code,
}: {
  receipt: Receipt;
  paperRef?: RefObject<HTMLDivElement | null>;
  hideAmounts?: boolean;
  code?: { modules: readonly string[]; label: string };
}) {
  const shownCode = code ?? {
    modules: receipt.qrModules,
    label: t("Scan for the summary"),
  };
  const isHourly = receipt.days === 1;
  const stats = [
    ...(isHourly
      ? []
      : [{ label: t("Active days"), value: formatCount(receipt.activeDays) }]),
    { label: t("Sessions"), value: formatCount(receipt.sessions) },
    { label: "TOKENS", value: formatTokens(receipt.tokens) },
    ...(hideAmounts
      ? []
      : [
          {
            label: t("Cache saved"),
            value: formatUsd(receipt.cacheSavingsUsd),
          },
        ]),
  ];
  return (
    <div
      ref={paperRef}
      className="receipt-paper"
      style={{ "--receipt-stat-columns": isHourly ? 3 : 2 } as CSSProperties}
      aria-label={t("Receipt {label}", { label: receipt.number })}
    >
      <div className="receipt-center receipt-brand">YOUSAGE</div>
      <div className="receipt-center receipt-kind">
        {isHourly
          ? t("24-HOUR RECEIPT")
          : t("{days}-DAY RECEIPT", { days: receipt.days })}
      </div>
      <div className="receipt-meta">
        <span>{receiptRange(receipt)}</span>
      </div>
      <div className="receipt-window">No. {receipt.number}</div>
      <hr className="receipt-rule" />
      {receipt.items.length === 0 ? (
        <div className="receipt-empty">{t("Nothing purchased")}</div>
      ) : (
        receipt.items.map((item) => (
          <div className="receipt-item" key={`${item.provider}:${item.model}`}>
            <div className="receipt-row">
              <span className="receipt-item-name" title={item.model}>
                {item.model}
              </span>
              <span className="receipt-number">
                {hideAmounts
                  ? formatTokens(item.tokens)
                  : item.costUsd === 0 && item.unpricedRecords > 0
                    ? t("Unpriced")
                    : formatUsd(item.costUsd)}
              </span>
            </div>
            {!hideAmounts && (
              <div className="receipt-faint receipt-small">
                {formatTokens(item.tokens)} tokens
              </div>
            )}
          </div>
        ))
      )}
      <hr className="receipt-rule-double" />
      <div className="receipt-row receipt-total">
        <span>{t("Total")}</span>
        <span className="receipt-number">
          {hideAmounts
            ? `${formatTokens(receipt.tokens)} tokens`
            : formatUsd(receipt.totalUsd)}
        </span>
      </div>
      {!hideAmounts && receipt.subscriptionUsd > 0 && (
        <div className="receipt-row receipt-faint">
          <span>{t("Paid by subscription")}</span>
          <span className="receipt-number">
            −{formatUsd(receipt.subscriptionUsd)}
          </span>
        </div>
      )}
      {!hideAmounts && (
        <div className="receipt-row receipt-strong">
          <span>{t("Amount paid")}</span>
          <span className="receipt-number">{formatUsd(receipt.paidUsd)}</span>
        </div>
      )}
      <hr className="receipt-rule" />
      <div className="receipt-stats">
        {stats.map((stat) => (
          <div key={stat.label}>
            <b className="receipt-number">{stat.value}</b>
            <span>{stat.label}</span>
          </div>
        ))}
      </div>
      {shownCode.modules.length > 0 && (
        <>
          <div className="receipt-code">
            <SummaryCode modules={shownCode.modules} />
          </div>
          <div className="receipt-center receipt-faint receipt-small">
            {shownCode.label}
          </div>
        </>
      )}
      <div className="receipt-center receipt-thanks">
        {t("Thank you · come again")}
      </div>
    </div>
  );
}

/** Light quiet zone, in modules, that scanners need around the symbol. */
const QUIET_ZONE = 2;

/**
 * Draw a QR matrix as dark modules on a light tile, so it scans in either theme.
 *
 * @param props - Matrix rows of `1` (dark) and `0` (light).
 * @returns The code as an SVG element.
 */
export function SummaryCode({ modules }: { modules: readonly string[] }) {
  const size = modules.length + QUIET_ZONE * 2;
  let path = "";
  modules.forEach((row, rowIndex) => {
    for (let column = 0; column < row.length; column++)
      if (row[column] === "1")
        path += `M${column + QUIET_ZONE} ${rowIndex + QUIET_ZONE}h1v1h-1z`;
  });
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      viewBox={`0 0 ${size} ${size}`}
      shapeRendering="crispEdges"
      role="img"
      aria-label={t("Summary code")}
    >
      <rect width={size} height={size} fill="#ffffff" />
      <path d={path} fill="#1f1e1c" />
    </svg>
  );
}
