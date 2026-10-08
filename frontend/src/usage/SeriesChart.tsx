/** Layered smooth-line chart in the Usage page style, for any per-bucket series. */
import {
  useCallback,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type MouseEvent,
  type ReactNode,
} from "react";
import {
  curvePath,
  niceScale,
  smoothCurve,
} from "../../vendor/UsageProviderChart";

const VIEW_WIDTH = 960;
const VIEW_HEIGHT = 260;
const TICK_COUNT = 4;
const PLOT_TOP = 8;

/** One line of the chart, with a value for every bucket. */
export interface ChartSeries {
  readonly id: string;
  readonly color: string;
  readonly values: readonly number[];
}

/**
 * Smooth layered lines with soft fills, an immediate hover readout and start, middle and end labels.
 *
 * Every series measures from zero and the scale tops out at the largest single value.
 *
 * @param props - Bucket labels, series, value format, label formatters and tooltip rows.
 * @returns The chart element.
 */
export function SeriesChart({
  periods,
  series,
  format,
  ariaLabel,
  formatPeriod,
  formatTooltipPeriod,
  renderTooltip,
}: {
  periods: readonly string[];
  series: readonly ChartSeries[];
  format: (value: number) => string;
  ariaLabel: string;
  formatPeriod: (period: string) => string;
  formatTooltipPeriod: (period: string) => string;
  renderTooltip: (index: number) => ReactNode;
}) {
  const [hoverIndex, setHoverIndex] = useState<number | null>(null);
  const plotRef = useRef<HTMLDivElement | null>(null);
  const tooltipRef = useRef<HTMLDivElement | null>(null);
  const hoverPositionRef = useRef<{ x: number; y: number } | null>(null);

  const { paths, ticks, stepX, toY } = useMemo(() => {
    const peak = Math.max(0, ...series.flatMap((line) => line.values));
    const { max, ticks: tickValues } = niceScale(peak, TICK_COUNT);
    const step = periods.length <= 1 ? 0 : VIEW_WIDTH / (periods.length - 1);
    const toY = (value: number) =>
      max === 0
        ? VIEW_HEIGHT
        : VIEW_HEIGHT - (value / max) * (VIEW_HEIGHT - PLOT_TOP);
    const built = series.map((line) => {
      const curve = curvePath(
        smoothCurve(
          line.values.map((value, index) => ({
            x: index * step,
            y: toY(value),
          })),
        ),
      );
      return {
        ...line,
        line: curve,
        area:
          curve === ""
            ? ""
            : `${curve} L${VIEW_WIDTH},${VIEW_HEIGHT} L0,${VIEW_HEIGHT} Z`,
        total: line.values.reduce((sum, value) => sum + value, 0),
      };
    });
    return {
      paths: built.toSorted((first, second) => second.total - first.total),
      ticks: tickValues,
      stepX: step,
      toY,
    };
  }, [periods.length, series]);

  /** Keep the readout beside the cursor and inside the plot. */
  const positionTooltip = useCallback(() => {
    const plot = plotRef.current;
    const tooltip = tooltipRef.current;
    const hoverPosition = hoverPositionRef.current;
    if (plot === null || tooltip === null || hoverPosition === null) return;
    const gap = 12;
    const preferredLeft =
      hoverPosition.x + gap + tooltip.offsetWidth <= plot.clientWidth
        ? hoverPosition.x + gap
        : hoverPosition.x - gap - tooltip.offsetWidth;
    const preferredTop =
      hoverPosition.y + gap + tooltip.offsetHeight <= plot.clientHeight
        ? hoverPosition.y + gap
        : hoverPosition.y - gap - tooltip.offsetHeight;
    const left = Math.min(
      Math.max(0, preferredLeft),
      Math.max(0, plot.clientWidth - tooltip.offsetWidth),
    );
    const top = Math.min(
      Math.max(0, preferredTop),
      Math.max(0, plot.clientHeight - tooltip.offsetHeight),
    );
    plot.style.setProperty("--usage-tooltip-left", `${left}px`);
    plot.style.setProperty("--usage-tooltip-top", `${top}px`);
  }, []);

  useLayoutEffect(() => {
    if (hoverIndex === null) return;
    positionTooltip();
    const plot = plotRef.current;
    const tooltip = tooltipRef.current;
    if (
      plot === null ||
      tooltip === null ||
      typeof ResizeObserver === "undefined"
    )
      return;
    const observer = new ResizeObserver(positionTooltip);
    observer.observe(plot);
    observer.observe(tooltip);
    return () => observer.disconnect();
  }, [hoverIndex, positionTooltip]);

  /** Snap the hover to the nearest bucket. */
  const handleMove = useCallback(
    (event: MouseEvent<HTMLDivElement>) => {
      const plot = plotRef.current;
      if (plot === null || periods.length === 0) return;
      const bounds = plot.getBoundingClientRect();
      if (bounds.width === 0) return;
      const localX = Math.min(
        bounds.width,
        Math.max(0, event.clientX - bounds.left),
      );
      const localY = Math.min(
        bounds.height,
        Math.max(0, event.clientY - bounds.top),
      );
      const index = Math.round((localX / bounds.width) * (periods.length - 1));
      hoverPositionRef.current = { x: localX, y: localY };
      positionTooltip();
      setHoverIndex(Math.min(periods.length - 1, Math.max(0, index)));
    },
    [periods.length, positionTooltip],
  );

  const hoveredPeriod = hoverIndex === null ? undefined : periods[hoverIndex];
  const middle = periods[Math.floor(periods.length / 2)];
  return (
    <div className="flex flex-col gap-1">
      <div className="flex gap-2">
        <div className="relative h-56 w-14 shrink-0">
          {ticks.map((tick) => (
            <span
              key={tick}
              className="absolute right-0 -translate-y-1/2 text-3xs text-muted-foreground tabular-nums"
              style={{ top: `${(toY(tick) / VIEW_HEIGHT) * 100}%` }}
            >
              {tick === 0 ? "0" : format(tick)}
            </span>
          ))}
        </div>
        <div
          ref={plotRef}
          className="relative h-56 flex-1"
          onMouseMove={handleMove}
          onMouseLeave={() => {
            hoverPositionRef.current = null;
            setHoverIndex(null);
          }}
        >
          <svg
            className="h-full w-full"
            viewBox={`0 0 ${VIEW_WIDTH} ${VIEW_HEIGHT}`}
            preserveAspectRatio="none"
            role="img"
            aria-label={ariaLabel}
          >
            {ticks.map((tick) => (
              <line
                key={tick}
                x1={0}
                x2={VIEW_WIDTH}
                y1={toY(tick)}
                y2={toY(tick)}
                stroke="currentColor"
                strokeWidth={1}
                className="text-border"
                vectorEffect="non-scaling-stroke"
              />
            ))}
            {paths.map(({ id, area, color }) => (
              <path key={id} d={area} fill={color} fillOpacity={0.12} />
            ))}
            {paths.map(({ id, line, color }) => (
              <path
                key={id}
                d={line}
                fill="none"
                stroke={color}
                strokeWidth={2}
                vectorEffect="non-scaling-stroke"
              />
            ))}
            {hoverIndex !== null && (
              <line
                x1={hoverIndex * stepX}
                x2={hoverIndex * stepX}
                y1={PLOT_TOP}
                y2={VIEW_HEIGHT}
                stroke="currentColor"
                strokeWidth={1}
                className="text-muted-foreground"
                vectorEffect="non-scaling-stroke"
              />
            )}
          </svg>
          {hoveredPeriod !== undefined && hoverIndex !== null && (
            <div
              ref={tooltipRef}
              className="surface-glass pointer-events-none absolute z-10 min-w-36 max-w-full rounded-xl border border-border/50 px-2.5 py-2 text-xs shadow-lg"
              style={{
                left: "var(--usage-tooltip-left, 0px)",
                top: "var(--usage-tooltip-top, 0px)",
              }}
            >
              <div className="mb-1 text-muted-foreground">
                {formatTooltipPeriod(hoveredPeriod)}
              </div>
              {renderTooltip(hoverIndex)}
            </div>
          )}
        </div>
      </div>
      <div className="flex justify-between pl-16 text-3xs text-muted-foreground uppercase">
        <span>{periods[0] === undefined ? "" : formatPeriod(periods[0])}</span>
        <span>{middle === undefined ? "" : formatPeriod(middle)}</span>
        <span>
          {periods.length === 0
            ? ""
            : formatPeriod(periods[periods.length - 1])}
        </span>
      </div>
    </div>
  );
}
