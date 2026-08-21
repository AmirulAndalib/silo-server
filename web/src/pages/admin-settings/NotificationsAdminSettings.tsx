import { useId, useMemo, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import {
  Bell,
  Bot,
  ChevronDown,
  ChevronRight,
  Inbox,
  KeyRound,
  Loader2,
  Mail,
  Megaphone,
  MonitorSmartphone,
  RadioTower,
  Rss,
  Send,
  TriangleAlert,
  Webhook,
  Workflow,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { Link } from "react-router";
import { toast } from "sonner";
import { api } from "@/api/client";
import { Button } from "@/components/ui/button";
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
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { AdvancedSection } from "@/components/settings/AdvancedSection";
import { SecretField } from "@/components/settings/SecretField";
import { adminKeys } from "@/hooks/queries/keys";
import { useServerNotificationChannels } from "@/hooks/queries/admin/serverNotificationChannels";
import { useRestartKeys } from "@/hooks/useRestartKeys";
import { useSettingsForm } from "@/hooks/useSettingsForm";
import { cn } from "@/lib/utils";
import { FieldGroup } from "./FieldGroup";
import { SaveBar } from "./SaveBar";
import { SettingField } from "./SettingField";
import ServerNotificationChannels from "./ServerNotificationChannels";

/** Batching and flood control; all advanced. */
const FANOUT_KEYS = [
  "notifications.fanout.settle_seconds",
  "notifications.fanout.max_series_burst",
  "notifications.fanout.max_event_age_hours",
];

/** Inbox and event cleanup; all advanced. */
const RETENTION_KEYS = [
  "notifications.retention.read_days",
  "notifications.retention.unread_days",
  "notifications.retention.event_days",
];

/** Personal webhook limits and the SSRF escape hatch; all advanced. */
const WEBHOOK_ADVANCED_KEYS = [
  "notifications.webhooks.max_per_profile",
  "notifications.webhooks.deliveries_per_minute_per_profile",
  "notifications.webhooks.allow_private_destinations",
];

/**
 * Outbound mail. The SMTP server used to be its own tab; it now lives in the
 * Email channel card because "email notifications don't arrive" is always an
 * SMTP question.
 */
const EMAIL_KEYS = [
  "email.enabled",
  "email.smtp_host",
  "email.smtp_port",
  "email.smtp_security",
  "email.smtp_username",
  "email.smtp_password",
  "email.from_address",
  "email.from_name",
];

const KEYS = [
  "notifications.release_events_enabled",
  "notifications.fanout_enabled",
  "notifications.ui_enabled",
  "notifications.webhooks_enabled",
  "notifications.web_push_enabled",
  "notifications.apple_push_delivery_enabled",
  "notifications.android_push_delivery_enabled",
  // Relay lifecycle fields are read for status but are never edited through
  // the shared settings form; credential endpoints replace them atomically.
  "notifications.push_relay_url",
  "notifications.push_relay_deployment_id",
  "notifications.push_relay_expires_at",
  "notifications.push_relay_key_prefix",
  "notifications.push_relay_reregistration_required",
  ...FANOUT_KEYS,
  ...WEBHOOK_ADVANCED_KEYS,
  "notifications.email_enabled",
  "notifications.email.allow_per_episode",
  "notifications.email.digest_hour",
  "notifications.email.external_url",
  "notifications.discord_enabled",
  "notifications.discord.allow_per_episode",
  "notifications.discord.digest_hour",
  "notifications.discord.poster_mode",
  "notifications.server_channels_enabled",
  "notifications.server_channels.batch_seconds",
  "notifications.server_channels.mention_requesters",
  ...RETENTION_KEYS,
  ...EMAIL_KEYS,
];

interface EmailTestResult {
  ok: boolean;
  duration_ms: number;
  message?: string;
}

interface AppleRelayRegisterResult {
  relay_url: string;
  deployment_id: string;
  key_prefix: string;
  api_key_configured: boolean;
  relay_request_id?: string;
  apns_topics?: string[];
  expires_at: string;
}

const DEFAULT_PUSH_RELAY_URL = "https://push.siloserver.org";

function digestHourLabel(raw: string): string {
  const hour = Number.parseInt(raw, 10);
  const valid = Number.isInteger(hour) && hour >= 0 && hour <= 23;
  return `${String(valid ? hour : 8).padStart(2, "0")}:00`;
}

/** Small status pill shown next to a channel title while the card is collapsed. */
function Chip({
  tone = "neutral",
  children,
}: {
  tone?: "neutral" | "positive" | "warning";
  children: React.ReactNode;
}) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[11px] font-medium whitespace-nowrap",
        tone === "neutral" && "border-border/70 text-muted-foreground",
        tone === "positive" && "border-emerald-500/30 bg-emerald-500/10 text-emerald-500",
        tone === "warning" && "border-amber-500/30 bg-amber-500/10 text-amber-500",
      )}
    >
      {children}
    </span>
  );
}

function ZoneHeading({ title, description }: { title: string; description?: string }) {
  return (
    <div className="space-y-1 px-1">
      <h3 className="text-muted-foreground text-xs font-semibold tracking-[0.22em] uppercase">
        {title}
      </h3>
      {description && <p className="text-muted-foreground/80 text-xs">{description}</p>}
    </div>
  );
}

