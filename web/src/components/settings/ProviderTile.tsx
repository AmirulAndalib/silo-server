import { useId, type ReactNode } from "react";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import "@/styles/admin-settings.css";

export type ProviderTileState = "connected" | "not_connected" | "error" | "editing";

export interface ProviderTileAction {
  label: string;
  onClick: () => void;
  disabled?: boolean;
}

export interface ProviderTileProps {
  name: string;
  /** One short line under the name, e.g. "Community subtitles". */
  tagline?: ReactNode;
  /** Two letters for the logo square; ignored when `logo` is given. */
  monogram?: string;
  /** Background/foreground classes for the logo square. */
  monogramClass?: string;
  /** Replaces the monogram, e.g. with an icon. */
  logo?: ReactNode;
  state: ProviderTileState;
  /** Overrides the default pill text for the state. */
  statePill?: string;
  /** Small line at the foot: quota, key expiry, or the error text. */
  meta?: ReactNode;
  /** The tile's own button — Connect, Manage, Fix. */
  primaryAction?: ProviderTileAction;
  /** Chips beside the name, e.g. a `RestartBadge`. */
  badge?: ReactNode;
  /** Controls level with the state pill, e.g. an enable switch. */
  headerActions?: ReactNode;
  /** Spans the tile across the grid and reveals `children` as an inline panel. */
  expanded?: boolean;
  /** Disables every control inside while a request is in flight. */
  busy?: boolean;
  className?: string;
  /** The inline connect panel: credential fields and their action row. */
  children?: ReactNode;
}

const PILL_LABELS: Record<ProviderTileState, string> = {
  connected: "Connected",
  not_connected: "Not connected",
  error: "Error",
  editing: "Editing",
};

const PILL_CLASSES: Record<ProviderTileState, string> = {
  connected: "border-green-500/30 bg-green-500/10 text-green-700 dark:text-green-300",
  not_connected: "border-border text-muted-foreground bg-foreground/[0.04]",
  error: "border-red-500/35 bg-red-500/10 text-red-600 dark:text-red-300",
  editing:
    "border-[var(--settings-accent-line)] bg-[var(--settings-accent-soft)] text-[var(--settings-accent)]",
};

const PILL_DOT_CLASSES: Record<ProviderTileState, string> = {
  connected: "bg-green-500 shadow-[0_0_6px_rgba(129,201,149,.8)]",
  not_connected: "bg-muted-foreground/50",
  error: "bg-red-500 shadow-[0_0_6px_rgba(239,68,68,.8)]",
  editing: "bg-[var(--settings-accent)]",
};

const TILE_CLASSES: Record<ProviderTileState, string> = {
  connected: "border-green-500/25 bg-[linear-gradient(165deg,rgba(129,201,149,.075),transparent)]",
  not_connected: "border-border/70 bg-foreground/[0.025]",
  error: "border-red-500/30 bg-[linear-gradient(165deg,rgba(239,68,68,.07),transparent)]",
  editing:
    "border-[var(--settings-accent-line)] bg-[linear-gradient(165deg,var(--settings-accent-soft),transparent)]",
};

/** Status pill used on a tile and, standalone, anywhere a provider is listed. */
export function ProviderStatePill({
  state,
  label,
  className,
}: {
  state: ProviderTileState;
  label?: string;
  className?: string;
}) {
  return (
    <span
      data-state={state}
      className={cn(
        "inline-flex shrink-0 items-center gap-1.5 rounded-full border px-2 py-0.5",
        "text-[11px] leading-5 font-medium whitespace-nowrap",
        PILL_CLASSES[state],
        className,
      )}
    >
      <span aria-hidden="true" className={cn("size-1.5 rounded-full", PILL_DOT_CLASSES[state])} />
      {label ?? PILL_LABELS[state]}
    </span>
  );
}

/** Three-up grid for tiles; an expanded tile spans the full width inside it. */
export function ProviderTileGrid({
  className,
  children,
}: {
  className?: string;
  children: ReactNode;
}) {
  return (
    <div className={cn("grid gap-3 sm:grid-cols-2 xl:grid-cols-3", className)}>{children}</div>
  );
}

/**
 * One third-party provider: identity, connection state, and — once expanded —
 * its credential panel inline instead of in a dialog, so the admin never loses
 * sight of the list they are working through.
 */
export function ProviderTile({
  name,
  tagline,
  monogram,
  monogramClass,
  logo,
  state,
  statePill,
  meta,
  primaryAction,
  badge,
  headerActions,
  expanded = false,
  busy = false,
  className,
  children,
}: ProviderTileProps) {
  const headingId = useId();
  const mark = logo ?? (monogram ?? name.slice(0, 2)).toUpperCase();

  return (
    <fieldset
      disabled={busy}
      aria-labelledby={headingId}
      data-state={state}
      data-expanded={expanded ? "true" : undefined}
      className={cn(
        "flex min-w-0 flex-col rounded-2xl border p-4 transition-colors",
        TILE_CLASSES[state],
        expanded && "col-span-full shadow-lg",
        className,
      )}
    >
      <div className="flex items-center gap-3">
        <span
          aria-hidden="true"
          className={cn(
            "grid size-9 shrink-0 place-items-center rounded-[10px] text-sm font-bold tracking-tight",
            monogramClass ?? "bg-muted text-muted-foreground",
          )}
        >
          {mark}
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <h3 id={headingId} className="truncate text-sm font-medium">
              {name}
            </h3>
            {badge}
          </div>
          {tagline ? (
            <p className="text-muted-foreground mt-0.5 truncate text-xs">{tagline}</p>
          ) : null}
        </div>
        {expanded ? (
          <div className="flex shrink-0 items-center gap-2">
            <ProviderStatePill state={state} label={statePill} />
            {headerActions}
          </div>
        ) : (
          headerActions
        )}
      </div>

      {!expanded && (
        <div className="mt-3.5 flex items-center justify-between gap-2">
          <ProviderStatePill state={state} label={statePill} />
          {primaryAction ? (
            <Button
              type="button"
              size="sm"
              variant="outline"
              onClick={primaryAction.onClick}
              disabled={primaryAction.disabled}
            >
              {primaryAction.label}
            </Button>
          ) : null}
        </div>
      )}

      {meta ? (
        <p
          className={cn(
            "mt-2.5 text-[11.5px]",
            state === "error" ? "text-red-600 dark:text-red-300" : "text-muted-foreground",
          )}
        >
          {meta}
        </p>
      ) : null}

      {expanded && children ? (
        <div className="border-border/70 mt-3.5 border-t pt-3.5">{children}</div>
      ) : null}
    </fieldset>
  );
}
