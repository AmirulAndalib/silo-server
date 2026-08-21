import { useId, type ReactNode } from "react";

import { cn } from "@/lib/utils";

export interface FieldGroupProps {
  /** Sentence-case heading, e.g. "Transcoding". */
  label: string;
  /** Muted phrase beside the heading explaining what the group covers. */
  clarifier?: string;
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
export function FieldGroup({ label, clarifier, actions, className, children }: FieldGroupProps) {
  const labelId = useId();
  return (
    <section role="group" aria-labelledby={labelId} className={cn("min-w-0", className)}>
      <div className="border-border/80 flex flex-wrap items-baseline gap-x-3 gap-y-1 border-b pb-2.5">
        <h3 id={labelId} className="text-[15px] leading-6 font-semibold tracking-tight">
          {label}
        </h3>
        {clarifier ? (
          <p className="text-muted-foreground min-w-0 flex-1 text-xs">{clarifier}</p>
        ) : (
          <span className="flex-1" />
        )}
        {actions ? <div className="flex shrink-0 items-center gap-2">{actions}</div> : null}
      </div>
      <div className="[&>*]:border-b [&>*]:border-[color-mix(in_srgb,var(--border)_60%,transparent)] [&>*:last-child]:border-b-0">
        {children}
      </div>
    </section>
  );
}
