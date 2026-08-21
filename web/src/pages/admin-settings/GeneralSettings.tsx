import { useMemo } from "react";
import { Link } from "react-router";
import { ArrowRight } from "lucide-react";

import { useSettingsForm } from "@/hooks/useSettingsForm";
import { useRestartKeys } from "@/hooks/useRestartKeys";
import { AdvancedSection } from "@/components/settings/AdvancedSection";
import { Skeleton } from "@/components/ui/skeleton";
import { SettingField } from "./SettingField";
import { SaveBar } from "./SaveBar";
import { FieldGroup } from "./FieldGroup";

// Identity (server name, login subtitle) used to live on the Branding tab and
// public signups on the Invite Codes tab; both are plain server-wide switches an
// admin looks for under General, so they save with everything else on this tab.
const KEYS = [
  "branding.server_name",
  "branding.login_subtitle",
  "signup.enabled",
  "server.log_level",
  "server.log_quiet",
];

export default function GeneralSettings() {
  const form = useSettingsForm({ keys: useMemo(() => KEYS, []) });
  const restartKeys = useRestartKeys();

  if (form.isLoading)
    return (
      <div className="space-y-6" role="status" aria-label="Loading settings">
        <Skeleton className="h-8 w-48" />
        <div className="space-y-4">
          <Skeleton className="h-10 w-full" />
          <Skeleton className="h-10 w-full" />
        </div>
        <div className="space-y-4">
          <Skeleton className="h-10 w-full" />
          <Skeleton className="h-10 w-full" />
        </div>
        <span className="sr-only">Loading settings</span>
      </div>
    );

  return (
    <div className="flex h-full flex-col">
      <div className="mb-6 space-y-2">
        <h2 className="text-xl font-semibold tracking-tight">General</h2>
        <p className="text-muted-foreground text-sm leading-relaxed">
          What this server calls itself, who is allowed to sign up, and how much it writes to the
          log.
        </p>
      </div>

      <div className="flex-1 space-y-6">
        <FieldGroup label="Identity">
          <p className="text-muted-foreground pb-3 text-xs leading-relaxed">
            Shown in the browser tab, on the login page, in the sidebar, and in the installed app.
            Leave a field empty to use the Silo default.
          </p>
          <SettingField
            label="Server Name"
            hint="Silo"
            value={form.getValue("branding.server_name")}
            onChange={(v) => form.setValue("branding.server_name", v)}
            restartRequired={restartKeys.has("branding.server_name")}
          />
          <SettingField
            label="Login Page Subtitle"
            hint="Sign in with an existing account."
            value={form.getValue("branding.login_subtitle")}
            onChange={(v) => form.setValue("branding.login_subtitle", v)}
            restartRequired={restartKeys.has("branding.login_subtitle")}
          />
        </FieldGroup>

        <FieldGroup label="Access">
          <SettingField
            label="Public Signups"
            type="toggle"
            hint="When on, anyone who has a valid invite code can create their own account."
            value={form.getValue("signup.enabled")}
            onChange={(v) => form.setValue("signup.enabled", v)}
            restartRequired={restartKeys.has("signup.enabled")}
          />
          <div className="pt-3">
            <Link
              to="/admin/users"
              className="text-muted-foreground hover:text-foreground inline-flex items-center gap-1.5 text-xs font-medium transition-colors"
            >
              Manage invite codes
              <ArrowRight className="h-3 w-3" aria-hidden="true" />
            </Link>
          </div>
        </FieldGroup>

        <FieldGroup label="Logging">
          <SettingField
            label="Log Level"
            type="select"
            hint="How much detail the server writes. Info is right for everyday use; Debug is loud and mainly useful while chasing a problem."
            value={form.getValue("server.log_level")}
            onChange={(v) => form.setValue("server.log_level", v)}
            restartRequired={restartKeys.has("server.log_level")}
            options={[
              { value: "debug", label: "Debug" },
              { value: "info", label: "Info" },
              { value: "warn", label: "Warn" },
              { value: "error", label: "Error" },
            ]}
          />
          <AdvancedSection
            id="general.logging"
            count={1}
            forceOpen={form.isDirty("server.log_quiet")}
          >
            <div>
              <SettingField
                label="Silenced Log Messages"
                hint="metadata, scanner"
                value={form.getValue("server.log_quiet")}
                onChange={(v) => form.setValue("server.log_quiet", v)}
                restartRequired={restartKeys.has("server.log_quiet")}
              />
              <p className="text-muted-foreground pb-2 text-xs leading-relaxed">
                Log lines starting with any of these words are dropped, so a noisy subsystem stops
                filling the log. Separate them with commas.
              </p>
            </div>
          </AdvancedSection>
        </FieldGroup>
      </div>

      <SaveBar
        dirtyCount={form.dirtyCount}
        onSave={form.save}
        onDiscard={form.discard}
        isSaving={form.isSaving}
        restartRequired={form.restartRequired}
      />
    </div>
  );
}
