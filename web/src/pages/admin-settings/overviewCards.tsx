import type { LucideIcon } from "lucide-react";
import {
  Bell,
  ChevronRight,
  Database,
  HardDrive,
  Link2,
  Mail,
  Paintbrush,
  PlayCircle,
  Plug,
  Search,
  Server,
  Settings2,
  ShieldCheck,
  Sparkles,
  RefreshCw,
  Wand2,
} from "lucide-react";
import { Link } from "react-router";

import { Skeleton } from "@/components/ui/skeleton";
import { cn } from "@/lib/utils";
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

/** Icon per settings section, keyed by tab id. */
const CARD_ICONS: Record<SettingsOverviewTabID, LucideIcon> = {
  general: Settings2,
  appearance: Paintbrush,
  security: ShieldCheck,
  library: Wand2,
  playback: PlayCircle,
  providers: Plug,
  "watch-sync": RefreshCw,
  ai: Sparkles,
  notifications: Bell,
  compatibility: Link2,
  infrastructure: Server,
};

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

/** One settings section: icon, title, chevron, and what it is doing today. */
export function SectionCard({ card }: { card: OverviewCard }) {
  const Icon = CARD_ICONS[card.id] ?? Settings2;

  return (
    <Link
      to={settingsTabHref(card.id)}
      data-testid={`overview-card-${card.id}`}
      data-attention={card.attention ? "true" : undefined}
      className={cn(
        PANEL,
        "group hover:border-border block p-4",
        "focus-visible:ring-ring focus-visible:ring-2 focus-visible:outline-none",
      )}
    >
      <div className="flex items-center gap-2.5">
        <Icon className="text-muted-foreground size-4 shrink-0" aria-hidden="true" />
        <h3 className="flex-1 truncate text-sm font-medium">{card.title}</h3>
        <ChevronRight
          className="text-muted-foreground group-hover:text-foreground size-4 transition-colors"
          aria-hidden="true"
        />
      </div>
      <p
        className={cn(
          "mt-1.5 truncate text-xs",
          card.attention ? "text-amber-400" : "text-muted-foreground",
        )}
      >
        {card.summary}
      </p>
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
    <div className={cn(PANEL, "space-y-2 p-4")}>
      <Skeleton className="h-4 w-32" />
      <Skeleton className="h-3 w-2/3" />
    </div>
  );
}
