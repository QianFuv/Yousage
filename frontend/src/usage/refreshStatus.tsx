/** Page-header freshness shared by every page that the global refresh feeds. */
import type { ReactNode } from "react";
import { RefreshCwIcon } from "lucide-react";
import { Button } from "../../../tools/parity/upstream/apps/web/src/components/ui/button";
import { msg, t } from "../i18n";
import { useData, type RefreshState } from "./controller";
import { hour12ForTimestampFormat } from "./host";

/** Fixed failure notices the desktop scheduler publishes, listed for translation. */
export const REFRESH_FAILURES = [
  msg(
    "Usage or limits could not be fully refreshed. Last successful data is retained when available.",
  ),
  msg("Refresh was cancelled."),
];

/**
 * Describe the scheduler state for a page header without replacing page content.
 *
 * @param state - The latest published refresh state.
 * @param timestampFormat - The saved timestamp preference: `12h`, `24h` or `locale`.
 * @returns Progress, a failure with its sanitized cause as a tooltip, the last completion time, or null.
 */
export function describeRefresh(
  state: RefreshState,
  timestampFormat: string,
): ReactNode {
  if (state.isRefreshing) return t("Refreshing…");
  if (state.lastError)
    return (
      <span className="text-destructive-foreground" title={t(state.lastError)}>
        {t("Refresh failed")}
      </span>
    );
  const completed = new Date(state.lastCompletedAt);
  if (!state.lastCompletedAt || Number.isNaN(completed.getTime())) return null;
  return t("Updated {time}", {
    time: completed.toLocaleTimeString(undefined, {
      hour: "2-digit",
      minute: "2-digit",
      hour12: hour12ForTimestampFormat(timestampFormat),
    }),
  });
}

/**
 * Read the shared refresh state as a page-header status.
 *
 * @returns The header status for the current refresh state.
 */
export function useRefreshStatus(): ReactNode {
  const data = useData();
  return describeRefresh(data.refreshState, data.display.value.timestampFormat);
}

/**
 * Header button that asks for one refresh now, beside the automatic refreshes.
 *
 * It spins and stays disabled while any refresh, automatic or manual, is running.
 *
 * @returns The refresh icon button.
 */
export function RefreshButton() {
  const data = useData();
  const isRefreshing = data.refreshState.isRefreshing;
  return (
    <Button
      variant="ghost"
      size="icon-sm"
      aria-label={t("Refresh now")}
      title={t("Refresh now")}
      disabled={isRefreshing}
      onClick={() => void data.refresh()}
    >
      <RefreshCwIcon
        aria-hidden
        className={isRefreshing ? "animate-spin" : undefined}
      />
    </Button>
  );
}
