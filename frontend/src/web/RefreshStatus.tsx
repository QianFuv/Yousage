/** A small floating note when the viewer could not refresh the snapshot it shows. */
import { CircleAlertIcon } from "lucide-react";
import { t, useI18n } from "../i18n";
import type { FollowStatus } from "./follow";
import { formatSnapshotTime } from "./ViewerSettings";

/**
 * Explain a failed refresh over the pages; progress stays with the pages' own refresh buttons.
 *
 * @param props - The status, the shown snapshot's time, the viewing locale and a retry action.
 * @returns The note, or nothing unless the last shown refresh failed.
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
  if (status.state !== "failed") return null;
  return (
    <div className="viewer-refresh-status" role="status">
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
