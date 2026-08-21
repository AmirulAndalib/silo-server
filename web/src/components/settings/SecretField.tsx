import { useId, useState } from "react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { SettingFieldRow } from "@/pages/admin-settings/SettingField";

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
  const controlId = useId();
  const hintId = useId();
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
      <SettingFieldRow
        label={label}
        description={hint}
        descriptionId={hintId}
        restartRequired={restartRequired}
      >
        <div className="border-border/70 flex w-full items-center justify-between gap-3 rounded-md border px-3 py-1.5 sm:w-60">
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
      </SettingFieldRow>
    );
  }

  const description = configured ? "Enter a replacement value." : hint;

  return (
    <SettingFieldRow
      label={label}
      htmlFor={controlId}
      description={description}
      descriptionId={hintId}
      restartRequired={restartRequired}
    >
      <div className="flex w-full flex-col items-end gap-1 sm:w-60">
        <Input
          id={controlId}
          type="password"
          placeholder={configured ? "configured" : "Not configured"}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          disabled={disabled}
          className="w-full"
          aria-describedby={description ? hintId : undefined}
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
    </SettingFieldRow>
  );
}
