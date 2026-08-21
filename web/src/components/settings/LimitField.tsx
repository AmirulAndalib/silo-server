import { useId, useState } from "react";

import { Input } from "@/components/ui/input";
import { SettingFieldRow } from "@/pages/admin-settings/SettingField";

export interface LimitFieldProps {
  label: string;
  /** Stored value; equal to `unlimitedValue` when the limit is off. */
  value: string;
  onChange: (value: string) => void;
  /** Sentinel the backend reads as "no limit". */
  unlimitedValue?: string;
  /** Fallback used when a limit is re-enabled and nothing was typed before. */
  fallbackValue?: string;
  unlimitedLabel?: string;
  /** Rendered after the input, e.g. "Mbps". */
  unit?: string;
  hint?: string;
  min?: number;
  disabled?: boolean;
  restartRequired?: boolean;
}

/**
 * Number input paired with an "Unlimited" checkbox, replacing the
 * "0 = unlimited" hint convention. The sentinel never reaches the admin's
 * eyes, but the saved value is unchanged.
 */
export function LimitField({
  label,
  value,
  onChange,
  unlimitedValue = "0",
  fallbackValue = "",
  unlimitedLabel = "Unlimited",
  unit,
  hint,
  min = 0,
  disabled = false,
  restartRequired = false,
}: LimitFieldProps) {
  const controlId = useId();
  const checkboxId = useId();
  const hintId = useId();
  const unlimited = value.trim() === unlimitedValue;
  // Remembers the limit that Unlimited replaced so unchecking restores it
  // instead of dumping the admin back onto an empty box.
  const [lastLimit, setLastLimit] = useState(fallbackValue);

  function toggleUnlimited(checked: boolean) {
    if (checked) {
      setLastLimit(unlimited ? fallbackValue : value);
      onChange(unlimitedValue);
      return;
    }
    onChange(lastLimit.trim() === unlimitedValue ? fallbackValue : lastLimit);
  }

  return (
    <SettingFieldRow
      label={label}
      htmlFor={controlId}
      description={hint}
      descriptionId={hintId}
      restartRequired={restartRequired}
    >
      <div className="flex flex-wrap items-center justify-end gap-x-3 gap-y-2">
        <Input
          id={controlId}
          type="number"
          min={min}
          value={unlimited ? "" : value}
          placeholder={unlimited ? unlimitedLabel : undefined}
          onChange={(e) => onChange(e.target.value)}
          disabled={disabled || unlimited}
          className="w-28"
          aria-describedby={hint ? hintId : undefined}
        />
        {unit && <span className="text-muted-foreground text-xs">{unit}</span>}
        <label htmlFor={checkboxId} className="flex items-center gap-2 text-sm">
          <input
            id={checkboxId}
            type="checkbox"
            checked={unlimited}
            onChange={(e) => toggleUnlimited(e.target.checked)}
            disabled={disabled}
          />
          {unlimitedLabel}
        </label>
      </div>
    </SettingFieldRow>
  );
}
