import type { ReactNode } from "react";

import { cn } from "@/lib/utils";
import "@/styles/admin-settings.css";

export interface SettingsPageHeaderProps {
  title: string;
  /** Right-aligned page actions, level with the title. */
  actions?: ReactNode;
  className?: string;
}

/**
 * The heading every admin settings tab opens with: the tab's name and nothing
 * else. The tab owns its own heading (level 2, under the shell's "Settings")
 * so the page reads as a document rather than a panel inside a panel.
 */
export function SettingsPageHeader({ title, actions, className }: SettingsPageHeaderProps) {
  return (
    <header className={cn("flex min-w-0 flex-wrap items-start justify-between gap-3", className)}>
      <h2 className="text-[28px] leading-tight font-semibold tracking-[-0.03em]">{title}</h2>
      {actions ? <div className="flex shrink-0 items-center gap-2">{actions}</div> : null}
    </header>
  );
}
