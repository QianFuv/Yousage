/** A compact menu button that shows the current choice, for page headers on phones. */
import { ChevronDownIcon } from "lucide-react";
import { Button } from "../../../tools/parity/upstream/apps/web/src/components/ui/button";
import {
  Menu,
  MenuPopup,
  MenuRadioGroup,
  MenuRadioItem,
  MenuRadioItemIndicator,
  MenuTrigger,
} from "../../../tools/parity/upstream/apps/web/src/components/ui/menu";

/** One selectable value with its visible label. */
export type Choice<Value extends string> = {
  value: Value;
  label: string;
};

/**
 * Show the selected label on a small button and offer every choice in a menu.
 *
 * @param props - Accessible name, choices, the selected value and the change handler.
 * @returns The menu button.
 */
export function ChoiceMenu<Value extends string>({
  label,
  choices,
  value,
  onChange,
}: {
  label: string;
  choices: readonly Choice<Value>[];
  value: Value;
  onChange: (value: Value) => void;
}) {
  const selected = choices.find((choice) => choice.value === value);
  return (
    <Menu>
      <MenuTrigger
        render={
          <Button
            variant="outline"
            size="compact"
            aria-label={`${label}: ${selected?.label ?? value}`}
          />
        }
      >
        <span className="whitespace-nowrap">{selected?.label ?? value}</span>
        <ChevronDownIcon aria-hidden />
      </MenuTrigger>
      <MenuPopup align="end">
        <MenuRadioGroup
          value={value}
          onValueChange={(next) => {
            const choice = choices.find((item) => item.value === next);
            if (choice) onChange(choice.value);
          }}
        >
          {choices.map((choice) => (
            <MenuRadioItem key={choice.value} value={choice.value} closeOnClick>
              <span className="flex items-center justify-between gap-3">
                {choice.label}
                <MenuRadioItemIndicator />
              </span>
            </MenuRadioItem>
          ))}
        </MenuRadioGroup>
      </MenuPopup>
    </Menu>
  );
}
