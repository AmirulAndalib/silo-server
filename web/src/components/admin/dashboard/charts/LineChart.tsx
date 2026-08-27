import {
  useMemo,
  useState,
  type PointerEvent as ReactPointerEvent,
  type KeyboardEvent,
} from "react";

import { formatTime } from "@/lib/datetime";
import { cn } from "@/lib/utils";
import { buildAreaPath, buildLinePath, chartSeriesColor, niceTicks } from "./chartMath";
import { ChartTooltip, ChartTooltipRow } from "./chartChrome";
import { useMeasuredSize } from "./useMeasuredSize";

export interface LineChartPoint {
  /** Sample timestamp in epoch milliseconds. */
  t: number;
  /** `null` is a missing sample: the line breaks rather than inventing a zero. */
  value: number | null;
}

export interface LineChartProps {
  points: readonly LineChartPoint[];
  /** Plot height in pixels (axis labels sit outside it); ignored when `fill`. */
  height?: number;
  /**
   * Take the height of the parent instead of `height`. The chart then follows
   * the widget's row height, which the admin can change at any time.
   */
  fill?: boolean;
  /** Categorical slot for the series color; single-series charts stay on slot 0. */
  seriesIndex?: number;
  /** Series name for the hover readout. */
  seriesLabel?: string;
  formatValue?: (value: number) => string;
  /** Axis tick label, defaults to `formatValue`; pass a compact form when units are long. */
  formatTick?: (value: number) => string;
  formatTimestamp?: (t: number) => string;
  /**
   * Labels for the two ends of the time axis. Absolute timestamps answer "when
   * exactly" — which is the tooltip's job — while the axis only has to say how
   * far back the window reaches, so a chart with a chosen window passes
   * something like `{ start: "7d ago", end: "now" }`.
   */
  edgeLabels?: { start: string; end: string };
  /** Smallest axis tick gap — counts pass 1 to avoid fractional gridlines. */
  minTickStep?: number;
  ariaLabel: string;
  className?: string;
}

// Fallback plot box, used only until the ResizeObserver reports the real one.
// It is stretched to the card (`preserveAspectRatio="none"`); strokes opt out
// of that scaling so the line stays exactly 2px at every widget span. Once the
// plot has been measured the viewBox matches its CSS pixels one to one, so the
// same attribute stretches nothing and marks keep their shape at any height.
const VIEW_WIDTH = 1000;
const VIEW_HEIGHT = 100;

function defaultFormatValue(value: number): string {
  return value.toLocaleString();
}

/**
 * Single-series time line with an area wash, a snapping crosshair, and gaps
 * where samples are missing. One value axis; no legend (the card header names
 * the series).
 */
