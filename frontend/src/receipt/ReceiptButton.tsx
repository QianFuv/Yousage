/** Receipt button beside the Usage total: a hover preview and a printed, torn-off receipt. */
import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type CSSProperties,
} from "react";
import { createPortal } from "react-dom";
import { Events } from "@wailsio/runtime";
import { CopyIcon, ReceiptTextIcon } from "lucide-react";
import { Button } from "../../../tools/parity/upstream/apps/web/src/components/ui/button";
import { t } from "../i18n";
import { usePeriodDays } from "../usage/PeriodToggle";
import * as nativeApi from "../../bindings/yousage/app";
import type { Receipt } from "../../bindings/yousage/internal/service/models";
import { ReceiptPaper } from "./ReceiptPaper";
import { copyReceiptImage } from "./exportReceipt";
import "./printer.css";

/** Time for the paper to feed out of the printer. */
const PRINT_MS = 1800;

/** Time for the tug that tears the paper off. */
const TEAR_MS = 650;

/** Confetti pieces fired from each lower corner. */
const CONFETTI_PER_SIDE = 60;

/** Confetti colors: the theme accent, the provider marks and a few bright hues. */
const CONFETTI_COLORS = [
  "var(--primary)",
  "var(--foreground)",
  "#d97757",
  "#f5c542",
  "#4fb3a9",
  "#7c8cf8",
  "#e86fa0",
] as const;

/** Phases of the printing overlay; loading paints the paper before anything moves. */
type PrintPhase = "loading" | "printing" | "tearing" | "shown";

/**
 * A random number in a range.
 *
 * @param low - Inclusive lower bound.
 * @param high - Exclusive upper bound.
 * @returns The random number.
 */
function between(low: number, high: number): number {
  return low + Math.random() * (high - low);
}

/**
 * Random confetti launched inward and upward from both lower corners.
 *
 * @returns Inline styles of every piece.
 */
function createConfetti(): CSSProperties[] {
  return Array.from({ length: CONFETTI_PER_SIDE * 2 }, (_, index) => {
    const direction = index % 2 === 0 ? 1 : -1;
    return {
      left: direction === 1 ? "6%" : "94%",
      "--x": `${direction * between(8, 48)}vw`,
      "--peak": `${-between(50, 92)}vh`,
      "--fall": `${-between(0, 35)}vh`,
      "--spin": `${direction * between(360, 1260)}deg`,
      "--size": `${between(7, 13)}px`,
      "--duration": `${between(1800, 2900)}ms`,
      "--delay": `${between(0, 220)}ms`,
      "--color": CONFETTI_COLORS[index % CONFETTI_COLORS.length],
    } as CSSProperties;
  });
}

/**
 * Small receipt button that previews the receipt on hover and prints it on click.
 *
 * The receipt covers the shared Usage period and is read ahead, so the preview appears immediately.
 *
 * @returns The button with its preview and overlay.
 */
export function ReceiptButton() {
  const days = usePeriodDays();
  const zone = Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  const [receipt, setReceipt] = useState<Receipt | null>(null);
  const [error, setError] = useState("");
  const [isHovered, setIsHovered] = useState(false);
  const [isOpen, setIsOpen] = useState(false);
  const request = useRef(0);

  /** Read the receipt of the selected period, ignoring superseded answers. */
  const load = useCallback(async () => {
    const identity = ++request.current;
    try {
      const value = await nativeApi.GetReceipt(days, zone);
      if (identity !== request.current) return;
      setReceipt(value);
      setError("");
    } catch (cause) {
      if (identity === request.current) setError(String(cause));
    }
  }, [days, zone]);

  useEffect(() => {
    void load();
    return Events.On("usage-invalidated", () => void load());
  }, [load]);

  const shown = receipt?.days === days ? receipt : null;
  return (
    <span
      className="relative inline-flex"
      onMouseEnter={() => setIsHovered(true)}
      onMouseLeave={() => setIsHovered(false)}
    >
      <Button
        variant="ghost"
        size="icon-sm"
        aria-label={error || t("Print receipt")}
        disabled={!shown}
        onClick={() => {
          setIsHovered(false);
          setIsOpen(true);
        }}
      >
        <ReceiptTextIcon aria-hidden />
      </Button>
      {shown && (
        <div
          className="receipt-preview"
          data-visible={isHovered && !isOpen}
          aria-hidden
        >
          <div className="receipt-preview-scale receipt-lift">
            <ReceiptPaper receipt={shown} />
          </div>
        </div>
      )}
      {isOpen && shown && (
        <ReceiptPrinter receipt={shown} onClose={() => setIsOpen(false)} />
      )}
    </span>
  );
}

