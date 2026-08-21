import { useId, useMemo, useState } from "react";

import type {
  RateLimitAuthEndpointConfig,
  RateLimitConfig,
  RateLimitTierConfig,
} from "@/api/types";
import { AdvancedSection } from "@/components/settings/AdvancedSection";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { useRateLimitConfig, useUpdateRateLimitConfig } from "@/hooks/queries/admin/rateLimits";
import { useRestartKeys } from "@/hooks/useRestartKeys";
import { useSettingsForm } from "@/hooks/useSettingsForm";
import { FieldGroup } from "./FieldGroup";
import { SaveBar } from "./SaveBar";
import { SettingField } from "./SettingField";

// Sign-in lifetimes and proxy trust go through the batched settings endpoint.
// Rate limits do not: they live behind /admin/rate-limits/config and the batch
// endpoint rejects those keys, so this tab drives two writers behind one save
// bar rather than showing the admin two different Save buttons.
const KEYS = ["auth.access_token_expiry", "auth.refresh_token_expiry", "clientip.trusted_proxies"];

const DEFAULT_TIER: RateLimitTierConfig = {
  requests_per_second: 10,
  requests_per_minute: 300,
  burst: 20,
};

const DEFAULT_AUTH_ENDPOINT: RateLimitAuthEndpointConfig = {
  requests_per_minute: 20,
  burst: 10,
};

const DEFAULT_CONFIG: RateLimitConfig = {
  enabled: true,
  backend: "memory",
  global_requests_per_second: 1000,
  tiers: {
    standard: { requests_per_second: 20, requests_per_minute: 1200, burst: 20 },
    elevated: { requests_per_second: 100, requests_per_minute: 6000, burst: 100 },
  },
  ip_requests_per_second: 120,
  ip_requests_per_minute: 6000,
  ip_burst: 120,
  auth_endpoints: {
    login: { requests_per_minute: 20, burst: 10 },
    signup: { requests_per_minute: 10, burst: 6 },
    setup: { requests_per_minute: 10, burst: 6 },
    device_start: { requests_per_minute: 20, burst: 10 },
    device_lookup: { requests_per_minute: 60, burst: 20 },
    device_poll: { requests_per_minute: 120, burst: 30 },
    autoscan_webhook: { requests_per_minute: 60, burst: 30 },
  },
};

const TIER_LABELS: Record<string, string> = {
  standard: "Standard API keys",
  elevated: "Elevated API keys",
};

const AUTH_ENDPOINT_LABELS: Record<string, string> = {
  login: "Sign in",
  signup: "Sign up",
  setup: "First-run setup",
  device_start: "TV sign-in: start",
  device_lookup: "TV sign-in: code lookup",
  device_poll: "TV sign-in: waiting for approval",
  autoscan_webhook: "Autoscan webhook",
};

interface RateLimitField {
  value: number;
  onChange: (value: string) => void;
}

/**
 * One labelled row of request budgets. The per-second box is optional because
 * the public auth endpoints are only budgeted per minute; everything else on
 * this tab uses the full requests/second · requests/minute · burst triad.
 */
function RateTriadRow({
  label,
  description,
  perSecond,
  perMinute,
  burst,
  disabled = false,
}: {
  label: string;
  description?: string;
  perSecond?: RateLimitField;
  perMinute: RateLimitField;
  burst: RateLimitField;
  disabled?: boolean;
}) {
  const baseId = useId();
  const columns = perSecond ? "sm:grid-cols-3" : "sm:grid-cols-2";

  return (
    <div className="py-3">
      <div className="text-sm font-medium">{label}</div>
      {description && (
        <p className="text-muted-foreground mt-0.5 mb-2 text-xs leading-relaxed">{description}</p>
      )}
      <div className={`mt-2 grid gap-3 ${columns}`}>
        {perSecond && (
          <div className="space-y-1">
            <Label htmlFor={`${baseId}-rps`} className="text-muted-foreground text-xs">
              Requests per second
            </Label>
            <Input
              id={`${baseId}-rps`}
              type="number"
              min={1}
              value={perSecond.value}
              onChange={(e) => perSecond.onChange(e.target.value)}
              disabled={disabled}
              className="w-full"
            />
          </div>
        )}
        <div className="space-y-1">
          <Label htmlFor={`${baseId}-rpm`} className="text-muted-foreground text-xs">
            Requests per minute
          </Label>
          <Input
            id={`${baseId}-rpm`}
            type="number"
            min={1}
            value={perMinute.value}
            onChange={(e) => perMinute.onChange(e.target.value)}
            disabled={disabled}
            className="w-full"
          />
        </div>
        <div className="space-y-1">
          <Label htmlFor={`${baseId}-burst`} className="text-muted-foreground text-xs">
            Burst allowance
          </Label>
          <Input
            id={`${baseId}-burst`}
            type="number"
            min={1}
            value={burst.value}
            onChange={(e) => burst.onChange(e.target.value)}
            disabled={disabled}
            className="w-full"
          />
        </div>
      </div>
    </div>
  );
}

