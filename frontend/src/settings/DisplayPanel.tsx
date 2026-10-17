/** Immediate display preferences acknowledge durable native state before updating controls. */
import { useData } from "../usage/controller";
import * as nativeApi from "../../bindings/yousage/internal/desktop/app";
import {
  OptionSelect,
  PageBody,
  PageHeader,
  SettingsRow,
  SettingsSection,
} from "../ui";
import { msg, t } from "../i18n";
import { notifyError } from "../notify";
import { Button } from "../../../tools/parity/upstream/apps/web/src/components/ui/button";
import { Switch } from "../../../tools/parity/upstream/apps/web/src/components/ui/switch";

const LANGUAGE_OPTIONS = [
  { value: "system", label: msg("Follow system") },
  { value: "zh-CN", label: "简体中文" },
  { value: "en", label: "English" },
];
const DENSITY_OPTIONS = [
  { value: "comfortable", label: msg("Comfortable") },
  { value: "compact", label: msg("Compact") },
];
const ZOOM_OPTIONS = [1, 1.1, 1.25, 1.5, 1.75, 2].map((zoom) => ({
  value: zoom,
  label: `${Math.round(zoom * 100)}%`,
}));
const TIMESTAMP_OPTIONS = [
  { value: "locale", label: msg("System default") },
  { value: "12h", label: msg("12-hour") },
  { value: "24h", label: msg("24-hour") },
];
const SHORTCUTS: readonly [string, readonly string[]][] = [
  [msg("Open settings"), ["Ctrl / ⌘", ","]],
  [msg("Usage statistics"), ["Ctrl / ⌘", "1"]],
  [msg("Quota monitoring"), ["Ctrl / ⌘", "2"]],
  [msg("Refresh the active usage page"), ["Ctrl / ⌘", "R"]],
  [msg("Open Appearance"), ["Ctrl / ⌘", "Alt", "A"]],
  [msg("Cycle System, Light and Dark"), ["Ctrl / ⌘", "Alt", "Shift", "A"]],
  [msg("Dismiss the close confirmation"), ["Esc"]],
];

/**
 * Translate the labels of a constant option table.
 *
 * @param options - Options whose labels are English source messages.
 * @returns Options with localized labels.
 */
function translated<Value>(
  options: readonly { value: Value; label: string }[],
) {
  return options.map((option) => ({ ...option, label: t(option.label) }));
}

/** Keep language, scale, spacing, timestamps and activity policy independent of theme selection. */
export function DisplayPanel() {
  const data = useData();
  const value = data.display.value;
  /** Report failed ordinary preference writes while retaining the last acknowledged choice. */
  function change(patch: Partial<typeof value>) {
    void data.patchDisplay(patch).catch(notifyError);
  }
  return (
    <section aria-label={t("Display & behavior")}>
      <PageHeader parent={t("Settings")} title={t("Display & behavior")} />
      <PageBody width="narrow">
        <SettingsSection title={t("Display")}>
          <SettingsRow
            title={t("Language")}
            description={t(
              "Interface language. Follow system uses the Windows display language.",
            )}
            control={
              <OptionSelect
                label={t("Language")}
                value={value.language || "system"}
                options={LANGUAGE_OPTIONS.map((option) =>
                  option.value === "system"
                    ? { ...option, label: t(option.label) }
                    : option,
                )}
                onChange={(language) => change({ language })}
              />
            }
          />
          <SettingsRow
            title={t("Information density")}
            description={t(
              "Both modes show all statistics; compact reduces spacing.",
            )}
            control={
              <OptionSelect
                label={t("Information density")}
                value={value.density}
                options={translated(DENSITY_OPTIONS)}
                onChange={(density) => change({ density })}
              />
            }
          />
          <SettingsRow
            title={t("Interface scale")}
            description={t("Independent of Windows display scaling.")}
            control={
              <OptionSelect
                label={t("Interface scale")}
                value={value.zoom}
                options={ZOOM_OPTIONS}
                onChange={(zoom) => change({ zoom })}
              />
            }
          />
          <SettingsRow
            title={t("Reset time format")}
            description={t("How quota reset times are shown.")}
            control={
              <OptionSelect
                label={t("Reset time format")}
                value={value.timestampFormat}
                options={translated(TIMESTAMP_OPTIONS)}
                onChange={(timestampFormat) => change({ timestampFormat })}
              />
            }
          />
        </SettingsSection>
        <SettingsSection title={t("Behavior")}>
          <SettingsRow
            title={t("Automatic refresh")}
            description={t(
              "Refresh while Usage is active and visible. History refreshes when stale; limits wait five minutes after completion. Settings and minimized windows never start automatic queries.",
            )}
            control={
              <Switch
                aria-label={t("Automatic refresh")}
                checked={value.shouldAutoRefresh}
                onCheckedChange={(isChecked) =>
                  change({ shouldAutoRefresh: isChecked })
                }
              />
            }
          />
          <SettingsRow
            title={t("Restore window")}
            description={t(
              "Reopen with the previous size, position and maximized state.",
            )}
            control={
              <Switch
                aria-label={t("Restore window")}
                checked={value.shouldRestoreWindow}
                onCheckedChange={(isChecked) =>
                  change({ shouldRestoreWindow: isChecked })
                }
              />
            }
          />
          <SettingsRow
            title={t("Close to tray")}
            description={t(
              "Closing the window keeps Yousage running in the notification area. Choose Quit from the tray menu to exit.",
            )}
            control={
              <Switch
                aria-label={t("Close to tray")}
                checked={value.shouldCloseToTray}
                onCheckedChange={(isChecked) =>
                  change({ shouldCloseToTray: isChecked })
                }
              />
            }
          />
          <SettingsRow
            title={t("Recenter window")}
            description={t("Move the window back onto the primary display.")}
            control={
              <Button
                variant="outline"
                size="sm"
                onClick={() =>
                  void nativeApi.RecenterWindow().catch(notifyError)
                }
              >
                {t("Recenter")}
              </Button>
            }
          />
        </SettingsSection>
        <SettingsSection title={t("Keyboard shortcuts")}>
          {SHORTCUTS.map(([label, keys]) => (
            <SettingsRow
              key={label}
              title={t(label)}
              control={
                <span className="flex items-center gap-1">
                  {keys.map((key) => (
                    <kbd key={key}>{key}</kbd>
                  ))}
                </span>
              }
            />
          ))}
        </SettingsSection>
      </PageBody>
    </section>
  );
}
