/** Adapt the native pricing transaction to the original per-environment table. */
import { SettingsPriceEditor } from "../../vendor/SettingsPriceEditor";
import { useData } from "./controller";
import * as nativeApi from "../../bindings/yousage/internal/desktop/app";
import { Request } from "../../bindings/yousage/internal/refresh/models";
import { PricingPatch } from "../../bindings/yousage/internal/preferences/models";
import type { UsagePriceTarget } from "../../vendor/usagePriceTargets";
import { PageBody, PageHeader } from "../ui";
import { t } from "../i18n";

/** Keep unapplied rows mounted across ordinary navigation and surface ambiguous outcomes. */
export function PricingPanel({
  model,
  onDirtyChange,
}: {
  model?: string;
  onDirtyChange: (isDirty: boolean, isPending: boolean) => void;
}) {
  const data = useData();
  const usage = data.snapshot?.environments ?? [];
  const targets: UsagePriceTarget[] = usage.map((environment) => ({
    environmentId: environment.environmentId,
    label: environment.label,
    prices: environment.serverConfig?.settings.usagePriceOverrides ?? null,
    aliases:
      environment.serverConfig?.environment.capabilities.usageModelAliases ===
      true
        ? environment.serverConfig.settings.usageModelAliases
        : null,
    unavailable: !environment.connected
      ? t("Offline")
      : !environment.serverConfig
        ? t("Prices not loaded")
        : environment.serverConfig.environment.capabilities
              .usagePriceOverrides !== true
          ? t("Update server to edit prices")
          : !environment.canOperate
            ? t("Read-only access")
            : null,
  }));
  return (
    <section aria-label={t("Prices & mappings")}>
      <PageHeader parent={t("Settings")} title={t("Prices & mappings")} />
      <PageBody>
        <SettingsPriceEditor
          usage={usage}
          environments={targets}
          initialSelectedEnvironmentIds={data.selected}
          initialModel={model}
          onDirtyChange={onDirtyChange}
          onSaved={async () => {
            await data.reload();
            await nativeApi
              .RefreshUsage(
                data.display.value.windowDays,
                Intl.DateTimeFormat().resolvedOptions().timeZone ?? "UTC",
                targets
                  .filter((target) => target.unavailable === null)
                  .map((target) => target.environmentId),
                Request.createFrom({
                  IsManual: true,
                  IsVisible: document.visibilityState === "visible",
                  IsUsagePage: false,
                  IsAutoEnabled: false,
                  AfterPending: true,
                }),
              )
              .catch(() => {});
            await data.reload();
          }}
          write={async (transaction) => {
            const results = await nativeApi.ApplyPricing(
              [transaction.environmentId],
              PricingPatch.createFrom(
                JSON.stringify({
                  overrides: transaction.input.patch.usagePriceOverrides ?? {},
                  aliases: transaction.input.patch.usageModelAliases ?? {},
                }),
              ),
            );
            const result = results[0];
            return result?.saved
              ? { _tag: "Success" }
              : {
                  _tag: "Failure",
                  error: result?.outcomeUnknown
                    ? t(
                        "Outcome unknown. Reload this environment and review its saved settings before retrying.",
                      )
                    : (result?.error ?? t("Could not save.")),
                  outcomeUnknown: result?.outcomeUnknown,
                };
          }}
        />
      </PageBody>
    </section>
  );
}
