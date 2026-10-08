/** Daily and monthly receipts of local usage, printed on themed receipt paper. */
import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type CSSProperties,
  type RefObject,
} from "react";
import { Events } from "@wailsio/runtime";
import { ChevronLeftIcon, ChevronRightIcon, CopyIcon } from "lucide-react";
import {
  formatCount,
  formatTokens,
  formatUsd,
} from "@t3tools/shared/usageFormat";
import { Button } from "../../../tools/parity/upstream/apps/web/src/components/ui/button";
import {
  Toggle,
  ToggleGroup,
} from "../../../tools/parity/upstream/apps/web/src/components/ui/toggle-group";
import { Notice, OptionSelect, PageBody, PageHeader } from "../ui";
import { formattingLocale, t } from "../i18n";
import { useData } from "../usage/controller";
import * as nativeApi from "../../bindings/yousage/app";
import type { Receipt } from "../../bindings/yousage/internal/service/models";
import { copyReceiptImage } from "./exportReceipt";
import "./receipt.css";

/** Receipt period selected in the page header. */
type ReceiptPeriod = "day" | "month";

/** Milliseconds in one hour. */
const HOUR_MS = 3_600_000;

/** Hours at which a receipt day can begin. */
const DAY_START_HOURS = Array.from({ length: 24 }, (_, hour) => hour);

/**
 * The receipt day that contains now, when days begin at a given hour.
 *
 * @param timeZone - IANA time zone.
 * @param dayStartHour - Hour of the day at which a receipt day begins.
 * @returns The day in `YYYY-MM-DD` form.
 */
function currentDay(timeZone: string, dayStartHour: number): string {
  return new Intl.DateTimeFormat("en-CA", {
    timeZone,
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
  }).format(new Date(Date.now() - dayStartHour * HOUR_MS));
}

/**
 * Format an instant as a 24-hour `HH:mm` time.
 *
 * @param instantMs - Unix time in milliseconds.
 * @param timeZone - IANA time zone.
 * @returns The wall-clock time.
 */
function formatClock(instantMs: number, timeZone: string): string {
  return new Intl.DateTimeFormat("en-GB", {
    timeZone,
    hour: "2-digit",
    minute: "2-digit",
    hourCycle: "h23",
  }).format(new Date(instantMs));
}

/**
 * Format a receipt date in the active language.
 *
 * @param date - `YYYY-MM-DD` or `YYYY-MM`.
 * @param options - Date fields to show.
 * @returns The localized date, or the input when it is malformed.
 */
function formatReceiptDate(
  date: string,
  options: Intl.DateTimeFormatOptions,
): string {
  const [year, month, day = 1] = date.split("-").map(Number);
  if (!year || !month) return date;
  return new Intl.DateTimeFormat(formattingLocale(), {
    timeZone: "UTC",
    ...options,
  }).format(new Date(Date.UTC(year, month - 1, day)));
}

/** Header label fields: `Thu, Oct 8` or `October 2026`. */
const HEADER_DATE: Record<ReceiptPeriod, Intl.DateTimeFormatOptions> = {
  day: { month: "short", day: "numeric", weekday: "short" },
  month: { year: "numeric", month: "long" },
};

/**
 * Receipts page: one receipt per day or month, with a configurable day start and PNG copy.
 *
 * @param props - Whether the page is the visible route.
 * @returns The receipts page.
 */
