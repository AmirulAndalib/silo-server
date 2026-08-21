import { useId, useState, type ReactNode } from "react";
import type { LucideIcon } from "lucide-react";

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { RestartBadge } from "@/components/settings/RestartBadge";
import { cn } from "@/lib/utils";

/** Outcome of a provider's Test action, whatever endpoint produced it. */
export interface ProviderTestResult {
  success: boolean;
  message?: string;
}

export type ProviderStatus = "connected" | "unconfigured" | "failing";

const STATUS_LABELS: Record<ProviderStatus, string> = {
  connected: "Connected",
  unconfigured: "Not set up",
  failing: "Failing",
};

const STATUS_CLASSES: Record<ProviderStatus, string> = {
  connected: "border-green-500/30 text-green-600 dark:text-green-400",
  unconfigured: "border-border text-muted-foreground",
  failing: "border-amber-500/30 text-amber-600 dark:text-amber-400",
};

function StatusChip({ status, label }: { status: ProviderStatus; label?: string }) {
  return (
    <span
      className={cn(
        "inline-flex shrink-0 items-center gap-1.5 rounded-full border px-2 py-0.5 text-xs",
        STATUS_CLASSES[status],
      )}
    >
      <span
        aria-hidden="true"
        className={cn(
          "size-1.5 rounded-full",
          status === "connected"
            ? "bg-green-500"
            : status === "failing"
              ? "bg-amber-500"
              : "bg-muted-foreground/50",
        )}
      />
      {label ?? STATUS_LABELS[status]}
    </span>
  );
}

export interface ProviderCardProps {
  title: string;
  description?: ReactNode;
  icon?: LucideIcon;
  status: ProviderStatus;
  /** Overrides the default chip text ("Connected" / "Not set up" / "Failing"). */
  statusLabel?: string;
  /** Pass together with `onEnabledChange` to show the on/off switch. */
  enabled?: boolean;
  onEnabledChange?: (enabled: boolean) => void;
  /** Marks the whole card with a restart badge; drive it from `useRestartKeys`. */
  restartRequired?: boolean;
  /** Disables every control while a request is in flight. */
  busy?: boolean;
  /** Credential fields — use `SecretField` for anything the server stores. */
  children?: ReactNode;
  onSave?: () => void;
  saveLabel?: string;
  isSaving?: boolean;
  saveDisabled?: boolean;
  onTest?: () => void;
  testLabel?: string;
  testPendingLabel?: string;
  isTesting?: boolean;
  testDisabled?: boolean;
  testResult?: ProviderTestResult | null;
  /** Shows a Clear button guarded by a confirmation dialog. */
  onClear?: () => void;
  clearLabel?: string;
  clearTitle?: string;
  clearDescription?: string;
  clearActionLabel?: string;
  /** Rendered under the actions — notes, restart prompts, doc links. */
  footer?: ReactNode;
}

/**
 * The single card for a third-party integration: status, credentials, and the
 * Save / Test / Clear trio. Credentials save per card because a provider has to
 * be testable before its values are committed, so this card owns its own
 * actions rather than feeding the page's save bar.
 */
export function ProviderCard({
  title,
  description,
  icon: Icon,
  status,
  statusLabel,
  enabled,
  onEnabledChange,
  restartRequired = false,
  busy = false,
  children,
  onSave,
  saveLabel = "Save",
  isSaving = false,
  saveDisabled = false,
  onTest,
  testLabel = "Test",
  testPendingLabel = "Testing...",
  isTesting = false,
  testDisabled = false,
  testResult,
  onClear,
  clearLabel = "Clear credentials",
  clearTitle,
  clearDescription,
  clearActionLabel = "Clear",
  footer,
}: ProviderCardProps) {
  const headingId = useId();
  const switchId = useId();
  const [confirmClear, setConfirmClear] = useState(false);

  return (
    <fieldset
      disabled={busy}
      aria-labelledby={headingId}
      className="border-border bg-surface flex min-w-0 flex-col gap-4 rounded-lg border px-5 py-4"
    >
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="flex min-w-0 gap-3">
          {Icon && (
            <div className="bg-muted mt-0.5 flex size-8 shrink-0 items-center justify-center rounded-md">
              <Icon className="text-muted-foreground size-4" aria-hidden="true" />
            </div>
          )}
          <div className="min-w-0">
            <div className="flex items-center gap-2">
              <h3 id={headingId} className="text-sm font-semibold">
                {title}
              </h3>
              {restartRequired && <RestartBadge />}
            </div>
            {description && (
              <p className="text-muted-foreground mt-1 text-xs leading-relaxed">{description}</p>
            )}
          </div>
        </div>
        <div className="flex shrink-0 items-center gap-3">
          <StatusChip status={status} label={statusLabel} />
          {onEnabledChange && (
            <Switch
              id={switchId}
              checked={enabled === true}
              onCheckedChange={onEnabledChange}
              aria-label={`Enable ${title}`}
            />
          )}
        </div>
      </div>

      {children && <div className="divide-border divide-y">{children}</div>}

      {(onSave || onTest || onClear) && (
        <div className="flex flex-wrap items-center gap-2">
          {onTest && (
            <Button
              type="button"
              size="sm"
              variant="outline"
              onClick={onTest}
              disabled={testDisabled || isTesting}
            >
              {isTesting ? testPendingLabel : testLabel}
            </Button>
          )}
          {onSave && (
            <Button type="button" size="sm" onClick={onSave} disabled={saveDisabled || isSaving}>
              {isSaving ? "Saving..." : saveLabel}
            </Button>
          )}
          {onClear && (
            <Button type="button" size="sm" variant="ghost" onClick={() => setConfirmClear(true)}>
              {clearLabel}
            </Button>
          )}
        </div>
      )}

      {testResult && (
        <p
          role="status"
          aria-live="polite"
          className={cn(
            "text-xs",
            testResult.success
              ? "text-green-600 dark:text-green-400"
              : "text-red-600 dark:text-red-400",
          )}
        >
          {testResult.message ??
            (testResult.success ? "Connection successful." : "Connection failed.")}
        </p>
      )}

      {footer}

      {onClear && (
        <AlertDialog open={confirmClear} onOpenChange={setConfirmClear}>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>{clearTitle ?? `Clear ${title} credentials?`}</AlertDialogTitle>
              <AlertDialogDescription>
                {clearDescription ??
                  `Silo stops using ${title} until new credentials are saved here.`}
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>Cancel</AlertDialogCancel>
              <AlertDialogAction
                className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
                onClick={() => {
                  onClear();
                  setConfirmClear(false);
                }}
              >
                {clearActionLabel}
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      )}
    </fieldset>
  );
}
