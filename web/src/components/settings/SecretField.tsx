import { useState } from "react";

import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { RestartBadge } from "@/components/settings/RestartBadge";
import { SettingField } from "@/pages/admin-settings/SettingField";

export interface SecretFieldProps {
  label: string;
  /** Staged plaintext value; always empty while the saved secret is kept. */
  value: string;
  /** Whether the server already stores a value for this key. */
  configured: boolean;
  onChange: (value: string) => void;
  /**
   * Controlled replacement state. Leave undefined to let the field track it
   * itself; pass it when the parent has to reset several secrets at once.
   */
  editing?: boolean;
  /** Called when the admin starts replacing a saved secret. */
  onReplace?: () => void;
  /** Called when the admin abandons the replacement (revert the staged value). */
  onKeep?: () => void;
  hint?: string;
  disabled?: boolean;
  restartRequired?: boolean;
}

/**
 * The single credential control for admin settings. Three states:
 * saved (summary + Replace), replacing (password input + Keep saved value),
 * and unset (plain password input).
 */
export function SecretField({
  label,
  value,
  configured,
  onChange,
  editing,
  onReplace,
  onKeep,
  hint,
  disabled = false,
  restartRequired = false,
}: SecretFieldProps) {
  const [internalEditing, setInternalEditing] = useState(false);
  const isEditing = editing ?? internalEditing;

  function beginReplace() {
    if (disabled) return;
    setInternalEditing(true);
    onReplace?.();
  }

  function keepSaved() {
    if (disabled) return;
    setInternalEditing(false);
    // A parent that stages values (useSettingsForm) reverts the draft itself;
    // clearing through onChange there would leave the key marked dirty.
    if (onKeep) onKeep();
    else onChange("");
  }

  if (configured && !isEditing) {
    return (
      <div className="space-y-1 py-2">
        <div className="flex items-center gap-2">
          <Label className="text-sm font-medium">{label}</Label>
          {restartRequired && <RestartBadge />}
        </div>
        <div className="flex max-w-md items-center justify-between gap-3 rounded-md border px-3 py-1.5">
          <span className="text-muted-foreground text-sm">Configured</span>
          <Button
            type="button"
            size="xs"
            variant="outline"
            aria-label={`Replace ${label}`}
            onClick={beginReplace}
            disabled={disabled}
          >
            Replace
          </Button>
        </div>
        {hint && <p className="text-muted-foreground text-xs">{hint}</p>}
      </div>
    );
  }

  return (
    <div className="space-y-1">
      <SettingField
        label={label}
        type="password"
        value={value}
        onChange={onChange}
        hint={configured ? "Enter a replacement value." : hint}
        disabled={disabled}
        restartRequired={restartRequired}
      />
      {configured && (
        <Button
          type="button"
          size="xs"
          variant="ghost"
          aria-label={`Keep saved ${label}`}
          onClick={keepSaved}
          disabled={disabled}
        >
          Keep saved value
        </Button>
      )}
    </div>
  );
}