export function ReceiptPage({ isActive }: { isActive: boolean }) {
  const data = useData();
  const dayStartHour = data.display.value.receiptDayStartHour ?? 0;
  const zone = Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  const [period, setPeriod] = useState<ReceiptPeriod>("day");
  const [day, setDay] = useState(() => currentDay(zone, dayStartHour));
  const [month, setMonth] = useState(() =>
    currentDay(zone, dayStartHour).slice(0, 7),
  );
  const date = period === "day" ? day : month;
  const [receipt, setReceipt] = useState<Receipt | null>(null);
  const [error, setError] = useState("");
  const [isLoading, setIsLoading] = useState(false);
  const [actionStatus, setActionStatus] = useState("");
  const paper = useRef<HTMLDivElement>(null);
  const request = useRef(0);

  /** Read the selected receipt, ignoring answers to superseded selections. */
  const load = useCallback(async () => {
    const identity = ++request.current;
    setIsLoading(true);
    try {
      const value = await nativeApi.GetReceipt(
        period,
        date,
        zone,
        dayStartHour,
      );
      if (identity !== request.current) return;
      setReceipt(value);
      setError("");
    } catch (cause) {
      if (identity === request.current) setError(String(cause));
    } finally {
      if (identity === request.current) setIsLoading(false);
    }
  }, [period, date, zone, dayStartHour]);

  useEffect(() => {
    if (!isActive) return;
    void load();
    return Events.On("usage-invalidated", () => void load());
  }, [isActive, load]);

  useEffect(() => {
    if (!actionStatus) return;
    const timer = window.setTimeout(() => setActionStatus(""), 2000);
    return () => window.clearTimeout(timer);
  }, [actionStatus]);

  const today = currentDay(zone, dayStartHour);
  const current = period === "day" ? today : today.slice(0, 7);
  const shown = receipt?.period === period ? receipt : null;
  /** Move the selection to another date of the current period. */
  const select = (next: string) =>
    period === "day" ? setDay(next) : setMonth(next);

  return (
    <section className="receipt-page" aria-label={t("Receipts")}>
      <PageHeader
        title={t("Receipts")}
        status={isLoading && !shown ? t("Loading…") : actionStatus || null}
      >
        <ToggleGroup
          aria-label={t("Receipt period")}
          variant="segmented"
          value={[period]}
          onValueChange={(next) => {
            const value = next[0];
            if (value === "day" || value === "month") {
              if (value === "month") setMonth(day.slice(0, 7));
              setPeriod(value);
            }
          }}
        >
          <Toggle value="day">{t("Daily receipt")}</Toggle>
          <Toggle value="month">{t("Monthly receipt")}</Toggle>
        </ToggleGroup>
        <div className="flex items-center gap-1">
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label={
              period === "day" ? t("Previous day") : t("Previous month")
            }
            title={period === "day" ? t("Previous day") : t("Previous month")}
            disabled={!shown?.previousDate}
            onClick={() => shown?.previousDate && select(shown.previousDate)}
          >
            <ChevronLeftIcon aria-hidden />
          </Button>
          <span className="min-w-28 text-center text-sm tabular-nums">
            {formatReceiptDate(date, HEADER_DATE[period])}
          </span>
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label={period === "day" ? t("Next day") : t("Next month")}
            title={period === "day" ? t("Next day") : t("Next month")}
            disabled={!shown?.nextDate}
            onClick={() => shown?.nextDate && select(shown.nextDate)}
          >
            <ChevronRightIcon aria-hidden />
          </Button>
        </div>
        {date !== current && (
          <Button size="xs" variant="outline" onClick={() => select(current)}>
            {period === "day" ? t("Today") : t("This month")}
          </Button>
        )}
        <OptionSelect
          label={t("Day starts at")}
          value={dayStartHour}
          options={DAY_START_HOURS.map((hour) => ({
            value: hour,
            label: t("Starts {time}", {
              time: `${String(hour).padStart(2, "0")}:00`,
            }),
          }))}
          className="min-w-24"
          onChange={(hour) => {
            const next = currentDay(zone, hour);
            if (day === today) setDay(next);
            if (month === today.slice(0, 7)) setMonth(next.slice(0, 7));
            void data
              .patchDisplay({ receiptDayStartHour: hour })
              .catch((cause) => setError(String(cause)));
          }}
        />
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label={t("Copy as PNG")}
          title={t("Copy as PNG")}
          disabled={!shown}
          onClick={async () => {
            if (!paper.current) return;
            try {
              await copyReceiptImage(paper.current);
              setActionStatus(t("Copied"));
            } catch (cause) {
              setError(String(cause));
            }
          }}
        >
          <CopyIcon aria-hidden />
        </Button>
      </PageHeader>
      <PageBody>
        {error && <Notice tone="error">{error}</Notice>}
        {shown && (
          <div className="receipt-shadow flex justify-center">
            <ReceiptPaper receipt={shown} paperRef={paper} />
          </div>
        )}
      </PageBody>
    </section>
  );
}