/**
 * Full-window overlay that prints the receipt, tears it off and celebrates with confetti.
 *
 * Reduced-motion users get the finished receipt without animation.
 *
 * @param props - The receipt and a close handler.
 * @returns The overlay, rendered into the document body.
 */
function ReceiptPrinter({
  receipt,
  onClose,
}: {
  receipt: Receipt;
  onClose: () => void;
}) {
  const [isReducedMotion] = useState(
    () => window.matchMedia("(prefers-reduced-motion: reduce)").matches,
  );
  const [phase, setPhase] = useState<PrintPhase>(
    isReducedMotion ? "shown" : "loading",
  );
  const [confetti] = useState(createConfetti);
  const [status, setStatus] = useState("");
  const paper = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (isReducedMotion) return;
    const timers: number[] = [];
    let frame = requestAnimationFrame(() => {
      frame = requestAnimationFrame(() => {
        setPhase("printing");
        timers.push(
          window.setTimeout(() => setPhase("tearing"), PRINT_MS),
          window.setTimeout(() => setPhase("shown"), PRINT_MS + TEAR_MS),
        );
      });
    });
    return () => {
      cancelAnimationFrame(frame);
      timers.forEach((timer) => window.clearTimeout(timer));
    };
  }, [isReducedMotion]);

  useEffect(() => {
    /** Close on Escape. */
    const pressed = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    window.addEventListener("keydown", pressed);
    return () => window.removeEventListener("keydown", pressed);
  }, [onClose]);

  useEffect(() => {
    if (!status) return;
    const timer = window.setTimeout(() => setStatus(""), 2000);
    return () => window.clearTimeout(timer);
  }, [status]);

  return createPortal(
    <>
      <div
        className="receipt-overlay"
        data-phase={phase}
        role="dialog"
        aria-modal
        aria-label={t("Print receipt")}
        style={
          {
            "--receipt-print-ms": `${PRINT_MS}ms`,
            "--receipt-tear-ms": `${TEAR_MS}ms`,
          } as CSSProperties
        }
        onClick={(event) => {
          if (event.target === event.currentTarget) onClose();
        }}
      >
        <div className="receipt-printer" aria-hidden />
        <div className="receipt-slot">
          <div className="receipt-feed receipt-lift">
            <ReceiptPaper receipt={receipt} paperRef={paper} />
          </div>
        </div>
        <div className="receipt-actions">
          <Button
            variant="outline"
            size="sm"
            onClick={async () => {
              if (!paper.current) return;
              try {
                await copyReceiptImage(paper.current);
                setStatus(t("Copied"));
              } catch (cause) {
                setStatus(String(cause));
              }
            }}
          >
            <CopyIcon aria-hidden />
            {status || t("Copy as PNG")}
          </Button>
          <Button variant="ghost" size="sm" onClick={onClose}>
            {t("Close")}
          </Button>
        </div>
      </div>
      {!isReducedMotion && (
        <div className="receipt-confetti" aria-hidden>
          {confetti.map((style, index) => (
            <span key={index} style={style} />
          ))}
        </div>
      )}
    </>,
    document.body,
  );
}
