/** Compact secondary actions using the original Base UI menu primitives. */
import type { ReactNode } from "react";
import { MoreHorizontalIcon } from "lucide-react";
import { Button } from "../../tools/parity/upstream/apps/web/src/components/ui/button";
import {
  Menu,
  MenuItem,
  MenuPopup,
  MenuTrigger,
} from "../../tools/parity/upstream/apps/web/src/components/ui/menu";

/**
 * Icon-triggered overflow menu; children are {@link ActionMenuItem} entries.
 *
 * @param props - Accessible label and menu items.
 * @returns The menu element.
 */
export function ActionMenu({
  label,
  children,
}: {
  label: string;
  children: ReactNode;
}) {
  return (
    <Menu>
      <MenuTrigger
        render={
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label={label}
            title={label}
          />
        }
      >
        <MoreHorizontalIcon aria-hidden />
      </MenuTrigger>
      <MenuPopup align="end">{children}</MenuPopup>
    </Menu>
  );
}

export { MenuItem as ActionMenuItem };