export default function SecurityAccessSettings() {
  const form = useSettingsForm({ keys: useMemo(() => KEYS, []) });
  const restartKeys = useRestartKeys();
  const { data: serverConfig, isLoading: rateLimitsLoading } = useRateLimitConfig();
  const updateConfig = useUpdateRateLimitConfig();

  const trustedProxiesManaged = form.sensitiveManagedByEnv.includes("clientip.trusted_proxies");

  const hydratedConfig = useMemo<RateLimitConfig>(() => {
    if (!serverConfig) return DEFAULT_CONFIG;
    return {
      enabled: serverConfig.enabled,
      backend: serverConfig.backend || "memory",
      global_requests_per_second: serverConfig.global_requests_per_second,
      tiers: {
        standard: serverConfig.tiers?.standard ?? DEFAULT_CONFIG.tiers.standard!,
        elevated: serverConfig.tiers?.elevated ?? DEFAULT_CONFIG.tiers.elevated!,
      },
      ip_requests_per_second:
        serverConfig.ip_requests_per_second ?? DEFAULT_CONFIG.ip_requests_per_second,
      ip_requests_per_minute:
        serverConfig.ip_requests_per_minute ?? DEFAULT_CONFIG.ip_requests_per_minute,
      ip_burst: serverConfig.ip_burst ?? DEFAULT_CONFIG.ip_burst,
      auth_endpoints: Object.fromEntries(
        Object.keys(AUTH_ENDPOINT_LABELS).map((endpoint) => [
          endpoint,
          serverConfig.auth_endpoints?.[endpoint] ??
            DEFAULT_CONFIG.auth_endpoints[endpoint] ??
            DEFAULT_AUTH_ENDPOINT,
        ]),
      ),
    };
  }, [serverConfig]);

  // Keyed on the hydrated snapshot so a refetch that actually changes the saved
  // config wins over a stale draft instead of silently resurrecting it.
  const hydratedKey = JSON.stringify(hydratedConfig);
  const [configState, setConfigState] = useState<{ key: string; config: RateLimitConfig }>({
    key: hydratedKey,
    config: hydratedConfig,
  });
  const config = configState.key === hydratedKey ? configState.config : hydratedConfig;

  function updateConfigState(updater: (prev: RateLimitConfig) => RateLimitConfig) {
    setConfigState((prev) => {
      const base = prev.key === hydratedKey ? prev.config : hydratedConfig;
      return { key: hydratedKey, config: updater(base) };
    });
  }

  function setNumber(field: (value: number) => void) {
    return (raw: string) => {
      const num = parseInt(raw, 10);
      if (isNaN(num) || num <= 0) return;
      field(num);
    };
  }

  function handleTierChange(tier: string, field: keyof RateLimitTierConfig, value: number) {
    updateConfigState((prev) => {
      const existing: RateLimitTierConfig = prev.tiers[tier] ?? DEFAULT_TIER;
      return { ...prev, tiers: { ...prev.tiers, [tier]: { ...existing, [field]: value } } };
    });
  }

  function handleAuthEndpointChange(
    endpoint: string,
    field: keyof RateLimitAuthEndpointConfig,
    value: number,
  ) {
    updateConfigState((prev) => {
      const existing: RateLimitAuthEndpointConfig =
        prev.auth_endpoints[endpoint] ?? DEFAULT_AUTH_ENDPOINT;
      return {
        ...prev,
        auth_endpoints: { ...prev.auth_endpoints, [endpoint]: { ...existing, [field]: value } },
      };
    });
  }

  const rateLimitsDirty = JSON.stringify(config) !== hydratedKey;
  // Everything except the on/off switch lives in the disclosure, so compare the
  // two with `enabled` normalised away to decide whether to force it open.
  const advancedDirty =
    JSON.stringify({ ...config, enabled: true }) !==
    JSON.stringify({ ...hydratedConfig, enabled: true });
  const advancedCount =
    2 + 3 + Object.keys(TIER_LABELS).length * 3 + Object.keys(AUTH_ENDPOINT_LABELS).length * 2;

  // The limiter only starts (and only switches backend) at boot, so the saved
  // config can disagree with what this process is actually enforcing.
  const limiterNeedsRestart =
    (!!serverConfig &&
      ((serverConfig.enabled && serverConfig.active === false) ||
        (serverConfig.active === true &&
          !!serverConfig.active_backend &&
          serverConfig.backend !== serverConfig.active_backend))) ||
    updateConfig.data?.restart_required === true;

  async function handleSave() {
    if (rateLimitsDirty) updateConfig.mutate(config);
    if (form.dirtyCount > 0) await form.save();
  }

  function handleDiscard() {
    setConfigState({ key: hydratedKey, config: hydratedConfig });
    form.discard();
  }

  if (form.isLoading || rateLimitsLoading)
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

  const savingRateLimits = updateConfig.isPending;

  return (
    <div className="flex h-full flex-col">
      <div className="mb-6 space-y-2">
        <h2 className="text-xl font-semibold tracking-tight">Security &amp; Access</h2>
        <p className="text-muted-foreground text-sm leading-relaxed">
          How long people stay signed in, which proxies Silo believes about client addresses, and
          how many requests it accepts before turning clients away.
        </p>
      </div>

      <div className="flex-1 space-y-6">
        <FieldGroup label="Sign-in Sessions">
          <SettingField
            label="Access Token Expiry"
            type="duration"
            hint="How long a signed-in app can call the API before it silently renews. e.g. 1h, 30m"
            value={form.getValue("auth.access_token_expiry")}
            onChange={(v) => form.setValue("auth.access_token_expiry", v)}
            restartRequired={restartKeys.has("auth.access_token_expiry")}
          />
          <SettingField
            label="Refresh Token Expiry"
            type="duration"
            hint="How long someone stays signed in without typing their password again. e.g. 30d, 720h"
            value={form.getValue("auth.refresh_token_expiry")}
            onChange={(v) => form.setValue("auth.refresh_token_expiry", v)}
            restartRequired={restartKeys.has("auth.refresh_token_expiry")}
          />
        </FieldGroup>

        <FieldGroup label="Network">
          <SettingField
            label="Trusted Proxies"
            hint={
              (trustedProxiesManaged
                ? "Managed by SILO_TRUSTED_PROXIES. Remove that environment variable to edit here. "
                : "") +
              "Comma-separated address ranges of reverse proxies that are allowed to tell Silo a " +
              "request's real client address, e.g. 172.16.0.0/12, 203.0.113.7/32. Applies without " +
              "a restart."
            }
            value={form.getValue("clientip.trusted_proxies")}
            onChange={(v) => form.setValue("clientip.trusted_proxies", v)}
            disabled={trustedProxiesManaged}
            restartRequired={restartKeys.has("clientip.trusted_proxies")}
          />
          <div className="border-border/60 bg-muted/30 my-3 rounded-lg border px-3 py-2">
            <p className="text-sm font-medium">Choosing trusted proxy ranges</p>
            <ul className="text-muted-foreground mt-1 list-disc space-y-1 pl-4 text-xs leading-relaxed">
              <li>
                Setting this replaces the defaults (private ranges 10.0.0.0/8, 172.16.0.0/12,
                192.168.0.0/16 and loopback). Leave it empty to keep them.
              </li>
              <li>
                Recommended: keep the defaults, and only add your proxy&apos;s public address as a
                /32 (e.g. 203.0.113.7/32) if it reaches Silo from outside those ranges.
              </li>
              <li>
                CDNs such as Cloudflare connect from many published IP ranges — you must list all of
                their CIDRs and keep the list up to date as they change.
              </li>
              <li>
                Avoid 0.0.0.0/0 (trust everything): any client could then claim to be at any
                address, which throws off rate limits and audit logs.
              </li>
            </ul>
          </div>
        </FieldGroup>

        <FieldGroup label="Rate Limiting">
          <SettingField
            label="Enable Rate Limiting"
            type="toggle"
            hint="Caps how many requests one client can make in a short window, so a runaway app or a brute-force attempt cannot flood the server. When off, nothing is capped."
            value={config.enabled ? "true" : "false"}
            onChange={(v) => updateConfigState((prev) => ({ ...prev, enabled: v === "true" }))}
            disabled={savingRateLimits}
          />

          <AdvancedSection
            id="security.rate-limits"
            count={advancedCount}
            forceOpen={advancedDirty}
          >
            <p className="text-muted-foreground py-3 text-xs leading-relaxed">
              A burst allowance is how many requests may arrive back to back before the per-second
              or per-minute budget starts turning requests away. The defaults suit a household
              server; raise them only if legitimate clients are being blocked.
            </p>

            <div className="space-y-1 py-3">
              <Label htmlFor="rate-limit-backend" className="text-sm font-medium">
                Where Counters Are Kept
              </Label>
              <Select
                value={config.backend}
                onValueChange={(value) =>
                  updateConfigState((prev) => ({ ...prev, backend: value }))
                }
                disabled={savingRateLimits}
              >
                <SelectTrigger id="rate-limit-backend" className="w-full sm:w-48">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="memory">This server only</SelectItem>
                  <SelectItem value="redis">Shared via Redis</SelectItem>
                </SelectContent>
              </Select>
              <p className="text-muted-foreground text-xs leading-relaxed">
                Keeping counters in memory is fine for a single server. Choose Redis when several
                Silo servers share the same traffic, so one client cannot spend the budget once per
                server. Changing this takes effect after a restart, and Redis must be configured
                under Infrastructure first.
              </p>
            </div>

            <div className="space-y-1 py-3">
              <Label htmlFor="global-rps" className="text-sm font-medium">
                Whole-Server Requests Per Second
              </Label>
              <Input
                id="global-rps"
                type="number"
                min={1}
                value={config.global_requests_per_second}
                onChange={(e) =>
                  setNumber((num) =>
                    updateConfigState((prev) => ({ ...prev, global_requests_per_second: num })),
                  )(e.target.value)
                }
                disabled={savingRateLimits}
                className="w-full sm:w-40"
              />
              <p className="text-muted-foreground text-xs leading-relaxed">
                Ceiling across every rate-limited route, counting all clients together.
              </p>
            </div>

            <RateTriadRow
              label="Per Client Address"
              description="Budget one IP address gets, shared across signed-in routes and the public sign-in and Autoscan endpoints."
              disabled={savingRateLimits}
              perSecond={{
                value: config.ip_requests_per_second,
                onChange: setNumber((num) =>
                  updateConfigState((prev) => ({ ...prev, ip_requests_per_second: num })),
                ),
              }}
              perMinute={{
                value: config.ip_requests_per_minute,
                onChange: setNumber((num) =>
                  updateConfigState((prev) => ({ ...prev, ip_requests_per_minute: num })),
                ),
              }}
              burst={{
                value: config.ip_burst,
                onChange: setNumber((num) =>
                  updateConfigState((prev) => ({ ...prev, ip_burst: num })),
                ),
              }}
            />

            {Object.keys(TIER_LABELS).map((tier) => {
              const tierConfig = config.tiers[tier] ?? DEFAULT_TIER;
              return (
                <RateTriadRow
                  key={tier}
                  label={TIER_LABELS[tier]!}
                  description="Budget each API key in this group gets."
                  disabled={savingRateLimits}
                  perSecond={{
                    value: tierConfig.requests_per_second,
                    onChange: setNumber((num) =>
                      handleTierChange(tier, "requests_per_second", num),
                    ),
                  }}
                  perMinute={{
                    value: tierConfig.requests_per_minute,
                    onChange: setNumber((num) =>
                      handleTierChange(tier, "requests_per_minute", num),
                    ),
                  }}
                  burst={{
                    value: tierConfig.burst,
                    onChange: setNumber((num) => handleTierChange(tier, "burst", num)),
                  }}
                />
              );
            })}

            <div className="py-3">
              <div className="text-sm font-medium">Sign-in and Webhook Endpoints</div>
              <p className="text-muted-foreground mt-0.5 text-xs leading-relaxed">
                Extra per-address budgets for the endpoints anyone can reach without signing in.
                They apply on top of the limits above.
              </p>
            </div>
            {Object.keys(AUTH_ENDPOINT_LABELS).map((endpoint) => {
              const epConfig = config.auth_endpoints[endpoint] ?? DEFAULT_AUTH_ENDPOINT;
              return (
                <RateTriadRow
                  key={endpoint}
                  label={AUTH_ENDPOINT_LABELS[endpoint]!}
                  disabled={savingRateLimits}
                  perMinute={{
                    value: epConfig.requests_per_minute,
                    onChange: setNumber((num) =>
                      handleAuthEndpointChange(endpoint, "requests_per_minute", num),
                    ),
                  }}
                  burst={{
                    value: epConfig.burst,
                    onChange: setNumber((num) => handleAuthEndpointChange(endpoint, "burst", num)),
                  }}
                />
              );
            })}
          </AdvancedSection>
        </FieldGroup>
      </div>

      <SaveBar
        dirtyCount={form.dirtyCount + (rateLimitsDirty ? 1 : 0)}
        onSave={handleSave}
        onDiscard={handleDiscard}
        isSaving={form.isSaving || savingRateLimits}
        restartRequired={form.restartRequired || limiterNeedsRestart}
      />
    </div>
  );
}
