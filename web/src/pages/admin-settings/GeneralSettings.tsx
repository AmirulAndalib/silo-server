import { useMemo } from "react";
import { Link } from "react-router";
import { ArrowRight } from "lucide-react";

import { useSettingsForm } from "@/hooks/useSettingsForm";
import { useRestartKeys } from "@/hooks/useRestartKeys";
import { AdvancedSection } from "@/components/settings/AdvancedSection";
import { SettingsPageHeader } from "@/components/settings/SettingsPageHeader";
import { StatusStrip, type StatusStripItem } from "@/components/settings/StatusStrip";
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

const LOGGING_ADVANCED_KEYS = ["server.log_quiet"];

const LOG_LEVEL_LABELS: Record<string, string> = {
  debug: "Debug",
  info: "Info",
  warn: "Warn",
  error: "Error",
};

export default function GeneralSettings() {
  const form = useSettingsForm({ keys: useMemo(() => KEYS, []) });
  const restartKeys = useRestartKeys();

  const countDirty = (keys: string[]) => keys.filter((key) => form.isDirty(key)).length;
  const restartCount = KEYS.filter((key) => form.isDirty(key) && restartKeys.has(key)).length;

  const serverName = form.getValue("branding.server_name").trim() || "Silo";
  const signupsEnabled = form.getValue("signup.enabled") === "true";
  const logLevel = form.getValue("server.log_level") || "info";

  const stripItems: StatusStripItem[] = [
    { tone: "info", label: `Server name: ${serverName}` },
    signupsEnabled
      ? { tone: "info", label: "Public signups on" }
      : { tone: "muted", label: "Public signups off" },
    {
      tone: logLevel === "debug" ? "warn" : "ok",
      label: `Log level: ${LOG_LEVEL_LABELS[logLevel] ?? logLevel}`,
    },
  ];

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
      <SettingsPageHeader
        title="General"
        description="How the server introduces itself and what it logs."
        strip={<StatusStrip items={stripItems} />}
        className="mb-8"
      />

      <div className="flex-1 space-y-9">
        <FieldGroup
          label="Identity"
          clarifier="What people see before they sign in. Leave a field empty for the Silo default."
        >
          <SettingField
            label="Server name"
            hint="Silo"
            description="Shown in the browser tab, on the login page, in the sidebar, and in the installed app."
            value={form.getValue("branding.server_name")}
            onChange={(v) => form.setValue("branding.server_name", v)}
            restartRequired={restartKeys.has("branding.server_name")}
            dirty={form.isDirty("branding.server_name")}
          />
          <SettingField
            label="Login subtitle"
            hint="Sign in with an existing account."
            description="One line under the server name on the sign-in page."
            value={form.getValue("branding.login_subtitle")}
            onChange={(v) => form.setValue("branding.login_subtitle", v)}
            restartRequired={restartKeys.has("branding.login_subtitle")}
            dirty={form.isDirty("branding.login_subtitle")}
          />
        </FieldGroup>

        <FieldGroup
          label="Access"
          clarifier="Who can create an account on this server"
          actions={
            <Link
              to="/admin/users"
              className="text-muted-foreground hover:text-foreground inline-flex items-center gap-1.5 text-xs font-medium transition-colors"
            >
              Manage invite codes
              <ArrowRight className="h-3 w-3" aria-hidden="true" />
            </Link>
          }
        >
          <SettingField
            label="Public signups"
            type="toggle"
            description="When on, anyone who has a valid invite code can create their own account. Off means an admin creates every account."
            value={form.getValue("signup.enabled")}
            onChange={(v) => form.setValue("signup.enabled", v)}
            restartRequired={restartKeys.has("signup.enabled")}
            dirty={form.isDirty("signup.enabled")}
          />
        </FieldGroup>

        <FieldGroup label="Logging" clarifier="How much detail the server writes">
          <SettingField
            label="Log level"
            type="select"
            description="Info is right for everyday use. Debug is loud and mainly useful while chasing a problem."
            value={form.getValue("server.log_level")}
            onChange={(v) => form.setValue("server.log_level", v)}
            restartRequired={restartKeys.has("server.log_level")}
            dirty={form.isDirty("server.log_level")}
            options={[
              { value: "debug", label: "Debug" },
              { value: "info", label: "Info" },
              { value: "warn", label: "Warn" },
              { value: "error", label: "Error" },
            ]}
          />
          <AdvancedSection
            id="general.logging"
            count={LOGGING_ADVANCED_KEYS.length}
            changedCount={countDirty(LOGGING_ADVANCED_KEYS)}
            forceOpen={form.isDirty("server.log_quiet")}
          >
            <SettingField
              label="Quiet log prefixes"
              hint="metadata, scanner"
              description="Log lines starting with any of these words are dropped, so a noisy subsystem stops filling the log. Separate them with commas."
              value={form.getValue("server.log_quiet")}
              onChange={(v) => form.setValue("server.log_quiet", v)}
              restartRequired={restartKeys.has("server.log_quiet")}
              dirty={form.isDirty("server.log_quiet")}
            />
          </AdvancedSection>
        </FieldGroup>
      </div>

      <SaveBar
        dirtyCount={form.dirtyCount}
        onSave={form.save}
        onDiscard={form.discard}
        isSaving={form.isSaving}
        restartRequired={form.restartRequired}
        restartCount={restartCount}
      />
    </div>
  );
}
