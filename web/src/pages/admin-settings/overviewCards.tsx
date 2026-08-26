import type { LucideIcon } from "lucide-react";
import {
  ChevronRight,
  Database,
  HardDrive,
  Mail,
  PlayCircle,
  Search,
  Server,
  Settings2,
} from "lucide-react";
import { Link } from "react-router";

import { Skeleton } from "@/components/ui/skeleton";
import { cn } from "@/lib/utils";
import { ADMIN_SETTINGS_NAV } from "@/lib/adminSettingsSearch";
import {
  settingsTabHref,
  type OverviewCard,
  type OverviewTile,
  type SettingsOverviewTabID,
} from "@/hooks/admin/useSettingsOverview";

/** Icon per health tile, keyed by the tile ids the hook emits. */
const TILE_ICONS: Record<string, LucideIcon> = {
  storage: HardDrive,
  database: Database,
  transcoding: PlayCircle,
  search: Search,
  email: Mail,
};

const CARD_METADATA = Object.fromEntries(
  ADMIN_SETTINGS_NAV.map((item) => [item.id, { description: item.description, icon: item.icon }]),
) as Record<SettingsOverviewTabID, { description: string; icon: LucideIcon }>;

const PANEL = "border-border/70 rounded-xl border";

/**
 * One health tile. Only tiles asking for something are rendered, so the tile
 * says what is wrong and links to the tab that fixes it.
 */
export function HealthTile({ tile }: { tile: OverviewTile }) {
  const Icon = TILE_ICONS[tile.id] ?? Server;
  const warn = tile.state === "warn";

  return (
    <div
      data-testid={`overview-tile-${tile.id}`}
      data-state={tile.state}
      className={cn(PANEL, "flex flex-col p-3.5")}
    >
      <span className="flex items-center gap-2">
        <Icon
          className={cn("size-4 shrink-0", warn ? "text-amber-400" : "text-muted-foreground")}
          aria-hidden="true"
        />
        <span className="text-muted-foreground text-xs">{tile.label}</span>
      </span>
      <span className={cn("mt-1.5 text-sm font-medium", warn && "text-amber-300")}>
        {tile.stateText}
      </span>
      {tile.detail ? (
        <span className="text-muted-foreground mt-1 text-xs leading-snug">{tile.detail}</span>
      ) : null}
      {tile.action ? (
        <Link
          to={settingsTabHref(tile.action.tab)}
          aria-label={`${tile.action.label} — ${tile.label}`}
          className={cn(
            "text-foreground/80 hover:text-foreground mt-auto inline-flex items-center gap-1 pt-2.5",
            "focus-visible:ring-ring rounded-sm text-xs font-medium focus-visible:ring-2 focus-visible:outline-none",
          )}
        >
          {tile.action.label}
          <ChevronRight className="size-3.5" aria-hidden="true" />
        </Link>
      ) : null}
    </div>
  );
}

/** One settings section: its scope first, then the current configuration. */
export function SectionCard({ card }: { card: OverviewCard }) {
  const metadata = CARD_METADATA[card.id];
  const Icon = metadata?.icon ?? Settings2;

  return (
    <Link
      to={settingsTabHref(card.id)}
      data-testid={`overview-card-${card.id}`}
      data-attention={card.attention ? "true" : undefined}
      className={cn(
        PANEL,
        "group hover:border-ring/40 bg-card/25 flex min-h-44 flex-col p-5 transition-all",
        "hover:-translate-y-0.5 hover:shadow-lg hover:shadow-black/10",
        "focus-visible:ring-ring focus-visible:ring-2 focus-visible:outline-none",
      )}
    >
      <div className="flex items-start gap-3.5">
        <span className="bg-accent/75 text-foreground flex size-10 shrink-0 items-center justify-center rounded-xl">
          <Icon className="size-[18px]" aria-hidden="true" />
        </span>
        <div className="min-w-0 flex-1">
          <h3 className="text-base leading-6 font-semibold tracking-tight">{card.title}</h3>
          <p className="text-muted-foreground mt-1.5 max-w-2xl text-[13px] leading-relaxed">
            {metadata?.description}
          </p>
        </div>
        <ChevronRight
          className="text-muted-foreground group-hover:text-foreground mt-1 size-4 shrink-0 transition-colors"
          aria-hidden="true"
        />
      </div>
      <div className="border-border/50 mt-auto flex items-center gap-2 border-t pt-4">
        <span
          className={cn(
            "size-1.5 shrink-0 rounded-full",
            card.attention
              ? "bg-amber-400"
              : card.inactive
                ? "bg-muted-foreground/45"
                : "bg-emerald-500",
          )}
          aria-hidden="true"
        />
        <span className="text-muted-foreground text-[11px] font-semibold tracking-wide uppercase">
          Current
        </span>
        <p
          className={cn(
            "min-w-0 truncate text-xs",
            card.attention ? "text-amber-400" : "text-foreground/75",
          )}
        >
          {card.summary}
        </p>
      </div>
    </Link>
  );
}

/** Placeholder tile shown while the settings map is still in flight. */
export function HealthTileSkeleton() {
  return (
    <div className={cn(PANEL, "flex flex-col gap-2 p-3.5")}>
      <Skeleton className="h-3 w-16" />
      <Skeleton className="h-4 w-20" />
      <Skeleton className="h-3 w-24" />
    </div>
  );
}

/** Placeholder card shown while the settings map is still in flight. */
export function SectionCardSkeleton() {
  return (
    <div className={cn(PANEL, "flex min-h-44 flex-col p-5")}>
      <div className="flex items-start gap-3.5">
        <Skeleton className="size-10 rounded-xl" />
        <div className="flex-1 space-y-2">
          <Skeleton className="h-5 w-36" />
          <Skeleton className="h-3 w-4/5" />
          <Skeleton className="h-3 w-3/5" />
        </div>
      </div>
      <Skeleton className="mt-auto h-3 w-1/2" />
    </div>
  );
}
