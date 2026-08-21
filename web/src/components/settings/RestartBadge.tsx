import { RotateCw } from "lucide-react";

import { cn } from "@/lib/utils";

const RESTART_TITLE = "Takes effect after a server restart";

/**
 * Small amber chip marking a setting whose value is only read at startup.
 * Driven by the compiled restart-required key list (see `useRestartKeys`) so
 * the fact never has to be hand-copied into a field hint.
 */
export function RestartBadge({ className }: { className?: string }) {
  return (
    <span
      title={RESTART_TITLE}
      aria-label={RESTART_TITLE}
      className={cn(
        "inline-flex shrink-0 items-center gap-1 rounded-full bg-amber-500/10 px-2 py-0.5",
        "text-[10px] font-medium tracking-wide text-amber-600 uppercase dark:text-amber-400",
        className,
      )}
    >
      <RotateCw className="h-2.5 w-2.5" aria-hidden="true" />
      restart
    </span>
  );
}