/** Sub-grouping label inside an expanded channel card. */
function SubsectionLabel({ children }: { children: React.ReactNode }) {
  return (
    <div className="text-muted-foreground/80 pt-4 pb-1 text-[11px] font-semibold tracking-wider uppercase">
      {children}
    </div>
  );
}

interface PipelineStageProps {
  icon: LucideIcon;
  title: string;
  description: string;
  /** Visually de-emphasize the stage when an upstream stage is switched off. */
  dimmed?: boolean;
  control: React.ReactNode;
}

function PipelineStage({ icon: Icon, title, description, dimmed, control }: PipelineStageProps) {
  return (
    <div className="flex items-start justify-between gap-3">
      <div className={cn("min-w-0 transition-opacity", dimmed && "opacity-50")}>
        <div className="flex items-center gap-2">
          <Icon className="text-muted-foreground h-4 w-4 shrink-0" />
          <span className="text-sm font-medium">{title}</span>
        </div>
        <p className="text-muted-foreground mt-1 text-xs leading-relaxed">{description}</p>
      </div>
      {control}
    </div>
  );
}

function PipelineArrow() {
  return (
    <div className="hidden items-center md:flex">
      <ChevronRight className="text-muted-foreground/50 h-4 w-4" />
    </div>
  );
}

interface ChannelCardProps {
  icon: LucideIcon;
  title: string;
  description: string;
  enabled: boolean;
  onEnabledChange: (enabled: boolean) => void;
  chips?: React.ReactNode;
  children?: React.ReactNode;
}

/**
 * One delivery channel: an always-visible header row (icon, title, status
 * chips, enable switch) with settings tucked behind an expandable body.
 * Settings stay editable while the channel is off so admins can configure
 * before enabling.
 */
function ChannelCard({
  icon: Icon,
  title,
  description,
  enabled,
  onEnabledChange,
  chips,
  children,
}: ChannelCardProps) {
  const [open, setOpen] = useState(false);
  const bodyId = useId();
  const expandable = children != null;

  const header = (
    <>
      <span
        className={cn(
          "flex h-10 w-10 shrink-0 items-center justify-center rounded-xl transition-colors",
          enabled ? "bg-primary/15 text-primary" : "bg-muted text-muted-foreground",
        )}
      >
        <Icon className="h-5 w-5" />
      </span>
      <span className="min-w-0 flex-1">
        <span className="flex flex-wrap items-center gap-x-2 gap-y-1">
          <span className="text-sm font-medium">{title}</span>
          {chips}
        </span>
        <span className="text-muted-foreground mt-0.5 block text-xs leading-relaxed">
          {description}
        </span>
      </span>
    </>
  );

  return (
    <div className="surface-panel overflow-hidden rounded-2xl border-0">
      <div className="flex items-center gap-3 p-4 sm:px-5">
        {expandable ? (
          <button
            type="button"
            aria-expanded={open}
            aria-controls={bodyId}
            onClick={() => setOpen((current) => !current)}
            className="flex min-w-0 flex-1 cursor-pointer items-center gap-3 text-left"
          >
            {header}
            <ChevronDown
              className={cn(
                "text-muted-foreground h-4 w-4 shrink-0 transition-transform duration-200",
                open && "rotate-180",
              )}
            />
          </button>
        ) : (
          <div className="flex min-w-0 flex-1 items-center gap-3">{header}</div>
        )}
        <Switch
          checked={enabled}
          onCheckedChange={(value) => {
            onEnabledChange(value);
            // Enabling a channel usually means configuring it next.
            if (value && expandable) setOpen(true);
          }}
          aria-label={`Enable ${title}`}
        />
      </div>
      {expandable && open && (
        <div
          id={bodyId}
          className="border-border/60 animate-in fade-in-0 slide-in-from-top-1 border-t px-4 pt-1 pb-4 duration-200 sm:px-5"
        >
          {children}
        </div>
      )}
    </div>
  );
}

/** Sends a real message through the saved SMTP settings. */
function TestEmailRow() {
  const [recipient, setRecipient] = useState("");
  const [pending, setPending] = useState(false);
  const [result, setResult] = useState<EmailTestResult | null>(null);

  const sendTest = async () => {
    setPending(true);
    setResult(null);
    try {
      const response = await api<EmailTestResult>("/admin/email/test", {
        method: "POST",
        body: JSON.stringify({ to: recipient.trim() }),
      });
      setResult(response);
      if (response.ok) {
        toast.success("Test email sent");
      }
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Test request failed");
    } finally {
      setPending(false);
    }
  };

  return (
    <div className="space-y-2 py-3">
      <div className="flex max-w-md gap-2">
        <Input
          type="email"
          aria-label="Test email recipient"
          placeholder="you@example.com"
          value={recipient}
          onChange={(event) => setRecipient(event.target.value)}
        />
        <Button
          variant="outline"
          disabled={pending || !recipient.trim()}
          onClick={() => void sendTest()}
        >
          {pending ? (
            <Loader2 className="mr-1.5 h-4 w-4 animate-spin" />
          ) : (
            <Send className="mr-1.5 h-4 w-4" />
          )}
          Send test
        </Button>
      </div>
      {result && (
        <p className={`text-xs ${result.ok ? "text-emerald-500" : "text-amber-500"}`}>
          {result.ok
            ? `Delivered to the mail server in ${result.duration_ms}ms.`
            : result.message || "Test failed."}
        </p>
      )}
      <p className="text-muted-foreground text-xs">
        Save your changes first — the test uses the saved settings.
      </p>
    </div>
  );
}

