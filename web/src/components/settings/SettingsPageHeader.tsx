import type { ReactNode } from "react";

import { cn } from "@/lib/utils";
import "@/styles/admin-settings.css";

export interface SettingsPageHeaderProps {
  title: string;
  /** One or two lines saying what the page decides. */
  description?: ReactNode;
  /** Defaults to "Settings › {title}". Pass `null` to drop the line. */
  breadcrumb?: ReactNode;
  /** A `StatusStrip`, rendered under the description. */
  strip?: ReactNode;
  /** Right-aligned page actions, level with the title. */
  actions?: ReactNode;
  className?: string;
}

/**
 * The heading block every admin settings tab opens with. The tab owns its own
 * heading (level 2, under the shell's "Settings") so the page reads as a
 * document rather than a panel inside a panel.
 */
export function SettingsPageHeader({
  title,
  description,
  breadcrumb,
  strip,
  actions,
  className,
}: SettingsPageHeaderProps) {
  const crumb =
    breadcrumb === undefined ? (
      <>
        Settings <span aria-hidden="true">›</span>{" "}
        <span className="text-foreground/70">{title}</span>
      </>
    ) : (
      breadcrumb
    );

  return (
    <header className={cn("min-w-0 space-y-2", className)}>
      {crumb ? <p className="text-muted-foreground text-xs">{crumb}</p> : null}
      <div className="flex flex-wrap items-start justify-between gap-3">
        <h2 className="text-[28px] leading-tight font-semibold tracking-[-0.03em]">{title}</h2>
        {actions ? <div className="flex shrink-0 items-center gap-2">{actions}</div> : null}
      </div>
      {description ? (
        <p className="text-muted-foreground max-w-[60ch] text-sm leading-relaxed">{description}</p>
      ) : null}
      {strip ? <div className="pt-2">{strip}</div> : null}
    </header>
  );
}
