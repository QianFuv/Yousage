/** About Yousage and its local diagnostics report, exported only by an explicit native action. */
import { useEffect, useState } from "react";
import {
  CopyIcon,
  DownloadIcon,
  ExternalLinkIcon,
  RefreshCwIcon,
} from "lucide-react";
import * as nativeApi from "../../bindings/yousage/internal/desktop/app";
import { t } from "../i18n";
import { notify, notifyError } from "../notify";
import {
  Notice,
  PageBody,
  PageHeader,
  SettingsRow,
  SettingsSection,
} from "../ui";
import { Button } from "../../../tools/parity/upstream/apps/web/src/components/ui/button";

/** Public project page of Yousage. */
export const PROJECT_URL = "https://github.com/QianFuv/Yousage";

/** Where problems with Yousage are reported. */
export const ISSUES_URL = `${PROJECT_URL}/issues`;

/** The whitelisted fields of the backend diagnostics report. */
type DiagnosticSummary = {
  version?: string;
  platform?: string;
  historySources?: number;
  quotaTargets?: number;
  quotaHubs?: number;
  metadataAvailable?: boolean;
  logs?: string;
};

/**
 * Read the report's fields for display, keeping the raw text as the logs when it is not JSON.
 *
 * @param report - The report exactly as the backend returned it.
 * @returns The parsed fields.
 */
export function parseDiagnosticReport(report: string): DiagnosticSummary {
  try {
    const value: unknown = JSON.parse(report);
    return typeof value === "object" && value !== null
      ? (value as DiagnosticSummary)
      : { logs: report };
  } catch {
    return { logs: report };
  }
}

/**
 * Settings page about Yousage, with its redacted diagnostics report and recent logs.
 *
 * @param props - A native startup problem to show above the page, if any.
 * @returns The About & diagnostics page.
 */
export function DiagnosticsPanel({ nativeStatus }: { nativeStatus: string }) {
  const [report, setReport] = useState("");
  const [version, setVersion] = useState("");
  useEffect(() => {
    void nativeApi.GetDiagnostics().then(setReport).catch(notifyError);
    void nativeApi.GetAppVersion().then(setVersion).catch(notifyError);
  }, []);
  /** Show only the actual outcome of the explicit native operation. */
  async function act(operation: () => Promise<unknown>, message: string) {
    try {
      const result = await operation();
      if (result === false) notify(t("Canceled."), "info");
      else notify(message);
    } catch (cause) {
      notifyError(cause);
    }
  }
  /** Open a Yousage project page in the system browser. */
  function open(url: string) {
    void nativeApi.OpenExternal(url).catch(notifyError);
  }
  const summary = report ? parseDiagnosticReport(report) : null;
  const count = (value: number | undefined) =>
    summary?.metadataAvailable && value !== undefined ? String(value) : "—";
  return (
    <section aria-label={t("About & diagnostics")}>
      <PageHeader parent={t("Settings")} title={t("About & diagnostics")} />
      <PageBody width="narrow">
        {nativeStatus && <Notice tone="error">{nativeStatus}</Notice>}
        <SettingsSection title={t("About")}>
          <SettingsRow
            title="Yousage"
            description={t(
              "Local usage monitor for AI coding tools: token usage, API-equivalent cost, subscription quotas, cache loss and output speed.",
            )}
            control={
              version && (
                <span className="text-xs text-muted-foreground tabular-nums">
                  {t("Version {version}", { version })}
                </span>
              )
            }
          />
          <SettingsRow
            title={t("Project page")}
            description="github.com/QianFuv/Yousage"
            control={
              <Button
                variant="outline"
                size="sm"
                onClick={() => open(PROJECT_URL)}
              >
                <ExternalLinkIcon aria-hidden />
                {t("Open")}
              </Button>
            }
          />
          <SettingsRow
            title={t("Report a problem")}
            description={t(
              "Open an issue on GitHub. Attach the diagnostics report below when it helps.",
            )}
            control={
              <Button
                variant="outline"
                size="sm"
                onClick={() => open(ISSUES_URL)}
              >
                <ExternalLinkIcon aria-hidden />
                {t("Open")}
              </Button>
            }
          />
        </SettingsSection>
        <SettingsSection
          title={t("Diagnostics")}
          action={
            <Button
              variant="ghost"
              size="icon-sm"
              aria-label={t("Refresh report")}
              title={t("Refresh report")}
              onClick={() =>
                void nativeApi
                  .GetDiagnostics()
                  .then(setReport)
                  .catch(notifyError)
              }
            >
              <RefreshCwIcon aria-hidden />
            </Button>
          }
        >
          <SettingsRow
            title={t("Platform")}
            control={
              <span className="text-sm tabular-nums">
                {summary?.platform ?? "—"}
              </span>
            }
          />
          <SettingsRow
            title={t("History sources")}
            control={
              <span className="text-sm tabular-nums">
                {count(summary?.historySources)}
              </span>
            }
          />
          <SettingsRow
            title={t("Quota accounts")}
            control={
              <span className="text-sm tabular-nums">
                {count(summary?.quotaTargets)}
              </span>
            }
          />
          <SettingsRow
            title={t("Quota hubs")}
            control={
              <span className="text-sm tabular-nums">
                {count(summary?.quotaHubs)}
              </span>
            }
          />
          <SettingsRow
            title={t("Recent logs")}
            description={t(
              "Recent redacted events stored on this device. Nothing is uploaded unless you share it.",
            )}
          />
          <pre className="max-h-96 overflow-auto bg-muted/30 px-4 py-3 font-mono text-xs leading-relaxed whitespace-pre-wrap text-foreground [overflow-wrap:anywhere]">
            {summary ? summary.logs || "—" : t("Loading report…")}
          </pre>
          <div className="flex flex-wrap justify-end gap-2 px-4 py-3">
            <Button
              variant="outline"
              size="sm"
              disabled={!report}
              onClick={() =>
                void act(
                  () => nativeApi.ExportDiagnostics(),
                  t("Redacted report exported."),
                )
              }
            >
              <DownloadIcon aria-hidden />
              {t("Export")}
            </Button>
            <Button
              size="sm"
              disabled={!report}
              onClick={() =>
                void act(
                  () => nativeApi.CopyText(report),
                  t("Redacted report copied."),
                )
              }
            >
              <CopyIcon aria-hidden />
              {t("Copy report")}
            </Button>
          </div>
        </SettingsSection>
      </PageBody>
    </section>
  );
}