function RegisterRelayRow({
  relayURL,
  deploymentID,
  keyPrefix,
  expiresAt,
  reregistrationRequired,
  urlEdited,
  onRegistered,
}: {
  relayURL: string;
  deploymentID: string;
  keyPrefix: string;
  expiresAt: string;
  reregistrationRequired: boolean;
  urlEdited: boolean;
  onRegistered: (submittedRelayURL: string) => void;
}) {
  const queryClient = useQueryClient();
  const [pending, setPending] = useState(false);
  const [confirmClear, setConfirmClear] = useState(false);
  const [result, setResult] = useState<AppleRelayRegisterResult | null>(null);

  const configured = deploymentID.trim() !== "";
  const actionLabel = reregistrationRequired
    ? "Re-register relay"
    : configured
      ? "Rotate credential"
      : "Register relay";
  const expiration = expiresAt ? new Date(expiresAt) : null;
  const expirationValid = expiration != null && !Number.isNaN(expiration.getTime());
  const renewalStatus = expirationValid
    ? expiration.getTime() <= Date.now()
      ? "Expired; Silo will renew before the next delivery when the relay grace period permits."
      : `Expires ${expiration.toLocaleString()}; Silo renews automatically during the final week with per-server jitter.`
    : configured
      ? "Expiration is unknown; the credential will be refreshed on its next lifecycle operation."
      : "No relay credential is registered.";

  const registerRelay = async () => {
    if (pending) return;
    setPending(true);
    setResult(null);
    try {
      const response = await api<AppleRelayRegisterResult>(
        "/admin/notifications/push/relay/register",
        {
          method: "POST",
          body: JSON.stringify({
            relay_url: relayURL,
          }),
        },
      );
      setResult(response);
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: adminKeys.serverSettings() }),
        queryClient.invalidateQueries({
          queryKey: [...adminKeys.serverSettings(), "sensitive-status"] as const,
        }),
      ]);
      onRegistered(relayURL);
      toast.success("Push relay registered");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Relay registration failed");
    } finally {
      setPending(false);
    }
  };

  const clearRelay = async () => {
    if (pending) return;
    setPending(true);
    setResult(null);
    try {
      await api<void>("/admin/notifications/push/relay", { method: "DELETE" });
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: adminKeys.serverSettings() }),
        queryClient.invalidateQueries({
          queryKey: [...adminKeys.serverSettings(), "sensitive-status"] as const,
        }),
      ]);
      setConfirmClear(false);
      toast.success("Push relay credential cleared");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Failed to clear relay credential");
    } finally {
      setPending(false);
    }
  };

  return (
    <div className="space-y-3 py-3">
      <SettingField
        label="Deployment ID"
        hint="The name this server is known by on the relay. It is created for you when you register."
        type="text"
        value={deploymentID}
        onChange={() => {}}
        disabled
      />
      <div className="flex flex-wrap items-center gap-2 py-2">
        <Button variant="outline" size="sm" disabled={pending} onClick={() => void registerRelay()}>
          {pending ? (
            <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />
          ) : (
            <KeyRound className="mr-1.5 h-3.5 w-3.5" />
          )}
          {actionLabel}
        </Button>
        {configured && (
          <Button
            variant="ghost"
            size="sm"
            disabled={pending}
            onClick={() => setConfirmClear(true)}
          >
            Clear credential
          </Button>
        )}
      </div>
      {reregistrationRequired && (
        <div className="text-xs text-amber-500">
          The current capability was rejected or revoked. Automatic registration is disabled;
          re-register explicitly to create a new relay deployment.
        </div>
      )}
      <div className="text-muted-foreground space-y-1 text-xs">
        {keyPrefix && <div>Credential: {keyPrefix}</div>}
        <div>{renewalStatus}</div>
      </div>
      {urlEdited && (
        <div className="text-muted-foreground text-xs">
          The relay URL change is applied when you register; credentials are stored immediately.
        </div>
      )}
      {result && (
        <div className="text-xs text-emerald-500">
          Credential ready for {result.deployment_id}
          {result.key_prefix ? ` — key ${result.key_prefix}` : ""}
          {result.relay_request_id ? ` — relay ${result.relay_request_id}` : ""}
        </div>
      )}
      <AlertDialog open={confirmClear} onOpenChange={setConfirmClear}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Clear the push relay credential?</AlertDialogTitle>
            <AlertDialogDescription>
              Mobile push delivery will stop until a relay is registered again. This clears the
              local deployment identity and lets you select a different relay origin.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={pending}>Cancel</AlertDialogCancel>
            <AlertDialogAction
              disabled={pending}
              onClick={() => void clearRelay()}
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
            >
              {pending ? "Clearing..." : "Clear credential"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

function MobilePushPrivacyDisclosure() {
  return (
    <div className="space-y-2 py-3">
      <div className="text-sm font-medium">Privacy disclosure</div>
      <div className="text-muted-foreground space-y-2 text-xs leading-relaxed">
        <p>
          If you enable push notifications, your Silo Server sends a content-free request to Silo's
          push relay so Silo can deliver notifications through Apple Push Notification service or
          Firebase Cloud Messaging.
        </p>
        <p>
          The relay does not receive notification titles, message bodies, media names, user names,
          profile names, or your server URL. It does process technical metadata needed to deliver
          and operate the service, including an opaque deployment identifier, push delivery timing,
          request status, app topic, the IP address your self-hosted Silo Server uses to contact the
          relay, and a hashed device push token. Apple or Google may also process standard push
          delivery metadata for their platform.
        </p>
        <p>
          Push notifications are generic; the app fetches private content directly from your Silo
          Server after receiving the push.
        </p>
      </div>
    </div>
  );
}

export default function NotificationsAdminSettings() {
  const form = useSettingsForm({ keys: useMemo(() => KEYS, []) });
  const restartKeys = useRestartKeys();
  const { data: serverChannels } = useServerNotificationChannels();
  // Local draft for the relay URL; null means "show the saved value".
  const [pushRelayURLDraft, setPushRelayURLDraft] = useState<string | null>(null);
  // Replacing the saved SMTP password is a deliberate act, tracked here so a
  // Discard also drops the field back to its "Configured" summary.
  const [replacingSMTPPassword, setReplacingSMTPPassword] = useState(false);

  if (form.isLoading) {
    return (
      <div className="space-y-6">
        <div className="space-y-2">
          <Skeleton className="h-7 w-44" />
          <Skeleton className="h-4 w-full max-w-xl" />
        </div>
        <Skeleton className="h-36 w-full rounded-2xl" />
        <div className="space-y-3">
          {[0, 1, 2, 3].map((i) => (
            <Skeleton key={i} className="h-[72px] w-full rounded-2xl" />
          ))}
        </div>
      </div>
    );
  }

  // Kill switches default to enabled when unset; the backend treats any
  // unrecognized value as the default, so an empty stored value means "on".
  const toggleValue = (key: string) => form.getValue(key) || "true";
  const isOn = (key: string) => toggleValue(key) === "true";
  const setToggle = (key: string) => (value: boolean) =>
    form.setValue(key, value ? "true" : "false");
  // Numeric settings fall back to their server-side defaults when unset;
  // surface the effective default instead of an empty input.
  const numberValue = (key: string, fallback: string) => form.getValue(key) || fallback;
  const needsRestart = (key: string) => restartKeys.has(key);
  const anyDirty = (keys: string[]) => keys.some(form.isDirty);

  const releaseEventsOn = isOn("notifications.release_events_enabled");
  const fanoutOn = isOn("notifications.fanout_enabled");
  const uiOn = isOn("notifications.ui_enabled");
  const webPushOn = isOn("notifications.web_push_enabled");
  const emailOn = isOn("notifications.email_enabled");
  const serverChannelsOn = isOn("notifications.server_channels_enabled");
  // Mobile push, Discord, and personal webhooks are opt-in (default off).
  const applePushOn = form.getValue("notifications.apple_push_delivery_enabled") === "true";
  const androidPushOn = form.getValue("notifications.android_push_delivery_enabled") === "true";
  const mobilePushOn = applePushOn || androidPushOn;
  const discordOn = form.getValue("notifications.discord_enabled") === "true";
  const webhooksOn = form.getValue("notifications.webhooks_enabled") === "true";

  // The relay URL is not part of the settings form: the server only persists
  // it through the registration endpoint, alongside the credentials it mints.
  const savedPushRelayURL = form.getValue("notifications.push_relay_url") || DEFAULT_PUSH_RELAY_URL;
  const pushRelayURL = pushRelayURLDraft ?? savedPushRelayURL;
  const pushRelayURLEdited = pushRelayURL !== savedPushRelayURL;
  const pushRelayDeploymentID = form.getValue("notifications.push_relay_deployment_id");
  const pushRelayKeyPrefix = form.getValue("notifications.push_relay_key_prefix");
  const pushRelayExpiresAt = form.getValue("notifications.push_relay_expires_at");
  const pushRelayReregistrationRequired =
    form.getValue("notifications.push_relay_reregistration_required") === "true";
  const pushRelayAPIKeyReady = form.sensitiveConfigured.includes(
    "notifications.push_relay_api_key",
  );
  const allowPrivate =
    form.getValue("notifications.webhooks.allow_private_destinations") === "true";

  // Mail is only usable when the outbound switch is on and a server is set.
  const mailServerSet = form.getValue("email.smtp_host").trim() !== "";
  const outboundMailOn = form.getValue("email.enabled") === "true";
  const mailReady = mailServerSet && outboundMailOn;
  // The Discord application (client id, secret, bot token) is configured on
  // the Integrations tab; only its delivery behaviour lives here.
  const discordAppConfigured = form.sensitiveConfigured.includes("discord.bot_token");

  const channelStates = [
    uiOn,
    webPushOn,
    mobilePushOn,
    emailOn,
    discordOn,
    webhooksOn,
    serverChannelsOn,
  ];
  const enabledChannelCount = channelStates.filter(Boolean).length;

  const failingServerChannels = (serverChannels ?? []).filter(
    (channel) =>
      channel.last_failure_at != null &&
      (channel.last_success_at == null || channel.last_failure_at > channel.last_success_at),
  ).length;

  const pausedMessage = !releaseEventsOn
    ? "New content is not being recorded, so nothing can be sent."
    : !fanoutOn
      ? "Sending is paused. New content is still recorded and waits until you turn this back on, or until it is too old to send."
      : null;

  return (
    <div className="flex h-full flex-col">
      <div className="mb-6 space-y-2">
        <h2 className="text-xl font-semibold tracking-tight">Notifications</h2>
        <p className="text-muted-foreground text-sm leading-relaxed">
          Who gets told about new content, and how it reaches them. Everything here applies
          immediately — no restart needed. Each person still chooses what they want in their own
          notification settings.
        </p>
      </div>

      <div className="flex-1 space-y-7">
        {/* ── Pipeline: the master switches, framed as the flow they gate ── */}
        <div className="surface-panel rounded-2xl border-0 p-4 sm:p-5">
          <div className="text-muted-foreground mb-4 text-xs font-semibold tracking-[0.22em] uppercase">
            Pipeline
          </div>
          <div className="grid gap-4 md:grid-cols-[1fr_auto_1fr_auto_1fr] md:gap-3">
            <PipelineStage
              icon={Rss}
              title="Notice new content"
              description="Record what shows up during library scans. Off means nothing is noticed, so nothing is ever sent."
              control={
                <Switch
                  checked={releaseEventsOn}
                  onCheckedChange={setToggle("notifications.release_events_enabled")}
                  aria-label="Enable release events"
                />
              }
            />
            <PipelineArrow />
            <PipelineStage
              icon={Workflow}
              title="Work out who wants it"
              description="Silo checks each new item against everyone's preferences and lines up a message per person. Off holds new items in the queue instead."
              dimmed={!releaseEventsOn}
              control={
                <Switch
                  checked={fanoutOn}
                  onCheckedChange={setToggle("notifications.fanout_enabled")}
                  aria-label="Enable fanout"
                />
              }
            />
            <PipelineArrow />
            <PipelineStage
              icon={Bell}
              title="Send it"
              description="Hand the queued messages to the delivery channels below."
              dimmed={!releaseEventsOn || !fanoutOn}
              control={
                <Chip>
                  {enabledChannelCount}/{channelStates.length} channels on
                </Chip>
              }
            />
          </div>
          {pausedMessage && (
            <div className="mt-4 flex items-start gap-2 text-xs text-amber-500">
              <TriangleAlert className="mt-0.5 h-4 w-4 shrink-0" />
              <p>{pausedMessage}</p>
            </div>
          )}
        </div>

        {/* ── Delivery channels ── */}
        <section className="space-y-3">
          <ZoneHeading
            title="Delivery Channels"
            description="Where notifications go once they are queued. Channels can be configured while switched off."
          />

          <ChannelCard
            icon={Inbox}
            title="In-App"
            description="Show the notification inbox and preferences in the web app and the mobile and TV apps."
            enabled={uiOn}
            onEnabledChange={setToggle("notifications.ui_enabled")}
          />

          <ChannelCard
            icon={MonitorSmartphone}
            title="Web Push"
            description="Browser push notifications to subscribed devices."
            enabled={webPushOn}
            onEnabledChange={setToggle("notifications.web_push_enabled")}
          />

          <ChannelCard
            icon={RadioTower}
            title="Silo Push Relay"
            description="Content-free mobile wakeups through Silo's relay, delivered by APNs or FCM."
            enabled={mobilePushOn}
            onEnabledChange={(enabled) => {
              form.setValue("notifications.apple_push_delivery_enabled", String(enabled));
              form.setValue("notifications.android_push_delivery_enabled", String(enabled));
            }}
            chips={
              pushRelayReregistrationRequired ? (
                <Chip tone="warning">Re-registration required</Chip>
              ) : pushRelayAPIKeyReady ? (
                <Chip tone="positive">Relay configured</Chip>
              ) : (
                <Chip tone={mobilePushOn ? "warning" : "neutral"}>Relay registration required</Chip>
              )
            }
          >
            <div className="divide-border divide-y">
              <MobilePushPrivacyDisclosure />
              <SettingField
                label="Apple Push (APNs)"
                hint="Deliver generic wakeups to registered iPhone, iPad, Apple TV, and Mac devices."
                type="toggle"
                value={String(applePushOn)}
                onChange={(value) =>
                  form.setValue("notifications.apple_push_delivery_enabled", value)
                }
                restartRequired={needsRestart("notifications.apple_push_delivery_enabled")}
              />
              <SettingField
                label="Android Push (FCM)"
                hint="Deliver generic wakeups to registered Android phone and TV devices."
                type="toggle"
                value={String(androidPushOn)}
                onChange={(value) =>
                  form.setValue("notifications.android_push_delivery_enabled", value)
                }
                restartRequired={needsRestart("notifications.android_push_delivery_enabled")}
              />
              <SettingField
                label="Relay URL"
                hint="The relay this server talks to. Saved when you register below."
                type="text"
                value={pushRelayURL}
                onChange={(v) => setPushRelayURLDraft(v)}
                restartRequired={needsRestart("notifications.push_relay_url")}
              />
              <RegisterRelayRow
                relayURL={pushRelayURL}
                deploymentID={pushRelayDeploymentID}
                keyPrefix={pushRelayKeyPrefix}
                expiresAt={pushRelayExpiresAt}
                reregistrationRequired={pushRelayReregistrationRequired}
                urlEdited={pushRelayURLEdited}
                onRegistered={(submittedRelayURL) =>
                  setPushRelayURLDraft((currentDraft) => {
                    const currentURL = currentDraft ?? savedPushRelayURL;
                    return currentURL === submittedRelayURL ? null : currentDraft;
                  })
                }
              />
            </div>
          </ChannelCard>

          <ChannelCard
            icon={Mail}
            title="Email"
            description="Email for accounts that opt in, either one daily summary or a message per episode."
            enabled={emailOn}
            onEnabledChange={setToggle("notifications.email_enabled")}
            chips={
              <>
                {mailReady ? (
                  <Chip tone="positive">Mail server set up</Chip>
                ) : (
                  <Chip tone={emailOn ? "warning" : "neutral"}>Mail server not set up</Chip>
                )}
                <Chip>
                  Summary at {digestHourLabel(form.getValue("notifications.email.digest_hour"))}
                </Chip>
              </>
            }
          >
            <SubsectionLabel>Mail Server</SubsectionLabel>
            <div className="divide-border divide-y">
              <SettingField
                label="Send email from this server"
                hint="Covers every email Silo sends, including address verification — not just notifications. Off means no email leaves the server."
                type="toggle"
                value={form.getValue("email.enabled")}
                onChange={(v) => form.setValue("email.enabled", v)}
                restartRequired={needsRestart("email.enabled")}
              />
              <SettingField
                label="From address"
                hint="The address recipients see mail coming from, e.g. silo@example.com"
                value={form.getValue("email.from_address")}
                onChange={(v) => form.setValue("email.from_address", v)}
                restartRequired={needsRestart("email.from_address")}
              />
              <SettingField
                label="From name"
                hint='The name shown next to that address (default "Silo")'
                value={form.getValue("email.from_name")}
                onChange={(v) => form.setValue("email.from_name", v)}
                restartRequired={needsRestart("email.from_name")}
              />
              <SettingField
                label="Mail server address"
                hint="The SMTP host that accepts your outgoing mail, e.g. smtp.example.com"
                value={form.getValue("email.smtp_host")}
                onChange={(v) => form.setValue("email.smtp_host", v)}
                restartRequired={needsRestart("email.smtp_host")}
              />
              <SettingField
                label="Port"
                hint="587 for STARTTLS (typical), 465 for implicit TLS"
                type="number"
                value={form.getValue("email.smtp_port")}
                onChange={(v) => form.setValue("email.smtp_port", v)}
                restartRequired={needsRestart("email.smtp_port")}
              />
              <SettingField
                label="Encryption"
                hint="STARTTLS starts unencrypted and upgrades; TLS is encrypted from the first byte. Pick whatever your mail provider documents."
                type="select"
                options={[
                  { value: "starttls", label: "STARTTLS" },
                  { value: "tls", label: "TLS (implicit)" },
                  { value: "none", label: "None (insecure)" },
                ]}
                value={form.getValue("email.smtp_security") || "starttls"}
                onChange={(v) => form.setValue("email.smtp_security", v)}
                restartRequired={needsRestart("email.smtp_security")}
              />
              <SettingField
                label="Username"
                hint="Leave empty when the mail server requires no sign-in"
                value={form.getValue("email.smtp_username")}
                onChange={(v) => form.setValue("email.smtp_username", v)}
                restartRequired={needsRestart("email.smtp_username")}
              />
              <SecretField
                label="Password"
                value={form.getValue("email.smtp_password")}
                configured={form.sensitiveConfigured.includes("email.smtp_password")}
                editing={replacingSMTPPassword}
                onReplace={() => setReplacingSMTPPassword(true)}
                onKeep={() => {
                  setReplacingSMTPPassword(false);
                  form.resetValue("email.smtp_password");
                }}
                onChange={(v) => form.setValue("email.smtp_password", v)}
                restartRequired={needsRestart("email.smtp_password")}
              />
              <TestEmailRow />
            </div>
            <SubsectionLabel>Delivery</SubsectionLabel>
            <div className="divide-border divide-y">
              <SettingField
                label="Let people pick an email per episode"
                hint="When off, everyone who chose per-episode email gets the daily summary instead."
                type="toggle"
                value={toggleValue("notifications.email.allow_per_episode")}
                onChange={(v) => form.setValue("notifications.email.allow_per_episode", v)}
                restartRequired={needsRestart("notifications.email.allow_per_episode")}
              />
              <SettingField
                label="Send the daily summary at"
                hint="One email covering everything added since the last one, sent at this hour in the server's own time zone. Use 0-23; the default is 8 (8am)."
                type="number"
                value={numberValue("notifications.email.digest_hour", "8")}
                onChange={(v) => form.setValue("notifications.email.digest_hour", v)}
                restartRequired={needsRestart("notifications.email.digest_hour")}
              />
              <SettingField
                label="Link back to this server at"
                hint="The address people reach this server on, e.g. https://silo.example.com. It is used to build the links inside emails; leave it empty to send emails with no links."
                type="text"
                value={form.getValue("notifications.email.external_url")}
                onChange={(v) => form.setValue("notifications.email.external_url", v)}
                restartRequired={needsRestart("notifications.email.external_url")}
              />
            </div>
          </ChannelCard>

          <ChannelCard
            icon={Bot}
            title="Discord"
            description="Direct messages from your Discord bot to people who linked their account, plus how every Discord message looks."
            enabled={discordOn}
            onEnabledChange={setToggle("notifications.discord_enabled")}
            chips={
              discordAppConfigured ? (
                <Chip tone="positive">Discord app connected</Chip>
              ) : (
                <Chip tone={discordOn ? "warning" : "neutral"}>Discord app not connected</Chip>
              )
            }
          >
            <div className="text-muted-foreground border-border/60 border-b py-3 text-xs leading-relaxed">
              The Discord application itself — client ID, client secret, bot token, and the invite
              link — is set up on the{" "}
              <Link
                to="/admin/settings?tab=integrations"
                className="text-foreground font-medium underline-offset-2 hover:underline"
              >
                Integrations tab
              </Link>
              . The settings here only control what Silo sends and how it looks.
            </div>
            <SubsectionLabel>Delivery</SubsectionLabel>
            <div className="divide-border divide-y">
              <SettingField
                label="Let people pick a DM per episode"
                hint="When off, everyone who chose per-episode DMs gets the daily summary instead."
                type="toggle"
                value={toggleValue("notifications.discord.allow_per_episode")}
                onChange={(v) => form.setValue("notifications.discord.allow_per_episode", v)}
                restartRequired={needsRestart("notifications.discord.allow_per_episode")}
              />
              <SettingField
                label="Send the daily summary at"
                hint="One DM covering everything added since the last one, sent at this hour in the server's own time zone. Use 0-23; the default is 8 (8am)."
                type="number"
                value={numberValue("notifications.discord.digest_hour", "8")}
                onChange={(v) => form.setValue("notifications.discord.digest_hour", v)}
                restartRequired={needsRestart("notifications.discord.digest_hour")}
              />
            </div>
            <SubsectionLabel>Appearance — All Discord Messages</SubsectionLabel>
            <SettingField
              label="Show artwork in Discord messages"
              hint="Where the artwork comes from. Metadata provider images (TMDB, TVDB) are loaded from the provider and never reveal your server's address. Adding this server's own images also shows posters Silo stored itself, which means your server URL is visible to anyone who can see the message and must be reachable from the internet."
              type="select"
              value={form.getValue("notifications.discord.poster_mode") || "provider"}
              options={[
                { value: "provider", label: "Metadata provider images only (default)" },
                { value: "server", label: "Provider images and this server's own images" },
                { value: "off", label: "No artwork" },
              ]}
              onChange={(v) => form.setValue("notifications.discord.poster_mode", v)}
              restartRequired={needsRestart("notifications.discord.poster_mode")}
            />
          </ChannelCard>

          <ChannelCard
            icon={Webhook}
            title="Personal Webhooks"
            description="Webhooks people create for themselves (Discord or generic) — this server sends requests to addresses they choose."
            enabled={webhooksOn}
            onEnabledChange={setToggle("notifications.webhooks_enabled")}
            chips={
              allowPrivate ? <Chip tone="warning">Private destinations allowed</Chip> : undefined
            }
          >
            <AdvancedSection
              id="notifications.webhooks"
              count={WEBHOOK_ADVANCED_KEYS.length}
              forceOpen={anyDirty(WEBHOOK_ADVANCED_KEYS)}
            >
              <SettingField
                label="Webhooks each person may create"
                hint="Default 10."
                type="number"
                value={numberValue("notifications.webhooks.max_per_profile", "10")}
                onChange={(v) => form.setValue("notifications.webhooks.max_per_profile", v)}
                restartRequired={needsRestart("notifications.webhooks.max_per_profile")}
              />
              <SettingField
                label="Webhook calls per minute, per person"
                hint="Anything over the limit is dropped from the webhook only; the notification still lands in their inbox. Default 60."
                type="number"
                value={numberValue(
                  "notifications.webhooks.deliveries_per_minute_per_profile",
                  "60",
                )}
                onChange={(v) =>
                  form.setValue("notifications.webhooks.deliveries_per_minute_per_profile", v)
                }
                restartRequired={needsRestart(
                  "notifications.webhooks.deliveries_per_minute_per_profile",
                )}
              />
              <SettingField
                label="Allow webhooks to private addresses"
                hint="Turns off the guard that blocks LAN and localhost addresses. Development only."
                type="toggle"
                value={form.getValue("notifications.webhooks.allow_private_destinations")}
                onChange={(v) =>
                  form.setValue("notifications.webhooks.allow_private_destinations", v)
                }
                restartRequired={needsRestart("notifications.webhooks.allow_private_destinations")}
              />
              {allowPrivate && (
                <div className="flex items-start gap-2 py-3 text-xs text-amber-500">
                  <TriangleAlert className="mt-0.5 h-4 w-4 flex-shrink-0" />
                  <p>
                    Private destinations are allowed: any user with webhook access can make this
                    server send requests to internal network addresses. Leave this off outside
                    development.
                  </p>
                </div>
              )}
            </AdvancedSection>
          </ChannelCard>

          <ChannelCard
            icon={Megaphone}
            title="Server Channels"
            description="Announcements you set up that post server-wide events (new content, request activity) to a shared place, like a community Discord channel."
            enabled={serverChannelsOn}
            onEnabledChange={setToggle("notifications.server_channels_enabled")}
            chips={
              <>
                {serverChannels != null && (
                  <Chip>
                    {serverChannels.length} destination{serverChannels.length === 1 ? "" : "s"}
                  </Chip>
                )}
                {failingServerChannels > 0 && (
                  <Chip tone="warning">{failingServerChannels} failing</Chip>
                )}
              </>
            }
          >
            <div className="divide-border divide-y">
              <SettingField
                label="Collect new items for (seconds)"
                hint="New content waits this long before posting, so a whole season arrives as one message instead of ten. Default 300, minimum 120."
                type="number"
                value={numberValue("notifications.server_channels.batch_seconds", "300")}
                onChange={(v) => form.setValue("notifications.server_channels.batch_seconds", v)}
                restartRequired={needsRestart("notifications.server_channels.batch_seconds")}
              />
              <SettingField
                label="Mention the requester on Discord"
                hint="Posts about requests @mention the person who asked, when their account is linked to Discord. Unlinked accounts show their Silo username instead."
                type="toggle"
                value={form.getValue("notifications.server_channels.mention_requesters")}
                onChange={(v) =>
                  form.setValue("notifications.server_channels.mention_requesters", v)
                }
                restartRequired={needsRestart("notifications.server_channels.mention_requesters")}
              />
              <div className="pt-3">
                <ServerNotificationChannels />
              </div>
            </div>
          </ChannelCard>
        </section>

        {/* ── Tuning: everything here has a working default ── */}
        <section className="space-y-3">
          <ZoneHeading
            title="Tuning"
            description="Grouping, flood control, and cleanup. The defaults work well for most servers."
          />
          <div className="grid gap-3 xl:grid-cols-2 xl:gap-6">
            <FieldGroup label="Grouping and flood control">
              <AdvancedSection
                id="notifications.fanout"
                count={FANOUT_KEYS.length}
                forceOpen={anyDirty(FANOUT_KEYS)}
              >
                <SettingField
                  label="Wait before sending (seconds)"
                  hint="New content sits this long before Silo sends anything, so episodes that finish scanning together arrive as one notification. Default 30."
                  type="number"
                  value={numberValue("notifications.fanout.settle_seconds", "30")}
                  onChange={(v) => form.setValue("notifications.fanout.settle_seconds", v)}
                  restartRequired={needsRestart("notifications.fanout.settle_seconds")}
                />
                <SettingField
                  label="Most messages per show at once"
                  hint="Stops a big import from sending one message per episode. Anything past this many for the same show is skipped for that batch. Default 3."
                  type="number"
                  value={numberValue("notifications.fanout.max_series_burst", "3")}
                  onChange={(v) => form.setValue("notifications.fanout.max_series_burst", v)}
                  restartRequired={needsRestart("notifications.fanout.max_series_burst")}
                />
                <SettingField
                  label="Give up on content older than (hours)"
                  hint="After downtime, anything that has been waiting longer than this is dropped instead of arriving as stale news. Default 72."
                  type="number"
                  value={numberValue("notifications.fanout.max_event_age_hours", "72")}
                  onChange={(v) => form.setValue("notifications.fanout.max_event_age_hours", v)}
                  restartRequired={needsRestart("notifications.fanout.max_event_age_hours")}
                />
              </AdvancedSection>
            </FieldGroup>

            <FieldGroup label="How long notifications are kept">
              <AdvancedSection
                id="notifications.retention"
                count={RETENTION_KEYS.length}
                forceOpen={anyDirty(RETENTION_KEYS)}
              >
                <SettingField
                  label="Read notifications (days)"
                  hint="How long a notification someone already read stays in their inbox. Default 90."
                  type="number"
                  value={numberValue("notifications.retention.read_days", "90")}
                  onChange={(v) => form.setValue("notifications.retention.read_days", v)}
                  restartRequired={needsRestart("notifications.retention.read_days")}
                />
                <SettingField
                  label="Unread notifications (days)"
                  hint="How long an unread notification stays in their inbox. Default 180."
                  type="number"
                  value={numberValue("notifications.retention.unread_days", "180")}
                  onChange={(v) => form.setValue("notifications.retention.unread_days", v)}
                  restartRequired={needsRestart("notifications.retention.unread_days")}
                />
                <SettingField
                  label="Sent history (days)"
                  hint="How long Silo keeps the record of content it already sent notifications for, which is only useful for troubleshooting. Default 30."
                  type="number"
                  value={numberValue("notifications.retention.event_days", "30")}
                  onChange={(v) => form.setValue("notifications.retention.event_days", v)}
                  restartRequired={needsRestart("notifications.retention.event_days")}
                />
              </AdvancedSection>
            </FieldGroup>
          </div>
        </section>
      </div>

      <SaveBar
        dirtyCount={form.dirtyCount}
        onSave={() => {
          // A saved secret is dropped from the draft, so the field has to go
          // back to its "Configured" summary; a failed save keeps the typed
          // value visible (the mutation reports the error itself).
          void form.save().then(
            () => setReplacingSMTPPassword(false),
            () => {},
          );
        }}
        onDiscard={() => {
          setReplacingSMTPPassword(false);
          form.discard();
        }}
        isSaving={form.isSaving}
        restartRequired={form.restartRequired}
      />
    </div>
  );
}
