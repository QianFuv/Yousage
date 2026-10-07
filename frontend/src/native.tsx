import "culori/css";
import { converter, parse } from "culori/fn";

/** Native fixture DTO comes directly from Wails generated Go bindings. */
export type Bootstrap = import("../bindings/yousage/models").Bootstrap;

/** Keep absolute dropped paths out of status text and diagnostics. */
export function droppedNames(paths: string[]): string[] {
  return paths.map((path) => path.split(/[\\/]/).at(-1) ?? "");
}

/** Explicit modes stay stable while System follows the native media query. */
export function effectiveAppearance(
  mode: string,
  isSystemDark: boolean,
): "light" | "dark" {
  return mode === "dark" || (mode === "system" && isSystemDark)
    ? "dark"
    : "light";
}

const TO_RGB = converter("rgb");

/**
 * Convert a computed CSS color to opaque sRGB channels for Wails.
 *
 * Parsing avoids a canvas: its first pixel read in the hidden startup WebView takes seconds.
 *
 * @param color - Computed CSS color in any CSS color syntax.
 * @returns Red, green and blue channels clamped to 0–255.
 */
export function opaqueChannels(color: string): [number, number, number] {
  const parsed = parse(color);
  if (!parsed) throw new Error("Unsupported background color");
  const { r, g, b } = TO_RGB(parsed);
  const channel = (value: number) =>
    Math.round(Math.min(1, Math.max(0, value || 0)) * 255);
  return [channel(r), channel(g), channel(b)];
}

/** Page canvas color, used for the native window background. */
export function canvasBackground(): [number, number, number] {
  return opaqueChannels(getComputedStyle(document.body).backgroundColor);
}

/** Sidebar color, used for the native title bar so it reads as one surface with the sidebar. */
export function sidebarBackground(): [number, number, number] {
  const sidebar = document.querySelector(".app-sidebar") ?? document.body;
  return opaqueChannels(getComputedStyle(sidebar).backgroundColor);
}
