/** The one period selector shared by every page, backed by the saved display period. */
import {
  Toggle,
  ToggleGroup,
} from "../../../tools/parity/upstream/apps/web/src/components/ui/toggle-group";
import { msg, t } from "../i18n";
import { useData } from "./controller";
import { ChoiceMenu } from "./ChoiceMenu";

/** Period choices matching the original Usage page labels. */
export const PERIOD_OPTIONS = [
  { days: 1, label: msg("24h") },
  { days: 7, label: msg("7d") },
  { days: 30, label: msg("30d") },
  { days: 90, label: msg("90d") },
] as const;

/**
 * The saved period in days, shared by Usage, Loss and Speed.
 *
 * @returns The selected period: 1, 7, 30 or 90.
 */
export function usePeriodDays(): number {
  return useData().display.value.windowDays;
}

/**
 * Segmented 24h/7d/30d/90d selector that saves the choice for every page.
 *
 * @param props - Accessible name of the selector.
 * @returns The period toggle group.
 */
export function PeriodToggle({ label }: { label: string }) {
  const data = useData();
  return (
    <ToggleGroup
      aria-label={label}
      variant="segmented"
      value={[String(data.display.value.windowDays)]}
      onValueChange={(next) => {
        const value = Number(next[0]);
        if (value)
          void data.patchDisplay({ windowDays: value }).catch(() => {});
      }}
    >
      {PERIOD_OPTIONS.map((option) => (
        <Toggle key={option.days} value={String(option.days)}>
          {t(option.label)}
        </Toggle>
      ))}
    </ToggleGroup>
  );
}

/**
 * Compact phone form of {@link PeriodToggle}: the selected period on a small menu button.
 *
 * @param props - Accessible name of the selector.
 * @returns The period menu.
 */
export function PeriodMenu({ label }: { label: string }) {
  const data = useData();
  return (
    <ChoiceMenu
      label={label}
      value={String(data.display.value.windowDays)}
      choices={PERIOD_OPTIONS.map((option) => ({
        value: String(option.days),
        label: t(option.label),
      }))}
      onChange={(value) =>
        void data.patchDisplay({ windowDays: Number(value) }).catch(() => {})
      }
    />
  );
}