/**
 * The receipt itself: items, payment lines, a few totals and a scannable summary code.
 *
 * @param props - Receipt data and a ref for exports.
 * @returns The receipt paper element.
 */
function ReceiptPaper({
  receipt,
  paperRef,
}: {
  receipt: Receipt;
  paperRef: RefObject<HTMLDivElement | null>;
}) {
  const isDay = receipt.period === "day";
  const dateLabel = isDay
    ? `${receipt.label.replaceAll("-", ".")} ${formatReceiptDate(receipt.label, { weekday: "short" })}`
    : receipt.label.replace("-", ".");
  const hours =
    receipt.dayStartHour > 0
      ? isDay
        ? t("{start} to {end} the next day", {
            start: formatClock(receipt.startsAtMs, receipt.timeZone),
            end: formatClock(receipt.endsAtMs, receipt.timeZone),
          })
        : t("Days start at {time}", {
            time: formatClock(receipt.startsAtMs, receipt.timeZone),
          })
      : "";
  const stats = [
    ...(isDay
      ? []
      : [{ label: t("Active days"), value: formatCount(receipt.activeDays) }]),
    { label: t("Sessions"), value: formatCount(receipt.sessions) },
    { label: "TOKENS", value: formatTokens(receipt.tokens) },
    { label: t("Cache saved"), value: formatUsd(receipt.cacheSavingsUsd) },
  ];
  return (
    <div
      ref={paperRef}
      className="receipt-paper"
      style={{ "--receipt-stat-columns": isDay ? 3 : 2 } as CSSProperties}
      aria-label={t("Receipt {label}", { label: receipt.label })}
    >
      <div className="receipt-center receipt-brand">YOUSAGE</div>
      <div className="receipt-center receipt-kind">
        {isDay ? t("DAILY RECEIPT") : t("MONTHLY RECEIPT")}
      </div>
      <div className="receipt-meta">
        <span>{dateLabel}</span>
        <span>No. {receipt.number}</span>
      </div>
      {hours && <div className="receipt-window">{hours}</div>}
      <hr className="receipt-rule" />
      {receipt.items.length === 0 ? (
        <div className="receipt-empty">
          {isDay ? t("Nothing purchased today") : t("Nothing purchased")}
        </div>
      ) : (
        receipt.items.map((item) => (
          <div className="receipt-item" key={`${item.provider}:${item.model}`}>
            <div className="receipt-row">
              <span className="receipt-item-name" title={item.model}>
                {item.model}
              </span>
              <span className="receipt-number">
                {item.costUsd === 0 && item.unpricedRecords > 0
                  ? t("Unpriced")
                  : formatUsd(item.costUsd)}
              </span>
            </div>
            <div className="receipt-faint receipt-small">
              {formatTokens(item.tokens)} tokens
            </div>
          </div>
        ))
      )}
      <hr className="receipt-rule-double" />
      <div className="receipt-row receipt-total">
        <span>{t("Total")}</span>
        <span className="receipt-number">{formatUsd(receipt.totalUsd)}</span>
      </div>
      {receipt.subscriptionUsd > 0 && (
        <div className="receipt-row receipt-faint">
          <span>{t("Paid by subscription")}</span>
          <span className="receipt-number">
            −{formatUsd(receipt.subscriptionUsd)}
          </span>
        </div>
      )}
      <div className="receipt-row receipt-strong">
        <span>{t("Amount paid")}</span>
        <span className="receipt-number">{formatUsd(receipt.paidUsd)}</span>
      </div>
      <hr className="receipt-rule" />
      <div className="receipt-stats">
        {stats.map((stat) => (
          <div key={stat.label}>
            <b className="receipt-number">{stat.value}</b>
            <span>{stat.label}</span>
          </div>
        ))}
      </div>
      {receipt.qrModules.length > 0 && (
        <>
          <div className="receipt-code">
            <SummaryCode modules={receipt.qrModules} />
          </div>
          <div className="receipt-center receipt-faint receipt-small">
            {t("Scan for the summary")}
          </div>
        </>
      )}
      <div className="receipt-center receipt-thanks">
        {isDay
          ? t("Thank you · see you tomorrow")
          : t("Thank you · see you next month")}
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
function SummaryCode({ modules }: { modules: readonly string[] }) {
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
