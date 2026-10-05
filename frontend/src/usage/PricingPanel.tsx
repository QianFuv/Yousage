/** Adapt the native pricing transaction to the original per-environment table. */
import { SettingsPriceEditor } from "../../vendor/SettingsPriceEditor";
import { useData } from "./controller";
import * as nativeApi from "../../bindings/yousage/app";
import { Request } from "../../bindings/yousage/internal/refresh/models";
import { PricingPatch } from "../../bindings/yousage/internal/preferences/models";
import type { UsagePriceTarget } from "../../vendor/usagePriceTargets";

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
      ? "Offline"
      : !environment.serverConfig
        ? "Prices not loaded"
        : environment.serverConfig.environment.capabilities
              .usagePriceOverrides !== true
          ? "Update server to edit prices"
          : !environment.canOperate
            ? "Read-only access"
            : null,
  }));
  return (
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
                ? "Outcome unknown. Reload this environment and review its saved settings before retrying."
                : (result?.error ?? "Could not save."),
              outcomeUnknown: result?.outcomeUnknown,
            };
      }}
    />
  );
}
