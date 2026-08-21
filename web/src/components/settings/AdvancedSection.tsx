import { useState, type ReactNode } from "react";
import { ChevronDown } from "lucide-react";

import { cn } from "@/lib/utils";

const STORAGE_PREFIX = "silo.admin.advanced.";

function storageKey(id: string) {
  return `${STORAGE_PREFIX}${id}`;
}

function readPersisted(id: string): boolean | null {
  try {
    const raw = localStorage.getItem(storageKey(id));
    if (raw === "true") return true;
    if (raw === "false") return false;
    return null;
  } catch {
    return null;
  }
}

function writePersisted(id: string, open: boolean): void {
  try {
    localStorage.setItem(storageKey(id), open ? "true" : "false");
  } catch {
    // Storage full or unavailable: the disclosure still works this session.
  }
}

export interface AdvancedSectionProps {
  /** Stable id for the persisted open state, e.g. `playback.transcoding`. */
  id: string;
  /** Number of settings inside, rendered as "Advanced · N settings". */
  count?: number;
  title?: string;
  /** Open state used when nothing is persisted yet. */
  defaultOpen?: boolean;
  /**
   * Forces the section open regardless of the persisted state — pass the
   * section's dirty/invalid/search-match state so a hidden field can never be
   * the reason a save bar refuses to save.
   */
  forceOpen?: boolean;
  children: ReactNode;
}

/**
 * The single disclosure primitive for advanced admin settings. Collapsed by
 * default, remembers the admin's choice per section in localStorage, and
 * auto-expands while `forceOpen` is set.
 */
export function AdvancedSection({
  id,
  count,
  title = "Advanced",
  defaultOpen = false,
  forceOpen = false,
  children,
}: AdvancedSectionProps) {
  // Persisted choice, read once: a section's id is fixed for the life of the
  // instance (give the component a `key` if a caller ever swaps ids).
  const [persistedOpen, setPersistedOpen] = useState(() => readPersisted(id) ?? defaultOpen);
  // Explicit toggle this session, which also wins over `forceOpen` so an
  // auto-expanded section can still be collapsed.
  const [override, setOverride] = useState<boolean | null>(null);
  const [wasForcedOpen, setWasForcedOpen] = useState(forceOpen);

  // A manual collapse only outranks the *current* reason to force the section
  // open. When a new one arrives (a field inside just went dirty or invalid, or
  // a search started matching), drop the override so the save bar can never
  // block on a field the admin cannot see. Adjusting state during render is
  // cheaper than an effect: React re-renders before committing.
  if (forceOpen !== wasForcedOpen) {
    setWasForcedOpen(forceOpen);
    if (forceOpen) setOverride(null);
  }

  const open = override ?? (persistedOpen || forceOpen);

  function toggle() {
    const next = !open;
    setOverride(next);
    setPersistedOpen(next);
    writePersisted(id, next);
  }

  const label =
    typeof count === "number" ? `${title} · ${count} setting${count === 1 ? "" : "s"}` : title;

  return (
    <section className="surface-panel-subtle overflow-hidden rounded-xl">
      <button
        type="button"
        className="hover:bg-surface-hover/40 flex w-full items-center gap-2 px-4 py-3 text-left transition-colors"
        aria-expanded={open}
        onClick={toggle}
      >
        <ChevronDown
          className={cn(
            "text-muted-foreground h-4 w-4 shrink-0 transition-transform",
            !open && "-rotate-90",
          )}
          aria-hidden="true"
        />
        <span className="text-muted-foreground min-w-0 flex-1 text-xs font-semibold tracking-wide">
          {label}
        </span>
      </button>
      {open ? <div className="divide-border divide-y px-4 pb-3">{children}</div> : null}
    </section>
  );
}
