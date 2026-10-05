/** Native action adapters for original quota renderers without app atoms or browser credentials. */
import { formatUpcomingTimestamp as upstreamUpcomingTimestamp } from "../../vendor/timestampFormat";
import { useData } from "./controller";
import { useRef, useState } from "react";
import * as nativeApi from "../../bindings/yousage/app";
import { Request } from "../../bindings/yousage/internal/refresh/models";
import { RedeemRoute } from "../../bindings/yousage/internal/quota/models";
import type { ProviderConsumeResetCreditInput } from "./contracts";

/** Format a verified upcoming instant using the selected timestamp presentation. */
export function formatUpcomingTimestamp(
  instant: string,
  format: string,
  now: number,
): string {
  return upstreamUpcomingTimestamp(
    instant,
    format === "12h" ? "12-hour" : format === "24h" ? "24-hour" : "locale",
    now,
  );
}
/** Supply the desktop timestamp preference to original renderer selectors. */
export function usePrimarySettings<T>(
  select: (settings: { timestampFormat: string }) => T,
): T {
  return select({ timestampFormat: useData().display.value.timestampFormat });
}
/** Open only an explicit user-selected account destination through the native shell. */
export function ensureLocalApi() {
  return {
    shell: { openExternal: (url: string) => nativeApi.OpenExternal(url) },
  };
}
/** Keep labels equivalent across native and pooled account glyphs. */
export function getDriverOption(driver: string) {
  return {
    label:
      (
        {
          codex: "Codex",
          claudeAgent: "Claude Code",
          cursor: "Cursor",
          grok: "Grok Build",
          opencode: "OpenCode",
          antigravity: "Antigravity",
        } as Record<string, string>
      )[driver] ?? driver,
  };
}
/** Reveal account text only after an explicit visibility toggle. */
export function RedactedSensitiveText({
  value,
  className,
  ariaLabel,
  revealTooltip,
  hideTooltip,
}: {
  value: string;
  className?: string;
  ariaLabel?: string;
  revealTooltip?: string;
  hideTooltip?: string;
}) {
  const [isRevealed, setIsRevealed] = useState(false);
  return (
    <button
      type="button"
      aria-label={ariaLabel}
      aria-pressed={isRevealed}
      title={isRevealed ? hideTooltip : revealTooltip}
      onClick={() => setIsRevealed(!isRevealed)}
      className={className}
    >
      <span className={isRevealed ? "" : "blur-sm select-none"}>{value}</span>
    </button>
  );
}
const RESET_OPERATIONS = new Map<string, string>();
const OUTCOMES: Record<string, string> = {
  reset: "Reset applied. Your windows have cleared.",
  nothingToReset: "Nothing to reset right now.",
  noCredit: "No reset credit left.",
  alreadyRedeemed: "That credit was already redeemed.",
  unknown:
    "The reset outcome is unknown. Refresh limits before deciding to retry explicitly.",
};

/** Preserve a caller operation identity through ambiguous results and explicit retries. */
export function useResetCredit(
  environmentId: string,
  input: ProviderConsumeResetCreditInput,
) {
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);
  const [status, setStatus] = useState<string | null>(null);
  const attempted = useRef(false);
  const isWorking = useRef(false);
  const identity = JSON.stringify([environmentId, input]);
  /** Redeem only after confirmation; a subsequent confirmation is an explicit retry of the same operation. */
  async function redeem() {
    if (isWorking.current) return;
    isWorking.current = true;
    setConfirming(false);
    setBusy(true);
    setStatus(null);
    let operationId = RESET_OPERATIONS.get(identity);
    if (!operationId) {
      operationId = crypto.randomUUID();
      RESET_OPERATIONS.set(identity, operationId);
    }
    try {
      const result = await nativeApi.ConsumeResetCredit(
        RedeemRoute.createFrom(JSON.stringify({ environmentId, ...input })),
        operationId,
        attempted.current,
      );
      attempted.current = true;
      await nativeApi
        .RefreshLimits(
          [environmentId],
          Request.createFrom({
            IsManual: true,
            IsVisible: document.visibilityState === "visible",
            IsUsagePage: true,
            IsAutoEnabled: false,
            AfterPending: true,
          }),
        )
        .catch(() => {});
      setStatus(
        result.warning ||
          OUTCOMES[result.outcome] ||
          "Could not use the reset credit.",
      );
    } catch (cause) {
      attempted.current = true;
      setStatus(String(cause));
    } finally {
      isWorking.current = false;
      setBusy(false);
    }
  }
  return { confirming, setConfirming, busy, status, redeem };
}
