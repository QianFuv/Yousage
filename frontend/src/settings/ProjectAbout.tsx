/** The About section shared by the desktop settings and the read-only viewer. */
import type { ReactNode } from "react";
import { ExternalLinkIcon } from "lucide-react";
import * as nativeApi from "../../bindings/yousage/internal/desktop/app";
import { Button } from "../../../tools/parity/upstream/apps/web/src/components/ui/button";
import { t } from "../i18n";
import { notifyError } from "../notify";
import { SettingsRow, SettingsSection } from "../ui";

/** Public project page of Yousage. */
export const PROJECT_URL = "https://github.com/QianFuv/Yousage";

/** Where problems with Yousage are reported. */
export const ISSUES_URL = `${PROJECT_URL}/issues`;

/**
 * Open a Yousage project page in the system browser, or in a new tab in the viewer.
 *
 * @param url - The project page to open.
 */
function openProjectPage(url: string) {
  void nativeApi.OpenExternal(url).catch(notifyError);
}

/**
 * Introduce Yousage with its version, project page and problem reporting.
 *
 * @param props - The version label shown beside the name, if known.
 * @returns The About settings section.
 */
export function ProjectAbout({ version }: { version?: ReactNode }) {
  return (
    <SettingsSection title={t("About")}>
      <SettingsRow
        title="Yousage"
        description={t(
          "Local usage monitor for AI coding tools: token usage, API-equivalent cost, subscription quotas, cache loss and output speed.",
        )}
        control={
          version && (
            <span className="text-xs text-muted-foreground tabular-nums">
              {version}
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
            onClick={() => openProjectPage(PROJECT_URL)}
          >
            <ExternalLinkIcon aria-hidden />
            {t("Open")}
          </Button>
        }
      />
      <SettingsRow
        title={t("Report a problem")}
        description={t(
          "Open an issue on GitHub. Attach the diagnostics report when it helps.",
        )}
        control={
          <Button
            variant="outline"
            size="sm"
            onClick={() => openProjectPage(ISSUES_URL)}
          >
            <ExternalLinkIcon aria-hidden />
            {t("Open")}
          </Button>
        }
      />
    </SettingsSection>
  );
}
