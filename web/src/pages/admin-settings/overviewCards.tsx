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
  type OverviewState,
  type OverviewTile,
  type OverviewTone,
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

/**
 * Layered surface shared by tiles and cards: a slightly lifted gradient with a
 * 1px highlight along the top edge, so panels read as raised rather than as
 * flat grey boxes.
 */
const RAISED_SURFACE = [
  "relative overflow-hidden rounded-2xl border transition-colors",
  "bg-[linear-gradient(168deg,var(--surface-hover),var(--surface)_62%)]",
  "shadow-[inset_0_1px_0_rgb(255_255_255/0.05),0_18px_40px_-28px_rgb(0_0_0/0.75)]",
  "before:pointer-events-none before:absolute before:inset-x-0 before:top-0 before:h-px",
  "before:bg-[linear-gradient(90deg,transparent,rgb(255_255_255/0.10),transparent)]",
  "before:content-['']",
];

const STATE_ICON_WELL: Record<OverviewState, string> = {
  ok: "bg-emerald-500/12 text-emerald-400",
  warn: "bg-amber-500/12 text-amber-400",
  info: "bg-sky-500/12 text-sky-300",
  off: "bg-foreground/[0.05] text-muted-foreground",
};

const STATE_TEXT: Record<OverviewState, string> = {
  ok: "text-emerald-300",
  warn: "text-amber-300",
  info: "text-sky-200",
  off: "text-foreground/85",
};

const TONE_TEXT: Record<OverviewTone, string> = {
  default: "text-foreground font-medium",
  muted: "text-muted-foreground",
  ok: "text-emerald-400 font-medium",
  warn: "text-amber-400 font-medium",
};

/**
 * One compact health tile: icon well, section name, a one-word state in
 * colour, a supporting line, and a quiet link into the tab that fixes it.
 */
export function HealthTile({ tile }: { tile: OverviewTile }) {
  const Icon = TILE_ICONS[tile.id] ?? Server;
  const needsAttention = tile.state === "warn";

  return (
    <div
      data-testid={`overview-tile-${tile.id}`}
      data-state={tile.state}
      className={cn(
        RAISED_SURFACE,
        "flex flex-col p-3.5",
        needsAttention
          ? "border-amber-500/35 bg-[linear-gradient(168deg,rgb(245_158_11/0.09),var(--surface)_60%)]"
          : "border-border/70",
      )}
    >
      <span
        className={cn(
          "mb-2.5 grid size-7 shrink-0 place-items-center rounded-[9px]",
          STATE_ICON_WELL[tile.state],
        )}
      >
        <Icon className="size-4" aria-hidden="true" />
      </span>
      <span className="text-muted-foreground text-xs">{tile.label}</span>
      <span
        className={cn(
          "mt-0.5 text-[15px] leading-tight font-semibold tracking-[-0.015em]",
          STATE_TEXT[tile.state],
        )}
      >
        {tile.stateText}
      </span>
      {tile.detail ? (
        <span className="text-muted-foreground/80 mt-1 text-xs leading-snug">{tile.detail}</span>
      ) : null}
      {tile.action ? (
        <Link
          to={settingsTabHref(tile.action.tab)}
          aria-label={`${tile.action.label} — ${tile.label}`}
          className={cn(
            "mt-auto inline-flex items-center gap-1 pt-2.5 text-xs font-medium",
            "focus-visible:ring-ring rounded-sm focus-visible:ring-2 focus-visible:outline-none",
            needsAttention
              ? "text-amber-300 hover:text-amber-200"
              : "text-muted-foreground hover:text-foreground",
          )}
        >
          {tile.action.label}
          <ChevronRight className="size-3.5" aria-hidden="true" />
        </Link>
      ) : null}
    </div>
  );
}

/**
 * One settings section as a card: icon, title, chevron, and the two or three
 * live values that say what the section is currently doing.
 */
export function SectionCard({ card }: { card: OverviewCard }) {
  const Icon = CARD_ICONS[card.id] ?? Settings2;

  return (
    <Link
      to={settingsTabHref(card.id)}
      data-testid={`overview-card-${card.id}`}
      data-attention={card.attention ? "true" : undefined}
      className={cn(
        RAISED_SURFACE,
        "group block p-4 pt-3.5",
        "focus-visible:ring-ring focus-visible:ring-2 focus-visible:outline-none",
        card.attention
          ? "border-amber-500/35 hover:border-amber-500/50"
          : "border-border/70 hover:border-border",
      )}
    >
      <div className="mb-2 flex items-center gap-2.5">
        <span
          className={cn(
            "grid size-7 shrink-0 place-items-center rounded-[9px]",
            card.attention ? "bg-amber-500/12 text-amber-400" : "bg-primary/10 text-foreground/80",
          )}
        >
          <Icon className="size-4" aria-hidden="true" />
        </span>
        <h3 className="flex-1 text-sm font-semibold tracking-[-0.012em]">{card.title}</h3>
        <ChevronRight
          className="text-muted-foreground group-hover:text-foreground size-4 transition-colors"
          aria-hidden="true"
        />
      </div>
      <dl>
        {card.rows.map((row) => (
          <div
            key={row.label}
            className="border-border/45 flex items-center justify-between gap-3 border-t py-1 text-xs first:border-t-0"
          >
            <dt className="text-muted-foreground min-w-0 truncate">{row.label}</dt>
            <dd className={cn("truncate text-right", TONE_TEXT[row.tone ?? "default"])}>
              {row.value}
            </dd>
          </div>
        ))}
      </dl>
    </Link>
  );
}

/** Placeholder tile shown while the settings map is still in flight. */
export function HealthTileSkeleton() {
  return (
    <div className={cn(RAISED_SURFACE, "border-border/70 flex flex-col gap-2 p-3.5")}>
      <Skeleton className="size-7 rounded-[9px]" />
      <Skeleton className="h-3 w-16" />
      <Skeleton className="h-4 w-20" />
      <Skeleton className="h-3 w-24" />
    </div>
  );
}

/** Placeholder card shown while the settings map is still in flight. */
export function SectionCardSkeleton() {
  return (
    <div className={cn(RAISED_SURFACE, "border-border/70 space-y-2 p-4 pt-3.5")}>
      <div className="mb-2 flex items-center gap-2.5">
        <Skeleton className="size-7 rounded-[9px]" />
        <Skeleton className="h-4 w-32" />
      </div>
      <Skeleton className="h-3 w-full" />
      <Skeleton className="h-3 w-full" />
      <Skeleton className="h-3 w-2/3" />
    </div>
  );
}

/** Small ruled heading above each band of the overview. */
export function OverviewSectionTitle({ title, note }: { title: string; note?: string }) {
  return (
    <div className="mb-3 flex items-baseline gap-2.5">
      <h2 className="text-sm font-semibold tracking-[-0.01em]">{title}</h2>
      {note ? <span className="text-muted-foreground text-xs">{note}</span> : null}
      <span
        className="from-border h-px flex-1 bg-gradient-to-r to-transparent"
        aria-hidden="true"
      />
    </div>
  );
}
