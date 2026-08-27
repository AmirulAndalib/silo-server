import { Link } from "react-router";
import { CheckCircle2, CircleSlash, XCircle } from "lucide-react";

import type { StreamNode } from "@/api/types";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { useAdminNodes } from "@/hooks/queries/admin/nodes";
import { formatRelativeTime } from "@/lib/date";
import { SectionError } from "../feedback";
import { formatMbps } from "../format";

/**
 * Remote transcode/streaming workers from `nodepool`.
 *
 * A deployment with no stream nodes is the normal single-server shape, not a
 * misconfiguration, so the empty state says where transcodes actually run
 * rather than reading as a missing dependency.
 */
export function TranscodeNodesWidget() {
  const nodesQuery = useAdminNodes();
  const nodes = nodesQuery.data ?? [];

  return (
    <Card className="h-full">
      <CardHeader className="flex shrink-0 flex-row items-center justify-between space-y-0 pb-3">
        <CardTitle className="text-sm font-bold">Transcode nodes</CardTitle>
        <Link
          to="/admin/nodes"
          className="text-muted-foreground hover:text-primary text-[11px] transition-colors"
        >
          Manage ›
        </Link>
      </CardHeader>
      <CardContent className="min-h-0 flex-1 space-y-2 overflow-y-auto">
        {nodesQuery.isLoading ? (
          <>
            {Array.from({ length: 2 }).map((_, i) => (
              <Skeleton key={i} className="h-[68px] rounded-md" />
            ))}
          </>
        ) : nodesQuery.error ? (
          <SectionError message="Failed to load stream nodes." />
        ) : nodes.length === 0 ? (
          <div className="text-muted-foreground py-4 text-center text-sm">
            No stream nodes — transcodes run on this server
          </div>
        ) : (
          nodes.map((node) => <NodeRow key={node.id} node={node} />)
        )}
      </CardContent>
    </Card>
  );
}

function NodeRow({ node }: { node: StreamNode }) {
  const status = nodeStatus(node);
  const StatusIcon = status.icon;
  const jobs = jobLoad(node);
  const lastCheck =
    formatRelativeTime(node.last_health_check, {
      rounding: "floor",
      justNowLabel: "Just now",
    }) ?? "never checked";

  return (
    <div className="bg-surface border-border rounded-md border p-3">
      <div className="flex items-center gap-2">
        <div className="min-w-0 flex-1">
          <div className="truncate text-sm font-bold">{node.name}</div>
          <div className="text-muted-foreground text-[11px]">
            {node.type}
            {node.group ? ` · ${node.group}` : ""} · checked {lastCheck}
          </div>
        </div>
        {/* Status is an icon plus the word: the tint alone would be the only
            signal for anyone who cannot separate the hues. */}
        <span
          className={`flex flex-shrink-0 items-center gap-1 text-[11px] font-medium ${status.className}`}
        >
          <StatusIcon className="h-3.5 w-3.5" aria-hidden="true" />
          {status.label}
        </span>
      </div>
      <div className="mt-2.5 flex items-center gap-3">
        <div className="min-w-0 flex-1">
          <div className="bg-muted/60 h-1.5 w-full overflow-hidden rounded-full">
            <div
              className="h-full rounded-full"
              style={{
                width: `${jobs.percent}%`,
                background: "var(--chart-1)",
              }}
              role="meter"
              aria-label={`${node.name} transcode jobs`}
              aria-valuenow={node.active_jobs}
              aria-valuemin={0}
              aria-valuemax={jobs.max ?? undefined}
            />
          </div>
        </div>
        <div className="text-muted-foreground flex flex-shrink-0 items-center gap-2 text-[11px] tabular-nums">
          <span>{jobs.label}</span>
          <span className="text-border/70">·</span>
          <span>{formatMbps(node.egress_kbps / 1_000)}</span>
        </div>
      </div>
    </div>
  );
}

function nodeStatus(node: StreamNode) {
  if (!node.enabled) {
    return { label: "Disabled", icon: CircleSlash, className: "text-muted-foreground" };
  }
  if (!node.healthy) {
    return { label: "Unhealthy", icon: XCircle, className: "text-destructive" };
  }
  return { label: "Healthy", icon: CheckCircle2, className: "text-emerald-500" };
}

/**
 * A node with no `max_jobs` is uncapped, so there is no fraction to draw — the
 * meter then reflects nothing and stays empty rather than inventing a ceiling.
 */
function jobLoad(node: StreamNode): { percent: number; label: string; max: number | null } {
  const max = node.max_jobs && node.max_jobs > 0 ? node.max_jobs : null;
  if (max === null) {
    return {
      percent: 0,
      label: `${node.active_jobs.toLocaleString()} jobs`,
      max: null,
    };
  }
  const percent = Math.max(0, Math.min(100, Math.round((node.active_jobs / max) * 100)));
  return {
    percent,
    label: `${node.active_jobs.toLocaleString()}/${max.toLocaleString()} jobs`,
    max,
  };
}
