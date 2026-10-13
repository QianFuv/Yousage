/** Diagnostics export is an explicit native action over a backend-owned whitelist. */
import { useEffect, useState } from "react";
import { CopyIcon, DownloadIcon, RefreshCwIcon } from "lucide-react";
import * as nativeApi from "../../bindings/yousage/internal/desktop/app";
import { t } from "../i18n";
import {
  Notice,
  PageBody,
  PageHeader,
  SettingsRow,
  SettingsSection,
} from "../ui";
import { Button } from "../../../tools/parity/upstream/apps/web/src/components/ui/button";

/** Preview precisely what is copied or exported; no account labels, URLs, paths or credentials. */
export function DiagnosticsPanel({ nativeStatus }: { nativeStatus: string }) {
  const [report, setReport] = useState("");
  const [status, setStatus] = useState("");
  useEffect(() => {
    void nativeApi
      .GetDiagnostics()
      .then(setReport)
      .catch((cause) => setStatus(String(cause)));
  }, []);
  /** Show only the actual outcome of the explicit native operation. */
  async function act(operation: () => Promise<unknown>, message: string) {
    try {
      const result = await operation();
      setStatus(result === false ? t("Canceled.") : message);
    } catch (cause) {
      setStatus(String(cause));
    }
  }
  return (
    <section aria-label={t("About & diagnostics")}>
      <PageHeader parent={t("Settings")} title={t("About & diagnostics")} />
      <PageBody width="narrow">
        {nativeStatus && <Notice tone="error">{nativeStatus}</Notice>}
        {status && <Notice>{status}</Notice>}
        <SettingsSection title={t("About")}>
          <SettingsRow
            title="Yousage"
            description={t("Standalone Wails v3 desktop application")}
          />
          <SettingsRow
            title={t("Documentation")}
            description={t("Wails runtime documentation")}
            control={
              <Button
                variant="outline"
                size="sm"
                onClick={() =>
                  void act(
                    () => nativeApi.OpenDocumentation(),
                    t("Documentation opened."),
                  )
                }
              >
                {t("Open")}
              </Button>
            }
          />
        </SettingsSection>
        <SettingsSection
          title={t("Diagnostics report")}
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
                  .catch((cause) => setStatus(String(cause)))
              }
            >
              <RefreshCwIcon aria-hidden />
            </Button>
          }
        >
          <pre className="max-h-96 overflow-auto bg-muted/30 px-4 py-3 font-mono text-xs leading-relaxed whitespace-pre-wrap text-foreground [overflow-wrap:anywhere]">
            {report || t("Loading report…")}
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
