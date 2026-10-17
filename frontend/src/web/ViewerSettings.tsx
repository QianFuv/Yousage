/** Settings page of the read-only viewer: snapshot freshness, the relay link and the app version. */
import { RefreshCwIcon, Unlink2Icon } from "lucide-react";
import { Button } from "../../../tools/parity/upstream/apps/web/src/components/ui/button";
import { cn } from "../../../tools/parity/src/bridges/utils";
import { useData } from "../usage/controller";
import { t, useI18n } from "../i18n";
import { PageBody, PageHeader, SettingsRow, SettingsSection } from "../ui";

/** The relay a viewer reads from, and how to replace its link when the host allows it. */
export type ViewerConnection = {
  /** Relay origin shown to the reader. */
  relay: string;
  /** Ask for another link; the saved one stays until a new one connects. */
  onChangeLink?: () => void;
};

/**
 * Format the snapshot time in the viewer's locale.
 *
 * @param instant - RFC 3339 time the desktop generated the snapshot.
 * @param locale - Viewing device locale.
 * @returns A short date and time, or the raw value when it cannot be parsed.
 */
export function formatSnapshotTime(instant: string, locale: string): string {
  const date = new Date(instant);
  if (Number.isNaN(date.getTime())) return instant;
  return new Intl.DateTimeFormat(locale, {
    month: "numeric",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date);
}

/**
 * Snapshot details and connection controls for the viewer.
 *
 * @param props - Snapshot time and version, viewing locale and the relay connection.
 * @returns The settings page.
 */
export function ViewerSettings({
  generatedAt,
  appVersion,
  locale,
  connection,
}: {
  generatedAt: string;
  appVersion: string;
  locale: string;
  connection?: ViewerConnection;
}) {
  useI18n();
  const data = useData();
  return (
    <section aria-label={t("Settings")}>
      <PageHeader title={t("Settings")} />
      <PageBody width="narrow">
        <SettingsSection title={t("Data")}>
          <SettingsRow
            title={t("Last updated")}
            description={t(
              "The desktop app uploads a new snapshot after each refresh.",
            )}
            control={
              <span className="text-sm font-medium tabular-nums">
                {formatSnapshotTime(generatedAt, locale)}
              </span>
            }
          />
          <SettingsRow
            title={t("Check for a newer snapshot")}
            description={t(
              "Reads the relay again. It does not make the desktop refresh.",
            )}
            control={
              <Button
                variant="outline"
                size="sm"
                disabled={data.isBusy}
                onClick={() => void data.refresh(true, true)}
              >
                <RefreshCwIcon
                  aria-hidden
                  className={cn(data.isBusy && "animate-spin")}
                />
                {t("Refresh")}
              </Button>
            }
          />
        </SettingsSection>
        {connection && (
          <SettingsSection title={t("Connection")}>
            <SettingsRow
              title={t("Relay")}
              description={
                <span className="[overflow-wrap:anywhere]">
                  {connection.relay}
                </span>
              }
              control={
                connection.onChangeLink && (
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={connection.onChangeLink}
                  >
                    <Unlink2Icon aria-hidden />
                    {t("Change link")}
                  </Button>
                )
              }
            />
          </SettingsSection>
        )}
        <SettingsSection title={t("About")}>
          <SettingsRow
            title="Yousage"
            description={t(
              "Read-only view of a snapshot from the desktop app. Changes made here are not saved.",
            )}
            control={
              appVersion && (
                <span className="text-xs text-muted-foreground tabular-nums">
                  {t("Desktop {version}", { version: appVersion })}
                </span>
              )
            }
          />
        </SettingsSection>
      </PageBody>
    </section>
  );
}
