import { useId } from "react";
import { Switch } from "@/components/ui/switch";
import { Label } from "@/components/ui/label";
import { Input } from "@/components/ui/input";
import { RestartBadge } from "@/components/settings/RestartBadge";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";

interface SelectOption {
  value: string;
  label: string;
  disabled?: boolean;
}

interface SettingFieldProps {
  label: string;
  type?: "text" | "number" | "password" | "toggle" | "duration" | "select";
  hint?: string;
  value: string;
  onChange: (value: string) => void;
  options?: SelectOption[];
  sensitiveConfigured?: boolean;
  disabled?: boolean;
  /** Marks the field with a restart badge; drive it from `useRestartKeys`. */
  restartRequired?: boolean;
}

function FieldLabel({
  htmlFor,
  label,
  restartRequired,
}: {
  htmlFor: string;
  label: string;
  restartRequired?: boolean;
}) {
  return (
    <div className="flex items-center gap-2">
      <Label htmlFor={htmlFor} className="text-sm font-medium">
        {label}
      </Label>
      {restartRequired && <RestartBadge />}
    </div>
  );
}

export function SettingField({
  label,
  type = "text",
  hint,
  value,
  onChange,
  options,
  sensitiveConfigured,
  disabled,
  restartRequired,
}: SettingFieldProps) {
  const controlId = useId();
  const hintId = useId();

  if (type === "toggle") {
    const checked = value === "true";
    return (
      <div className="flex flex-col justify-between gap-3 py-3 sm:flex-row sm:items-center">
        <div className="space-y-0.5">
          <FieldLabel htmlFor={controlId} label={label} restartRequired={restartRequired} />
          {hint && (
            <p id={hintId} className="text-muted-foreground text-xs">
              {hint}
            </p>
          )}
        </div>
        <Switch
          id={controlId}
          checked={checked}
          onCheckedChange={(val) => onChange(val ? "true" : "false")}
          disabled={disabled}
          aria-describedby={hint ? hintId : undefined}
        />
      </div>
    );
  }

  if (type === "select" && options) {
    const currentVal = value || options[0]?.value || "";
    return (
      <div className="space-y-1 py-2">
        <FieldLabel htmlFor={controlId} label={label} restartRequired={restartRequired} />
        <Select value={currentVal} onValueChange={onChange} disabled={disabled}>
          <SelectTrigger
            id={controlId}
            className="w-full sm:w-fit"
            aria-describedby={hint ? hintId : undefined}
          >
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {options.map((opt) => (
              <SelectItem key={opt.value} value={opt.value} disabled={opt.disabled}>
                {opt.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        {hint && (
          <p id={hintId} className="text-muted-foreground text-xs">
            {hint}
          </p>
        )}
      </div>
    );
  }

  if (type === "password") {
    const placeholder = sensitiveConfigured ? "configured" : (hint ?? "Not configured");
    return (
      <div className="space-y-1 py-2">
        <FieldLabel htmlFor={controlId} label={label} restartRequired={restartRequired} />
        <Input
          id={controlId}
          type="password"
          placeholder={placeholder}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          disabled={disabled}
          className="max-w-md"
          aria-describedby={hint ? hintId : undefined}
        />
        {hint && (
          <p id={hintId} className="text-muted-foreground text-xs">
            {hint}
          </p>
        )}
      </div>
    );
  }

  if (type === "number") {
    return (
      <div className="space-y-1 py-2">
        <FieldLabel htmlFor={controlId} label={label} restartRequired={restartRequired} />
        <Input
          id={controlId}
          type="number"
          value={value}
          onChange={(e) => onChange(e.target.value)}
          disabled={disabled}
          className="w-full sm:w-40"
          aria-describedby={hint ? hintId : undefined}
        />
        {hint && (
          <p id={hintId} className="text-muted-foreground text-xs">
            {hint}
          </p>
        )}
      </div>
    );
  }

  // text and duration
  return (
    <div className="space-y-1 py-2">
      <FieldLabel htmlFor={controlId} label={label} restartRequired={restartRequired} />
      <Input
        id={controlId}
        type="text"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        disabled={disabled}
        className="max-w-md"
        placeholder={hint}
        aria-describedby={hint && type === "duration" ? hintId : undefined}
      />
      {hint && type === "duration" && (
        <p id={hintId} className="text-muted-foreground text-xs">
          {hint}
        </p>
      )}
    </div>
  );
}
