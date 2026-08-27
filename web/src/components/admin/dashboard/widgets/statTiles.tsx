import type { ReactNode } from "react";
import { Activity, Film, HardDrive, Tv, Users } from "lucide-react";
import { Skeleton } from "@/components/ui/skeleton";
import { useAdminSessions, useAdminStats } from "@/hooks/queries/admin/stats";
import { formatFileCount } from "../format";

function StatTile({
  label,
  value,
  sub,
  icon,
  isLoading,
  error,
}: {
  label: string;
  value: string;
  sub: string;
  icon: ReactNode;
  isLoading: boolean;
  error: unknown;
}) {
  if (isLoading) {
    return <Skeleton className="h-24 rounded-2xl" />;
  }

  return (
    <div className="surface-panel h-full rounded-2xl border-0 p-[18px] transition-colors duration-150">
      <div className="mb-2 flex items-center justify-between">
        <div className="text-muted-foreground text-[11px] font-medium">{label}</div>
        <div className="text-muted-foreground">{icon}</div>
      </div>
      {error ? (
        <div className="text-destructive text-sm">Unavailable</div>
      ) : (
        <>
          <div className="mb-0.5 text-[28px] leading-none font-extrabold tracking-tight">
            {value}
          </div>
          <div className="text-muted-foreground text-[11px]">{sub}</div>
        </>
      )}
    </div>
  );
}

export function ActiveStreamsStatWidget() {
  const sessionsQuery = useAdminSessions();
  const sessionCount = sessionsQuery.data?.length ?? 0;
  return (
    <StatTile
      label="Active Streams"
      value={String(sessionCount)}
      sub={sessionCount === 1 ? "1 session" : `${sessionCount} sessions`}
      icon={<Activity className="h-4 w-4" />}
      isLoading={sessionsQuery.isLoading}
      error={sessionsQuery.error}
    />
  );
}

export function MoviesStatWidget() {
  const statsQuery = useAdminStats();
  const stats = statsQuery.data;
  return (
    <StatTile
      label="Total Movies"
      value={stats ? stats.total_movies.toLocaleString() : "—"}
      sub={formatFileCount(stats?.total_movie_files)}
      icon={<Film className="h-4 w-4" />}
      isLoading={statsQuery.isLoading || (!stats && !statsQuery.error)}
      error={statsQuery.error}
    />
  );
}

export function ShowsStatWidget() {
  const statsQuery = useAdminStats();
  const stats = statsQuery.data;
  return (
    <StatTile
      label="Total Shows"
      value={stats ? stats.total_shows.toLocaleString() : "—"}
      sub={formatFileCount(stats?.total_show_files)}
      icon={<Tv className="h-4 w-4" />}
      isLoading={statsQuery.isLoading || (!stats && !statsQuery.error)}
      error={statsQuery.error}
    />
  );
}

export function UsersStatWidget() {
  const statsQuery = useAdminStats();
  const stats = statsQuery.data;
  return (
    <StatTile
      label="Users"
      value={stats ? String(stats.total_users) : "—"}
      sub={`${stats?.total_users ?? 0} registered`}
      icon={<Users className="h-4 w-4" />}
      isLoading={statsQuery.isLoading || (!stats && !statsQuery.error)}
      error={statsQuery.error}
    />
  );
}

export function StorageStatWidget() {
  const statsQuery = useAdminStats();
  const stats = statsQuery.data;
  let storageDisplay = "—";
  if (stats) {
    const storageGB = stats.total_storage_bytes / (1024 * 1024 * 1024);
    const storageTB = storageGB / 1024;
    storageDisplay = storageTB >= 1 ? `${storageTB.toFixed(1)} TB` : `${storageGB.toFixed(0)} GB`;
  }
  return (
    <StatTile
      label="Storage"
      value={storageDisplay}
      sub={formatFileCount(stats?.total_files)}
      icon={<HardDrive className="h-4 w-4" />}
      isLoading={statsQuery.isLoading || (!stats && !statsQuery.error)}
      error={statsQuery.error}
    />
  );
}
