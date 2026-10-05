/** Type boundary for the hash-verified upstream workbench, built by Vite without rewriting it. */
import type { ComponentType } from "react";
export const Reference: ComponentType<{
  appearance?: "light" | "dark";
  view?: "usage" | "appearance";
  shouldManageTheme?: boolean;
  onAppearanceChange?: (appearance: "light" | "dark") => void;
}>;
