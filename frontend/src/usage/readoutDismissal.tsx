/** Close chart readouts that a touch screen would otherwise leave open. */
import { useEffect, type RefObject } from "react";

/**
 * Hide a chart readout when a press lands outside its plot.
 *
 * Touch input never sends `mouseleave`, so without this a readout stays open
 * until the chart is touched again.
 *
 * @param plotRef - The plot element that shows the readout.
 * @param isShown - Whether a readout is showing.
 * @param onDismiss - Hides the readout; keep it stable between renders.
 */
export function useReadoutDismissal(
  plotRef: RefObject<HTMLElement | null>,
  isShown: boolean,
  onDismiss: () => void,
) {
  useEffect(() => {
    if (!isShown) return;
    /** Close the readout for presses anywhere but the plot itself. */
    const pressed = (event: PointerEvent) => {
      if (!plotRef.current?.contains(event.target as Node)) onDismiss();
    };
    document.addEventListener("pointerdown", pressed, true);
    return () => document.removeEventListener("pointerdown", pressed, true);
  }, [isShown, plotRef, onDismiss]);
}
