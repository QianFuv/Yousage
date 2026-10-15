/** Capture the active theme so a share page opens in the sharer's colors. */
import {
  getThemeColorVariable,
  THEME_COLOR_ROLES,
} from "../../vendor/themePalette";
import { isSafePaletteEntry, MAX_PALETTE_ENTRIES } from "../web/palette";

/**
 * Read the applied theme colors from the document root.
 *
 * Only theme role variables are taken, so fonts and layout tokens stay on the desktop.
 *
 * @returns Appearance and the role variables that pass the share palette checks.
 */
export function captureSharePalette(): {
  appearance: "dark" | "light";
  colors: Record<string, string>;
} {
  const root = document.documentElement;
  const computed = getComputedStyle(root);
  const colors: Record<string, string> = {};
  for (const role of THEME_COLOR_ROLES) {
    const name = getThemeColorVariable(role);
    const value = computed.getPropertyValue(name).trim();
    if (
      Object.keys(colors).length < MAX_PALETTE_ENTRIES &&
      isSafePaletteEntry(name, value)
    )
      colors[name] = value;
  }
  return {
    appearance: root.classList.contains("dark") ? "dark" : "light",
    colors,
  };
}
