import { createContext, useContext, useId, type ReactNode } from "react";

import { cn } from "@/lib/utils";

const GroupRestartContext = createContext(false);

/** True inside a `FieldGroup` where every field only applies after a restart. */
export function useGroupRestartAll(): boolean {
  return useContext(GroupRestartContext);
}

export interface FieldGroupProps {
  /** Sentence-case heading, e.g. "Transcoding". */
  label: string;
  /**
   * Every field in the group only takes effect after a restart. The group says
   * so once and the fields inside drop their own chips.
   */
  restartAll?: boolean;
  /** Right-aligned controls on the heading line. */
  actions?: ReactNode;
  className?: string;
  children: ReactNode;
}

/**
 * A settings group: a heading with a hairline rule under it and the rows
 * directly below. Deliberately not a card — nesting a box per group made the
 * page read as a stack of panels rather than one document.
 */
export function FieldGroup({
  label,
  restartAll = false,
  actions,
  className,
  children,
}: FieldGroupProps) {
  const labelId = useId();
  return (
    <section role="group" aria-labelledby={labelId} className={cn("min-w-0", className)}>
      <div className="border-border/80 flex flex-wrap items-baseline gap-x-3 gap-y-1 border-b pb-2.5">
        <h3 id={labelId} className="flex-1 text-[15px] leading-6 font-semibold tracking-tight">
          {label}
        </h3>
        {actions ? <div className="flex shrink-0 items-center gap-2">{actions}</div> : null}
      </div>
      {restartAll ? (
        <p className="text-muted-foreground pt-2.5 text-xs">Changes apply after a restart</p>
      ) : null}
      <GroupRestartContext.Provider value={restartAll}>
        <div className="[&>*]:border-b [&>*]:border-[color-mix(in_srgb,var(--border)_60%,transparent)] [&>*:last-child]:border-b-0">
          {children}
        </div>
      </GroupRestartContext.Provider>
    </section>
  );
}
