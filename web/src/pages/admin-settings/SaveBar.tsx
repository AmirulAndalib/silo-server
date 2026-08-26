import { useEffect, useState, type ReactNode } from "react";
import { TriangleAlert } from "lucide-react";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { RestartServerButton } from "./RestartServerButton";
import "@/styles/admin-settings.css";

interface SaveBarProps {
  dirtyCount: number;
  onSave: () => void;
  onDiscard: () => void;
  isSaving: boolean;
}

function plural(count: number, word: string) {
  return `${count} ${word}${count === 1 ? "" : "s"}`;
}

/**
 * The floating save pill: the staged count and the two actions. Hidden while
 * the page is clean, so a page with nothing staged has no permanent furniture at
 * the bottom of the viewport. It says nothing about restarts — the one restart
 * prompt is `RestartBanner`, rendered once by the settings shell.
 */
export function SaveBar({ dirtyCount, onSave, onDiscard, isSaving }: SaveBarProps) {
  if (dirtyCount <= 0) return null;

  return (
    <>
      {/* Scrim so page content dissolves under the pill instead of colliding. */}
      <div
        aria-hidden="true"
        className="pointer-events-none fixed inset-x-0 bottom-0 z-30 h-40 bg-gradient-to-t from-[var(--background)] via-[color-mix(in_srgb,var(--background)_72%,transparent)] to-transparent"
      />
      <div
        role="status"
        className="pointer-events-none fixed inset-x-0 bottom-[var(--settings-dock-offset,1.5rem)] z-40 flex justify-center px-4"
      >
        <div className="glass pointer-events-auto flex max-w-full items-center gap-3 rounded-full py-2 pr-2 pl-4 shadow-2xl backdrop-blur-xl sm:gap-4 sm:pl-5">
          <span className="min-w-0 truncate text-[13px] font-medium">
            {plural(dirtyCount, "unsaved change")}
          </span>
          <span className="flex shrink-0 items-center gap-1.5">
            <Button variant="ghost" size="sm" className="rounded-full" onClick={onDiscard}>
              Discard
            </Button>
            <Button
              size="sm"
              onClick={onSave}
              disabled={isSaving}
              className="rounded-full bg-[var(--settings-accent)] text-[#15151a] hover:bg-[var(--settings-accent)] hover:brightness-110"
            >
              {isSaving ? "Saving..." : "Save"}
            </Button>
          </span>
        </div>
      </div>
    </>
  );
}

export interface RestartBannerProps {
  /**
   * Whether a restart is owed. The settings shell already reads
   * `useAdminServerStatus()` for its nav dots, so the flag is passed in rather
   * than queried again here — one caller, one query, one banner.
   */
  restartRequired?: boolean;
  /** One-line explanation under the title. */
  description?: ReactNode;
  className?: string;
}

const DEFAULT_RESTART_DESCRIPTION =
  "Saved settings that are only read at startup take effect after a restart. Playback sessions will reconnect.";

/**
 * The single restart prompt for admin settings: a persistent amber bar pinned
 * to the bottom of the viewport. Render it once from the settings shell — every
 * page shares one server, so a per-page copy just stacks the same warning.
 */
export function RestartBanner({
  restartRequired = false,
  description,
  className,
}: RestartBannerProps) {
  const pending = restartRequired;
  const [dismissed, setDismissed] = useState(false);
  const visible = pending && !dismissed;

  // A fresh reason to restart outranks an earlier "Later".
  const [wasPending, setWasPending] = useState(pending);
  if (pending !== wasPending) {
    setWasPending(pending);
    if (pending) setDismissed(false);
  }

  // Lifts the floating save pill clear of the banner without either component
  // having to know the other is mounted.
  useEffect(() => {
    if (!visible) return;
    const root = document.documentElement;
    root.style.setProperty("--settings-dock-offset", "5.25rem");
    return () => {
      root.style.removeProperty("--settings-dock-offset");
    };
  }, [visible]);

  if (!visible) return null;

  return (
    <div
      role="status"
      className={cn(
        "fixed inset-x-0 bottom-0 z-50 flex flex-wrap items-center gap-3 border-t border-amber-500/30",
        "bg-[linear-gradient(90deg,color-mix(in_srgb,var(--warning)_16%,transparent),color-mix(in_srgb,var(--warning)_6%,transparent))]",
        "px-4 py-3 backdrop-blur-xl sm:px-6",
        className,
      )}
    >
      <span className="grid size-7 shrink-0 place-items-center rounded-lg bg-amber-500/15 text-amber-600 dark:text-amber-400">
        <TriangleAlert className="size-3.5" aria-hidden="true" />
      </span>
      <div className="min-w-0 flex-1">
        <p className="text-[13px] font-medium">Restart required</p>
        <p className="text-muted-foreground truncate text-[11.5px]">
          {description ?? DEFAULT_RESTART_DESCRIPTION}
        </p>
      </div>
      <div className="flex shrink-0 items-center gap-2">
        <Button variant="ghost" size="sm" onClick={() => setDismissed(true)}>
          Later
        </Button>
        <RestartServerButton label="Restart server" />
      </div>
    </div>
  );
}
