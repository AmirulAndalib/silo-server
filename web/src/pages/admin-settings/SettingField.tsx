import { useId, type ReactNode } from "react";
import { Check, TriangleAlert } from "lucide-react";

import { Switch } from "@/components/ui/switch";
import { Label } from "@/components/ui/label";
import { Input } from "@/components/ui/input";
import { RestartBadge } from "@/components/settings/RestartBadge";
import { cn } from "@/lib/utils";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { useGroupRestartAll } from "./FieldGroup";
import "@/styles/admin-settings.css";

interface SelectOption {
  value: string;
  label: string;
  disabled?: boolean;
}

/**
 * One-line probe result rendered under a field description, e.g.
 * "Detected VA-API on renderD128". Pass any node to `status` instead when the
 * copy needs richer markup.
 */
export function SettingFieldStatus({
  tone = "ok",
  children,
}: {
  tone?: "ok" | "warn" | "muted";
  children: ReactNode;
}) {
  const Icon = tone === "warn" ? TriangleAlert : tone === "ok" ? Check : null;
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 text-[12.5px] leading-snug",
        tone === "ok" && "text-green-600 dark:text-green-400",
        tone === "warn" && "text-amber-600 dark:text-amber-400",
        tone === "muted" && "text-muted-foreground",
      )}
    >
      {Icon ? <Icon className="size-3.5 shrink-0" aria-hidden="true" /> : null}
      {children}
    </span>
  );
}

export interface SettingFieldRowProps {
  label: ReactNode;
  /** Ties the label to the control; omit for rows whose control has no id. */
  htmlFor?: string;
  /** Description under the label. One short sentence, or nothing at all. */
  description?: ReactNode;
  descriptionId?: string;
  /** Extra line under the description — probe results, quota notes. */
  status?: ReactNode;
  /** Amber "Restart" chip after the label; drive it from `useRestartKeys`. */
  restartRequired?: boolean;
  /** Toggles sit flush right; every other control reserves a 200px column. */
  align?: "control" | "toggle";
  className?: string;
  /** The control itself. */
  children: ReactNode;
}

/**
 * The row shell every admin setting sits in: label and description on the
 * left, control right-aligned, hairline underneath. Exported so the credential
 * and limit variants line up with plain fields instead of re-deriving spacing.
 */
export function SettingFieldRow({
  label,
  htmlFor,
  description,
  descriptionId,
  status,
  restartRequired,
  align = "control",
  className,
  children,
}: SettingFieldRowProps) {
  // A group that already says "Changes apply after a restart" does not need the
  // same fact repeated on every row inside it.
  const groupSaysRestart = useGroupRestartAll();

  return (
    <div
      className={cn(
        "border-border/60 relative flex flex-col gap-3 border-b py-3.5 last:border-b-0",
        "sm:flex-row sm:items-start sm:gap-6",
        className,
      )}
    >
      <div className="min-w-0 flex-1 sm:max-w-[520px]">
        <div className="flex flex-wrap items-center gap-2">
          <Label htmlFor={htmlFor} className="text-sm font-medium">
            {label}
          </Label>
          {restartRequired && !groupSaysRestart && <RestartBadge />}
        </div>
        {description ? (
          <p id={descriptionId} className="text-muted-foreground mt-1 text-xs leading-relaxed">
            {description}
          </p>
        ) : null}
        {status ? <div className="mt-1.5">{status}</div> : null}
      </div>
      <div
        className={cn(
          "flex shrink-0 items-center gap-2 sm:justify-end",
          align === "control" && "sm:min-w-[200px]",
        )}
      >
        {children}
      </div>
    </div>
  );
}

interface SettingFieldProps {
  label: string;
  type?: "text" | "number" | "password" | "toggle" | "duration" | "select";
  /**
   * Placeholder for `text`, description for every other type. Prefer
   * `description` for new call sites.
   */
  hint?: string;
  /** Always rendered under the label, whatever the type. */
  description?: ReactNode;
  /** Extra line under the description, e.g. a detection result. */
  status?: ReactNode;
  /** Rendered after the control, e.g. "%" or "Mbps". */
  unit?: string;
  value: string;
  onChange: (value: string) => void;
  options?: SelectOption[];
  sensitiveConfigured?: boolean;
  disabled?: boolean;
  /** Marks the field with a restart badge; drive it from `useRestartKeys`. */
  restartRequired?: boolean;
  className?: string;
}

export function SettingField({
  label,
  type = "text",
  hint,
  description,
  status,
  unit,
  value,
  onChange,
  options,
  sensitiveConfigured,
  disabled,
  restartRequired,
  className,
}: SettingFieldProps) {
  const controlId = useId();
  const hintId = useId();

  // `text` keeps treating `hint` as a placeholder (its long-standing
  // behaviour); every other type shows it under the label.
  const hintAsDescription = type === "text" ? undefined : hint;
  const rowDescription = description ?? hintAsDescription;
  const describedBy = rowDescription ? hintId : undefined;

  const row = (control: ReactNode, align: "control" | "toggle" = "control") => (
    <SettingFieldRow
      label={label}
      htmlFor={controlId}
      description={rowDescription}
      descriptionId={hintId}
      status={status}
      restartRequired={restartRequired}
      align={align}
      className={className}
    >
      {control}
      {unit ? <span className="text-muted-foreground text-xs">{unit}</span> : null}
    </SettingFieldRow>
  );

  if (type === "toggle") {
    return row(
      <Switch
        id={controlId}
        checked={value === "true"}
        onCheckedChange={(val) => onChange(val ? "true" : "false")}
        disabled={disabled}
        aria-describedby={describedBy}
      />,
      "toggle",
    );
  }

  if (type === "select" && options) {
    const currentVal = value || options[0]?.value || "";
    return row(
      <Select value={currentVal} onValueChange={onChange} disabled={disabled}>
        <SelectTrigger id={controlId} className="w-full sm:w-60" aria-describedby={describedBy}>
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {options.map((opt) => (
            <SelectItem key={opt.value} value={opt.value} disabled={opt.disabled}>
              {opt.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>,
    );
  }

  if (type === "password") {
    return row(
      <Input
        id={controlId}
        type="password"
        placeholder={sensitiveConfigured ? "configured" : (hint ?? "Not configured")}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        disabled={disabled}
        className="w-full sm:w-60"
        aria-describedby={describedBy}
      />,
    );
  }

  if (type === "number") {
    return row(
      <Input
        id={controlId}
        type="number"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        disabled={disabled}
        className="w-full sm:w-40"
        aria-describedby={describedBy}
      />,
    );
  }

  // text and duration
  return row(
    <Input
      id={controlId}
      type="text"
      value={value}
      onChange={(e) => onChange(e.target.value)}
      disabled={disabled}
      className="w-full sm:w-60"
      placeholder={type === "text" ? hint : undefined}
      aria-describedby={describedBy}
    />,
  );
}
