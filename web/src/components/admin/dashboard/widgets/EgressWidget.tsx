import { useMemo } from "react";

import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { useAdminTimeseries } from "@/hooks/queries/admin/dashboardInsights";
import { formatMbps, formatMbpsValue } from "../format";
import {
  formatRangeTimestamp,
  rangeEdgeLabels,
  rangeHours,
  rangePhrase,
  rangeTitle,
} from "../range";
import { useWidgetRange } from "../widgetChrome";
import { WidgetRangePicker } from "../WidgetRangePicker";
import { TimeseriesChartBody } from "./timeseriesChart";
import { buildTimeseriesPoints } from "./timeseriesSeries";

/**
 * Egress the deployment served over the chosen window, in Mbps.
 *
 * The sampler mixes two sources into one number: the rolling average each
 * stream node reports, and the exact bytes each API process served itself. A
 * node-less single-server install charts the second alone. Wide windows are
 * bucketed server-side to the peak minute in each bucket, so "Peak" means the
 * same thing at every range.
 *
 * When the window contains measured download traffic the chart splits into
 * Playback (`egress_kbps - download_egress_kbps`) and Downloads
 * (`download_egress_kbps`) series. Without any — including on samples written
 * before the server measured the split — it stays a single "Egress" line
 * rather than labeling a possibly-mixed total "Playback".
 */
export function EgressWidget() {
  const { range } = useWidgetRange();
  const query = useAdminTimeseries(rangeHours(range));
  const hasDownloadTraffic = useMemo(
    () => (query.data?.points ?? []).some((point) => (point.download_egress_kbps ?? 0) > 0),
    [query.data],
  );
  const points = useMemo(
    () =>
      buildTimeseriesPoints(query.data, (point) =>
        hasDownloadTraffic
          ? (point.egress_kbps - (point.download_egress_kbps ?? 0)) / 1_000
          : point.egress_kbps / 1_000,
      ),
    [query.data, hasDownloadTraffic],
  );
  const downloadPoints = useMemo(
    () =>
      hasDownloadTraffic
        ? buildTimeseriesPoints(query.data, (point) => (point.download_egress_kbps ?? 0) / 1_000)
        : [],
    [query.data, hasDownloadTraffic],
  );

  // Peak stays the total the deployment pushed, whichever way it is charted.
  const peak = points.reduce(
    (max, point, index) => Math.max(max, (point.value ?? 0) + (downloadPoints[index]?.value ?? 0)),
    0,
  );

  return (
    <Card className="h-full">
      <CardHeader className="flex shrink-0 flex-row items-center justify-between gap-2 space-y-0 pb-3">
        <CardTitle className="text-sm font-bold">{rangeTitle("Egress", range)}</CardTitle>
        <div className="flex min-w-0 items-center gap-2">
          <span className="text-muted-foreground text-[11px] tabular-nums">
            Peak {formatMbps(peak)}
          </span>
          <WidgetRangePicker />
        </div>
      </CardHeader>
      <CardContent className="flex min-h-0 flex-1 flex-col">
        <TimeseriesChartBody
          query={query}
          points={points}
          seriesLabel={hasDownloadTraffic ? "Playback" : "Egress"}
          overlays={
            hasDownloadTraffic
              ? [{ label: "Downloads", points: downloadPoints, seriesIndex: 1 }]
              : undefined
          }
          ariaLabel={`Egress over ${rangePhrase(range)}, in megabits per second`}
          errorMessage="Failed to load egress history."
          emptyMessage="No egress samples yet"
          formatValue={formatMbps}
          formatTick={formatMbpsValue}
          formatTimestamp={(t) => formatRangeTimestamp(range, t, { withTime: true })}
          edgeLabels={rangeEdgeLabels(range)}
          fill
        />
      </CardContent>
    </Card>
  );
}
