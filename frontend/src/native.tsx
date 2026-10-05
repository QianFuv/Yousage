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

/** Convert browser-rendered CSS colors to opaque sRGB channels for Wails. */
export function canvasBackground(): [number, number, number] {
  const canvas = document.createElement("canvas");
  canvas.width = canvas.height = 1;
  const context = canvas.getContext("2d");
  if (!context) throw new Error("Canvas color conversion unavailable");
  context.fillStyle = getComputedStyle(document.body).backgroundColor;
  context.fillRect(0, 0, 1, 1);
  const channels = context.getImageData(0, 0, 1, 1).data;
  return [channels[0], channels[1], channels[2]];
}
