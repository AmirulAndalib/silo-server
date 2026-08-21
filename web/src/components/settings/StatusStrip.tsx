import { Fragment, type ReactNode } from "react";

import { cn } from "@/lib/utils";
import "@/styles/admin-settings.css";

export type StatusStripTone = "ok" | "warn" | "muted" | "info";

export interface StatusStripItem {
  tone: StatusStripTone;
  label: ReactNode;
}

export interface StatusStripProps {
  items: StatusStripItem[];
  /** Right-aligned aside, e.g. "Saved 4 minutes ago". */
  trailing?: ReactNode;
  className?: string;
}

const DOT_CLASSES: Record<StatusStripTone, string> = {
  ok: "bg-green-500 shadow-[0_0_7px_rgba(129,201,149,.6)]",
  warn: "bg-amber-500 shadow-[0_0_7px_rgba(232,168,124,.6)]",
  info: "bg-[var(--settings-accent)] shadow-[0_0_7px_var(--settings-accent-soft)]",
  muted: "bg-muted-foreground/50",
};

/**
 * The one-line health summary under a settings page title: a few dotted
 * phrases on a slightly raised surface. Facts only — anything actionable
 * belongs in a field or a banner, not here.
 */
export function StatusStrip({ items, trailing, className }: StatusStripProps) {
  if (items.length === 0 && !trailing) return null;

  return (
    <div
      className={cn(
        "border-border/70 text-foreground/85 flex flex-wrap items-center gap-x-3.5 gap-y-2",
        "rounded-xl border bg-[linear-gradient(180deg,color-mix(in_srgb,var(--foreground)_4.5%,transparent),color-mix(in_srgb,var(--foreground)_1.8%,transparent))]",
        "px-3.5 py-2.5 text-[12.5px]",
        className,
      )}
    >
      {items.map((item, index) => (
        <Fragment key={index}>
          {index > 0 ? (
            <span aria-hidden="true" className="bg-muted-foreground/40 size-[3px] rounded-full" />
          ) : null}
          <span data-tone={item.tone} className="flex min-w-0 items-center gap-2">
            <span
              aria-hidden="true"
              className={cn("size-1.5 shrink-0 rounded-full", DOT_CLASSES[item.tone])}
            />
            <span className="truncate">{item.label}</span>
          </span>
        </Fragment>
      ))}
      {trailing ? (
        <span className="text-muted-foreground ml-auto shrink-0 text-xs">{trailing}</span>
      ) : null}
    </div>
  );
}
