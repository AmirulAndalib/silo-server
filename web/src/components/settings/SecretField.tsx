import { useId } from "react";

import { Input } from "@/components/ui/input";
import { SettingFieldRow } from "@/pages/admin-settings/SettingField";

export interface SecretFieldProps {
  label: string;
  /** Staged plaintext value; empty while the saved secret is kept. */
  value: string;
  /** Whether the server already stores a value for this key. */
  configured: boolean;
  onChange: (value: string) => void;
  /**
   * Called when the input is emptied while a saved value exists, so a
   * settings-form parent can revert the staged draft (`form.resetValue`)
   * instead of staging `""` — a dirty `""` would clear the secret on save.
   * Draft-based parents that already skip empty values may omit it.
   */
  onKeep?: () => void;
  hint?: string;
  disabled?: boolean;
  restartRequired?: boolean;
}

/**
 * The single credential control for admin settings: one always-editable
 * password input. A saved secret shows as a masked placeholder, typing stages
 * a replacement, and emptying the input keeps the saved value — clearing a
 * secret for real is a page-level action (Disconnect, Clear credentials),
 * never an empty save.
 */
export function SecretField({
  label,
  value,
  configured,
  onChange,
  onKeep,
  hint,
  disabled = false,
  restartRequired = false,
}: SecretFieldProps) {
  const controlId = useId();
  const hintId = useId();

  const description =
    hint ?? (configured ? "Type to replace the saved value; leave blank to keep it." : undefined);

  function handleChange(next: string) {
    if (next === "" && configured) {
      // Emptying the field means "keep the saved secret", never "clear it".
      if (onKeep) onKeep();
      else onChange("");
      return;
    }
    onChange(next);
  }

  return (
    <SettingFieldRow
      label={label}
      htmlFor={controlId}
      description={description}
      descriptionId={hintId}
      restartRequired={restartRequired}
    >
      <Input
        id={controlId}
        type="password"
        placeholder={configured ? "••••••••••••" : "Not configured"}
        value={value}
        onChange={(e) => handleChange(e.target.value)}
        disabled={disabled}
        className="border-muted-foreground/25 w-full sm:w-60"
        aria-describedby={description ? hintId : undefined}
      />
    </SettingFieldRow>
  );
}