export function LineChart({
  points,
  height = 160,
  fill = false,
  seriesIndex = 0,
  seriesLabel = "Value",
  formatValue = defaultFormatValue,
  formatTick,
  formatTimestamp = (t) => formatTime(t),
  edgeLabels,
  minTickStep,
  ariaLabel,
  className,
}: LineChartProps) {
  const [activeIndex, setActiveIndex] = useState<number | null>(null);
  const color = chartSeriesColor(seriesIndex);
  const { ref: plotRef, size: plotSize } = useMeasuredSize<HTMLDivElement>();
  const viewWidth = plotSize && plotSize.width > 0 ? plotSize.width : VIEW_WIDTH;
  const viewHeight = plotSize && plotSize.height > 0 ? plotSize.height : VIEW_HEIGHT;

  const geometry = useMemo(() => {
    const values = points
      .map((point) => point.value)
      .filter((value): value is number => value !== null && Number.isFinite(value));
    const ticks = niceTicks(0, values.length > 0 ? Math.max(...values) : 0, 3, {
      minStep: minTickStep,
    });
    const top = ticks[ticks.length - 1] || 1;

    const firstT = points[0]?.t ?? 0;
    const lastT = points[points.length - 1]?.t ?? 0;
    const span = lastT - firstT;
    const xRatios = points.map((point, index) => {
      if (points.length <= 1) {
        return 0.5;
      }
      if (span <= 0) {
        return index / (points.length - 1);
      }
      return (point.t - firstT) / span;
    });

    const projected = points.map((point, index) => ({
      x: (xRatios[index] ?? 0) * viewWidth,
      y:
        point.value === null || !Number.isFinite(point.value)
          ? null
          : viewHeight - (point.value / top) * viewHeight,
    }));

    return {
      ticks,
      top,
      xRatios,
      linePath: buildLinePath(projected),
      areaPath: buildAreaPath(projected, viewHeight),
    };
  }, [points, minTickStep, viewWidth, viewHeight]);

  const plottedIndexes = useMemo(
    () =>
      points.map((point, index) => ({ point, index })).filter(({ point }) => point.value !== null),
    [points],
  );

  function nearestIndex(ratio: number): number | null {
    let best: number | null = null;
    let bestDistance = Number.POSITIVE_INFINITY;
    for (const { index } of plottedIndexes) {
      const distance = Math.abs((geometry.xRatios[index] ?? 0) - ratio);
      if (distance < bestDistance) {
        bestDistance = distance;
        best = index;
      }
    }
    return best;
  }

  function handlePointerMove(event: ReactPointerEvent<HTMLDivElement>) {
    const bounds = event.currentTarget.getBoundingClientRect();
    if (bounds.width <= 0) {
      return;
    }
    setActiveIndex(nearestIndex((event.clientX - bounds.left) / bounds.width));
  }

  function handleKeyDown(event: KeyboardEvent<HTMLDivElement>) {
    if (plottedIndexes.length === 0) {
      return;
    }
    if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") {
      return;
    }
    event.preventDefault();
    const order = plottedIndexes.map(({ index }) => index);
    const current = activeIndex === null ? -1 : order.indexOf(activeIndex);
    const step = event.key === "ArrowRight" ? 1 : -1;
    const next =
      current === -1 ? order.length - 1 : Math.min(Math.max(current + step, 0), order.length - 1);
    setActiveIndex(order[next] ?? null);
  }

  const activePoint = activeIndex === null ? null : (points[activeIndex] ?? null);
  const activeValue = activePoint && activePoint.value !== null ? activePoint.value : null;
  const activeRatio = activeIndex === null ? 0 : (geometry.xRatios[activeIndex] ?? 0);
  const firstPoint = points[0];
  const lastPoint = points[points.length - 1];

  return (
    <div className={cn("w-full", fill && "flex min-h-0 flex-1 flex-col", className)}>
      <div className={cn("flex items-stretch gap-2", fill && "min-h-0 flex-1")}>
        <div className="relative w-10 shrink-0" style={fill ? undefined : { height }}>
          {geometry.ticks.map((tick) => (
            <span
              key={tick}
              className="text-muted-foreground absolute right-0 -translate-y-1/2 text-[10px] leading-none tabular-nums"
              style={{ top: `${(1 - tick / geometry.top) * 100}%` }}
            >
              {(formatTick ?? formatValue)(tick)}
            </span>
          ))}
        </div>
        <div
          className="relative min-w-0 flex-1"
          ref={plotRef}
          style={fill ? undefined : { height }}
        >
          <svg
            role="img"
            aria-label={ariaLabel}
            className="absolute inset-0 h-full w-full overflow-visible"
            viewBox={`0 0 ${viewWidth} ${viewHeight}`}
            preserveAspectRatio="none"
          >
            {geometry.ticks.map((tick) => {
              const y = (1 - tick / geometry.top) * viewHeight;
              return (
                <line
                  key={tick}
                  x1={0}
                  x2={viewWidth}
                  y1={y}
                  y2={y}
                  stroke="var(--border)"
                  strokeWidth={1}
                  opacity={tick === 0 ? 0.9 : 0.5}
                  vectorEffect="non-scaling-stroke"
                />
              );
            })}
            {geometry.areaPath ? (
              <path d={geometry.areaPath} fill={color} opacity={0.12} stroke="none" />
            ) : null}
            {geometry.linePath ? (
              <path
                d={geometry.linePath}
                fill="none"
                stroke={color}
                strokeWidth={2}
                strokeLinecap="round"
                strokeLinejoin="round"
                vectorEffect="non-scaling-stroke"
              />
            ) : null}
          </svg>

          {activePoint && activeValue !== null ? (
            <>
              <div
                aria-hidden="true"
                className="bg-border pointer-events-none absolute inset-y-0 w-px"
                style={{ left: `${activeRatio * 100}%` }}
              />
              <div
                aria-hidden="true"
                className="pointer-events-none absolute h-2 w-2 -translate-x-1/2 -translate-y-1/2 rounded-full"
                style={{
                  left: `${activeRatio * 100}%`,
                  top: `${(1 - activeValue / geometry.top) * 100}%`,
                  backgroundColor: color,
                  boxShadow: "0 0 0 2px var(--card)",
                }}
              />
              <ChartTooltip xRatio={activeRatio} title={formatTimestamp(activePoint.t)}>
                <ChartTooltipRow
                  color={color}
                  label={seriesLabel}
                  value={formatValue(activeValue)}
                />
              </ChartTooltip>
            </>
          ) : null}

          {/* Full-plot hit layer: the pointer only has to be closest on X, never
              land on the 2px stroke. Arrow keys walk the same samples. */}
          <div
            className="absolute inset-0 cursor-crosshair focus-visible:outline-none"
            tabIndex={0}
            role="application"
            aria-label={`${ariaLabel} — use arrow keys to inspect samples`}
            onPointerMove={handlePointerMove}
            onPointerLeave={() => setActiveIndex(null)}
            onBlur={() => setActiveIndex(null)}
            onKeyDown={handleKeyDown}
          />
        </div>
      </div>
      {firstPoint && lastPoint ? (
        <div className="text-muted-foreground mt-1.5 flex justify-between pl-12 text-[10px] tabular-nums">
          <span>{edgeLabels ? edgeLabels.start : formatTimestamp(firstPoint.t)}</span>
          <span>{edgeLabels ? edgeLabels.end : formatTimestamp(lastPoint.t)}</span>
        </div>
      ) : null}
    </div>
  );
}
