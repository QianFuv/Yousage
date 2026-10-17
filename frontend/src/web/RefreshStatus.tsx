/** A small floating note while the viewer refreshes a saved snapshot, or when it could not. */
import { CircleAlertIcon, RefreshCwIcon } from "lucide-react";
import { t, useI18n } from "../i18n";
import type { FollowStatus } from "./follow";
import { formatSnapshotTime } from "./ViewerSettings";

/**
 * Show refresh progress over the pages; nothing while the snapshot is current.
 *
 * @param props - The status, the shown snapshot's time, the viewing locale and a retry action.
 * @returns The note, or nothing.
 */
export function RefreshStatus({
  status,
  generatedAt,
  locale,
  onRetry,
}: {
  status: FollowStatus;
  generatedAt: string;
  locale: string;
  onRetry: () => void;
}) {
  useI18n();
  if (status.state === "current") return null;
  if (status.state === "refreshing")
    return (
      <div className="viewer-refresh-status" role="status">
        <RefreshCwIcon aria-hidden className="animate-spin" />
        {t("Refreshing…")}
      </div>
    );
  return (
    <div className="viewer-refresh-status" data-state="failed" role="status">
      <CircleAlertIcon aria-hidden />
      <span>
        {status.problem}
        <small>
          {t("Showing data from {time}", {
            time: formatSnapshotTime(generatedAt, locale),
          })}
        </small>
      </span>
      <button type="button" onClick={onRetry}>
        {t("Try again")}
      </button>
    </div>
  );
}
