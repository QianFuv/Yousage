/** Theme colors carried by a share payload: captured on the desktop, applied on the share page. */
import type { SharePayload } from "./payloads";

/** Largest number of custom properties a share palette may carry. */
export const MAX_PALETTE_ENTRIES = 128;

const PROPERTY_PATTERN = /^--[a-z0-9-]{1,64}$/;

/**
 * Whether a palette entry is a plain custom property with an inert value.
 *
 * @param name - Custom property name.
 * @param value - Property value.
 * @returns `true` when the entry cannot load resources or break out of its declaration.
 */
export function isSafePaletteEntry(name: string, value: string): boolean {
  return (
    PROPERTY_PATTERN.test(name) &&
    value.length > 0 &&
    value.length <= 128 &&
    !/url\(|[;<]/i.test(value)
  );
}

/**
 * Apply a share palette to the document root, ignoring unsafe entries.
 *
 * @param palette - Appearance and custom properties from the payload.
 */
export function applySharePalette(palette: SharePayload["palette"]) {
  const root = document.documentElement;
  const isDark = palette?.appearance !== "light";
  root.classList.toggle("dark", isDark);
  root.style.colorScheme = isDark ? "dark" : "light";
  const entries = Object.entries(palette?.colors ?? {}).slice(
    0,
    MAX_PALETTE_ENTRIES,
  );
  for (const [name, value] of entries)
    if (typeof value === "string" && isSafePaletteEntry(name, value))
      root.style.setProperty(name, value);
}
