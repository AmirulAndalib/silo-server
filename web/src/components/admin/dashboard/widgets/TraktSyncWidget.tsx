import { Link } from "react-router";
import { Skeleton } from "@/components/ui/skeleton";
import { useAdminStats } from "@/hooks/queries/admin/stats";
import { formatRelativeTime } from "@/lib/date";
import { SectionError } from "../feedback";

export function TraktSyncWidget() {
  const statsQuery = useAdminStats();
  const activity = statsQuery.data?.watch_provider_activity;

  if (statsQuery.isLoading) {
    return <Skeleton className="h-full min-h-14 rounded-2xl" />;
  }

  if (statsQuery.error) {
    return (
      <div className="surface-panel flex h-full items-center rounded-2xl border-0 px-4">
        <SectionError message="Failed to load Trakt activity." />
      </div>
    );
  }

  const hasActivity =
    activity !== undefined &&
    (activity.trakt_connected_profiles > 0 ||
      activity.sync_runs_24h > 0 ||
      activity.pending_exports > 0 ||
      activity.open_scrobbles > 0);

  if (!activity || !hasActivity) {
    return (
      <div className="surface-panel flex h-full min-h-14 items-center gap-3 rounded-2xl border-0 px-4 py-3">
        <TraktMark />
        <span className="text-muted-foreground text-sm">Trakt not connected</span>
      </div>
    );
  }

  const lastSync =
    formatRelativeTime(activity.last_sync_completed_at, {
      rounding: "floor",
      justNowLabel: "Just now",
    }) ?? "never";
  const errors = activity.sync_errors_24h + activity.failed_exports;
  const profiles = activity.trakt_connected_profiles;

  return (
    <div className="surface-panel flex h-full min-h-14 flex-wrap items-center gap-x-3 gap-y-1 rounded-2xl border-0 px-4 py-3">
      <TraktMark />
      <span className="text-muted-foreground min-w-0 flex-1 text-sm">
        <span className="text-foreground font-semibold">Trakt</span>
        {" · "}
        {profiles.toLocaleString()} {profiles === 1 ? "profile" : "profiles"}
        {" · synced "}
        {lastSync}
        <span className="text-border/70 mx-1.5">|</span>
        {"24h: "}
        <span className="text-foreground font-medium">
          {activity.imported_watched_24h.toLocaleString()}
        </span>
        {" in / "}
        <span className="text-foreground font-medium">
          {activity.exported_watched_24h.toLocaleString()}
        </span>
        {" out · "}
        <span
          className={errors > 0 ? "text-destructive font-medium" : "text-foreground font-medium"}
        >
          {errors.toLocaleString()}
        </span>
        {errors === 1 ? " error" : " errors"}
      </span>
      <Link
        to="/admin/tasks/sync_watch_providers"
        className="text-muted-foreground hover:text-primary ml-auto text-[11px] whitespace-nowrap transition-colors"
      >
        Manage ›
      </Link>
    </div>
  );
}

function TraktMark() {
  return (
    <span
      aria-hidden="true"
      className="bg-primary/10 text-primary flex h-7 w-7 flex-shrink-0 items-center justify-center rounded-lg text-[10px] font-extrabold tracking-tight"
    >
      TK
    </span>
  );
}
